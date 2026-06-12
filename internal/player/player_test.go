package player

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
)

// fakeMPV is an in-process stand-in for mpv's JSON-IPC endpoint. It speaks over
// a real unix socket so the Player exercises its actual net.Conn read/write
// paths.
type fakeMPV struct {
	ln   net.Listener
	conn net.Conn // the server side of the accepted connection
	wmu  sync.Mutex

	rmu     sync.Mutex
	records [][]json.RawMessage // command arrays seen by serveRecord
}

// newFakeMPV starts a unix-socket server in a temp dir, accepts one connection,
// and returns the server plus the client conn the Player should use.
func newFakeMPV(t *testing.T) (*fakeMPV, net.Conn) {
	t.Helper()
	// Keep the socket filename to one char: macOS caps sun_path at ~104 bytes
	// and t.TempDir() paths are already long.
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	type accepted struct {
		conn net.Conn
		err  error
	}
	accCh := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		accCh <- accepted{c, err}
	}()

	client, err := net.Dial("unix", sock)
	if err != nil {
		ln.Close()
		t.Fatalf("dial: %v", err)
	}

	a := <-accCh
	if a.err != nil {
		ln.Close()
		t.Fatalf("accept: %v", a.err)
	}

	f := &fakeMPV{ln: ln, conn: a.conn}
	t.Cleanup(func() {
		f.conn.Close()
		ln.Close()
	})
	return f, client
}

// push writes a raw event/reply line (a newline is appended).
func (f *fakeMPV) push(line string) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_, _ = f.conn.Write([]byte(line + "\n"))
}

// serveEcho replies to every request with success, echoing the request_id and
// reflecting the request's second argument back as the reply's data. This lets
// a caller prove it received the reply to its own request.
func (f *fakeMPV) serveEcho() {
	dec := json.NewDecoder(f.conn)
	for {
		var req struct {
			Command   []json.RawMessage `json:"command"`
			RequestID int               `json:"request_id"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		data := json.RawMessage("null")
		if len(req.Command) >= 2 {
			data = req.Command[1]
		}
		f.push(fmt.Sprintf(`{"error":"success","request_id":%d,"data":%s}`, req.RequestID, string(data)))
	}
}

// serveRecord records every command array it receives and replies success with
// null data, so a caller can assert exactly which IPC commands were issued.
func (f *fakeMPV) serveRecord() {
	dec := json.NewDecoder(f.conn)
	for {
		var req struct {
			Command   []json.RawMessage `json:"command"`
			RequestID int               `json:"request_id"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		f.rmu.Lock()
		f.records = append(f.records, req.Command)
		f.rmu.Unlock()
		f.push(fmt.Sprintf(`{"error":"success","request_id":%d,"data":null}`, req.RequestID))
	}
}

// cmdRecords returns a snapshot of recorded command arrays.
func (f *fakeMPV) cmdRecords() [][]json.RawMessage {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	out := make([][]json.RawMessage, len(f.records))
	copy(out, f.records)
	return out
}

// serveProps answers get_property by name from props (value is raw JSON text);
// unknown properties report "property unavailable". Every other command (incl.
// observe_property) replies success with null data.
func (f *fakeMPV) serveProps(props map[string]string) {
	dec := json.NewDecoder(f.conn)
	for {
		var req struct {
			Command   []json.RawMessage `json:"command"`
			RequestID int               `json:"request_id"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		var name string
		if len(req.Command) >= 1 {
			_ = json.Unmarshal(req.Command[0], &name)
		}
		if name == "get_property" && len(req.Command) >= 2 {
			var prop string
			_ = json.Unmarshal(req.Command[1], &prop)
			if v, ok := props[prop]; ok {
				f.push(fmt.Sprintf(`{"error":"success","request_id":%d,"data":%s}`, req.RequestID, v))
				continue
			}
			f.push(fmt.Sprintf(`{"error":"property unavailable","request_id":%d}`, req.RequestID))
			continue
		}
		f.push(fmt.Sprintf(`{"error":"success","request_id":%d,"data":null}`, req.RequestID))
	}
}

// TestConcurrentCommandsCorrelate spins many goroutines issuing commands at
// once and verifies each one receives the reply to *its* request (the echoed
// argument matches), proving request_id correlation under -race.
func TestConcurrentCommandsCorrelate(t *testing.T) {
	f, conn := newFakeMPV(t)
	p := newConn(conn)
	defer p.Close()
	go f.serveEcho()

	const n = 20
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := p.command("ping", i)
			if err != nil {
				errs[i] = err
				return
			}
			var got int
			if err := json.Unmarshal(resp.Data, &got); err != nil {
				errs[i] = fmt.Errorf("decode data %q: %w", resp.Data, err)
				return
			}
			if got != i {
				errs[i] = fmt.Errorf("got reply for %d, want %d (mis-correlated)", got, i)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: %v", i, err)
		}
	}
}

// TestEventMapping checks every property-change/event/end-file line maps to the
// expected Event (or to nothing). For the "no event" cases we push a sentinel
// file-loaded line afterwards and assert it is the first thing received.
func TestEventMapping(t *testing.T) {
	cases := []struct {
		name string
		line string
		want *Event // nil => no event should be produced for this line
	}{
		{"time-pos", `{"event":"property-change","id":1,"name":"time-pos","data":12.5}`, &Event{Kind: EvTimePos, Float: 12.5}},
		{"time-pos-null", `{"event":"property-change","id":1,"name":"time-pos","data":null}`, nil},
		{"duration", `{"event":"property-change","id":2,"name":"duration","data":210}`, &Event{Kind: EvDuration, Float: 210}},
		{"duration-null", `{"event":"property-change","id":2,"name":"duration","data":null}`, nil},
		{"pause", `{"event":"property-change","id":3,"name":"pause","data":true}`, &Event{Kind: EvPause, Bool: true}},
		{"volume", `{"event":"property-change","id":4,"name":"volume","data":73.5}`, &Event{Kind: EvVolume, Float: 73.5}},
		{"mute", `{"event":"property-change","id":5,"name":"mute","data":true}`, &Event{Kind: EvMute, Bool: true}},
		{"playlist-pos", `{"event":"property-change","id":6,"name":"playlist-pos","data":2}`, &Event{Kind: EvPlaylistPos, Int: 2}},
		{"playlist-pos-idle", `{"event":"property-change","id":6,"name":"playlist-pos","data":-1}`, &Event{Kind: EvPlaylistPos, Int: -1}},
		// playlist-playing-pos must NOT map: it reports a transient -1 between
		// tracks on every auto-advance, which would flash consumers idle.
		{"playing-pos-skip", `{"event":"property-change","id":7,"name":"playlist-playing-pos","data":-1}`, nil},
		{"file-loaded", `{"event":"file-loaded"}`, &Event{Kind: EvFileLoaded}},
		{"end-file-eof", `{"event":"end-file","reason":"eof"}`, &Event{Kind: EvTrackEnded}},
		{"end-file-error", `{"event":"end-file","reason":"error","file_error":"loading failed"}`, &Event{Kind: EvError, Str: "loading failed"}},
		{"end-file-stop", `{"event":"end-file","reason":"stop"}`, nil},
		{"end-file-redirect", `{"event":"end-file","reason":"redirect"}`, nil},
		{"end-file-quit", `{"event":"end-file","reason":"quit"}`, nil},
		{"unknown-event", `{"event":"seek"}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, conn := newFakeMPV(t)
			p := newConn(conn)
			defer p.Close()

			f.push(tc.line)
			f.push(`{"event":"file-loaded"}`) // sentinel

			select {
			case got := <-p.Events():
				if tc.want == nil {
					if got.Kind != EvFileLoaded {
						t.Fatalf("expected line to emit nothing then sentinel, but got %+v", got)
					}
					return
				}
				if got != *tc.want {
					t.Fatalf("got %+v, want %+v", got, *tc.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for event")
			}
		})
	}
}

// TestEventOverflowDropsNeverBlocks floods the player with far more events than
// the buffer holds, without draining, and asserts the producing side never
// stalls (the reader drops rather than blocks).
func TestEventOverflowDropsNeverBlocks(t *testing.T) {
	f, conn := newFakeMPV(t)
	p := newConn(conn)
	defer p.Close()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			f.push(`{"event":"property-change","id":1,"name":"time-pos","data":1.0}`)
		}
		close(done)
	}()

	stalled := false
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		stalled = true
	}

	// Drain events so that, even under a (hypothetical) blocking-send regression
	// where the reader is wedged on a full channel, Close can still complete
	// instead of deadlocking the whole test binary at <-readerDone. In the
	// normal drop-oldest case the buffer is already full by now, so this does
	// not weaken the overflow assertion.
	go func() {
		for range p.Events() {
		}
	}()
	p.Close()

	if stalled {
		t.Fatal("event production stalled: reader blocked on a full events channel")
	}
}

// TestCommandTimeout exercises the commandTimeout/ErrTimeout branch: a live
// connection whose peer never replies must fail with ErrTimeout (not hang).
// cmdTimeout is shrunk so the test is fast.
func TestCommandTimeout(t *testing.T) {
	_, conn := newFakeMPV(t) // fake never replies, but keeps the conn open
	p := newConn(conn)
	defer p.Close()
	p.cmdTimeout = 50 * time.Millisecond

	start := time.Now()
	_, err := p.command("get_property", "duration")
	if err != ErrTimeout {
		t.Fatalf("command err = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("command took %v, want it to fail near cmdTimeout", elapsed)
	}
}

// TestCloseIdempotentUnblocksInflight verifies Close can be called repeatedly
// and that it unblocks a command waiting on a reply that never comes.
func TestCloseIdempotentUnblocksInflight(t *testing.T) {
	f, conn := newFakeMPV(t)
	_ = f // server intentionally does not reply
	p := newConn(conn)

	errCh := make(chan error, 1)
	go func() {
		_, err := p.command("get_property", "duration")
		errCh <- err
	}()

	// Give the command time to register and write before we close.
	time.Sleep(50 * time.Millisecond)

	if err := p.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close #2 (idempotent): %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("in-flight command returned nil error after Close, want error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the in-flight command")
	}
}

// TestEventsClosedAfterClose asserts the events channel is closed once Close
// returns.
func TestEventsClosedAfterClose(t *testing.T) {
	_, conn := newFakeMPV(t)
	p := newConn(conn)
	p.Close()

	select {
	case _, ok := <-p.Events():
		if ok {
			t.Fatal("received a value; expected events channel to be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel not closed after Close")
	}
}

// TestCommandsAfterCloseError checks that commands issued after Close fail
// cleanly instead of hanging.
func TestCommandsAfterCloseError(t *testing.T) {
	_, conn := newFakeMPV(t)
	p := newConn(conn)
	p.Close()

	if err := p.Load("x"); err == nil {
		t.Fatal("Load after Close returned nil error, want error")
	}
}

// TestSetVolumeClamps verifies SetVolume bounds the value to 0..120 before
// sending it to mpv.
func TestSetVolumeClamps(t *testing.T) {
	f, conn := newFakeMPV(t)
	p := newConn(conn)
	defer p.Close()

	// Capture what the player sends for an out-of-range volume.
	gotCh := make(chan int, 1)
	go func() {
		dec := json.NewDecoder(f.conn)
		var req struct {
			Command   []json.RawMessage `json:"command"`
			RequestID int               `json:"request_id"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		var v int
		if len(req.Command) >= 3 {
			_ = json.Unmarshal(req.Command[2], &v)
		}
		gotCh <- v
		f.push(fmt.Sprintf(`{"error":"success","request_id":%d}`, req.RequestID))
	}()

	if err := p.SetVolume(500); err != nil {
		t.Fatalf("SetVolume: %v", err)
	}
	select {
	case v := <-gotCh:
		if v != 120 {
			t.Fatalf("sent volume %d, want clamped to 120", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for set_property")
	}
}

// startFakeServer listens on sock and serves serveEcho on every accepted
// connection, so the real New() attach path can dial it. It is the seam for the
// attach-vs-spawn test (a pre-existing socket must make New attach, not spawn).
func startFakeServer(t *testing.T, sock string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f := &fakeMPV{conn: c}
			go f.serveEcho()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln
}

// TestNewAttachesToExistingSocket proves New attaches to a live daemon and never
// spawns: the MPVPath points at a non-existent binary, so a spawn attempt would
// fail — New succeeding means it took the attach fast-path.
func TestNewAttachesToExistingSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	startFakeServer(t, sock)

	p, err := New(Options{SocketPath: sock, MPVPath: "/no/such/tubeamp-mpv-binary"})
	if err != nil {
		t.Fatalf("New should attach to the existing socket, got error: %v", err)
	}
	defer p.Close()
	if p.pid != 0 {
		t.Errorf("attached player recorded a spawn pid %d; it must not have spawned", p.pid)
	}
}

// TestNewAttachOnlyErrNotRunning checks AttachOnly returns ErrNotRunning when no
// daemon is listening (and never spawns).
func TestNewAttachOnlyErrNotRunning(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s") // nothing listening here
	_, err := New(Options{SocketPath: sock, AttachOnly: true, MPVPath: "/no/such/mpv"})
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("New(AttachOnly) err = %v, want ErrNotRunning", err)
	}
}

// TestQueueFileRoundtrip persists a rich queue and reads it back.
func TestQueueFileRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	p := &Player{queuePath: path}
	want := []model.Track{
		{VideoID: "a", Title: "Alpha", Artists: []string{"X", "Y"}, Album: "Al", Duration: 90 * time.Second},
		{VideoID: "b", Title: "Beta"},
	}
	if err := p.persist(want); err != nil {
		t.Fatalf("persist: %v", err)
	}
	got, err := readQueueFile(path)
	if err != nil {
		t.Fatalf("readQueueFile: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("roundtrip len = %d, want %d", len(got), len(want))
	}
	if got[0].Title != "Alpha" || got[0].Album != "Al" || got[0].Duration != 90*time.Second {
		t.Errorf("roundtrip lost metadata: %+v", got[0])
	}
	if len(got[0].Artists) != 2 || got[0].Artists[0] != "X" {
		t.Errorf("roundtrip lost artists: %+v", got[0].Artists)
	}
}

// TestReadQueueFileTolerates checks corrupt and missing files error cleanly
// (callers treat the error as "no sidecar" and degrade) rather than panicking.
func TestReadQueueFileTolerates(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(corrupt, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readQueueFile(corrupt); err == nil {
		t.Error("readQueueFile on corrupt file returned nil error")
	}
	if _, err := readQueueFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Error("readQueueFile on missing file returned nil error")
	}
}

// TestPlaylistMutationsMirrorAndPersist drives the playlist methods against an
// echo fake and asserts the in-memory model and queue.json sidecar track the
// mutations (replace, append, remove, move, clear).
func TestPlaylistMutationsMirrorAndPersist(t *testing.T) {
	f, conn := newFakeMPV(t)
	go f.serveEcho()
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json")
	defer p.Close()

	a := model.Track{VideoID: "a", Title: "A"}
	b := model.Track{VideoID: "b", Title: "B"}
	c := model.Track{VideoID: "c", Title: "C"}

	titlesOf := func(ts []model.Track) string {
		var s []byte
		for _, t := range ts {
			s = append(s, t.Title...)
		}
		return string(s)
	}
	wantSidecar := func(want string) {
		t.Helper()
		got, err := readQueueFile(p.queuePath)
		if err != nil {
			t.Fatalf("readQueueFile: %v", err)
		}
		if titlesOf(got) != want {
			t.Fatalf("sidecar = %q, want %q", titlesOf(got), want)
		}
	}

	if err := p.PlaylistReplace([]model.Track{a, b}, 0); err != nil {
		t.Fatalf("PlaylistReplace: %v", err)
	}
	wantSidecar("AB")
	if err := p.PlaylistAppend(c); err != nil {
		t.Fatalf("PlaylistAppend: %v", err)
	}
	wantSidecar("ABC")
	if err := p.PlaylistRemove(1); err != nil { // drop B
		t.Fatalf("PlaylistRemove: %v", err)
	}
	wantSidecar("AC")
	if err := p.PlaylistMove(1, 0); err != nil { // C before A
		t.Fatalf("PlaylistMove: %v", err)
	}
	wantSidecar("CA")
	if err := p.PlaylistClear(); err != nil {
		t.Fatalf("PlaylistClear: %v", err)
	}
	wantSidecar("")
}

// TestLoadEntryCommandForm asserts loadfile uses the mpv >= 0.38 five-element
// form with index -1 and a force-media-title options *map* (so titles with
// commas survive). This is the form a probe proved mpv 0.41 requires.
func TestLoadEntryCommandForm(t *testing.T) {
	f, conn := newFakeMPV(t)
	go f.serveRecord()
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json")
	defer p.Close()

	if err := p.PlaylistAppend(model.Track{VideoID: "vid", Title: "Hello, World"}); err != nil {
		t.Fatalf("PlaylistAppend: %v", err)
	}

	var loadfile []json.RawMessage
	for _, rec := range f.cmdRecords() {
		var name string
		if len(rec) >= 1 {
			_ = json.Unmarshal(rec[0], &name)
		}
		if name == "loadfile" {
			loadfile = rec
		}
	}
	if loadfile == nil {
		t.Fatal("no loadfile command was issued")
	}
	if len(loadfile) != 5 {
		t.Fatalf("loadfile arity = %d, want 5 [loadfile url mode index options]: %v", len(loadfile), loadfile)
	}
	var url, mode string
	var index int
	var opts map[string]string
	_ = json.Unmarshal(loadfile[1], &url)
	_ = json.Unmarshal(loadfile[2], &mode)
	_ = json.Unmarshal(loadfile[3], &index)
	_ = json.Unmarshal(loadfile[4], &opts)
	if url != (model.Track{VideoID: "vid"}).URL() {
		t.Errorf("loadfile url = %q", url)
	}
	if mode != "append" {
		t.Errorf("loadfile mode = %q, want append", mode)
	}
	if index != -1 {
		t.Errorf("loadfile index = %d, want -1", index)
	}
	if opts["force-media-title"] != "Hello, World" {
		t.Errorf("force-media-title = %q, want %q", opts["force-media-title"], "Hello, World")
	}
}

// TestPlaylistReplaceAppendsThenJumps asserts PlaylistReplace clears via stop,
// appends every entry while idle, and only then sets playlist-pos — so mpv
// never transiently loads/plays entry 0 (wasting a yt-dlp resolve and emitting
// a spurious playlist-pos=0 event) when start > 0.
func TestPlaylistReplaceAppendsThenJumps(t *testing.T) {
	f, conn := newFakeMPV(t)
	go f.serveRecord()
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json")
	defer p.Close()

	ts := []model.Track{
		{VideoID: "a", Title: "A"},
		{VideoID: "b", Title: "B"},
		{VideoID: "c", Title: "C"},
	}
	if err := p.PlaylistReplace(ts, 2); err != nil {
		t.Fatalf("PlaylistReplace: %v", err)
	}

	var seq []string
	pos := -100
	for _, rec := range f.cmdRecords() {
		var name string
		_ = json.Unmarshal(rec[0], &name)
		switch name {
		case "stop":
			seq = append(seq, "stop")
		case "loadfile":
			var mode string
			_ = json.Unmarshal(rec[2], &mode)
			if mode != "append" {
				t.Errorf("loadfile mode = %q, want append (replace would transiently play entry 0)", mode)
			}
			seq = append(seq, "loadfile")
		case "set_property":
			var prop string
			_ = json.Unmarshal(rec[1], &prop)
			if prop == "playlist-pos" {
				_ = json.Unmarshal(rec[2], &pos)
				seq = append(seq, "set-pos")
			}
		}
	}
	want := []string{"stop", "loadfile", "loadfile", "loadfile", "set-pos"}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Fatalf("command order = %v, want %v", seq, want)
	}
	if pos != 2 {
		t.Fatalf("playlist-pos set to %d, want 2", pos)
	}
}

// sentPauseFalse reports whether a `set_property pause false` was issued —
// the unpause an explicit track change sends so a new song plays even if
// playback was paused.
func sentPauseFalse(records [][]json.RawMessage) bool {
	for _, rec := range records {
		if len(rec) < 3 {
			continue
		}
		var name, prop string
		_ = json.Unmarshal(rec[0], &name)
		_ = json.Unmarshal(rec[1], &prop)
		if name != "set_property" || prop != "pause" {
			continue
		}
		var paused bool
		if json.Unmarshal(rec[2], &paused) == nil && !paused {
			return true
		}
	}
	return false
}

// TestPlayOnSwap pins the play-on-swap behavior: PlaylistReplace, PlaylistJump,
// Next, and Prev each force playback unpaused (set_property pause false), so
// picking a new song while paused starts it playing. mpv then emits a pause
// property-change and the UI indicator follows. (Flat loop, not subtests: the
// fake mpv's unix socket lives under t.TempDir(), and long subtest names blow
// past macOS's ~104-byte sun_path limit.)
func TestPlayOnSwap(t *testing.T) {
	ts := []model.Track{{VideoID: "a", Title: "A"}, {VideoID: "b", Title: "B"}}
	cases := []struct {
		name string
		act  func(p *Player) error
	}{
		{"PlaylistReplace", func(p *Player) error { return p.PlaylistReplace(ts, 1) }},
		{"PlaylistJump", func(p *Player) error { _ = p.PlaylistReplace(ts, 0); return p.PlaylistJump(1) }},
		{"Next", func(p *Player) error { _ = p.PlaylistReplace(ts, 0); return p.Next() }},
		{"Prev", func(p *Player) error { _ = p.PlaylistReplace(ts, 1); return p.Prev() }},
	}
	for _, tc := range cases {
		f, conn := newFakeMPV(t)
		go f.serveRecord()
		p := newConn(conn)
		p.queuePath = filepath.Join(t.TempDir(), "queue.json")
		if err := tc.act(p); err != nil {
			p.Close()
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !sentPauseFalse(f.cmdRecords()) {
			t.Errorf("%s did not send set_property pause false (new song would stay paused)", tc.name)
		}
		p.Close()
	}
}

// TestStopResetsMirrorAndSidecar checks Stop matches what mpv's stop does to
// the playlist (clears it): the in-memory mirror and queue.json must reset too,
// or later index-based mutations would target entries that no longer exist.
func TestStopResetsMirrorAndSidecar(t *testing.T) {
	f, conn := newFakeMPV(t)
	go f.serveEcho()
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json")
	defer p.Close()

	if err := p.PlaylistReplace([]model.Track{{VideoID: "a", Title: "A"}}, 0); err != nil {
		t.Fatalf("PlaylistReplace: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := p.getTracks(); len(got) != 0 {
		t.Errorf("Stop left %d tracks in the mirror, want 0", len(got))
	}
	disk, err := readQueueFile(p.queuePath)
	if err != nil {
		t.Fatalf("readQueueFile: %v", err)
	}
	if len(disk) != 0 {
		t.Errorf("Stop left %d tracks in the sidecar, want 0", len(disk))
	}
}

// TestSnapshotUsesRichSidecar checks Snapshot reconciles the live playlist with
// queue.json (matching counts => rich metadata wins) and reads the properties.
func TestSnapshotUsesRichSidecar(t *testing.T) {
	f, conn := newFakeMPV(t)
	props := map[string]string{
		"playlist": `[{"filename":"https://music.youtube.com/watch?v=a","title":"A"},` +
			`{"filename":"https://music.youtube.com/watch?v=b","title":"B"}]`,
		"playlist-pos": "1",
		"pause":        "true",
		"time-pos":     "12.5",
		"duration":     "200",
		"volume":       "70",
		"mute":         "false",
	}
	go f.serveProps(props)
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json")
	defer p.Close()

	// Sidecar with the same count but richer titles.
	if err := p.persist([]model.Track{
		{VideoID: "a", Title: "Alpha", Artists: []string{"X"}},
		{VideoID: "b", Title: "Beta"},
	}); err != nil {
		t.Fatal(err)
	}

	s, err := p.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(s.Tracks) != 2 || s.Tracks[0].Title != "Alpha" || s.Tracks[1].Title != "Beta" {
		t.Errorf("snapshot tracks = %+v, want rich sidecar titles", s.Tracks)
	}
	if s.PlaylistPos != 1 || !s.Paused || s.TimePos != 12.5 || s.Duration != 200 || s.Volume != 70 || s.Mute {
		t.Errorf("snapshot props = %+v", s)
	}
}

// TestSnapshotDegradesWithoutSidecar checks that a missing queue.json falls back
// to titles from the live mpv playlist rather than crashing.
func TestSnapshotDegradesWithoutSidecar(t *testing.T) {
	f, conn := newFakeMPV(t)
	props := map[string]string{
		"playlist":     `[{"filename":"https://music.youtube.com/watch?v=zz","title":"Zee"}]`,
		"playlist-pos": "0",
	}
	go f.serveProps(props)
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json") // never written
	defer p.Close()

	s, err := p.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(s.Tracks) != 1 || s.Tracks[0].Title != "Zee" || s.Tracks[0].VideoID != "zz" {
		t.Errorf("degraded snapshot = %+v, want one track Zee/zz from the live playlist", s.Tracks)
	}
}

// TestSnapshotMatchesSidecarPerEntry asserts reconciliation cross-checks each
// sidecar track against the live entry's filename instead of trusting a bare
// length match: a same-length sidecar diverging from the live playlist (another
// client mutated the shared queue) must not report metadata for the wrong
// track, while still-matching entries keep their rich metadata.
func TestSnapshotMatchesSidecarPerEntry(t *testing.T) {
	f, conn := newFakeMPV(t)
	props := map[string]string{
		"playlist": `[{"filename":"https://music.youtube.com/watch?v=a","title":"A"},` +
			`{"filename":"https://music.youtube.com/watch?v=x","title":"X"}]`,
		"playlist-pos": "0",
	}
	go f.serveProps(props)
	p := newConn(conn)
	p.queuePath = filepath.Join(t.TempDir(), "queue.json")
	defer p.Close()

	// Same length as the live playlist, but only entry 0 still matches it.
	if err := p.persist([]model.Track{
		{VideoID: "a", Title: "Alpha", Artists: []string{"AA"}},
		{VideoID: "b", Title: "Beta"},
	}); err != nil {
		t.Fatal(err)
	}

	s, err := p.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(s.Tracks) != 2 {
		t.Fatalf("snapshot tracks = %d, want 2", len(s.Tracks))
	}
	if s.Tracks[0].Title != "Alpha" {
		t.Errorf("matching entry lost rich metadata: %+v", s.Tracks[0])
	}
	if s.Tracks[1].Title != "X" || s.Tracks[1].VideoID != "x" {
		t.Errorf("mismatched entry = %+v, want it degraded to the live playlist data (X/x)", s.Tracks[1])
	}
}

// TestLockPidParsing covers the lock-file pid reader: missing, empty, and
// malformed files yield no pid; a recorded pid round-trips.
func TestLockPidParsing(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "l")
	if _, ok := lockPid(""); ok {
		t.Error("empty path should yield no pid")
	}
	if _, ok := lockPid(lock); ok {
		t.Error("missing lock file should yield no pid")
	}
	for _, content := range []string{"", "\n", "garbage", "-4"} {
		if err := os.WriteFile(lock, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := lockPid(lock); ok {
			t.Errorf("content %q should yield no pid", content)
		}
	}
	if err := os.WriteFile(lock, []byte("1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid, ok := lockPid(lock); !ok || pid != 1234 {
		t.Errorf("lockPid = %d, %v; want 1234, true", pid, ok)
	}
}

// TestReclaimDeadLock verifies the stale-lock fast path: a lock recording a
// dead pid is reclaimed immediately, while a lock held by a live process (or
// one with no recorded pid) is never stolen.
func TestReclaimDeadLock(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "l")

	// Live owner (this test process) must never be reclaimed.
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if reclaimDeadLock(lock) {
		t.Fatal("reclaimed a lock held by a live process")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("live lock was removed: %v", err)
	}

	// No recorded pid: not provably dead, so not reclaimable by the fast path.
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if reclaimDeadLock(lock) {
		t.Fatal("reclaimed a lock with no recorded pid")
	}

	// Dead owner: run a short-lived child to completion so its pid is
	// definitely dead (and reaped).
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run true: %v", err)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if !reclaimDeadLock(lock) {
		t.Fatal("did not reclaim the lock of a dead process")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("reclaimed lock still present: stat err = %v", err)
	}
}

// TestQuitKillsUnresponsiveDaemon verifies Quit does not just assume the quit
// command worked: with an IPC peer that never acts on it, Quit must verify the
// recorded pid exits and escalate to signals until it does.
func TestQuitKillsUnresponsiveDaemon(t *testing.T) {
	_, conn := newFakeMPV(t) // never replies; the quit write is best-effort
	p := newConn(conn)
	p.quitBudget = 100 * time.Millisecond

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	p.pid = pid

	if err := p.Quit(); err != nil {
		t.Fatalf("Quit: %v", err)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("daemon process still alive after Quit")
	}
}

// writeSilenceWAV writes a minimal 16-bit PCM mono WAV of ~0.4s of silence and
// returns its path. Used by the real-mpv integration test.
func writeSilenceWAV(t *testing.T, path string) string {
	t.Helper()
	const (
		sampleRate = 8000
		channels   = 1
		bits       = 16
	)
	nSamples := sampleRate * 4 / 10 // 0.4s
	dataSize := nSamples * channels * bits / 8

	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataSize))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate*channels*bits/8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*bits/8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataSize))
	b.Write(make([]byte, dataSize))

	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	return path
}

// waitPlaylistPos drains events until an EvPlaylistPos with the wanted index
// arrives (proving mpv advanced the playlist itself) or the timeout elapses.
func waitPlaylistPos(p *Player, want int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-p.Events():
			if !ok {
				return false
			}
			if ev.Kind == EvPlaylistPos && ev.Int == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// waitProcessGone reaps the (possibly zombie) child pid via wait4(WNOHANG) until
// it is gone or the timeout elapses. The child is a zombie until reaped because
// the test process — its parent — never Waits on the detached mpv.
func waitProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
		if err != nil || wpid == pid {
			return true // ECHILD (already gone) or reaped just now
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestPersistentDaemonRealMPV is the end-to-end test for the persistent,
// detached daemon. It spawns mpv, plays two tiny WAVs, watches mpv auto-advance,
// detaches (Close) while mpv keeps running, re-attaches and rebuilds state via
// Snapshot, then Quits and verifies the process and socket are gone. Skipped
// when mpv is absent. Kept hermetic by pointing HOME/XDG at temp dirs.
func TestPersistentDaemonRealMPV(t *testing.T) {
	// This is a real-mpv INTEGRATION test: it spawns mpv and depends on a
	// working real-time audio output to play silence WAVs and auto-advance. That
	// makes it environment-fragile (no/locked/changed audio device → no
	// playback progress → no auto-advance), so it is gated like the other live
	// tests. The fake-mpv tests above cover the playlist/auto-advance logic
	// deterministically. Run on demand: TUBEAMP_LIVE_MPV=1 go test ./internal/player/
	if os.Getenv("TUBEAMP_LIVE_MPV") != "1" {
		t.Skip("real-mpv integration test; set TUBEAMP_LIVE_MPV=1 (needs a working audio output)")
	}
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))

	dir := t.TempDir()
	sock := filepath.Join(dir, "s")
	w1 := writeSilenceWAV(t, filepath.Join(dir, "a.wav"))
	w2 := writeSilenceWAV(t, filepath.Join(dir, "b.wav"))
	urlByID := map[string]string{"a": w1, "b": w2}
	urlFn := func(tr model.Track) string { return urlByID[tr.VideoID] }

	p, err := New(Options{SocketPath: sock, Volume: 0})
	if err != nil {
		t.Fatalf("New(spawn): %v", err)
	}
	p.urlFunc = urlFn
	pid := p.pid
	if pid == 0 {
		t.Fatal("a spawned player must record its mpv pid")
	}
	// Detach semantics mean Close() leaves mpv running; if the test fails before
	// its Quit() step the daemon would leak. Guarantee teardown on every path.
	t.Cleanup(func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	ta := model.Track{VideoID: "a", Title: "Track A", Duration: 400 * time.Millisecond}
	tb := model.Track{VideoID: "b", Title: "Track B", Duration: 400 * time.Millisecond}
	if err := p.PlaylistReplace([]model.Track{ta, tb}, 0); err != nil {
		t.Fatalf("PlaylistReplace: %v", err)
	}

	if !waitPlaylistPos(p, 1, 6*time.Second) {
		t.Fatal("mpv did not auto-advance to playlist index 1 (no EvPlaylistPos=1)")
	}

	// Detach: mpv must keep running.
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !processAlive(pid) {
		t.Fatal("mpv exited after Close; detach must leave playback running")
	}

	// Re-attach: must not spawn, and must rebuild rich state.
	p2, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New(re-attach): %v", err)
	}
	p2.urlFunc = urlFn
	if p2.pid != 0 {
		t.Errorf("re-attach spawned a process (pid %d); it should have attached", p2.pid)
	}
	snap, err := p2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Tracks) != 2 {
		t.Fatalf("snapshot tracks = %d, want 2", len(snap.Tracks))
	}
	if snap.Tracks[0].Title != "Track A" || snap.Tracks[1].Title != "Track B" {
		t.Errorf("snapshot lost rich titles: %q, %q", snap.Tracks[0].Title, snap.Tracks[1].Title)
	}

	// Quit: terminate the daemon and remove socket + lock.
	if err := p2.Quit(); err != nil {
		t.Fatalf("Quit: %v", err)
	}
	if !waitProcessGone(pid, 6*time.Second) {
		t.Error("mpv still alive after Quit")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket not removed after Quit: stat err = %v", err)
	}
	if _, err := os.Stat(sock + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file not removed after Quit: stat err = %v", err)
	}
}

// processAlive reports whether pid refers to a live (or zombie) process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
