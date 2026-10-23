// Package media implements api.Media: a size-capped LRU disk cache of
// decrypted WhatsApp media, small JPEG thumbnails and avatars, plus
// preparing local files for sending.
//
//	files/<2 hex>/<sha256(chat/id)>.<ext>   decrypted media
//	thumbs/<2 hex>/<sha256(chat/id)>.jpg    thumbnails, ≤ 100 px
//	avatars/<jid>-<pictureID>.jpg           profile pictures, ≤ 256 px
//
// Writes go through a temp file and a rename. The LRU index is built by one
// scan at startup and kept up to date on every hit and write; last use is
// persisted as the file mtime (at most hourly per file).
package media

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"wisp/daemon/internal/api"
)

// DefaultCap is the LRU size cap used when Config.CapBytes <= 0.
const DefaultCap int64 = 1 << 30

const (
	// touchInterval limits how often a hit rewrites the file mtime.
	touchInterval = time.Hour
	// staleTemp is the age after which leftover temp files are deleted by
	// the startup scan (younger ones may belong to an in-progress write).
	staleTemp  = time.Hour
	tempPrefix = ".tmp-"
)

// Config configures a Cache.
type Config struct {
	Dir        string           // cache root, created if missing (required)
	CapBytes   int64            // LRU cap; <= 0 means DefaultCap
	Downloader api.Downloader   // used by Fetch on a miss (may be nil: misses fail)
	Avatars    api.AvatarSource // used by Avatar (may be nil: only cached avatars)
	HTTP       *http.Client     // used to GET avatar URLs (nil: a default client)
	Log        *slog.Logger     // nil: slog.Default()
}

// Cache is the media disk cache. Safe for concurrent use.
type Cache struct {
	dir  string
	dl   api.Downloader
	av   api.AvatarSource
	http *http.Client
	log  *slog.Logger

	mu      sync.Mutex
	cap     int64
	total   int64
	entries map[string]*entry // absolute path -> entry
	lru     *list.List        // of *entry; front = most recently used

	scanned chan struct{} // closed when the startup scan has finished

	avMu    sync.Mutex
	avatars map[string]avatarFile // sanitized jid -> current avatar file
	noPic   map[string]time.Time  // sanitized jid -> negative-cache expiry

	fetches flight
	thumbs  flight
	avFetch flight
}

type entry struct {
	path  string
	size  int64
	mtime time.Time // last persisted use (file mtime)
	el    *list.Element
}

var _ api.Media = (*Cache)(nil)

// New creates the cache directories and starts the one-off background scan
// that builds the LRU index.
func New(cfg Config) (*Cache, error) {
	if cfg.Dir == "" {
		return nil, errors.New("media: Config.Dir is required")
	}
	dir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"", "files", "thumbs", "avatars"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	c := &Cache{
		dir:     dir,
		dl:      cfg.Downloader,
		av:      cfg.Avatars,
		http:    cfg.HTTP,
		log:     cfg.Log,
		cap:     cfg.CapBytes,
		entries: make(map[string]*entry),
		lru:     list.New(),
		scanned: make(chan struct{}),
		avatars: make(map[string]avatarFile),
		noPic:   make(map[string]time.Time),
	}
	if c.cap <= 0 {
		c.cap = DefaultCap
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: avatarTimeout}
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	c.log = c.log.With("component", "media")
	go c.scan()
	return c, nil
}

// SetCap changes the LRU size cap (<= 0 means DefaultCap) and evicts.
func (c *Cache) SetCap(bytes int64) {
	if bytes <= 0 {
		bytes = DefaultCap
	}
	c.mu.Lock()
	c.cap = bytes
	c.mu.Unlock()
	c.evict("")
}

// Wipe deletes every cached file (after logout).
func (c *Cache) Wipe() error {
	<-c.scanned
	c.mu.Lock()
	c.entries, c.lru, c.total = make(map[string]*entry), list.New(), 0
	c.mu.Unlock()
	c.avMu.Lock()
	c.avatars, c.noPic = make(map[string]avatarFile), make(map[string]time.Time)
	c.avMu.Unlock()
	var errs []error
	for _, sub := range []string{"files", "thumbs", "avatars"} {
		p := filepath.Join(c.dir, sub)
		errs = append(errs, os.RemoveAll(p), os.MkdirAll(p, 0o700))
	}
	return errors.Join(errs...)
}

func keyHash(chat, id string) string {
	sum := sha256.Sum256([]byte(chat + "/" + id))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) filePath(ref *api.MediaRef) string {
	h := keyHash(ref.Chat, ref.ID)
	return filepath.Join(c.dir, "files", h[:2], h+"."+extFor(ref))
}

func (c *Cache) thumbPath(ref *api.MediaRef) string {
	h := keyHash(ref.Chat, ref.ID)
	return filepath.Join(c.dir, "thumbs", h[:2], h+".jpg")
}

// lookupFile finds the cached media file for ref, tolerating an extension
// different from the one the current mime would give.
func (c *Cache) lookupFile(ref *api.MediaRef) (string, bool) {
	p := c.filePath(ref)
	if c.hit(p) {
		return p, true
	}
	h := keyHash(ref.Chat, ref.ID)
	matches, _ := filepath.Glob(filepath.Join(c.dir, "files", h[:2], h+".*"))
	for _, m := range matches {
		if c.hit(m) {
			return m, true
		}
	}
	return "", false
}

// hit reports whether path is a regular file and, if so, marks it used.
func (c *Cache) hit(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	c.touch(path, info.Size(), info.ModTime())
	return true
}

// touch moves path to the front of the LRU and persists the use time in the
// file mtime if the persisted one is older than touchInterval.
func (c *Cache) touch(path string, size int64, mtime time.Time) {
	now := time.Now()
	persist := false
	c.mu.Lock()
	if e, ok := c.entries[path]; ok {
		c.lru.MoveToFront(e.el)
		if now.Sub(e.mtime) > touchInterval {
			e.mtime = now
			persist = true
		}
	} else {
		e := &entry{path: path, size: size, mtime: mtime}
		if now.Sub(mtime) > touchInterval {
			e.mtime = now
			persist = true
		}
		e.el = c.lru.PushFront(e)
		c.entries[path] = e
		c.total += size
	}
	c.mu.Unlock()
	if persist {
		_ = os.Chtimes(path, now, now)
	}
}

// added records a freshly written file as most recently used and evicts.
func (c *Cache) added(path string, size int64) {
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.entries[path]; ok {
		c.total += size - e.size
		e.size = size
		e.mtime = now
		c.lru.MoveToFront(e.el)
	} else {
		e := &entry{path: path, size: size, mtime: now}
		e.el = c.lru.PushFront(e)
		c.entries[path] = e
		c.total += size
	}
	c.mu.Unlock()
	c.evict(path)
}

// forget drops path from the index (after deleting it).
func (c *Cache) forget(path string) {
	c.mu.Lock()
	if e, ok := c.entries[path]; ok {
		c.lru.Remove(e.el)
		delete(c.entries, path)
		c.total -= e.size
	}
	c.mu.Unlock()
}

// evict removes least recently used files until the total is within the
// cap. keep (if non-empty) is never evicted (the file just written).
func (c *Cache) evict(keep string) {
	var victims []string
	c.mu.Lock()
	for c.total > c.cap {
		el := c.lru.Back()
		if el == nil {
			break
		}
		e := el.Value.(*entry)
		if e.path == keep {
			// Only the protected file is left over the cap; try the one
			// before it.
			prev := el.Prev()
			if prev == nil {
				break
			}
			e = prev.Value.(*entry)
			el = prev
		}
		c.lru.Remove(el)
		delete(c.entries, e.path)
		c.total -= e.size
		victims = append(victims, e.path)
	}
	c.mu.Unlock()
	for _, p := range victims {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			c.log.Warn("evict", "path", p, "err", err)
		}
	}
	if len(victims) > 0 {
		c.log.Debug("evicted", "files", len(victims))
	}
}

type scanned struct {
	path  string
	size  int64
	mtime time.Time
}

// scan walks the cache once at startup to build the LRU index (oldest mtime
// = least recently used) and the avatar map. Entries touched or written
// while the scan ran are already in the index and keep their position.
func (c *Cache) scan() {
	defer close(c.scanned)
	start := time.Now()
	var found []scanned
	type avCand struct {
		key, id, path string
		mtime         time.Time
	}
	var avs []avCand
	for _, sub := range []string{"files", "thumbs", "avatars"} {
		root := filepath.Join(c.dir, sub)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.Mode().IsRegular() {
				return nil
			}
			if strings.HasPrefix(d.Name(), tempPrefix) {
				if start.Sub(info.ModTime()) > staleTemp {
					_ = os.Remove(p)
				}
				return nil
			}
			found = append(found, scanned{p, info.Size(), info.ModTime()})
			if sub == "avatars" {
				if key, id, ok := parseAvatarName(d.Name()); ok {
					avs = append(avs, avCand{key, id, p, info.ModTime()})
				}
			}
			return nil
		})
	}
	// Newest first, appended at the back after anything already indexed:
	// the oldest ends up at the very back and is evicted first.
	sort.Slice(found, func(i, j int) bool { return found[i].mtime.After(found[j].mtime) })
	c.mu.Lock()
	for _, f := range found {
		if _, ok := c.entries[f.path]; ok {
			continue
		}
		e := &entry{path: f.path, size: f.size, mtime: f.mtime}
		e.el = c.lru.PushBack(e)
		c.entries[f.path] = e
		c.total += f.size
	}
	total := c.total
	c.mu.Unlock()

	// Avatar map: newest file per jid wins; older duplicates are removed.
	sort.Slice(avs, func(i, j int) bool { return avs[i].mtime.After(avs[j].mtime) })
	var stale []string
	c.avMu.Lock()
	for _, a := range avs {
		if _, ok := c.avatars[a.key]; ok {
			stale = append(stale, a.path)
			continue
		}
		c.avatars[a.key] = avatarFile{path: a.path, id: a.id}
	}
	c.avMu.Unlock()
	for _, p := range stale {
		_ = os.Remove(p)
		c.forget(p)
	}

	c.log.Debug("scan done", "files", len(found), "bytes", total, "took", time.Since(start))
	c.evict("")
}

// waitScan blocks until the startup scan has finished or ctx is done.
func (c *Cache) waitScan(ctx context.Context) error {
	select {
	case <-c.scanned:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// writeAtomic writes data to path via a temp file in the same directory and
// a rename, then indexes it (which may evict older files).
func (c *Cache) writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	c.added(path, int64(len(data)))
	return nil
}
