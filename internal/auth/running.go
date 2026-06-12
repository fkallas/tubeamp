package auth

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// browserProcessNames maps a supported browser id to the OS process-name
// patterns to look for when detecting whether it is currently running. The
// patterns are matched case-insensitively (pgrep -i) against the process table,
// and cover both the macOS app name ("Google Chrome") and the shorter
// Linux/binary name ("chrome") where they differ.
var browserProcessNames = map[string][]string{
	"chrome":   {"Google Chrome", "chrome"},
	"chromium": {"Chromium", "chromium"},
	"edge":     {"Microsoft Edge", "msedge"},
	"brave":    {"Brave Browser", "brave"},
	"firefox":  {"firefox"},
	"safari":   {"Safari"},
}

// IsBrowserRunning reports whether the named browser appears to be running, by
// checking the OS process table (pgrep on darwin/linux). It exists so the -auth
// command can warn that cookies copied from a RUNNING browser are often stale
// (a fresh login lives in the browser's memory and the SQLite WAL, not yet in
// the on-disk snapshot kooky reads).
//
// It is strictly best-effort: an unsupported browser, "" / "auto" (no single
// process to attribute), an unsupported OS, or a missing/failing pgrep all
// report false. Detection must never make the import fail — it only improves the
// message.
func IsBrowserRunning(browser string) bool {
	names, ok := browserProcessNames[strings.ToLower(strings.TrimSpace(browser))]
	if !ok {
		return false
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		for _, n := range names {
			if processMatches(n) {
				return true
			}
		}
	}
	return false
}

// processMatches reports whether at least one running process matches the given
// (case-insensitive) name pattern, via pgrep. Any failure to run pgrep is
// treated as "not running" so detection can never break the import flow.
func processMatches(pattern string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// pgrep exits 0 when at least one process matches, 1 when none do, and >1 on
	// error; we only trust the clean match.
	return exec.CommandContext(ctx, "pgrep", "-i", "--", pattern).Run() == nil
}
