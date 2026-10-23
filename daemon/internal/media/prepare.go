package media

import (
	"bytes"
	"context"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"wisp/daemon/internal/api"
)

const (
	// MaxSendBytes is the largest file Prepare accepts.
	MaxSendBytes = 100 << 20
	sendThumbPx  = 72 // WhatsApp jpegThumbnail
	sendThumbQ   = 70
)

// Prepare reads a local file for sending. JPEG and PNG files that decode go
// as images, stripped of metadata, with dimensions and a small JPEG
// thumbnail; everything else goes as a document.
func (c *Cache) Prepare(ctx context.Context, path string) (*api.OutgoingMedia, error) {
	if path == "" {
		return nil, api.InvalidParams("path is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, api.InvalidParams("cannot open %s: %v", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, api.InvalidParams("cannot stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, api.InvalidParams("%s is not a regular file", path)
	}
	if info.Size() > MaxSendBytes {
		return nil, api.InvalidParams("file too large (%d bytes, max %d)", info.Size(), MaxSendBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxSendBytes+1))
	if err != nil {
		return nil, api.InvalidParams("cannot read %s: %v", path, err)
	}
	if len(data) > MaxSendBytes {
		return nil, api.InvalidParams("file too large (max %d bytes)", MaxSendBytes)
	}
	if len(data) == 0 {
		return nil, api.InvalidParams("%s is empty", path)
	}

	m := detectMime(path, data)
	om := &api.OutgoingMedia{Type: api.MsgDocument, Mime: m, FileName: filepath.Base(path), Data: data}
	if m != "image/jpeg" && m != "image/png" {
		return om, nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return om, nil
	}
	stripped, ok := stripMeta(m, data)
	if !ok {
		return nil, api.InvalidParams("%s is not a valid image", filepath.Base(path))
	}
	om.Type, om.Width, om.Height, om.Data = api.MsgImage, cfg.Width, cfg.Height, stripped
	if thumb, _, err := thumbJPEG(bytes.NewReader(stripped), sendThumbPx, sendThumbQ); err == nil {
		om.Thumb = thumb
	} else {
		c.log.Debug("prepare: no thumbnail", "path", path, "err", err)
	}
	return om, nil
}

// detectMime combines the file extension and content sniffing. Content
// wins for images; otherwise the extension is more precise.
func detectMime(path string, data []byte) string {
	sniffed := baseMime(http.DetectContentType(data))
	byExt := mimeByExt(filepath.Ext(path))
	switch {
	case strings.HasPrefix(sniffed, "image/"):
		return sniffed
	case byExt != "":
		return byExt
	}
	return sniffed
}
