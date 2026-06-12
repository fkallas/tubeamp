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
