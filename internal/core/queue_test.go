package core

import (
	"testing"
	"time"

	"github.com/fkallas/tubeamp/internal/model"
)

// Helper to create a test track
func track(id, title string) model.Track {
	return model.Track{
		VideoID:  id,
		Title:    title,
		Artists:  []string{"Artist"},
		Album:    "Album",
		Duration: time.Second,
		ThumbURL: "",
	}
}

// Helper to compare two tracks
func tracksEqual(t1, t2 model.Track) bool {
	if t1.VideoID != t2.VideoID {
		return false
	}
	if t1.Title != t2.Title {
		return false
	}
	// For tests, we only care about VideoID and Title
	return true
}

// Helper to compare track slices
func trackSlicesEqual(a, b []model.Track) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !tracksEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestNewQueue(t *testing.T) {
	q := NewQueue()
	if q.Len() != 0 {
		t.Errorf("NewQueue: expected len 0, got %d", q.Len())
	}
	if q.Index() != -1 {
		t.Errorf("NewQueue: expected index -1, got %d", q.Index())
	}
	if _, ok := q.Current(); ok {
		t.Errorf("NewQueue: expected no current track")
	}
}

func TestItems(t *testing.T) {
	q := NewQueue()
	tracks := []model.Track{track("1", "A"), track("2", "B"), track("3", "C")}
	q.Set(tracks, 1)

	items := q.Items()
	if !trackSlicesEqual(items, tracks) {
		t.Errorf("Items: got unexpected tracks")
	}

	// Verify it's a defensive copy
	items[0].Title = "Modified"
	if q.Items()[0].Title == "Modified" {
		t.Errorf("Items: should be a defensive copy")
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		name            string
		tracks          []model.Track
		start           int
		expectedLen     int
		expectedIndex   int
		expectedCurrent bool
	}{
		{
			name:          "empty tracks",
			tracks:        []model.Track{},
			start:         0,
			expectedLen:   0,
			expectedIndex: -1,
		},
		{
			name:            "single track, start 0",
			tracks:          []model.Track{track("1", "A")},
			start:           0,
			expectedLen:     1,
			expectedIndex:   0,
			expectedCurrent: true,
		},
		{
			name:            "three tracks, start 1",
			tracks:          []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			start:           1,
			expectedLen:     3,
			expectedIndex:   1,
			expectedCurrent: true,
		},
		{
			name:            "start negative, clamped to 0",
			tracks:          []model.Track{track("1", "A"), track("2", "B")},
			start:           -5,
			expectedLen:     2,
			expectedIndex:   0,
			expectedCurrent: true,
		},
		{
			name:            "start beyond length, clamped to len-1",
			tracks:          []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			start:           10,
			expectedLen:     3,
			expectedIndex:   2,
			expectedCurrent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			q.Set(tt.tracks, tt.start)

			if q.Len() != tt.expectedLen {
				t.Errorf("Len: expected %d, got %d", tt.expectedLen, q.Len())
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}

			if tt.expectedCurrent {
				if curr, ok := q.Current(); !ok {
					t.Errorf("Current: expected track, got none")
				} else if !tracksEqual(curr, tt.tracks[tt.expectedIndex]) {
					t.Errorf("Current: wrong track")
				}
			} else {
				if _, ok := q.Current(); ok {
					t.Errorf("Current: expected no track")
				}
			}
		})
	}
}

func TestAppend(t *testing.T) {
	tests := []struct {
		name          string
		initial       []model.Track
		initialIndex  int
		append        []model.Track
		expectedLen   int
		expectedIndex int
	}{
		{
			name:          "append to empty queue",
			initial:       []model.Track{},
			initialIndex:  -1,
			append:        []model.Track{track("1", "A"), track("2", "B")},
			expectedLen:   2,
			expectedIndex: -1, // Index doesn't change
		},
		{
			name:          "append to non-empty queue",
			initial:       []model.Track{track("1", "A")},
			initialIndex:  0,
			append:        []model.Track{track("2", "B"), track("3", "C")},
			expectedLen:   3,
			expectedIndex: 0, // Index doesn't change
		},
		{
			name:          "append empty list",
			initial:       []model.Track{track("1", "A")},
			initialIndex:  0,
			append:        []model.Track{},
			expectedLen:   1,
			expectedIndex: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			q.Append(tt.append...)

			if q.Len() != tt.expectedLen {
				t.Errorf("Len: expected %d, got %d", tt.expectedLen, q.Len())
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
		})
	}
}

func TestInsertNext(t *testing.T) {
	tests := []struct {
		name           string
		initial        []model.Track
		initialIndex   int
		insert         []model.Track
		expectedLen    int
		expectedIndex  int
		expectedTracks []model.Track
	}{
		{
			name:           "insert next on empty queue",
			initial:        []model.Track{},
			initialIndex:   -1,
			insert:         []model.Track{track("1", "A")},
			expectedLen:    1,
			expectedIndex:  -1,
			expectedTracks: []model.Track{track("1", "A")},
		},
		{
			name:          "insert next after first of three",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  0,
			insert:        []model.Track{track("x", "X"), track("y", "Y")},
			expectedLen:   5,
			expectedIndex: 0,
			expectedTracks: []model.Track{
				track("1", "A"), track("x", "X"), track("y", "Y"), track("2", "B"), track("3", "C"),
			},
		},
		{
			name:          "insert next after last",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  2,
			insert:        []model.Track{track("x", "X")},
			expectedLen:   4,
			expectedIndex: 2,
			expectedTracks: []model.Track{
				track("1", "A"), track("2", "B"), track("3", "C"), track("x", "X"),
			},
		},
		{
			name:           "insert empty list",
			initial:        []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:   0,
			insert:         []model.Track{},
			expectedLen:    2,
			expectedIndex:  0,
			expectedTracks: []model.Track{track("1", "A"), track("2", "B")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			q.InsertNext(tt.insert...)

			if q.Len() != tt.expectedLen {
				t.Errorf("Len: expected %d, got %d", tt.expectedLen, q.Len())
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
			if !trackSlicesEqual(q.Items(), tt.expectedTracks) {
				t.Errorf("Tracks: unexpected result")
			}
		})
	}
}

func TestRemove(t *testing.T) {
	tests := []struct {
		name           string
		initial        []model.Track
		initialIndex   int
		removeIndex    int
		expectedLen    int
		expectedIndex  int
		expectedTracks []model.Track
	}{
		{
			name:          "remove from empty queue",
			initial:       []model.Track{},
			initialIndex:  -1,
			removeIndex:   0,
			expectedLen:   0,
			expectedIndex: -1,
		},
		{
			name:           "remove out of range",
			initial:        []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:   0,
			removeIndex:    10,
			expectedLen:    2,
			expectedIndex:  0,
			expectedTracks: []model.Track{track("1", "A"), track("2", "B")},
		},
		{
			name:           "remove before current",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:   2,
			removeIndex:    0,
			expectedLen:    2,
			expectedIndex:  1, // 2 - 1 = 1, points to C
			expectedTracks: []model.Track{track("2", "B"), track("3", "C")},
		},
		{
			name:           "remove current (not last)",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:   1,
			removeIndex:    1,
			expectedLen:    2,
			expectedIndex:  1, // stays same, now points to C
			expectedTracks: []model.Track{track("1", "A"), track("3", "C")},
		},
		{
			name:           "remove current (last item)",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:   2,
			removeIndex:    2,
			expectedLen:    2,
			expectedIndex:  1, // becomes len-1 = 1, points to B
			expectedTracks: []model.Track{track("1", "A"), track("2", "B")},
		},
		{
			name:           "remove only item",
			initial:        []model.Track{track("1", "A")},
			initialIndex:   0,
			removeIndex:    0,
			expectedLen:    0,
			expectedIndex:  -1, // queue empty
			expectedTracks: []model.Track{},
		},
		{
			name:           "remove after current",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:   0,
			removeIndex:    2,
			expectedLen:    2,
			expectedIndex:  0, // unchanged
			expectedTracks: []model.Track{track("1", "A"), track("2", "B")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			q.Remove(tt.removeIndex)

			if q.Len() != tt.expectedLen {
				t.Errorf("Len: expected %d, got %d", tt.expectedLen, q.Len())
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
			if !trackSlicesEqual(q.Items(), tt.expectedTracks) {
				t.Errorf("Tracks: unexpected result")
			}
		})
	}
}

func TestMove(t *testing.T) {
	tests := []struct {
		name           string
		initial        []model.Track
		initialIndex   int
		fromIndex      int
		toIndex        int
		expectedLen    int
		expectedIndex  int
		expectedTracks []model.Track
	}{
		{
			name:          "move on empty queue",
			initial:       []model.Track{},
			initialIndex:  -1,
			fromIndex:     0,
			toIndex:       1,
			expectedLen:   0,
			expectedIndex: -1,
		},
		{
			name:           "move out of range",
			initial:        []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:   0,
			fromIndex:      10,
			toIndex:        0,
			expectedLen:    2,
			expectedIndex:  0,
			expectedTracks: []model.Track{track("1", "A"), track("2", "B")},
		},
		{
			name:           "move same position",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:   1,
			fromIndex:      1,
			toIndex:        1,
			expectedLen:    3,
			expectedIndex:  1,
			expectedTracks: []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
		},
		{
			name:           "move current to later position",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D")},
			initialIndex:   1,
			fromIndex:      1,
			toIndex:        3,
			expectedLen:    4,
			expectedIndex:  2, // B moves to position 3, but after removal it's 2
			expectedTracks: []model.Track{track("1", "A"), track("3", "C"), track("2", "B"), track("4", "D")},
		},
		{
			name:           "move current to earlier position",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D")},
			initialIndex:   3,
			fromIndex:      3,
			toIndex:        0,
			expectedLen:    4,
			expectedIndex:  0,
			expectedTracks: []model.Track{track("4", "D"), track("1", "A"), track("2", "B"), track("3", "C")},
		},
		{
			name:           "move before current forward",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D")},
			initialIndex:   3,
			fromIndex:      0,
			toIndex:        2,
			expectedLen:    4,
			expectedIndex:  3, // current points to D, which stays at index 3 after A moves
			expectedTracks: []model.Track{track("2", "B"), track("1", "A"), track("3", "C"), track("4", "D")},
		},
		{
			name:           "move after current backward",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D")},
			initialIndex:   0,
			fromIndex:      3,
			toIndex:        1,
			expectedLen:    4,
			expectedIndex:  0, // not affected by removal or insertion
			expectedTracks: []model.Track{track("1", "A"), track("4", "D"), track("2", "B"), track("3", "C")},
		},
		{
			name:           "move across current forward",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D"), track("5", "E")},
			initialIndex:   1,
			fromIndex:      0,
			toIndex:        3,
			expectedLen:    5,
			expectedIndex:  0, // removed at 0, current at 1 > 0, so 1-1=0; then insert at 2 (after removal), current at 0 < 2, so stays 0
			expectedTracks: []model.Track{track("2", "B"), track("3", "C"), track("1", "A"), track("4", "D"), track("5", "E")},
		},
		{
			name:           "move across current backward",
			initial:        []model.Track{track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D")},
			initialIndex:   2,
			fromIndex:      3,
			toIndex:        0,
			expectedLen:    4,
			expectedIndex:  3, // removed at 3 > current 2, so 2 stays; then insert at 0, current 2 >= 0, so 2+1=3
			expectedTracks: []model.Track{track("4", "D"), track("1", "A"), track("2", "B"), track("3", "C")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			q.Move(tt.fromIndex, tt.toIndex)

			if q.Len() != tt.expectedLen {
				t.Errorf("Len: expected %d, got %d", tt.expectedLen, q.Len())
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
			if !trackSlicesEqual(q.Items(), tt.expectedTracks) {
				t.Errorf("Tracks: unexpected result")
			}
		})
	}
}

func TestJumpTo(t *testing.T) {
	tests := []struct {
		name          string
		initial       []model.Track
		initialIndex  int
		jumpToIndex   int
		expectedIndex int
		shouldSucceed bool
	}{
		{
			name:          "jump on empty queue",
			initial:       []model.Track{},
			initialIndex:  -1,
			jumpToIndex:   0,
			expectedIndex: -1,
			shouldSucceed: false,
		},
		{
			name:          "jump to valid index",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  0,
			jumpToIndex:   2,
			expectedIndex: 2,
			shouldSucceed: true,
		},
		{
			name:          "jump to negative index",
			initial:       []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:  0,
			jumpToIndex:   -1,
			expectedIndex: 0,
			shouldSucceed: false,
		},
		{
			name:          "jump beyond length",
			initial:       []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:  0,
			jumpToIndex:   10,
			expectedIndex: 0,
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			_, ok := q.JumpTo(tt.jumpToIndex)

			if ok != tt.shouldSucceed {
				t.Errorf("JumpTo: expected success=%v, got %v", tt.shouldSucceed, ok)
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
		})
	}
}

func TestAdvance(t *testing.T) {
	tests := []struct {
		name          string
		initial       []model.Track
		initialIndex  int
		expectedIndex int
		shouldSucceed bool
	}{
		{
			name:          "advance on empty queue",
			initial:       []model.Track{},
			initialIndex:  -1,
			expectedIndex: -1,
			shouldSucceed: false,
		},
		{
			name:          "advance from middle",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  1,
			expectedIndex: 2,
			shouldSucceed: true,
		},
		{
			name:          "advance at last track",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  2,
			expectedIndex: 2,
			shouldSucceed: false,
		},
		{
			name:          "advance from first",
			initial:       []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:  0,
			expectedIndex: 1,
			shouldSucceed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			_, ok := q.Advance()

			if ok != tt.shouldSucceed {
				t.Errorf("Advance: expected success=%v, got %v", tt.shouldSucceed, ok)
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
		})
	}
}

func TestPrev(t *testing.T) {
	tests := []struct {
		name          string
		initial       []model.Track
		initialIndex  int
		expectedIndex int
		shouldSucceed bool
	}{
		{
			name:          "prev on empty queue",
			initial:       []model.Track{},
			initialIndex:  -1,
			expectedIndex: -1,
			shouldSucceed: false,
		},
		{
			name:          "prev from middle",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  1,
			expectedIndex: 0,
			shouldSucceed: true,
		},
		{
			name:          "prev at first track",
			initial:       []model.Track{track("1", "A"), track("2", "B"), track("3", "C")},
			initialIndex:  0,
			expectedIndex: 0,
			shouldSucceed: false,
		},
		{
			name:          "prev from last",
			initial:       []model.Track{track("1", "A"), track("2", "B")},
			initialIndex:  1,
			expectedIndex: 0,
			shouldSucceed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.initial) > 0 {
				q.Set(tt.initial, tt.initialIndex)
			}

			_, ok := q.Prev()

			if ok != tt.shouldSucceed {
				t.Errorf("Prev: expected success=%v, got %v", tt.shouldSucceed, ok)
			}
			if q.Index() != tt.expectedIndex {
				t.Errorf("Index: expected %d, got %d", tt.expectedIndex, q.Index())
			}
		})
	}
}

func TestSetIndex(t *testing.T) {
	tests := []struct {
		name     string
		tracks   []model.Track
		set      int
		expected int
	}{
		{"empty queue clamps to -1", nil, 2, -1},
		{"valid index", []model.Track{track("1", "A"), track("2", "B"), track("3", "C")}, 2, 2},
		{"idle (-1) preserved when tracks present", []model.Track{track("1", "A"), track("2", "B")}, -1, -1},
		{"below -1 clamps to -1", []model.Track{track("1", "A"), track("2", "B")}, -5, -1},
		{"beyond end clamps to len-1", []model.Track{track("1", "A"), track("2", "B")}, 9, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if len(tt.tracks) > 0 {
				q.Set(tt.tracks, 0)
			}
			q.SetIndex(tt.set)
			if q.Index() != tt.expected {
				t.Errorf("SetIndex(%d): index = %d, want %d", tt.set, q.Index(), tt.expected)
			}
		})
	}
}

func TestClear(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "clear non-empty queue"},
		{name: "clear empty queue"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := NewQueue()
			if tt.name == "clear non-empty queue" {
				q.Set([]model.Track{track("1", "A"), track("2", "B")}, 1)
			}

			q.Clear()

			if q.Len() != 0 {
				t.Errorf("Len: expected 0, got %d", q.Len())
			}
			if q.Index() != -1 {
				t.Errorf("Index: expected -1, got %d", q.Index())
			}
			if items := q.Items(); len(items) != 0 {
				t.Errorf("Items: expected empty, got %d items", len(items))
			}
		})
	}
}

// Edge case: remove current when it's the last item, then check that we can navigate
func TestRemoveCurrentLastThenNavigate(t *testing.T) {
	q := NewQueue()
	q.Set([]model.Track{track("1", "A"), track("2", "B"), track("3", "C")}, 2)

	q.Remove(2) // Remove the current (last) item
	if q.Index() != 1 {
		t.Errorf("After remove current last: expected index 1, got %d", q.Index())
	}

	curr, ok := q.Current()
	if !ok || !tracksEqual(curr, track("2", "B")) {
		t.Errorf("After remove current last: expected B")
	}

	// Try to advance from B (now the last item) - should fail
	_, ok = q.Advance()
	if ok {
		t.Errorf("After remove current last and advance: should fail at end")
	}

	// But we should be able to go back to A
	prev, ok := q.Prev()
	if !ok || !tracksEqual(prev, track("1", "A")) {
		t.Errorf("After remove current last and prev: expected A")
	}
}

// Edge case: multiple removes affecting current
func TestMultipleRemoves(t *testing.T) {
	q := NewQueue()
	q.Set([]model.Track{
		track("1", "A"), track("2", "B"), track("3", "C"),
		track("4", "D"), track("5", "E"),
	}, 2) // Current is C

	// Remove before current
	q.Remove(0) // Remove A, current shifts to 1, points to C
	curr, ok := q.Current()
	if q.Index() != 1 || !ok || !tracksEqual(curr, track("3", "C")) {
		t.Error("After removing A: current should be at C")
	}

	// Remove after current
	q.Remove(3) // Remove E, current unchanged
	if q.Index() != 1 {
		t.Errorf("After removing E: expected index 1, got %d", q.Index())
	}
}

// Edge case: move around current multiple times
func TestMoveMultipleTimes(t *testing.T) {
	q := NewQueue()
	tracks := []model.Track{
		track("1", "A"), track("2", "B"), track("3", "C"),
		track("4", "D"), track("5", "E"),
	}
	q.Set(tracks, 2) // Current is C

	// Move A to after C
	q.Move(0, 2) // A moves to position 2 (after removal it's 1)
	// [B, A, C, D, E] with current still on C which is now at index 2
	curr, ok := q.Current()
	if q.Index() != 2 || !ok || !tracksEqual(curr, track("3", "C")) {
		t.Error("After first move: current should still be C")
	}

	// Move E to before C
	q.Move(4, 1) // Move E (was at 4) to position 1
	// [B, E, A, C, D] with current still on C which is now at index 3
	curr, ok = q.Current()
	if q.Index() != 3 || !ok || !tracksEqual(curr, track("3", "C")) {
		t.Error("After second move: current should still be C")
	}
}

// Edge case: operations on queue with single item
func TestSingleItemOperations(t *testing.T) {
	q := NewQueue()
	q.Set([]model.Track{track("1", "A")}, 0)

	// Try to advance
	_, ok := q.Advance()
	if ok {
		t.Error("Expected Advance to fail at single item")
	}
	if q.Index() != 0 {
		t.Errorf("Advance should not change index, got %d", q.Index())
	}

	// Try to prev
	_, ok = q.Prev()
	if ok {
		t.Error("Expected Prev to fail at single item")
	}
	if q.Index() != 0 {
		t.Errorf("Prev should not change index, got %d", q.Index())
	}

	// Remove the single item
	q.Remove(0)
	if q.Len() != 0 || q.Index() != -1 {
		t.Error("After removing single item, queue should be empty with index -1")
	}
}

// Edge case: insert next at end, then advance
func TestInsertNextAtEndThenAdvance(t *testing.T) {
	q := NewQueue()
	q.Set([]model.Track{track("1", "A"), track("2", "B")}, 1) // Current is B

	q.InsertNext(track("3", "C"), track("4", "D"))
	if !trackSlicesEqual(q.Items(), []model.Track{
		track("1", "A"), track("2", "B"), track("3", "C"), track("4", "D"),
	}) {
		t.Error("InsertNext at end failed")
	}

	// Now advance should move to C
	next, ok := q.Advance()
	if !ok || !tracksEqual(next, track("3", "C")) {
		t.Error("Expected to advance to C")
	}

	// And again to D
	next, ok = q.Advance()
	if !ok || !tracksEqual(next, track("4", "D")) {
		t.Error("Expected to advance to D")
	}

	// And fail at end
	_, ok = q.Advance()
	if ok {
		t.Error("Expected Advance to fail at end")
	}
}
