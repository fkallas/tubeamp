// Package core implements the play queue for tubeamp.
package core

import (
	"github.com/fkallas/tubeamp/internal/model"
)

// Queue is the play queue. Not goroutine-safe; owned by the UI loop.
// It maintains an ordered list of tracks and a current index.
// The index is -1 when the queue is empty or nothing is selected.
type Queue struct {
	tracks []model.Track
	index  int // -1 when nothing current
}

// NewQueue creates an empty queue with no current selection.
func NewQueue() *Queue {
	return &Queue{
		tracks: make([]model.Track, 0),
		index:  -1,
	}
}

// Items returns a defensive copy of the queue tracks.
// Modifications to the returned slice do not affect the queue.
func (q *Queue) Items() []model.Track {
	cp := make([]model.Track, len(q.tracks))
	copy(cp, q.tracks)
	return cp
}

// Len returns the number of tracks in the queue.
func (q *Queue) Len() int {
	return len(q.tracks)
}

// Index returns the index of the current track, or -1 if no track is selected.
func (q *Queue) Index() int {
	return q.index
}

// Current returns the current track and true if a track is selected,
// or (zero Track, false) if the queue is empty or no track is selected.
func (q *Queue) Current() (model.Track, bool) {
	if q.index < 0 || q.index >= len(q.tracks) {
		var zero model.Track
		return zero, false
	}
	return q.tracks[q.index], true
}

// Set replaces the queue contents with ts and sets the current index to start.
// If ts is empty, the queue becomes empty and index is set to -1.
// If start is out of range [0, len(ts)-1], it is clamped to that range.
func (q *Queue) Set(ts []model.Track, start int) {
	if len(ts) == 0 {
		q.tracks = make([]model.Track, 0)
		q.index = -1
		return
	}

	q.tracks = make([]model.Track, len(ts))
	copy(q.tracks, ts)

	// Clamp start to [0, len-1]
	if start < 0 {
		start = 0
	} else if start >= len(q.tracks) {
		start = len(q.tracks) - 1
	}
	q.index = start
}

// Append adds tracks to the end of the queue. Does not change the current index.
func (q *Queue) Append(ts ...model.Track) {
	q.tracks = append(q.tracks, ts...)
}

// InsertNext inserts tracks immediately after the current track.
// If there is no current track (index == -1), tracks are appended to the end.
func (q *Queue) InsertNext(ts ...model.Track) {
	if len(ts) == 0 {
		return
	}

	if q.index < 0 {
		// No current track, append instead
		q.Append(ts...)
		return
	}

	// Insert after current track
	insertPos := q.index + 1
	newTracks := make([]model.Track, len(q.tracks)+len(ts))
	copy(newTracks, q.tracks[:insertPos])
	copy(newTracks[insertPos:], ts)
	copy(newTracks[insertPos+len(ts):], q.tracks[insertPos:])
	q.tracks = newTracks
}

// Remove removes the track at index i.
//   - If i < current: the current index is decremented (current track shifts down by one position).
//   - If i == current: the current index stays the same numerically, now pointing to the next track
//     (or the previous track if this was the last track).
//   - If i > current: the current index is unchanged.
//
// Calling Remove with an out-of-range index is a no-op.
func (q *Queue) Remove(i int) {
	if i < 0 || i >= len(q.tracks) {
		return
	}

	q.tracks = append(q.tracks[:i], q.tracks[i+1:]...)

	if i < q.index {
		// Item before current was removed, shift current down
		q.index--
	} else if i == q.index {
		// Current item was removed
		if q.index >= len(q.tracks) {
			// Was the last item
			if len(q.tracks) == 0 {
				q.index = -1
			} else {
				q.index = len(q.tracks) - 1
			}
		}
		// Otherwise, q.index stays the same (now points to next item)
	}
	// If i > q.index, nothing changes
}

// Move moves the track at index i to index j.
// The current track selection follows the same track it pointed to before the move.
// Calling Move with out-of-range indices or i == j is a no-op.
func (q *Queue) Move(i, j int) {
	if i < 0 || i >= len(q.tracks) || j < 0 || j >= len(q.tracks) {
		return
	}

	if i == j {
		return
	}

	// Remember the VideoID of the current track to follow it through the operation
	var currentVideoID string
	currentWasValid := q.index >= 0 && q.index < len(q.tracks)
	if currentWasValid {
		currentVideoID = q.tracks[q.index].VideoID
	}

	// Remove item at i
	item := q.tracks[i]
	q.tracks = append(q.tracks[:i], q.tracks[i+1:]...)

	// Determine where to insert in the modified array
	// j refers to the position in the original array, so adjust for removal
	insertPos := j
	if i < j {
		insertPos = j - 1
	}

	// Insert at position
	if insertPos >= len(q.tracks) {
		q.tracks = append(q.tracks, item)
	} else {
		newTracks := make([]model.Track, len(q.tracks)+1)
		copy(newTracks, q.tracks[:insertPos])
		newTracks[insertPos] = item
		copy(newTracks[insertPos+1:], q.tracks[insertPos:])
		q.tracks = newTracks
	}

	// Update current index to follow the same track
	if currentWasValid {
		for idx, t := range q.tracks {
			if t.VideoID == currentVideoID {
				q.index = idx
				return
			}
		}
		// If track not found, reset index
		q.index = -1
	}
}

// JumpTo sets the current track to index i and returns it.
// If i is out of range, returns (zero Track, false) and does not change the current index.
func (q *Queue) JumpTo(i int) (model.Track, bool) {
	if i < 0 || i >= len(q.tracks) {
		var zero model.Track
		return zero, false
	}
	q.index = i
	return q.tracks[i], true
}

// Advance moves to the next track and returns it.
// If already at the last track, returns (zero Track, false) and leaves the index unchanged.
// If the queue is empty or no track is selected, returns (zero Track, false).
func (q *Queue) Advance() (model.Track, bool) {
	if q.index < 0 {
		var zero model.Track
		return zero, false
	}

	nextIdx := q.index + 1
	if nextIdx >= len(q.tracks) {
		var zero model.Track
		return zero, false
	}

	q.index = nextIdx
	return q.tracks[q.index], true
}

// Prev moves to the previous track and returns it.
// If already at the first track, returns (zero Track, false) and leaves the index unchanged.
// If the queue is empty or no track is selected, returns (zero Track, false).
func (q *Queue) Prev() (model.Track, bool) {
	if q.index <= 0 {
		var zero model.Track
		return zero, false
	}

	q.index--
	return q.tracks[q.index], true
}

// Clear empties the queue and resets the current index to -1.
func (q *Queue) Clear() {
	q.tracks = make([]model.Track, 0)
	q.index = -1
}
