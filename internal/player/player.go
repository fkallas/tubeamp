// Package player is a thin client for a *persistent, detached* mpv process that
// owns the playback playlist. mpv is spawned once (setsid, stdio to /dev/null,
// the parent never waits on it), listens on a fixed JSON-IPC unix socket, and
// keeps playing after the TUI exits. Any number of clients — the TUI or the
// one-shot CLI control commands — attach to the same socket, drive the shared
// mpv playlist, and detach again without disturbing playback.
//
// mpv resolves YouTube URLs itself via its yt-dlp hook, so this package never
// handles raw stream URLs. The ordered, richly-typed queue is mirrored to a
// sidecar (queue.json) on every mutation so a re-attaching client can rebuild
// full state via Snapshot.
package player

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fkallas/tubeamp/internal/config"
	"github.com/fkallas/tubeamp/internal/model"
)

// Sentinel errors returned by command helpers and New.
var (
	// ErrClosed is returned by commands issued on a closed (detached) player,
	// or pending when Close races an in-flight request.
	ErrClosed = errors.New("player: closed")
	// ErrTimeout is returned when mpv does not reply within commandTimeout.
	ErrTimeout = errors.New("player: command timed out")
	// ErrNotRunning is returned by New when Options.AttachOnly is set and no
	// live mpv daemon is listening on the socket. CLI control paths use it to
	// tell "daemon down" apart from other failures.
	ErrNotRunning = errors.New("player: mpv daemon not running")
)

const (
	// connectBudget is the total time we wait for the IPC socket to appear
	// after spawning mpv (or while waiting on another client's spawn).
	connectBudget = 5 * time.Second
	// connectRetry is the delay between socket dial attempts.
	connectRetry = 50 * time.Millisecond
	// quitExitBudget bounds how long Quit waits for the daemon to die after
	// each escalation step (quit IPC, SIGTERM, SIGKILL).
	quitExitBudget = 2 * time.Second
	// quitPollInterval is the delay between liveness probes while waiting for
	// the daemon to exit.
	quitPollInterval = 25 * time.Millisecond
	// volumeMin and volumeMax bound SetVolume (mpv accepts 0..max-volume).
	volumeMin = 0
	volumeMax = 120
)

// Options configures a Player. Zero values fall back to the documented
// defaults.
type Options struct {
	MPVPath    string // "" => "mpv" (resolved via PATH)
	SocketPath string // "" => config.DataDir()/mpv.sock
	YTDLFormat string // "" => "bestaudio"
	Volume     int    // initial volume passed to mpv --volume (spawn only)
	// AttachOnly makes New attach to an existing daemon and never spawn one.
	// When no daemon is listening it returns ErrNotRunning. CLI control
	// commands set this so they never accidentally start a background mpv.
	AttachOnly bool
}

// EventKind identifies which playback state an Event reports.
type EventKind int

const (
	EvTimePos     EventKind = iota // Float seconds (current position)
	EvDuration                     // Float seconds (track length)
	EvPause                        // Bool (paused?)
	EvVolume                       // Float 0-100 (mpv volume)
	EvMute                         // Bool (muted?)
	EvFileLoaded                   // a new track started playing
	EvTrackEnded                   // end-file with reason "eof"
	EvError                        // Str message (incl. end-file reason "error")
	EvPlaylistPos                  // Int: current playlist index (-1 when idle)
)

// Event is a single playback state change emitted by mpv. Only the field
// relevant to Kind is meaningful.
type Event struct {
	Kind  EventKind
	Float float64
	Bool  bool
	Str   string
	Int   int
}

// Player is a client attached to the persistent mpv daemon. It is safe for
// concurrent use by multiple goroutines.
type Player struct {
	conn      net.Conn
	sock      string // socket path (for Quit cleanup); "" => leave alone
	lock      string // spawn lock-file path (for Quit cleanup)
	queuePath string // sidecar queue.json path; "" => persistence disabled
	pid       int    // spawned mpv pid; 0 when we attached to an existing one

	reqID   atomic.Int64
	writeMu sync.Mutex // serializes writes on conn

	cmdTimeout time.Duration // bound on a synchronous command's reply wait
	quitBudget time.Duration // per-step wait for the daemon to exit in Quit

	mu      sync.Mutex                // guards pending and the closed check
	pending map[int]chan *ipcResponse // request_id -> reply channel
	closed  chan struct{}             // closed once shutdown begins

	events     chan Event
	readerDone chan struct{} // closed when readLoop returns

	closeStateOnce sync.Once // guards close(closed)
	finishOnce     sync.Once // guards failing pending + close(events)
	closeOnce      sync.Once // guards the full Close sequence

	plMu     sync.Mutex               // serializes playlist mutations + sidecar writes
	tracksMu sync.RWMutex             // guards tracks
	tracks   []model.Track            // in-memory ordered queue model (rich metadata)
	urlFunc  func(model.Track) string // test seam; nil => model.Track.URL
}

// markClosed closes the closed channel exactly once. After this, new commands
// fail fast (ErrClosed) and in-flight ones unblock. It is called both by Close
// and by the reader goroutine when it detects the connection dropping.
func (p *Player) markClosed() {
	p.closeStateOnce.Do(func() {
		p.mu.Lock()
		close(p.closed)
		p.mu.Unlock()
	})
}

// finish fails every pending request and closes the events channel, exactly
// once. It must only run once the reader goroutine is no longer sending on
// events, so it can never race a send onto a closed channel.
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
// reader goroutine. Path/pid fields are filled in by the caller (New) or left
// zero (tests that drive a fake socket directly).
func newConn(conn net.Conn) *Player {
	p := &Player{
		conn:       conn,
		cmdTimeout: commandTimeout,
		quitBudget: quitExitBudget,
		pending:    make(map[int]chan *ipcResponse),
		closed:     make(chan struct{}),
		events:     make(chan Event, eventBufferSize),
		readerDone: make(chan struct{}),
	}
	go p.readLoop()
	return p
}

// New connects the caller to the mpv daemon. It first tries to ATTACH to an
// existing socket; if that succeeds no process is spawned. Otherwise (unless
// AttachOnly is set) it SPAWNS a detached mpv — guarded by an O_EXCL lock file
// so two concurrent clients never start two daemons — waits for the socket, and
// attaches. With AttachOnly and no live daemon it returns ErrNotRunning.
func New(o Options) (*Player, error) {
	sock := o.SocketPath
	if sock == "" {
		sock = filepath.Join(config.DataDir(), "mpv.sock")
	}
	lock := sock + ".lock"
	queuePath := filepath.Join(filepath.Dir(sock), "queue.json")

	// Fast path: a daemon is already listening — just attach.
	if conn, err := net.Dial("unix", sock); err == nil {
		return finishAttach(conn, sock, lock, queuePath), nil
	}
	if o.AttachOnly {
		return nil, ErrNotRunning
	}

	bin := o.MPVPath
	if bin == "" {
		bin = "mpv"
	}
	mpvPath, err := exec.LookPath(bin)
	if err != nil {
		return nil, fmt.Errorf("player: mpv binary %q not found: %w", bin, err)
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		return nil, fmt.Errorf("player: create data dir: %w", err)
	}
	return spawnOrAttach(mpvPath, o, sock, lock, queuePath)
}

// finishAttach wraps a live connection: observe the properties we surface and
// seed the in-memory model from the live playlist reconciled with the sidecar.
func finishAttach(conn net.Conn, sock, lock, queuePath string) *Player {
	p := newConn(conn)
	p.sock, p.lock, p.queuePath = sock, lock, queuePath
	p.observeProps()
	if ts, ok := p.reconcileTracks(); ok {
		p.setTracks(ts)
	}
	return p
}

// spawnOrAttach claims the spawn lock and starts mpv, or — if another client is
// already spawning — waits for the socket and attaches. The lock records the
// daemon's pid: a lock whose recorded owner is dead (crashed daemon) is
// reclaimed immediately instead of waiting out the connect budget, while a
// lock with a live owner is never stolen — stealing it could spawn a second
// daemon and orphan the first, unreachable, one. A lock with no readable pid
// (the spawner crashed before recording it) is stolen once after the budget
// elapses, as before.
func spawnOrAttach(mpvPath string, o Options, sock, lock, queuePath string) (*Player, error) {
	deadline := time.Now().Add(connectBudget)
	stole := false
	for {
		lf, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			return spawnDetached(mpvPath, o, sock, lock, queuePath, lf)
		}
		// Another client holds the lock (spawning) — try its socket.
		if conn, derr := net.Dial("unix", sock); derr == nil {
			return finishAttach(conn, sock, lock, queuePath), nil
		}
		if !stole && reclaimDeadLock(lock) {
			stole = true
			deadline = time.Now().Add(connectBudget)
			continue
		}
		if time.Now().After(deadline) {
			if stole {
				return nil, fmt.Errorf("player: mpv did not start (stale lock %q)", lock)
			}
			if pid, ok := lockPid(lock); ok && pidAlive(pid) {
				return nil, fmt.Errorf("player: spawn lock %q held by running mpv (pid %d) but its socket never appeared", lock, pid)
			}
			// The lock holder vanished before recording a pid: reclaim it once.
			_ = os.Remove(lock)
			stole = true
			deadline = time.Now().Add(connectBudget)
		}
		time.Sleep(connectRetry)
	}
}

// lockPid reads the daemon pid recorded in the spawn lock file. ok is false
// when the file is missing, empty (the spawner has not written it yet), or
// malformed.
func lockPid(lock string) (int, bool) {
	if lock == "" {
		return 0, false
	}
	data, err := os.ReadFile(lock)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// pidAlive reports whether a process with the given pid exists. EPERM still
// proves existence (we just may not signal it).
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// reclaimDeadLock removes the spawn lock if the pid it records is no longer
// alive (a crashed daemon). The lock is moved aside with an atomic rename
// first so two clients racing to reclaim the same stale lock cannot both
// "succeed" and spawn two daemons. It reports whether the lock was reclaimed.
func reclaimDeadLock(lock string) bool {
	pid, ok := lockPid(lock)
	if !ok || pidAlive(pid) {
		return false
	}
	stale := fmt.Sprintf("%s.stale.%d", lock, os.Getpid())
	if err := os.Rename(lock, stale); err != nil {
		return false
	}
	_ = os.Remove(stale)
	return true
}

// spawnDetached starts mpv fully detached (setsid, stdio to /dev/null, the
// process Released so we never Wait or signal it). The lock FILE is kept on
// disk for the daemon's lifetime — it is the spawn guard, removed only by Quit.
func spawnDetached(mpvPath string, o Options, sock, lock, queuePath string, lf *os.File) (*Player, error) {
	// A stale socket from a crashed run would make mpv fail to bind.
	_ = os.Remove(sock)

	format := o.YTDLFormat
	if format == "" {
		format = "bestaudio"
	}
	vol := clampVolume(o.Volume)

	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		lf.Close()
		_ = os.Remove(lock)
		return nil, fmt.Errorf("player: open %s: %w", os.DevNull, err)
	}

	cmd := exec.Command(mpvPath,
		"--idle=yes",
		"--no-video",
		"--no-terminal",
		"--input-ipc-server="+sock,
		fmt.Sprintf("--volume=%d", vol),
		"--ytdl-format="+format,
		// Gapless transitions: mpv prefetches (resolves + opens) the next
		// playlist entry slightly before the current one ends, then crossfeeds
		// without re-initialising the audio chain when codecs match ("weak").
		"--prefetch-playlist=yes",
		"--gapless-audio=weak",
	)
	cmd.Stdin = devnull
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		devnull.Close()
		lf.Close()
		_ = os.Remove(lock)
		return nil, fmt.Errorf("player: start mpv: %w", err)
	}
	pid := cmd.Process.Pid
	// Detach: hand the child to init. We never Wait and never kill it.
	_ = cmd.Process.Release()
	devnull.Close()
	// Record the pid in the lock file (diagnostics) and keep the file on disk.
	_, _ = lf.WriteString(strconv.Itoa(pid) + "\n")
	lf.Close()

	conn, err := dialWithRetry(sock, connectBudget)
	if err != nil {
		// mpv never exposed its socket — drop the lock so a later run retries.
		_ = os.Remove(lock)
		return nil, fmt.Errorf("player: connect mpv ipc: %w", err)
	}

	p := newConn(conn)
	p.sock, p.lock, p.queuePath, p.pid = sock, lock, queuePath, pid
	p.observeProps()
	// Fresh daemon: empty playlist. Reset the sidecar so a stale queue.json from
	// a previous session does not mislead the next re-attach.
	p.setTracks(nil)
	_ = p.persist(nil)
	return p, nil
}

// dialWithRetry repeatedly dials the unix socket until it connects or the budget
// elapses.
func dialWithRetry(sock string, budget time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(budget)
	var lastErr error
	for {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("socket %q not ready: %w", sock, lastErr)
		}
		time.Sleep(connectRetry)
	}
}

// observeProps subscribes to every property we translate into Events. Failures
// are best-effort: the connection is already proven. playlist-playing-pos is
// deliberately NOT observed: it emits a transient -1 between tracks on every
// auto-advance (while the next entry loads), which consumers would mistake for
// end-of-queue idle; playlist-pos moves directly from one index to the next
// and only reports -1 when the player is truly idle.
func (p *Player) observeProps() {
	props := []string{
		"time-pos", "duration", "pause", "volume", "mute",
		"playlist-pos",
	}
	for i, prop := range props {
		_, _ = p.command("observe_property", i+1, prop)
	}
}

// Events returns the channel of playback events. It is buffered; when full the
// player drops the oldest buffered event to make room (non-blocking) so the
// consumer always converges on the most recent state and mpv is never stalled.
// The channel is closed by Close or when the reader detects the conn dropped.
func (p *Player) Events() <-chan Event { return p.events }

// Load replaces the current file with the given URL (mpv resolves it via
// yt-dlp). Prefer the Playlist* methods for queue-backed playback; Load is a
// single-file convenience that does not update the sidecar.
func (p *Player) Load(url string) error {
	_, err := p.command("loadfile", url, "replace")
	return err
}

// TogglePause flips the paused state.
func (p *Player) TogglePause() error {
	_, err := p.command("cycle", "pause")
	return err
}

// Stop stops playback. mpv's stop also clears the playlist, so the in-memory
// tracks mirror and the queue.json sidecar are reset to match — otherwise a
// long-lived client would keep issuing index-based mutations against playlist
// entries that no longer exist.
func (p *Player) Stop() error {
	p.plMu.Lock()
	defer p.plMu.Unlock()
	if _, err := p.command("stop"); err != nil {
		return err
	}
	p.setTracks(nil)
	return p.persist(nil)
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

// Close DETACHES this client from the daemon: it stops the reader goroutine,
// fails pending requests, closes the Events channel, and closes the connection.
// mpv, its socket, and playback are left untouched — that is what lets playback
// survive the TUI exiting. Idempotent and safe to call concurrently. Use Quit to
// actually terminate the daemon.
func (p *Player) Close() error {
	p.closeOnce.Do(func() {
		// 1. Signal shutdown: new commands fail fast, in-flight ones unblock.
		p.markClosed()
		// 2. Close our socket end; the reader unblocks on the resulting error.
		_ = p.conn.Close()
		// 3. The reader must be fully gone before we touch the channels it
		//    sends on — this is what prevents a send-on-closed-channel race.
		<-p.readerDone
		// 4. Fail any pending requests and close events (idempotent).
		p.finish()
	})
	return nil
}

// Quit terminates the daemon: it asks mpv to quit, detaches this client,
// verifies the process actually exits — escalating to SIGTERM then SIGKILL
// when the quit command was lost (e.g. a wedged mpv that stopped reading its
// IPC socket) — and removes the socket and lock files so the next New spawns a
// fresh daemon. Without the verification, an mpv that ignored quit would keep
// playing with its socket already deleted, unreachable by any future client.
// Used by the CLI -kill flag.
func (p *Player) Quit() error {
	// The spawning client knows the pid directly; an attached client reads back
	// the pid the spawner recorded in the lock file.
	pid := p.pid
	if pid <= 0 {
		pid, _ = lockPid(p.lock)
	}
	// Ask mpv to exit while the connection is still live (best effort).
	_ = p.write(ipcRequest{Command: []any{"quit"}, RequestID: p.nextRequestID()})
	// Detach our client side.
	_ = p.Close()
	// Confirm the daemon died; escalate if it did not.
	if pid > 0 && !waitProcessExit(pid, p.quitBudget) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		if !waitProcessExit(pid, p.quitBudget) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			_ = waitProcessExit(pid, p.quitBudget)
		}
	}
	// Remove the daemon's on-disk artifacts.
	var firstErr error
	for _, f := range []string{p.sock, p.lock} {
		if f == "" {
			continue
		}
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// waitProcessExit polls until the process is gone or the budget elapses.
func waitProcessExit(pid int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if processGone(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(quitPollInterval)
	}
}

// processGone reports whether pid no longer refers to a live process. A child
// of this process is reaped via wait4(WNOHANG) — a dead-but-unreaped child is
// a zombie that kill(pid, 0) would still report as alive; for non-children it
// falls back to the kill(pid, 0) existence probe.
func processGone(pid int) bool {
	var ws syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
	if wpid == pid {
		return true // our child: reaped its exit status just now
	}
	if err == syscall.ECHILD {
		return syscall.Kill(pid, 0) != nil
	}
	return false // still running (wpid == 0) or a transient wait error
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
