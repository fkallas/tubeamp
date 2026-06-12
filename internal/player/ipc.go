package player

import (
	"encoding/json"
	"fmt"
	"time"
)

// eventBufferSize is the capacity of the Events channel. When full, the reader
// drops the oldest buffered event to make room (never blocking), so a slow UI
// can never stall mpv yet still converges on the most recent playback state.
const eventBufferSize = 64

// commandTimeout bounds how long a synchronous command waits for its reply, so
// a wedged or dead mpv can never hang a caller forever.
const commandTimeout = 2 * time.Second

// ipcRequest is a single mpv JSON-IPC command line. The command is always sent
// as an array of arguments (never a concatenated string) and tagged with a
// monotonically increasing request_id for reply correlation.
type ipcRequest struct {
	Command   []any `json:"command"`
	RequestID int   `json:"request_id"`
}

// ipcResponse is a decoded mpv JSON-IPC line. It is the union of a command
// reply (carries request_id/error/data) and an asynchronous event (carries
// event and, depending on the event, name/data/reason/file_error).
type ipcResponse struct {
	RequestID int             `json:"request_id"`
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	Event     string          `json:"event"`
	Name      string          `json:"name"`
	Reason    string          `json:"reason"`
	FileError string          `json:"file_error"`
}

// nextRequestID returns a fresh, process-unique request id (always >= 1, which
// keeps it distinct from the implicit 0 of event lines that carry no id).
func (p *Player) nextRequestID() int {
	return int(p.reqID.Add(1))
}

// readLoop is the single goroutine that owns reads on the connection. It
// decodes one JSON line at a time, routing replies to their pending channel and
// translating events onto the Events channel. It exits when the connection is
// closed (or errors); on the way out it unblocks any in-flight commands and
// closes the events channel, so a dead mpv surfaces to consumers (as a closed
// channel) instead of blocking them forever.
func (p *Player) readLoop() {
	defer func() {
		// The reader is the sole sender on events. Once it stops, mark the
		// player closed (commands fast-fail instead of waiting out the timeout)
		// and close events so the UI's listen loop observes mpv's death.
		p.markClosed()
		p.finish()
		close(p.readerDone)
	}()

	dec := json.NewDecoder(p.conn)
	for {
		var r ipcResponse
		if err := dec.Decode(&r); err != nil {
			return // connection closed or unrecoverable decode error
		}

		if r.Event != "" {
			ev, ok := mapEvent(&r)
			if !ok {
				continue
			}
			p.sendEvent(ev)
			continue
		}

		// Otherwise it is a command reply; route by request_id.
		p.mu.Lock()
		ch, ok := p.pending[r.RequestID]
		if ok {
			delete(p.pending, r.RequestID)
		}
		p.mu.Unlock()
		if ok {
			resp := r
			ch <- &resp // buffered (cap 1); never blocks
		}
	}
}

// sendEvent delivers ev to the events channel without ever blocking the reader.
// When the buffer is full it discards the oldest buffered event and retries, so
// one-shot terminal events (EvTrackEnded/EvError) are not lost behind a backlog
// of coalescable time-pos ticks.
func (p *Player) sendEvent(ev Event) {
	select {
	case p.events <- ev:
		return
	default:
	}
	// Buffer full: drop the oldest to make room, then enqueue the newest. The
	// nested non-blocking ops keep this safe even if a consumer drains
	// concurrently (we simply never block).
	select {
	case <-p.events:
	default:
	}
	select {
	case p.events <- ev:
	default:
	}
}

// command sends a request and blocks until its correlated reply arrives, the
// command times out, or the player is closed. It is safe for concurrent use.
func (p *Player) command(args ...any) (*ipcResponse, error) {
	id := p.nextRequestID()
	ch := make(chan *ipcResponse, 1)

	p.mu.Lock()
	select {
	case <-p.closed:
		p.mu.Unlock()
		return nil, ErrClosed
	default:
	}
	p.pending[id] = ch
	p.mu.Unlock()

	if err := p.write(ipcRequest{Command: args, RequestID: id}); err != nil {
		p.removePending(id)
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp == nil { // channel closed by Close: request failed
			return nil, ErrClosed
		}
		if resp.Error != "" && resp.Error != "success" {
			return resp, fmt.Errorf("mpv command %v: %s", args, resp.Error)
		}
		return resp, nil
	case <-time.After(p.cmdTimeout):
		p.removePending(id)
		return nil, ErrTimeout
	case <-p.closed:
		p.removePending(id)
		return nil, ErrClosed
	}
}

// removePending drops a request from the pending map if it is still present.
func (p *Player) removePending(id int) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// write marshals and sends a single newline-terminated IPC line. Writes are
// serialized so concurrent callers never interleave bytes on the wire.
func (p *Player) write(req ipcRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal ipc request: %w", err)
	}
	b = append(b, '\n')

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	// Bound the write: if mpv stops reading its IPC socket and the kernel send
	// buffer fills, Write would otherwise block forever holding writeMu and
	// deadlock Close (which sends quit before it can close the conn).
	_ = p.conn.SetWriteDeadline(time.Now().Add(p.cmdTimeout))
	if _, err := p.conn.Write(b); err != nil {
		return fmt.Errorf("write ipc: %w", err)
	}
	return nil
}

// mapEvent translates an mpv event line into an Event. The second return value
// is false when the line carries no event we surface (null property data, or an
// end-file with a non-eof/non-error reason such as redirect/stop/quit).
func mapEvent(r *ipcResponse) (Event, bool) {
	switch r.Event {
	case "property-change":
		switch r.Name {
		case "time-pos":
			if f, ok := floatData(r.Data); ok {
				return Event{Kind: EvTimePos, Float: f}, true
			}
		case "duration":
			if f, ok := floatData(r.Data); ok {
				return Event{Kind: EvDuration, Float: f}, true
			}
		case "pause":
			if b, ok := boolData(r.Data); ok {
				return Event{Kind: EvPause, Bool: b}, true
			}
		case "volume":
			if f, ok := floatData(r.Data); ok {
				return Event{Kind: EvVolume, Float: f}, true
			}
		case "mute":
			if b, ok := boolData(r.Data); ok {
				return Event{Kind: EvMute, Bool: b}, true
			}
		}
	case "file-loaded":
		return Event{Kind: EvFileLoaded}, true
	case "end-file":
		switch r.Reason {
		case "eof":
			return Event{Kind: EvTrackEnded}, true
		case "error":
			msg := r.FileError
			if msg == "" {
				msg = "playback error"
			}
			return Event{Kind: EvError, Str: msg}, true
			// All other reasons (redirect, stop, quit, ...) are intentionally
			// ignored — they are not real track-end conditions.
		}
	}
	return Event{}, false
}

// floatData decodes numeric property data, reporting false for absent or null
// values (e.g. time-pos/duration before a file is loaded).
func floatData(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// boolData decodes boolean property data, reporting false for absent or null
// values.
func boolData(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, false
	}
	return b, true
}
