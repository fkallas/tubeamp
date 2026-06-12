package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/config"
)

// stubAccountInfo replaces the YT Music sign-in probe with a canned result so
// -status tests never touch the network; the original is restored on cleanup.
func stubAccountInfo(t *testing.T, name string, signedIn bool, err error) {
	t.Helper()
	orig := ytmAccountInfo
	ytmAccountInfo = func(context.Context, *config.Config) (string, bool, error) {
		return name, signedIn, err
	}
	t.Cleanup(func() { ytmAccountInfo = orig })
}

// fakeDaemon is an in-process stand-in for the mpv JSON-IPC endpoint, listening
// on a real unix socket so dispatchControl exercises the genuine attach path. It
// answers get_property from a fixed map and records every command it receives.
type fakeDaemon struct {
	ln    net.Listener
	props map[string]string // property name -> raw JSON value

	mu      sync.Mutex
	records [][]json.RawMessage
}

func startFakeDaemon(t *testing.T, sock string, props map[string]string) *fakeDaemon {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen %q: %v", sock, err)
	}
	d := &fakeDaemon{ln: ln, props: props}
	go d.acceptLoop()
	t.Cleanup(func() { ln.Close() })
	return d
}

func (d *fakeDaemon) acceptLoop() {
	for {
		c, err := d.ln.Accept()
		if err != nil {
			return
		}
		go d.serve(c)
	}
}

func (d *fakeDaemon) serve(c net.Conn) {
	dec := json.NewDecoder(c)
	for {
		var req struct {
			Command   []json.RawMessage `json:"command"`
			RequestID int               `json:"request_id"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		d.mu.Lock()
		d.records = append(d.records, req.Command)
		d.mu.Unlock()

		var name string
		if len(req.Command) >= 1 {
			_ = json.Unmarshal(req.Command[0], &name)
		}
		if name == "get_property" && len(req.Command) >= 2 {
			var prop string
			_ = json.Unmarshal(req.Command[1], &prop)
			if v, ok := d.props[prop]; ok {
				fmt.Fprintf(c, `{"error":"success","request_id":%d,"data":%s}`+"\n", req.RequestID, v)
				continue
			}
			fmt.Fprintf(c, `{"error":"property unavailable","request_id":%d}`+"\n", req.RequestID)
			continue
		}
		fmt.Fprintf(c, `{"error":"success","request_id":%d,"data":null}`+"\n", req.RequestID)
	}
}

func (d *fakeDaemon) sawCommand(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, rec := range d.records {
		var n string
		if len(rec) >= 1 {
			_ = json.Unmarshal(rec[0], &n)
		}
		if n == name {
			return true
		}
	}
	return false
}

// lastSetProp returns the value sent for the most recent set_property <prop>.
func (d *fakeDaemon) lastSetProp(prop string) (json.RawMessage, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var found json.RawMessage
	var ok bool
	for _, rec := range d.records {
		var n, p string
		if len(rec) >= 3 {
			_ = json.Unmarshal(rec[0], &n)
			_ = json.Unmarshal(rec[1], &p)
			if n == "set_property" && p == prop {
				found, ok = rec[2], true
			}
		}
	}
	return found, ok
}

// emptyXDG points XDG_DATA_HOME at a short empty temp dir (no daemon).
func emptyXDG(t *testing.T) {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "tba")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	t.Setenv("XDG_DATA_HOME", base)
}

// setupFakeDaemon points XDG_DATA_HOME at a short temp dir and starts a fake
// daemon at DataDir()/mpv.sock. A short /tmp path keeps the unix socket name
// under the platform's sun_path limit.
func setupFakeDaemon(t *testing.T, props map[string]string) (*fakeDaemon, string) {
	t.Helper()
	emptyXDG(t)
	dir := config.DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "mpv.sock")
	return startFakeDaemon(t, sock, props), sock
}

func TestDispatchNotRunning(t *testing.T) {
	emptyXDG(t)
	var out, errOut bytes.Buffer
	code := dispatchControl(&out, &errOut, config.Default(), controlFlags{pause: true})
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "not running") {
		t.Errorf("stderr = %q, want it to mention \"not running\"", errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", out.String())
	}
}

func TestDispatchLineSilentWhenNotRunning(t *testing.T) {
	emptyXDG(t)
	var out, errOut bytes.Buffer
	code := dispatchControl(&out, &errOut, config.Default(), controlFlags{line: true})
	if code != 0 {
		t.Errorf("exit = %d, want 0 (silent success)", code)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("expected silence; got stdout %q stderr %q", out.String(), errOut.String())
	}
}

func TestDispatchPauseNextPrevStop(t *testing.T) {
	cases := []struct {
		name string
		flag controlFlags
		want string
	}{
		{"pause", controlFlags{pause: true}, "cycle"},
		{"next", controlFlags{next: true}, "playlist-next"},
		{"prev", controlFlags{prev: true}, "playlist-prev"},
		{"stop", controlFlags{stop: true}, "stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := setupFakeDaemon(t, nil)
			var out, errOut bytes.Buffer
			code := dispatchControl(&out, &errOut, config.Default(), tc.flag)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
			}
			if !d.sawCommand(tc.want) {
				t.Errorf("daemon did not receive %q command", tc.want)
			}
		})
	}
}

func TestDispatchVolumeAbsolute(t *testing.T) {
	d, _ := setupFakeDaemon(t, map[string]string{"volume": "50"})
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{vol: "30"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	v, ok := d.lastSetProp("volume")
	if !ok {
		t.Fatal("no set_property volume command issued")
	}
	var got int
	_ = json.Unmarshal(v, &got)
	if got != 30 {
		t.Errorf("absolute volume set to %d, want 30", got)
	}
}

func TestDispatchVolumeRelative(t *testing.T) {
	d, _ := setupFakeDaemon(t, map[string]string{"volume": "50"})
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{vol: "+15"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	v, ok := d.lastSetProp("volume")
	if !ok {
		t.Fatal("no set_property volume command issued")
	}
	var got int
	_ = json.Unmarshal(v, &got)
	if got != 65 { // 50 (current) + 15
		t.Errorf("relative volume set to %d, want 65", got)
	}
}

func TestDispatchSeek(t *testing.T) {
	d, _ := setupFakeDaemon(t, nil)
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{seek: "-10"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	if !d.sawCommand("seek") {
		t.Errorf("daemon did not receive a seek command")
	}
}

func TestDispatchStatus(t *testing.T) {
	props := map[string]string{
		"playlist": `[{"filename":"https://music.youtube.com/watch?v=a","title":"Song A"},` +
			`{"filename":"https://music.youtube.com/watch?v=b","title":"Song B"}]`,
		"playlist-pos": "0",
		"pause":        "false",
		"time-pos":     "83",
		"duration":     "234",
		"volume":       "75",
		"mute":         "false",
	}
	setupFakeDaemon(t, props)
	stubAccountInfo(t, "Felipe Kallas", true, nil) // no network; canned sign-in
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{status: true}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	s := out.String()
	for _, want := range []string{"Song A", "1:23", "3:54", "75%", "1/2", "yt music: signed in as Felipe Kallas"} {
		if !strings.Contains(s, want) {
			t.Errorf("status missing %q; got:\n%s", want, s)
		}
	}
}

// TestYTMStatusLine pins the three sign-in renderings of -status' yt music line
// (signed in / anonymous / unknown), stubbing the probe so no network is touched.
func TestYTMStatusLine(t *testing.T) {
	cfg := config.Default()
	cases := []struct {
		name     string
		signedIn bool
		err      error
		want     string
	}{
		{"Felipe", true, nil, "yt music: signed in as Felipe"},
		{"", false, nil, "yt music: anonymous"},
		{"", false, context.DeadlineExceeded, "yt music: unknown"},
	}
	for _, tc := range cases {
		stubAccountInfo(t, tc.name, tc.signedIn, tc.err)
		if got := ytmStatusLine(cfg); got != tc.want {
			t.Errorf("ytmStatusLine = %q, want %q", got, tc.want)
		}
	}
}

func TestDispatchLine(t *testing.T) {
	props := map[string]string{
		"playlist":     `[{"filename":"https://music.youtube.com/watch?v=a","title":"Song A"}]`,
		"playlist-pos": "0",
		"time-pos":     "83",
		"duration":     "234",
	}
	setupFakeDaemon(t, props)
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{line: true}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	s := strings.TrimSpace(out.String())
	if !strings.Contains(s, "Song A") || !strings.Contains(s, "1:23/3:54") {
		t.Errorf("compact line = %q, want it to contain the title and 1:23/3:54", s)
	}
}

func TestDispatchLineIdle(t *testing.T) {
	props := map[string]string{"playlist": `[]`, "playlist-pos": "-1"}
	setupFakeDaemon(t, props)
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{line: true}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	if out.Len() != 0 {
		t.Errorf("idle -line should print nothing, got %q", out.String())
	}
}

func TestDispatchQueue(t *testing.T) {
	props := map[string]string{
		"playlist": `[{"filename":"https://music.youtube.com/watch?v=a","title":"Song A"},` +
			`{"filename":"https://music.youtube.com/watch?v=b","title":"Song B"}]`,
		"playlist-pos": "1",
	}
	setupFakeDaemon(t, props)
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{queue: true}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	s := out.String()
	if !strings.Contains(s, "1. Song A") || !strings.Contains(s, "2. Song B") {
		t.Errorf("queue listing = %q", s)
	}
	if !strings.Contains(s, "▶") {
		t.Errorf("queue listing has no playing marker: %q", s)
	}
}

func TestDispatchKillRemovesSocket(t *testing.T) {
	d, sock := setupFakeDaemon(t, nil)
	var out, errOut bytes.Buffer
	if code := dispatchControl(&out, &errOut, config.Default(), controlFlags{kill: true}); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errOut.String())
	}
	// Quit() writes "quit" then closes the connection; the fake daemon records
	// commands asynchronously in its reader goroutine, so poll rather than
	// checking once (the command is sent — the daemon just may not have logged
	// it the instant dispatchControl returns).
	sawQuit := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if d.sawCommand("quit") {
			sawQuit = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawQuit {
		t.Errorf("kill did not send a quit command")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("kill did not remove the socket file: stat err = %v", err)
	}
}
