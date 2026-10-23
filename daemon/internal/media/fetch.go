package media

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wisp/daemon/internal/api"
)

const (
	downloadTimeout = 10 * time.Minute
	thumbTimeout    = time.Minute
	thumbMaxPx      = 100 // generated message thumbnails
	thumbQuality    = 70
)

func checkRef(ref *api.MediaRef) error {
	if ref == nil || ref.Chat == "" || ref.ID == "" {
		return api.InvalidParams("media ref needs chat and id")
	}
	return nil
}

// Fetch returns the cached decrypted file for ref, downloading it via the
// Downloader on a miss. Concurrent calls for the same ref share one download.
func (c *Cache) Fetch(ctx context.Context, ref *api.MediaRef) (string, error) {
	if err := checkRef(ref); err != nil {
		return "", err
	}
	if p, ok := c.lookupFile(ref); ok {
		return p, nil
	}
	if c.dl == nil {
		return "", fmt.Errorf("%w: no downloader", api.ErrNotFound)
	}
	r := *ref // the shared download must not see later mutations by a caller
	return c.fetches.do(ctx, ref.Chat+"/"+ref.ID, downloadTimeout, func(ctx context.Context) (string, error) {
		if p, ok := c.lookupFile(&r); ok {
			return p, nil
		}
		start := time.Now()
		data, err := c.dl.Download(ctx, &r)
		if err != nil {
			return "", fmt.Errorf("download media: %w", err)
		}
		if len(data) == 0 {
			return "", errors.New("download media: empty file")
		}
		p := c.filePath(&r)
		if err := c.writeAtomic(p, data); err != nil {
			return "", fmt.Errorf("store media: %w", err)
		}
		c.log.Debug("fetched", "chat", r.Chat, "id", r.ID, "bytes", len(data), "took", time.Since(start))
		return p, nil
	})
}

// Put stores bytes we already have (e.g. our own sent media) under ref.
func (c *Cache) Put(ctx context.Context, ref *api.MediaRef, data []byte) (string, error) {
	if err := checkRef(ref); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", api.InvalidParams("empty media")
	}
	p := c.filePath(ref)
	if err := c.writeAtomic(p, data); err != nil {
		return "", fmt.Errorf("store media: %w", err)
	}
	return p, nil
}

// Thumb returns a small JPEG for the message: the embedded thumbnail bytes if
// given, else one generated from the cached full image, else api.ErrNotFound.
func (c *Cache) Thumb(ctx context.Context, ref *api.MediaRef, embedded []byte) (string, error) {
	if err := checkRef(ref); err != nil {
		return "", err
	}
	tp := c.thumbPath(ref)
	if c.hit(tp) {
		return tp, nil
	}
	r := *ref
	emb := embedded
	return c.thumbs.do(ctx, ref.Chat+"/"+ref.ID, thumbTimeout, func(ctx context.Context) (string, error) {
		if c.hit(tp) {
			return tp, nil
		}
		if len(emb) > 0 {
			if err := c.writeAtomic(tp, emb); err != nil {
				return "", fmt.Errorf("store thumb: %w", err)
			}
			return tp, nil
		}
		src, ok := c.lookupFile(&r)
		if !ok {
			return "", api.ErrNotFound
		}
		data, err := thumbFromFile(src, thumbMaxPx, thumbQuality)
		if err != nil {
			c.log.Debug("thumb generation failed", "path", src, "err", err)
			return "", fmt.Errorf("%w: %v", api.ErrNotFound, err)
		}
		if err := c.writeAtomic(tp, data); err != nil {
			return "", fmt.Errorf("store thumb: %w", err)
		}
		return tp, nil
	})
}

var mimeToExt = map[string]string{
	"image/jpeg":                    "jpg",
	"image/png":                     "png",
	"image/gif":                     "gif",
	"image/webp":                    "webp",
	"image/bmp":                     "bmp",
	"image/heic":                    "heic",
	"video/mp4":                     "mp4",
	"video/3gpp":                    "3gp",
	"video/quicktime":               "mov",
	"video/webm":                    "webm",
	"video/x-matroska":              "mkv",
	"audio/ogg":                     "ogg",
	"audio/opus":                    "opus",
	"audio/mpeg":                    "mp3",
	"audio/mp4":                     "m4a",
	"audio/aac":                     "aac",
	"audio/amr":                     "amr",
	"audio/wav":                     "wav",
	"audio/x-wav":                   "wav",
	"audio/flac":                    "flac",
	"application/pdf":               "pdf",
	"application/zip":               "zip",
	"text/plain":                    "txt",
	"text/vcard":                    "vcf",
	"application/msword":            "doc",
	"application/vnd.ms-excel":      "xls",
	"application/vnd.ms-powerpoint": "ppt",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "docx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "xlsx",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pptx",
	"application/vnd.oasis.opendocument.text":                                   "odt",
	"application/vnd.oasis.opendocument.spreadsheet":                            "ods",
}

var extToMime = func() map[string]string {
	m := make(map[string]string, len(mimeToExt)+8)
	for k, v := range mimeToExt {
		m[v] = k
	}
	m["wav"] = "audio/wav"
	m["jpeg"] = "image/jpeg"
	m["jpe"] = "image/jpeg"
	m["jfif"] = "image/jpeg"
	m["m4v"] = "video/mp4"
	m["oga"] = "audio/ogg"
	m["opus"] = "audio/ogg"
	m["text"] = "text/plain"
	m["md"] = "text/markdown"
	m["csv"] = "text/csv"
	return m
}()

// baseMime strips parameters and lowercases ("audio/ogg; codecs=opus" -> "audio/ogg").
func baseMime(m string) string {
	m, _, _ = strings.Cut(m, ";")
	return strings.ToLower(strings.TrimSpace(m))
}

// mimeByExt maps a file extension (with or without dot) to a mime type, ""
// if unknown.
func mimeByExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ext == "" {
		return ""
	}
	if m, ok := extToMime[ext]; ok {
		return m
	}
	return baseMime(mime.TypeByExtension("." + ext))
}

// cleanExt returns a safe lowercase extension (no dot) or "".
func cleanExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	if ext == "" || len(ext) > 10 {
		return ""
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return ext
}

// extFor picks the cache file extension for ref: documents keep their own
// file name's extension, everything else maps from the mime type.
func extFor(ref *api.MediaRef) string {
	m := baseMime(ref.Mime)
	fileExt := cleanExt(filepath.Ext(ref.FileName))
	if ref.Type == api.MsgDocument && fileExt != "" {
		return fileExt
	}
	if e, ok := mimeToExt[m]; ok {
		return e
	}
	if fileExt != "" {
		return fileExt
	}
	if m != "" {
		if exts, _ := mime.ExtensionsByType(m); len(exts) > 0 {
			sort.Strings(exts)
			if e := cleanExt(exts[0]); e != "" {
				return e
			}
		}
	}
	switch ref.Type {
	case api.MsgImage:
		return "jpg"
	case api.MsgSticker:
		return "webp"
	case api.MsgVideo, api.MsgGIF:
		return "mp4"
	case api.MsgVoice, api.MsgAudio:
		return "ogg"
	}
	return "bin"
}

// removeQuiet removes a cache file and drops it from the index.
func (c *Cache) removeQuiet(path string) {
	if err := os.Remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
		c.forget(path)
	}
}
