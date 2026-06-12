// Package lyrics fetches and parses time-synced (LRC) and plain song lyrics.
//
// Synced lyrics come from LRCLIB (a free, key-less lyrics API) over plain
// HTTP+JSON; the LRC body is parsed here. A plain-text fallback served by
// YouTube Music's InnerTube API lives in internal/ytm (Client.Lyrics). The
// parsing in this package is pure and side-effect free so it can be table
// tested without the network.
package lyrics

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Source identifiers for a Lyrics value. The empty string means "unknown".
const (
	SourceLRCLIB  = "lrclib"  // synced or plain lyrics from lrclib.net
	SourceYTMusic = "ytmusic" // plain lyrics from YouTube Music's InnerTube API
)

// Line is one lyric line with its start time. For unsynced (plain) lyrics the
// timestamps are absent and only Lyrics.Plain is populated.
type Line struct {
	At   time.Duration // start offset from the beginning of the track
	Text string        // the line text ("" for an instrumental/blank cue)
}

// Lyrics is a resolved lyrics result. Synced is true when At timestamps are
// present (the source was an LRC body); Lines then carries the timed lines and
// Plain may still hold the unsynced text when the source provided both. For
// unsynced results Synced is false, Lines is empty, and Plain holds the text.
// Source records where the lyrics came from (SourceLRCLIB/SourceYTMusic, or ""
// when unknown).
type Lyrics struct {
	Lines  []Line
	Synced bool
	Plain  string
	Source string
}

// ParseLRC parses an LRC body into timed lines, sorted by ascending start time.
//
// It understands LRC timestamp tags "[mm:ss.xx]" and "[mm:ss]" (one to three
// fractional digits are accepted). A single line may carry several leading
// timestamps ("[00:12.00][01:30.00] chorus"); one Line is emitted per
// timestamp. Metadata tags ([ar:], [ti:], [al:], [length:], [by:], …) and any
// line without a leading timestamp are ignored, as are malformed timestamps.
//
// The [offset:NNN] tag (milliseconds) is honoured: following the LRC
// convention a positive offset shifts the lyrics earlier, so each line's time
// is reduced by the offset (a negative offset pushes them later). Times that
// would go negative are clamped to zero.
func ParseLRC(s string) []Line {
	offset := parseOffset(s)

	var out []Line
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimRight(raw, "\r")

		// Consume leading "[...]" tags. A bracket whose content parses as a
		// timestamp is a cue; the first non-timestamp bracket (metadata such as
		// [ar:...], or bracketed lyric text) ends the timestamp run.
		rest := line
		var stamps []time.Duration
		for {
			rest = strings.TrimLeft(rest, " \t")
			if !strings.HasPrefix(rest, "[") {
				break
			}
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				break // unterminated bracket: malformed, keep as text
			}
			d, ok := parseTimestamp(rest[1:end])
			if !ok {
				break // metadata or non-timestamp bracket
			}
			stamps = append(stamps, d)
			rest = rest[end+1:]
		}
		if len(stamps) == 0 {
			continue // metadata-only, blank, or malformed line
		}

		text := strings.TrimSpace(rest)
		for _, d := range stamps {
			at := d - offset
			if at < 0 {
				at = 0
			}
			out = append(out, Line{At: at, Text: text})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// CurrentLine returns the index of the last line whose At is <= pos, or -1 when
// pos precedes the first line (or lines is empty). It assumes lines is sorted by
// ascending At, as returned by ParseLRC, and runs in O(log n) via binary search.
func CurrentLine(lines []Line, pos time.Duration) int {
	// Find the first index with At > pos; the answer is one before it.
	lo, hi := 0, len(lines)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if lines[mid].At <= pos {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

// parseOffset returns the [offset:NNN] adjustment (milliseconds) as a Duration,
// or 0 when no valid offset tag is present. Atoi accepts a leading sign.
func parseOffset(s string) time.Duration {
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if !strings.HasPrefix(line, "[offset:") || !strings.HasSuffix(line, "]") {
			continue
		}
		val := strings.TrimSpace(line[len("[offset:") : len(line)-1])
		if n, err := strconv.Atoi(val); err == nil {
			return time.Duration(n) * time.Millisecond
		}
	}
	return 0
}

// parseTimestamp parses an LRC timestamp tag body ("mm:ss", "mm:ss.xx",
// "mm:ss.xxx") into a Duration. Returns ok=false for any non-timestamp content
// (metadata tags, malformed numbers, seconds >= 60).
func parseTimestamp(tag string) (time.Duration, bool) {
	colon := strings.IndexByte(tag, ':')
	if colon <= 0 {
		return 0, false
	}
	minStr := tag[:colon]
	rest := tag[colon+1:]

	secStr, fracStr := rest, ""
	if dot := strings.IndexByte(rest, '.'); dot >= 0 {
		secStr, fracStr = rest[:dot], rest[dot+1:]
	}
	if !allDigits(minStr) || !allDigits(secStr) {
		return 0, false
	}
	min, _ := strconv.Atoi(minStr)
	sec, _ := strconv.Atoi(secStr)
	if sec >= 60 {
		return 0, false
	}
	d := time.Duration(min)*time.Minute + time.Duration(sec)*time.Second

	if fracStr != "" {
		if !allDigits(fracStr) {
			return 0, false
		}
		// Normalise to exactly three digits (milliseconds): ".5" => 500ms,
		// ".05" => 50ms, ".123" => 123ms; extra digits are truncated.
		if len(fracStr) > 3 {
			fracStr = fracStr[:3]
		}
		for len(fracStr) < 3 {
			fracStr += "0"
		}
		ms, _ := strconv.Atoi(fracStr)
		d += time.Duration(ms) * time.Millisecond
	}
	return d, true
}

// allDigits reports whether s is non-empty and every rune is an ASCII digit.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
