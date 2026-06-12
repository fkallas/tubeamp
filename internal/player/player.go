// Package player is a thin wrapper around an mpv subprocess driven over its
// JSON-IPC unix socket. It spawns mpv in idle/audio-only mode, correlates
// request/reply traffic by request_id, and surfaces playback state changes as a
// stream of Events. mpv resolves YouTube URLs itself via its yt-dlp hook, so
// this package never handles raw stream URLs.
package player

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Sentinel errors returned by command helpers.
var (
	// ErrClosed is returned by commands issued on a closed player (or pending
	// when Close races an in-flight request).
	ErrClosed = errors.New("player: closed")
	// ErrTimeout is returned when mpv does not reply within commandTimeout.
	ErrTimeout = errors.New("player: command timed out")
)

const (
	// connectBudget is the total time New waits for the IPC socket to appear.
	connectBudget = 5 * time.Second
	// connectRetry is the delay between socket dial attempts.
	connectRetry = 100 * time.Millisecond
	// processWait is how long Close waits for mpv to exit before killing it.
	processWait = 2 * time.Second
	// volumeMin and volumeMax bound SetVolume (mpv accepts 0..max-volume).
	volumeMin = 0
	volumeMax = 120
)

// Options configures a Player. Zero values fall back to the documented
// defaults.
type Options struct {
	MPVPath    string // "" => "mpv" (resolved via PATH)
	SocketPath string // "" => os.TempDir()/tubeamp-mpv-<pid>.sock
	YTDLFormat string // "" => "bestaudio"
	Volume     int    // initial volume passed to mpv --volume
}

// EventKind identifies which playback state an Event reports.
type EventKind int

const (
	EvTimePos    EventKind = iota // Float seconds (current position)
	EvDuration                    // Float seconds (track length)
	EvPause                       // Bool (paused?)
	EvVolume                      // Float 0-100 (mpv volume)
	EvMute                        // Bool (muted?)
	EvFileLoaded                  // a new track started playing
	EvTrackEnded                  // end-file with reason "eof"
	EvError                       // Str message (incl. end-file reason "error")
)

// Event is a single playback state change emitted by mpv. Only the field
// relevant to Kind is meaningful.
type Event struct {
	Kind  EventKind
	Float float64
	Bool  bool
	Str   string
}

// sockSeq disambiguates the default socket path between multiple Players in one
// process (the pid alone collides), so New never unlinks a sibling's live socket.
var sockSeq atomic.Int64

// Player owns an mpv subprocess and its IPC connection. It is safe for
// concurrent use by multiple goroutines.
type Player struct {
	conn net.Conn
	cmd  *exec.Cmd // nil when constructed from an existing conn (tests)
	sock string    // socket file to remove on Close; "" => leave alone

	procDone <-chan struct{} // closed when cmd.Wait() returns; nil when cmd == nil

	reqID   atomic.Int64
	writeMu sync.Mutex // serializes writes on conn

	cmdTimeout time.Duration // bound on a synchronous command's reply wait

	mu      sync.Mutex                // guards pending and the closed check
	pending map[int]chan *ipcResponse // request_id -> reply channel
	closed  chan struct{}             // closed once shutdown begins

	events     chan Event
	readerDone chan struct{} // closed when readLoop returns

	closeStateOnce sync.Once // guards close(closed): both Close and reader death may trigger it
	finishOnce     sync.Once // guards failing pending + close(events): done exactly once
	closeOnce      sync.Once // guards the full Close sequence
}

// markClosed closes the closed channel exactly once. After this, new commands
// fail fast (ErrClosed) and in-flight ones unblock. It is called both by Close
// and by the reader goroutine when it detects mpv's death.
func (p *Player) markClosed() {
	p.closeStateOnce.Do(func() {
		p.mu.Lock()
		close(p.closed)
		p.mu.Unlock()
	})
}

// finish fails every pending request and closes the events channel, exactly
// once. It must only run once the reader goroutine is no longer sending on
// events (from the reader's own exit path, or from Close after readerDone), so
// it can never race a send onto a closed channel.
func (p *Player) finish() {
	p.finishOnce.Do(func() {
		p.mu.Lock()
		for id, ch := range p.pending {
			close(ch)
			delete(p.pending, id)
		}
		p.mu.Unlock()
		close(p.events)
	})
}

// newConn builds a Player around an already-connected IPC socket and starts the
// reader goroutine. cmd may be nil (no child process to reap) and sock may be
// "" (no socket file to remove). This is the seam New and tests share.
func newConn(conn net.Conn, cmd *exec.Cmd, sock string) *Player {
	p := &Player{
		conn:       conn,
		cmd:        cmd,
		sock:       sock,
		cmdTimeout: commandTimeout,
		pending:    make(map[int]chan *ipcResponse),
		closed:     make(chan struct{}),
		events:     make(chan Event, eventBufferSize),
		readerDone: make(chan struct{}),
	}
	go p.readLoop()
	return p
}

// New spawns mpv, connects to its IPC socket (retrying for ~5s), starts
// observing the playback properties we report, and returns a ready Player. It
// returns an error if the mpv binary is missing or the IPC never connects.
func New(o Options) (*Player, error) {
	bin := o.MPVPath
	if bin == "" {
		bin = "mpv"
	}
	mpvPath, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("player: mpv binary %q not found: %w", bin, err)
	}

	sock := o.SocketPath
	if sock == "" {
		// pid + a per-instance sequence: the pid alone collides for two Players
		// in one process, where New would unlink the first's live socket.
		sock = filepath.Join(os.TempDir(), fmt.Sprintf("tubeamp-mpv-%d-%d.sock", os.Getpid(), sockSeq.Add(1)))
	}
	// A stale socket from a crashed run would make mpv fail to bind.
	_ = os.Remove(sock)

	format := o.YTDLFormat
	if format == "" {
		format = "bestaudio"
	}
	vol := clampVolume(o.Volume)

	cmd := exec.Command(mpvPath,
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--input-ipc-server="+sock,
		fmt.Sprintf("--volume=%d", vol),
		"--ytdl-format="+format,
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("player: start mpv: %w", err)
	}

	// Reap the child in one place: this channel closes when mpv exits, letting
	// the dial loop fail fast on early death and Close reap without a second
	// (illegal) cmd.Wait.
	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()

	conn, err := dialWithRetry(sock, connectBudget, procDone)
	if err != nil {
		// mpv never exposed the socket (or exited early): kill (harmless if it
		// already exited) and wait for the reaper goroutine.
		_ = cmd.Process.Kill()
		<-procDone
		_ = os.Remove(sock)
		return nil, fmt.Errorf("player: connect mpv ipc: %w", err)
	}

	p := newConn(conn, cmd, sock)
	p.procDone = procDone

	// Observe the properties we translate into Events. Failures here are
	// best-effort: the connection is already proven, so a transient hiccup
	// should not tear down the player.
	for i, prop := range []string{"time-pos", "duration", "pause", "volume", "mute"} {
		_, _ = p.command("observe_property", i+1, prop)
	}
	return p, nil
}

// dialWithRetry repeatedly dials the unix socket until it connects, the budget
// elapses, or the child process exits (observed via procDone) — the last lets
// New fail fast with mpv's real death instead of waiting out the full budget on
// a socket that will never appear.
func dialWithRetry(sock string, budget time.Duration, procDone <-chan struct{}) (net.Conn, error) {
	deadline := time.Now().Add(budget)
	var lastErr error
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		select {
		case <-procDone:
			if lastErr == nil {
				lastErr = errors.New("process exited")
			}
			return nil, fmt.Errorf("mpv exited before its IPC socket was ready: %w", lastErr)
		default:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("socket %q not ready: %w", sock, lastErr)
		}
		time.Sleep(connectRetry)
	}
}

// Events returns the channel of playback events. It is buffered; when full the
// player drops the oldest buffered event to make room (non-blocking) so the
// consumer always converges on the most recent state and mpv is never stalled.
// The channel is closed by Close or when the reader detects mpv has died.
func (p *Player) Events() <-chan Event { return p.events }

// Load replaces the current file with the given URL (mpv resolves it via
// yt-dlp).
func (p *Player) Load(url string) error {
	_, err := p.command("loadfile", url, "replace")
	return err
}

// TogglePause flips the paused state.
func (p *Player) TogglePause() error {
	_, err := p.command("cycle", "pause")
	return err
}

// Stop stops playback and clears the current file.
func (p *Player) Stop() error {
	_, err := p.command("stop")
	return err
}

// Seek moves the playback position by offsetSec seconds (relative; negative
// rewinds).
func (p *Player) Seek(offsetSec float64) error {
	_, err := p.command("seek", offsetSec, "relative")
	return err
}

// SetVolume sets mpv's volume, clamped to 0..120.
func (p *Player) SetVolume(pct int) error {
	_, err := p.command("set_property", "volume", clampVolume(pct))
	return err
}

// ToggleMute flips the muted state.
func (p *Player) ToggleMute() error {
	_, err := p.command("cycle", "mute")
	return err
}

// Close shuts mpv down and releases all resources. It is idempotent and safe to
// call concurrently. It best-effort sends quit, reaps the process (killing it
// after a timeout so no zombie is left), fails every pending request, ensures
// the reader goroutine has fully exited before closing the Events channel, and
// removes the socket file.
func (p *Player) Close() error {
	p.closeOnce.Do(func() {
		// 1. Signal shutdown: new commands fail fast, in-flight ones unblock.
		p.markClosed()

		// 2. Ask mpv to quit cleanly (best effort; bounded by the write
		//    deadline so a wedged socket cannot hang us here).
		p.writeQuit()

		// 3. Reap the child: give it processWait to exit, then kill. The
		//    single reaper goroutine started in New does the actual Wait.
		if p.cmd != nil {
			p.reap()
		}

		// 4. Close our socket end so the reader unblocks even if mpv ignored
		//    quit (and there is no child to reap, e.g. in tests).
		_ = p.conn.Close()

		// 5. The reader must be fully gone before we touch the channels it
		//    sends on — this is what prevents a send-on-closed-channel race.
		<-p.readerDone

		// 6. Fail any pending requests and close events (idempotent: the reader
		//    already ran finish on its way out, but Close guarantees it).
		p.finish()

		// 7. Remove the socket file we own.
		if p.sock != "" {
			_ = os.Remove(p.sock)
		}
	})
	return nil
}

// writeQuit sends a fire-and-forget quit command (we do not wait for a reply).
func (p *Player) writeQuit() {
	_ = p.write(ipcRequest{Command: []any{"quit"}, RequestID: p.nextRequestID()})
}

// reap waits for the mpv process to exit, killing it after processWait. The
// actual cmd.Wait runs in the reaper goroutine started by New (which closes
// procDone); reap only observes that channel so Wait is never called twice.
func (p *Player) reap() {
	select {
	case <-p.procDone:
	case <-time.After(processWait):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.procDone
	}
}

// clampVolume bounds a volume percentage to mpv's accepted range.
func clampVolume(v int) int {
	if v < volumeMin {
		return volumeMin
	}
	if v > volumeMax {
		return volumeMax
	}
	return v
}
