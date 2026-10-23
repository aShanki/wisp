package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"golang.org/x/sys/unix"
)

const maxLogSize = 1 << 20

// newLogger logs at info level, or debug with verbose. Without verbose,
// phone numbers (any run of 7+ digits) are masked.
func newLogger(w io.Writer, verbose bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: maskNumbers}
	if verbose {
		opts.Level, opts.ReplaceAttr = slog.LevelDebug, nil
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

var numberRE = regexp.MustCompile(`\d{7,}`)

func maskNumbers(_ []string, a slog.Attr) slog.Attr {
	var s string
	switch v := a.Value.Any().(type) {
	case string:
		s = v
	case error:
		s = v.Error()
	default:
		return a
	}
	if !numberRE.MatchString(s) {
		return a
	}
	return slog.String(a.Key, numberRE.ReplaceAllStringFunc(s, func(n string) string { return "…" + n[len(n)-3:] }))
}

// logToFile points stderr (the log and any crash) at
// $XDG_STATE_HOME/wisp/wispd.log unless it is a terminal, and returns the
// writer to log to. The file starts over once it is past 1 MB.
func logToFile() (io.Writer, error) {
	if _, err := unix.IoctlGetTermios(int(os.Stderr.Fd()), unix.TCGETS); err == nil {
		return os.Stderr, nil
	}
	dir := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return os.Stderr, err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	dir = filepath.Join(dir, "wisp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.Stderr, err
	}
	path := filepath.Join(dir, "wispd.log")
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return os.Stderr, err
	}
	defer f.Close()
	if err := unix.Dup3(int(f.Fd()), int(os.Stderr.Fd()), 0); err != nil {
		return os.Stderr, err
	}
	w := &capped{f: os.Stderr}
	if st, err := os.Stderr.Stat(); err == nil {
		w.n = st.Size()
	}
	return w, nil
}

// capped starts the (O_APPEND) log file over when a write would take it past
// maxLogSize, so a long-running daemon's log stays bounded.
type capped struct {
	mu sync.Mutex
	f  *os.File
	n  int64
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n+int64(len(p)) > maxLogSize && c.f.Truncate(0) == nil {
		c.n = 0
	}
	n, err := c.f.Write(p)
	c.n += int64(n)
	return n, err
}
