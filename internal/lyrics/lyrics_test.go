package lyrics

import (
	"reflect"
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestParseLRC(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Line
	}{
		{
			name: "basic mm:ss.xx and mm:ss",
			in:   "[00:12.50]hello\n[01:05]world",
			want: []Line{
				{At: 12*time.Second + ms(500), Text: "hello"},
				{At: 65 * time.Second, Text: "world"},
			},
		},
		{
			name: "multiple timestamps on one line emit one line each",
			in:   "[00:12.00][01:30.00] chorus",
			want: []Line{
				{At: 12 * time.Second, Text: "chorus"},
				{At: 90 * time.Second, Text: "chorus"},
			},
		},
		{
			name: "metadata tags are ignored",
			in:   "[ar:Artist]\n[ti:Title]\n[al:Album]\n[by:Maker]\n[length:03:21]\n[00:01.00]first",
			want: []Line{
				{At: 1 * time.Second, Text: "first"},
			},
		},
		{
			name: "offset shifts earlier (positive)",
			in:   "[offset:500]\n[00:10.00]hi",
			want: []Line{
				{At: 9*time.Second + ms(500), Text: "hi"},
			},
		},
		{
			name: "negative offset shifts later",
			in:   "[offset:-250]\n[00:10.00]hi",
			want: []Line{
				{At: 10*time.Second + ms(250), Text: "hi"},
			},
		},
		{
			name: "offset clamps negative times to zero",
			in:   "[offset:5000]\n[00:01.00]early",
			want: []Line{
				{At: 0, Text: "early"},
			},
		},
		{
			name: "malformed lines skipped, sorted output",
			in:   "garbage line\n[xx:yy]bad\n[99]nocolon\n[00:99]badsec\n[00:30.00]later\n[00:05.00]earlier",
			want: []Line{
				{At: 5 * time.Second, Text: "earlier"},
				{At: 30 * time.Second, Text: "later"},
			},
		},
		{
			name: "three fractional digits (milliseconds)",
			in:   "[00:01.123]x",
			want: []Line{
				{At: 1*time.Second + ms(123), Text: "x"},
			},
		},
		{
			name: "single fractional digit (tenths)",
			in:   "[00:01.5]x",
			want: []Line{
				{At: 1*time.Second + ms(500), Text: "x"},
			},
		},
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
		{
			name: "timestamp with empty text kept",
			in:   "[00:08.00]",
			want: []Line{
				{At: 8 * time.Second, Text: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseLRC(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseLRC(%q)\n got = %v\nwant = %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseLRC_sorted verifies the result is sorted even when input is shuffled.
func TestParseLRC_sorted(t *testing.T) {
	got := ParseLRC("[00:30.00]c\n[00:10.00]a\n[00:20.00]b")
	want := []Line{
		{At: 10 * time.Second, Text: "a"},
		{At: 20 * time.Second, Text: "b"},
		{At: 30 * time.Second, Text: "c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCurrentLine(t *testing.T) {
	lines := []Line{
		{At: 10 * time.Second, Text: "a"},
		{At: 20 * time.Second, Text: "b"},
		{At: 30 * time.Second, Text: "c"},
	}
	tests := []struct {
		name string
		pos  time.Duration
		want int
	}{
		{"before first", 5 * time.Second, -1},
		{"exactly on first", 10 * time.Second, 0},
		{"between first and second", 15 * time.Second, 0},
		{"exactly on second", 20 * time.Second, 1},
		{"exactly on last", 30 * time.Second, 2},
		{"after last", 60 * time.Second, 2},
		{"at zero", 0, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CurrentLine(lines, tt.pos); got != tt.want {
				t.Errorf("CurrentLine(pos=%v) = %d, want %d", tt.pos, got, tt.want)
			}
		})
	}
}

func TestCurrentLine_empty(t *testing.T) {
	if got := CurrentLine(nil, 5*time.Second); got != -1 {
		t.Errorf("CurrentLine(nil) = %d, want -1", got)
	}
	if got := CurrentLine([]Line{}, 0); got != -1 {
		t.Errorf("CurrentLine([]) = %d, want -1", got)
	}
}

// TestCurrentLine_firstAtZero covers a track whose first cue is at 0.
func TestCurrentLine_firstAtZero(t *testing.T) {
	lines := []Line{{At: 0, Text: "intro"}, {At: 5 * time.Second, Text: "next"}}
	if got := CurrentLine(lines, 0); got != 0 {
		t.Errorf("CurrentLine(0) = %d, want 0", got)
	}
}
