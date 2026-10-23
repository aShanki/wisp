// wispd is the Wisp daemon: it owns the WhatsApp session and serves the UI
// over a unix socket.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/core"
	"wisp/daemon/internal/mock"
	"wisp/daemon/internal/rpc"
	"wisp/daemon/internal/tray"
)

func main() {
	var (
		mockMode = flag.Bool("mock", false, "serve generated data, no network")
		socket   = flag.String("socket", "", "socket path (default $WISP_SOCKET or $XDG_RUNTIME_DIR/wisp.sock)")
		dataDir  = flag.String("data", defaultDataDir(), "data directory")
		traffic  = flag.Duration("mock-traffic", 0, "mock: mean gap between incoming messages (0 = none)")
		unpaired = flag.Bool("mock-unpaired", false, "mock: start unpaired and walk through the QR flow")
		ringIn   = flag.Duration("mock-call", 0, "mock: ring an incoming call after this long (0 = none)")
		verbose  = flag.Bool("v", false, "debug logging")
		trayOn   = flag.Bool("tray", false, "mock: show the tray icon (always shown otherwise)")
	)
	flag.Parse()

	var out io.Writer = os.Stderr
	var logErr error
	if !*mockMode { // the mock logs to stderr, where the scripts read it
		out, logErr = logToFile()
	}
	log := newLogger(out, *verbose)
	if logErr != nil {
		log.Warn("wispd: log file", "err", logErr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	path := rpc.SocketPath(*socket, *mockMode)
	ln, err := rpc.Listen(path)
	if errors.Is(err, rpc.ErrAlreadyRunning) { // not a failure for systemd to restart
		log.Info("wispd: already running", "socket", path)
		return
	}
	if err != nil {
		log.Error("wispd: listen", "err", err)
		os.Exit(1)
	}

	var (
		backend api.Backend
		done    <-chan struct{}
		closeFn func() error
		start   = func() {}
		openUI  func(chat, token string)
	)
	if *mockMode {
		m := mock.New(mock.Config{Seed: 1, Traffic: *traffic, Unpaired: *unpaired, Call: *ringIn, Log: log})
		backend, done, closeFn = m, m.Done(), m.Close
	} else {
		if os.Getenv("GOMEMLIMIT") == "" {
			// Collect harder near the budget instead of growing the heap.
			debug.SetMemoryLimit(40 << 20)
		}
		if err := os.MkdirAll(*dataDir, 0o700); err != nil {
			log.Error("wispd: data dir", "err", err)
			os.Exit(1)
		}
		c, err := core.New(ctx, core.Config{DataDir: *dataDir, Socket: path, Log: log})
		if err != nil {
			ln.Close()
			log.Error("wispd: start", "err", err)
			os.Exit(1)
		}
		backend, done, closeFn = c, c.Done(), c.Close
		start = func() { c.Start(ctx) }
		openUI = c.OpenUI
	}

	srv := rpc.NewServer(backend, log)
	var tr *tray.Tray
	if !*mockMode || *trayOn {
		tr = startTray(ctx, backend, srv, openUI, log)
	}
	start()

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-done:
			log.Info("wispd: shutdown requested")
			cancel()
		case <-ctx.Done():
		}
	}()
	err = srv.ServeListener(ctx, ln)
	cancel()
	if tr != nil {
		tr.Close()
	}
	if cerr := closeFn(); cerr != nil {
		log.Warn("wispd: close", "err", cerr)
	}
	if err != nil {
		log.Error("wispd: serve", "err", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Wisp")
	}
	return filepath.Join(home, ".local", "share", "wisp")
}
