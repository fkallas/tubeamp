package player

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeMPV is an in-process stand-in for mpv's JSON-IPC endpoint. It speaks over
// a real unix socket so the Player exercises its actual net.Conn read/write
// paths.
type fakeMPV struct {
	ln   net.Listener
	conn net.Conn // the server side of the accepted connection
	wmu  sync.Mutex
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

// TestConcurrentCommandsCorrelate spins many goroutines issuing commands at
// once and verifies each one receives the reply to *its* request (the echoed
// argument matches), proving request_id correlation under -race.
func TestConcurrentCommandsCorrelate(t *testing.T) {
	f, conn := newFakeMPV(t)
	p := newConn(conn, nil, "")
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
			p := newConn(conn, nil, "")
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
	p := newConn(conn, nil, "")
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
	p := newConn(conn, nil, "")
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
	p := newConn(conn, nil, "")

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
	p := newConn(conn, nil, "")
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
	p := newConn(conn, nil, "")
	p.Close()

	if err := p.Load("x"); err == nil {
		t.Fatal("Load after Close returned nil error, want error")
	}
}

// TestSetVolumeClamps verifies SetVolume bounds the value to 0..120 before
// sending it to mpv.
func TestSetVolumeClamps(t *testing.T) {
	f, conn := newFakeMPV(t)
	p := newConn(conn, nil, "")
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

// TestIntegrationRealMPV spawns a real mpv (skipped if not installed) and runs
// the full New -> SetVolume -> Close lifecycle, asserting no errors and that
// the process exits and the socket is removed.
func TestIntegrationRealMPV(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv not installed")
	}
	sock := filepath.Join(t.TempDir(), "s")
	p, err := New(Options{SocketPath: sock, Volume: 40})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.SetVolume(55); err != nil {
		t.Errorf("SetVolume: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Process must be reaped.
	if p.cmd != nil && p.cmd.ProcessState == nil {
		t.Error("mpv process was not reaped (ProcessState nil after Close)")
	}
	// Socket file must be gone.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket not removed after Close: stat err = %v", err)
	}
}
