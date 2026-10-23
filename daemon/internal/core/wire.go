package core

import (
	"context"
	"os"
	"path/filepath"

	"wisp/daemon/internal/media"
	"wisp/daemon/internal/notify"
	"wisp/daemon/internal/store"
	"wisp/daemon/internal/wa"
)

// New opens the real components under cfg.DataDir: wa.db, wisp.db, media/.
func New(ctx context.Context, cfg Config) (*Core, error) {
	stPath, waPath := filepath.Join(cfg.DataDir, "wisp.db"), filepath.Join(cfg.DataDir, "wa.db")
	for _, p := range []string{stPath, waPath} {
		if err := createPrivate(p); err != nil {
			return nil, err
		}
	}
	st, err := store.Open(ctx, stPath)
	if err != nil {
		return nil, err
	}
	w, err := wa.New(ctx, wa.Config{DBPath: waPath, Log: cfg.Log})
	if err != nil {
		st.Close()
		return nil, err
	}
	md, err := media.New(media.Config{Dir: filepath.Join(cfg.DataDir, "media"), Downloader: w, Avatars: w, Log: cfg.Log})
	if err != nil {
		w.Close()
		st.Close()
		return nil, err
	}
	nt := notify.New(notify.Config{Log: cfg.Log})
	c := NewWith(Deps{Store: st, Media: md, WA: w, Notifier: nt, Log: cfg.Log})
	c.socket = cfg.Socket
	return c, nil
}

// createPrivate makes sure path exists with mode 0600. SQLite gives the
// -wal and -shm files the database's mode.
func createPrivate(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	return os.Chmod(path, 0o600)
}
