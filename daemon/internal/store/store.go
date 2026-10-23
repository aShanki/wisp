// Package store implements api.Store on SQLite (modernc.org/sqlite, pure Go).
//
// Layout: one wisp.db file in WAL mode. All writes go through a single
// connection (so they are serialized and read-modify-write sequences inside
// a transaction are race free); reads use a small pool of query-only
// connections that never block on the writer. Wipe swaps both for a fresh
// database, so every operation holds mu for reading.
//
// Messages are keyed by (chat, id) and ordered by (ts, seq) where seq is the
// rowid assigned on first insert. Paging is keyset-based on the
// messages(chat, ts, seq) index, never OFFSET.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // driver "sqlite"

	"wisp/daemon/internal/api"
)

var _ api.Store = (*Store)(nil)

// Store is the SQLite-backed app store (wisp.db).
type Store struct {
	path string
	mu   sync.RWMutex
	w    *sql.DB // single connection: all writes
	r    *sql.DB // read pool (== w for in-memory databases)
}

// Connection pragmas. cache_size is per connection (negative = KiB); the
// daemon's whole RSS budget is < 60 MB so keep these small.
const (
	writerCacheKiB = 4000
	readerCacheKiB = 2000
	maxReaders     = 3
)

func dsn(path string, cacheKiB int, readOnly bool) string {
	q := url.Values{}
	pr := []string{
		"busy_timeout(5000)",
		"foreign_keys(1)",
		"synchronous(NORMAL)",
		"temp_store(MEMORY)",
		fmt.Sprintf("cache_size(-%d)", cacheKiB),
		"mmap_size(0)",
	}
	if path != ":memory:" && !readOnly {
		pr = append(pr, "journal_mode(WAL)", "journal_size_limit(4194304)")
	}
	if readOnly {
		pr = append(pr, "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	q["_pragma"] = pr
	return path + "?" + q.Encode()
}

// Open opens (creating if needed) the store at path and migrates it to the
// current schema. path may be ":memory:" (single connection, for tests).
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty path")
	}
	if strings.ContainsRune(path, '?') {
		return nil, fmt.Errorf("store: path must not contain '?': %q", path)
	}
	s := &Store{path: path}
	if err := s.open(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) open(ctx context.Context) error {
	w, err := sql.Open("sqlite", dsn(s.path, writerCacheKiB, false))
	if err != nil {
		return fmt.Errorf("store: open: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	w.SetConnMaxIdleTime(0)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return fmt.Errorf("store: open %s: %w", s.path, err)
	}
	if err := migrate(ctx, w); err != nil {
		w.Close()
		return err
	}
	r := w
	if s.path != ":memory:" {
		if r, err = sql.Open("sqlite", dsn(s.path, readerCacheKiB, true)); err != nil {
			w.Close()
			return fmt.Errorf("store: open readers: %w", err)
		}
		r.SetMaxOpenConns(maxReaders)
		r.SetMaxIdleConns(2)
	}
	s.w, s.r = w, r
	return nil
}

// Close closes all connections.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeLocked()
}

func (s *Store) closeLocked() error {
	var errs []error
	if s.r != s.w {
		errs = append(errs, s.r.Close())
	}
	// Fold the WAL back into the main file so the next start is cheap.
	_, _ = s.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	errs = append(errs, s.w.Close())
	return errors.Join(errs...)
}

// Wipe deletes the database (after logout) and starts an empty one with the
// same settings, which belong to the device rather than the account.
func (s *Store) Wipe(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, err := allSettings(ctx, s.w)
	if err != nil {
		return err
	}
	if err := s.closeLocked(); err != nil {
		return fmt.Errorf("store: wipe: %w", err)
	}
	if s.path != ":memory:" {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(s.path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("store: wipe: %w", err)
			}
		}
		// SQLite gives the -wal and -shm files the database's mode.
		f, err := os.OpenFile(s.path, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("store: wipe: %w", err)
		}
		f.Close()
	}
	if err := s.open(ctx); err != nil {
		return err
	}
	for k, v := range settings {
		if _, err := s.w.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)`, k, v); err != nil {
			return fmt.Errorf("store: wipe: restore setting: %w", err)
		}
	}
	return nil
}

// tx runs f in a write transaction on the writer connection.
func (s *Store) tx(ctx context.Context, f func(tx *sql.Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func notFound(what string, a ...any) error {
	return fmt.Errorf("%w: %s", api.ErrNotFound, fmt.Sprintf(what, a...))
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
