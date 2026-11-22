package media

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"wisp/daemon/internal/api"
)

func TestPrepareMP4(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable: %v", tool, err)
		}
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recording.mp4")
	// The default muxing layout writes the MP4 metadata at the end.
	if out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "color=c=blue:s=160x90:r=10:d=1.2", "-c:v", "libx264", "-threads", "1", path).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cache := newCache(t, "", Config{})
	om, err := cache.Prepare(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgVideo || om.Mime != "video/mp4" || om.Width != 160 || om.Height != 90 ||
		om.Duration != 2 || !bytes.Equal(om.Data, data) {
		t.Fatalf("prepared video: %s, duration %d", summary(om), om.Duration)
	}
	if w, h := jpegSize(t, om.Thumb); w != 72 || h != 41 {
		t.Fatalf("video thumbnail: %dx%d", w, h)
	}

	// Before video sending was supported, these bytes were cached as a
	// document with no embedded thumbnail.
	ref := &api.MediaRef{Chat: "c", ID: "legacy", Type: api.MsgDocument, Mime: "video/mp4", FileName: "recording.mp4"}
	if _, err := cache.Put(ctx, ref, data); err != nil {
		t.Fatal(err)
	}
	thumb, err := cache.Thumb(ctx, ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := fileJPEGSize(t, thumb); w != 100 || h != 56 {
		t.Fatalf("cached clip thumbnail: %dx%d", w, h)
	}

	if err := os.WriteFile(path, []byte("not a clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	om, err = cache.Prepare(ctx, path)
	if err != nil || om.Type != api.MsgDocument {
		t.Fatalf("invalid MP4: %+v, %v", om, err)
	}
}
