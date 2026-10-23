package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"wisp/daemon/internal/api"
)

const (
	avatarTimeout  = 15 * time.Second
	avatarMaxBytes = 5 << 20
	avatarMaxPx    = 256
	avatarQuality  = 85
	// noPictureTTL is how long a "has no picture" answer is remembered.
	noPictureTTL = 6 * time.Hour
	// noPictureSweep: prune expired negative entries when the map gets big.
	noPictureSweep = 4096
)

type avatarFile struct {
	path string
	id   string
}

// sanitizeJID makes a jid safe as a file name prefix. Separators that can't
// appear in the picture id part keep "<jid>-<id>.jpg" unambiguous.
func sanitizeJID(jid string) string {
	var b strings.Builder
	b.Grow(len(jid))
	for _, r := range jid {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '@', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// sanitizeID keeps only [A-Za-z0-9] so the id can't contain the separator.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

// parseAvatarName splits "<sanitized jid>-<id>.jpg".
func parseAvatarName(name string) (key, id string, ok bool) {
	base, found := strings.CutSuffix(name, ".jpg")
	if !found {
		return "", "", false
	}
	i := strings.LastIndexByte(base, '-')
	if i <= 0 || i == len(base)-1 {
		return "", "", false
	}
	key, id = base[:i], base[i+1:]
	if sanitizeID(id) != id {
		return "", "", false
	}
	return key, id, true
}

func (c *Cache) avatarPath(key, id string) string {
	return filepath.Join(c.dir, "avatars", key+"-"+id+".jpg")
}

// cachedAvatar returns the current avatar file for key if it still exists.
func (c *Cache) cachedAvatar(key string) (avatarFile, bool) {
	c.avMu.Lock()
	a, ok := c.avatars[key]
	c.avMu.Unlock()
	if !ok {
		return avatarFile{}, false
	}
	if !c.hit(a.path) {
		c.avMu.Lock()
		if cur, ok := c.avatars[key]; ok && cur.path == a.path {
			delete(c.avatars, key)
		}
		c.avMu.Unlock()
		return avatarFile{}, false
	}
	return a, true
}

func (c *Cache) knownNoPicture(key string) bool {
	c.avMu.Lock()
	defer c.avMu.Unlock()
	exp, ok := c.noPic[key]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(c.noPic, key)
		return false
	}
	return true
}

func (c *Cache) setNoPicture(key string) {
	now := time.Now()
	c.avMu.Lock()
	if len(c.noPic) >= noPictureSweep {
		for k, exp := range c.noPic {
			if now.After(exp) {
				delete(c.noPic, k)
			}
		}
	}
	c.noPic[key] = now.Add(noPictureTTL)
	old, had := c.avatars[key]
	delete(c.avatars, key)
	c.avMu.Unlock()
	if had {
		c.removeQuiet(old.path)
	}
}

// Avatar returns a cached avatar path for jid, fetching it via the
// AvatarSource when missing or when refresh is true. api.ErrNotFound if the
// jid has no (visible) picture; that answer is remembered for noPictureTTL
// unless refresh is set.
func (c *Cache) Avatar(ctx context.Context, jid string, refresh bool) (string, error) {
	if jid == "" {
		return "", api.InvalidParams("jid is required")
	}
	if err := c.waitScan(ctx); err != nil {
		return "", err
	}
	key := sanitizeJID(jid)
	if !refresh {
		if a, ok := c.cachedAvatar(key); ok {
			return a.path, nil
		}
		if c.knownNoPicture(key) {
			return "", api.ErrNotFound
		}
	}
	if c.av == nil {
		return "", api.ErrNotFound
	}
	return c.avFetch.do(ctx, key, 2*avatarTimeout, func(ctx context.Context) (string, error) {
		return c.fetchAvatar(ctx, jid, key)
	})
}

func (c *Cache) fetchAvatar(ctx context.Context, jid, key string) (string, error) {
	cur, haveCur := c.cachedAvatar(key)
	url, id, err := c.av.AvatarURL(ctx, jid)
	if errors.Is(err, api.ErrNotFound) {
		c.setNoPicture(key)
		return "", api.ErrNotFound
	}
	if err != nil {
		if haveCur {
			return cur.path, nil // stale beats nothing on a transient error
		}
		return "", fmt.Errorf("avatar url: %w", err)
	}
	if url == "" {
		c.setNoPicture(key)
		return "", api.ErrNotFound
	}
	id = sanitizeID(id)
	if id == "" {
		sum := sha256.Sum256([]byte(url))
		id = hex.EncodeToString(sum[:8])
	}
	if haveCur && cur.id == id {
		return cur.path, nil
	}

	data, err := c.download(ctx, url)
	if errors.Is(err, api.ErrNotFound) {
		c.setNoPicture(key)
		return "", api.ErrNotFound
	}
	if err != nil {
		if haveCur {
			return cur.path, nil
		}
		return "", err
	}
	jpg, err := avatarJPEG(data)
	if err != nil {
		if haveCur {
			return cur.path, nil
		}
		return "", fmt.Errorf("avatar decode: %w", err)
	}
	p := c.avatarPath(key, id)
	if err := c.writeAtomic(p, jpg); err != nil {
		return "", fmt.Errorf("store avatar: %w", err)
	}
	c.avMu.Lock()
	old, had := c.avatars[key]
	c.avatars[key] = avatarFile{path: p, id: id}
	delete(c.noPic, key)
	c.avMu.Unlock()
	if had && old.path != p {
		c.removeQuiet(old.path)
	}
	return p, nil
}

// download GETs url with a timeout and size limit.
func (c *Cache) download(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, avatarTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("avatar request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("avatar get: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, api.ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("avatar get: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > avatarMaxBytes {
		return nil, fmt.Errorf("avatar too large (%d bytes)", resp.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, avatarMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("avatar read: %w", err)
	}
	if len(data) > avatarMaxBytes {
		return nil, errors.New("avatar too large")
	}
	return data, nil
}

// avatarJPEG returns data as a JPEG fitting avatarMaxPx: small JPEGs are
// kept byte-for-byte, anything else is decoded, scaled and re-encoded.
func avatarJPEG(data []byte) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format == "jpeg" && cfg.Width <= avatarMaxPx && cfg.Height <= avatarMaxPx {
		return data, nil
	}
	out, _, err := thumbJPEG(bytes.NewReader(data), avatarMaxPx, avatarQuality)
	return out, err
}
