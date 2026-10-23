package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

type fakeDL struct {
	calls atomic.Int32
	gate  chan struct{} // if non-nil, Download blocks until closed
	data  func(ref *api.MediaRef) ([]byte, error)
}

func (f *fakeDL) Download(ctx context.Context, ref *api.MediaRef) ([]byte, error) {
	f.calls.Add(1)
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.data != nil {
		return f.data(ref)
	}
	return []byte("data:" + ref.ID), nil
}

type fakeAvatars struct {
	mu    sync.Mutex
	calls int
	url   string
	id    string
	err   error
}

func (f *fakeAvatars) AvatarURL(ctx context.Context, jid string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.url, f.id, f.err
}

func (f *fakeAvatars) set(url, id string, err error) {
	f.mu.Lock()
	f.url, f.id, f.err = url, id, err
	f.mu.Unlock()
}

func (f *fakeAvatars) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newCache(t *testing.T, dir string, cfg Config) *Cache {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	cfg.Dir = dir
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.waitScan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func testImage(w, h int, alpha bool) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha && x < w/2 {
				a = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 128, a})
		}
	}
	return img
}

func genJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(w, h, false), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func genPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(w, h, true)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if format != "jpeg" {
		t.Fatalf("format %q, want jpeg", format)
	}
	return cfg.Width, cfg.Height
}

func fileJPEGSize(t *testing.T, path string) (int, int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return jpegSize(t, data)
}

func ref(id string) *api.MediaRef {
	return &api.MediaRef{Chat: "61400000001@s.whatsapp.net", ID: id, Type: api.MsgImage, Mime: "image/jpeg"}
}

func size(c *Cache) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func cached(c *Cache, ref *api.MediaRef) (string, bool) { return c.lookupFile(ref) }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestFetchHitMiss(t *testing.T) {
	dl := &fakeDL{}
	c := newCache(t, "", Config{Downloader: dl})
	ctx := context.Background()

	p, err := c.Fetch(ctx, ref("A"))
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p) || !strings.HasPrefix(p, filepath.Join(c.dir, "files")+"/") || filepath.Ext(p) != ".jpg" {
		t.Fatalf("unexpected path %s", p)
	}
	if got, _ := os.ReadFile(p); string(got) != "data:A" {
		t.Fatalf("content %q", got)
	}
	p2, err := c.Fetch(ctx, ref("A"))
	if err != nil || p2 != p {
		t.Fatalf("second fetch: %v %s", err, p2)
	}
	if n := dl.calls.Load(); n != 1 {
		t.Fatalf("downloads = %d, want 1", n)
	}
	if cp, ok := cached(c, ref("A")); !ok || cp != p {
		t.Fatalf("Cached = %s %v", cp, ok)
	}
	if _, ok := cached(c, ref("B")); ok {
		t.Fatal("Cached(B) should miss")
	}
	if size(c) != int64(len("data:A")) {
		t.Fatalf("size %d", size(c))
	}
	// No temp files left behind.
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(p), tempPrefix+"*"))
	if len(matches) != 0 {
		t.Fatalf("temp files left: %v", matches)
	}
}

func TestFetchSingleflight(t *testing.T) {
	dl := &fakeDL{gate: make(chan struct{})}
	c := newCache(t, "", Config{Downloader: dl})
	const n = 10
	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			paths[i], errs[i] = c.Fetch(context.Background(), ref("S"))
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(dl.gate)
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("call %d: %v %s", i, errs[i], paths[i])
		}
	}
	if got := dl.calls.Load(); got != 1 {
		t.Fatalf("downloads = %d, want 1", got)
	}
}

func TestFetchCallerGivesUp(t *testing.T) {
	dl := &fakeDL{gate: make(chan struct{})}
	c := newCache(t, "", Config{Downloader: dl})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Fetch(ctx, ref("X")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	close(dl.gate) // the shared download still completes and is cached
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := cached(c, ref("X")); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("download did not land in the cache")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := c.Fetch(context.Background(), ref("X")); err != nil {
		t.Fatal(err)
	}
	if got := dl.calls.Load(); got != 1 {
		t.Fatalf("downloads = %d, want 1", got)
	}
}

func TestFetchErrors(t *testing.T) {
	dl := &fakeDL{data: func(*api.MediaRef) ([]byte, error) { return nil, api.ErrMediaExpired }}
	c := newCache(t, "", Config{Downloader: dl})
	if _, err := c.Fetch(context.Background(), ref("E")); !errors.Is(err, api.ErrMediaExpired) {
		t.Fatalf("err = %v, want ErrMediaExpired", err)
	}
	if _, err := c.Fetch(context.Background(), &api.MediaRef{ID: "x"}); !errors.Is(err, api.ErrInvalidParams) {
		t.Fatalf("err = %v, want ErrInvalidParams", err)
	}
	c2 := newCache(t, "", Config{})
	if _, err := c2.Fetch(context.Background(), ref("E")); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPutThenFetch(t *testing.T) {
	dl := &fakeDL{}
	c := newCache(t, "", Config{Downloader: dl})
	r := &api.MediaRef{Chat: "c@g.us", ID: "P", Type: api.MsgDocument, Mime: "application/pdf", FileName: "Report.PDF"}
	p, err := c.Put(context.Background(), r, []byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(p) != ".pdf" {
		t.Fatalf("ext of %s", p)
	}
	p2, err := c.Fetch(context.Background(), r)
	if err != nil || p2 != p || dl.calls.Load() != 0 {
		t.Fatalf("fetch after put: %v %s calls=%d", err, p2, dl.calls.Load())
	}
	// A later lookup with a different mime still finds the file.
	r2 := *r
	r2.Type, r2.Mime, r2.FileName = api.MsgImage, "image/png", ""
	if p3, ok := cached(c, &r2); !ok || p3 != p {
		t.Fatalf("Cached with other mime = %s %v", p3, ok)
	}
}

func TestWipe(t *testing.T) {
	dl := &fakeDL{}
	c := newCache(t, "", Config{Downloader: dl})
	ctx := context.Background()
	p, err := c.Fetch(ctx, ref("A"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Wipe(); err != nil {
		t.Fatal(err)
	}
	if exists(p) || size(c) != 0 {
		t.Fatalf("after wipe: file %v, size %d", exists(p), size(c))
	}
	if _, err := c.Fetch(ctx, ref("A")); err != nil || dl.calls.Load() != 2 {
		t.Fatalf("fetch after wipe: %v, downloads %d", err, dl.calls.Load())
	}
}

func TestExtFor(t *testing.T) {
	cases := []struct {
		ref  api.MediaRef
		want string
	}{
		{api.MediaRef{Type: api.MsgVoice, Mime: "audio/ogg; codecs=opus"}, "ogg"},
		{api.MediaRef{Type: api.MsgImage, Mime: "image/jpeg"}, "jpg"},
		{api.MediaRef{Type: api.MsgSticker, Mime: "image/webp"}, "webp"},
		{api.MediaRef{Type: api.MsgVideo, Mime: "video/mp4"}, "mp4"},
		{api.MediaRef{Type: api.MsgDocument, Mime: "application/octet-stream", FileName: "a.tar.GZ"}, "gz"},
		{api.MediaRef{Type: api.MsgDocument, Mime: "application/pdf", FileName: "noext"}, "pdf"},
		{api.MediaRef{Type: api.MsgDocument, Mime: "", FileName: "../../etc/pa$$wd"}, "bin"},
		{api.MediaRef{Type: api.MsgImage, Mime: ""}, "jpg"},
	}
	for _, tc := range cases {
		if got := extFor(&tc.ref); got != tc.want {
			t.Errorf("extFor(%+v) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestEvictionOrder(t *testing.T) {
	c := newCache(t, "", Config{CapBytes: 250, Downloader: &fakeDL{}})
	ctx := context.Background()
	blob := bytes.Repeat([]byte("x"), 100)
	put := func(id string) string {
		p, err := c.Put(ctx, ref(id), blob)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := put("a"), put("b")
	cc := put("c") // 300 > 250: a (oldest) goes
	if exists(a) || !exists(b) || !exists(cc) {
		t.Fatalf("after c: a=%v b=%v c=%v", exists(a), exists(b), exists(cc))
	}
	if _, err := c.Fetch(ctx, ref("b")); err != nil { // b becomes most recent
		t.Fatal(err)
	}
	d := put("d") // c is now the oldest
	if exists(cc) || !exists(b) || !exists(d) {
		t.Fatalf("after d: b=%v c=%v d=%v", exists(b), exists(cc), exists(d))
	}
	if size(c) != 200 {
		t.Fatalf("size %d", size(c))
	}
	c.SetCap(150)
	if exists(b) || !exists(d) || size(c) != 100 {
		t.Fatalf("after SetCap: b=%v d=%v size=%d", exists(b), exists(d), size(c))
	}
	// A single file bigger than the cap is kept (just written) but evicts
	// everything else.
	big, err := c.Put(ctx, ref("big"), bytes.Repeat([]byte("y"), 400))
	if err != nil {
		t.Fatal(err)
	}
	if !exists(big) || exists(d) {
		t.Fatalf("big=%v d=%v", exists(big), exists(d))
	}
}

func TestScanRebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	c := newCache(t, dir, Config{})
	ctx := context.Background()
	blob := bytes.Repeat([]byte("z"), 100)
	var paths []string
	for i, id := range []string{"old", "mid", "new"} {
		p, err := c.Put(ctx, ref(id), blob)
		if err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(i-3) * 24 * time.Hour)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	// Stale and fresh leftover temp files.
	stale := filepath.Join(filepath.Dir(paths[0]), tempPrefix+"stale")
	fresh := filepath.Join(filepath.Dir(paths[0]), tempPrefix+"fresh")
	os.WriteFile(stale, []byte("t"), 0o600)
	os.WriteFile(fresh, []byte("t"), 0o600)
	old := time.Now().Add(-2 * staleTemp)
	os.Chtimes(stale, old, old)

	c2 := newCache(t, dir, Config{CapBytes: 250})
	if size(c2) != 200 {
		t.Fatalf("size after scan = %d", size(c2))
	}
	if exists(paths[0]) || !exists(paths[1]) || !exists(paths[2]) {
		t.Fatalf("scan eviction: old=%v mid=%v new=%v", exists(paths[0]), exists(paths[1]), exists(paths[2]))
	}
	if exists(stale) || !exists(fresh) {
		t.Fatalf("temp cleanup: stale=%v fresh=%v", exists(stale), exists(fresh))
	}
	// A hit persists the use time in the mtime (it was days old).
	if _, ok := cached(c2, ref("mid")); !ok {
		t.Fatal("mid should be cached")
	}
	if info, _ := os.Stat(paths[1]); time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("mtime not bumped: %v", info.ModTime())
	}
}

func TestThumbEmbedded(t *testing.T) {
	c := newCache(t, "", Config{})
	emb := genJPEG(t, 32, 24)
	p, err := c.Thumb(context.Background(), ref("T"), emb)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, emb) {
		t.Fatal("thumb content differs from embedded bytes")
	}
	if !strings.HasPrefix(p, filepath.Join(c.dir, "thumbs")+"/") {
		t.Fatalf("path %s", p)
	}
	// Cached afterwards even without embedded bytes.
	if p2, err := c.Thumb(context.Background(), ref("T"), nil); err != nil || p2 != p {
		t.Fatalf("second: %v %s", err, p2)
	}
}

func TestThumbGenerated(t *testing.T) {
	c := newCache(t, "", Config{})
	ctx := context.Background()
	if _, err := c.Thumb(ctx, ref("G"), nil); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := c.Put(ctx, ref("G"), genJPEG(t, 400, 300)); err != nil {
		t.Fatal(err)
	}
	p, err := c.Thumb(ctx, ref("G"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := fileJPEGSize(t, p); w != 100 || h != 75 {
		t.Fatalf("thumb %dx%d, want 100x75", w, h)
	}
	// PNG with alpha, portrait.
	r := &api.MediaRef{Chat: "c", ID: "PNG", Type: api.MsgImage, Mime: "image/png"}
	if _, err := c.Put(ctx, r, genPNG(t, 50, 200)); err != nil {
		t.Fatal(err)
	}
	p, err = c.Thumb(ctx, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := fileJPEGSize(t, p); w != 25 || h != 100 {
		t.Fatalf("thumb %dx%d, want 25x100", w, h)
	}
	// Not an image.
	r = &api.MediaRef{Chat: "c", ID: "DOC", Type: api.MsgDocument, Mime: "text/plain"}
	if _, err := c.Put(ctx, r, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Thumb(ctx, r, nil); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("doc thumb err = %v", err)
	}
}

func TestPrepare(t *testing.T) {
	c := newCache(t, "", Config{})
	ctx := context.Background()
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	jp := write("photo.jpg", genJPEG(t, 200, 100))
	om, err := c.Prepare(ctx, jp)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgImage || om.Mime != "image/jpeg" || om.Width != 200 || om.Height != 100 || om.FileName != "photo.jpg" {
		t.Fatalf("jpeg: %+v", summary(om))
	}
	if w, h := jpegSize(t, om.Thumb); w != 72 || h != 36 {
		t.Fatalf("jpeg thumb %dx%d", w, h)
	}

	// PNG content with a misleading extension: content wins.
	pp := write("shot.jpg", genPNG(t, 30, 60))
	om, err = c.Prepare(ctx, pp)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgImage || om.Mime != "image/png" || om.Width != 30 || om.Height != 60 {
		t.Fatalf("png: %+v", summary(om))
	}
	if w, h := jpegSize(t, om.Thumb); w != 30 || h != 60 {
		t.Fatalf("png thumb %dx%d", w, h)
	}

	tp := write("notes.txt", []byte("hello world\n"))
	om, err = c.Prepare(ctx, tp)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgDocument || om.Mime != "text/plain" || om.Thumb != nil || string(om.Data) != "hello world\n" {
		t.Fatalf("text: %+v", summary(om))
	}

	bp := write("blob", []byte{0, 1, 2, 3, 0xff, 0xfe})
	om, err = c.Prepare(ctx, bp)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgDocument || om.Mime != "application/octet-stream" {
		t.Fatalf("blob: %+v", summary(om))
	}

	// Broken image: claimed png, isn't decodable -> document.
	xp := write("broken.png", []byte("\x89PNG\r\n\x1a\nnot really"))
	om, err = c.Prepare(ctx, xp)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgDocument {
		t.Fatalf("broken: %+v", summary(om))
	}

	for _, bad := range []string{"", filepath.Join(dir, "missing.jpg"), dir, write("empty.txt", nil)} {
		if _, err := c.Prepare(ctx, bad); !errors.Is(err, api.ErrInvalidParams) {
			t.Fatalf("Prepare(%q) err = %v", bad, err)
		}
	}
}

func summary(om *api.OutgoingMedia) string {
	return fmt.Sprintf("type=%s mime=%s name=%s %dx%d data=%d thumb=%d",
		om.Type, om.Mime, om.FileName, om.Width, om.Height, len(om.Data), len(om.Thumb))
}

func TestDetectMime(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		mime string
	}{
		{"a.mp4", []byte("\x00\x00\x00\x18ftypmp42"), "video/mp4"},
		{"a.pdf", []byte("%PDF-1.7"), "application/pdf"},
		{"a.jpg", genPNG(t, 2, 2), "image/png"},
		{"a.webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
	} {
		if m := detectMime(tc.name, tc.data); m != tc.mime {
			t.Errorf("%s: mime %q, want %q", tc.name, m, tc.mime)
		}
	}
}

func TestAvatar(t *testing.T) {
	var hits atomic.Int32
	img := genPNG(t, 600, 400)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(img)
	}))
	defer srv.Close()

	src := &fakeAvatars{url: srv.URL + "/p1", id: "111"}
	dir := t.TempDir()
	c := newCache(t, dir, Config{Avatars: src, HTTP: srv.Client()})
	ctx := context.Background()
	const jid = "61400000001@s.whatsapp.net"

	p, err := c.Avatar(ctx, jid, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "61400000001@s.whatsapp.net-111.jpg" || filepath.Dir(p) != filepath.Join(c.dir, "avatars") {
		t.Fatalf("path %s", p)
	}
	if w, h := fileJPEGSize(t, p); w != 256 || h != 170 {
		t.Fatalf("avatar %dx%d", w, h)
	}
	// Cached: no lookups.
	if p2, err := c.Avatar(ctx, jid, false); err != nil || p2 != p || src.n() != 1 || hits.Load() != 1 {
		t.Fatalf("cached: %v %s calls=%d hits=%d", err, p2, src.n(), hits.Load())
	}
	// Refresh, same id: lookup but no download.
	if p2, err := c.Avatar(ctx, jid, true); err != nil || p2 != p || src.n() != 2 || hits.Load() != 1 {
		t.Fatalf("refresh same id: %v %s calls=%d hits=%d", err, p2, src.n(), hits.Load())
	}
	// Refresh, new id: new file, old one removed.
	src.set(srv.URL+"/p2", "222", nil)
	p3, err := c.Avatar(ctx, jid, true)
	if err != nil || p3 == p || hits.Load() != 2 {
		t.Fatalf("refresh new id: %v %s hits=%d", err, p3, hits.Load())
	}
	if exists(p) || !exists(p3) {
		t.Fatalf("old=%v new=%v", exists(p), exists(p3))
	}
	// Transient error on refresh keeps the cached picture.
	src.set("", "", api.ErrNotConnected)
	if p4, err := c.Avatar(ctx, jid, true); err != nil || p4 != p3 {
		t.Fatalf("transient: %v %s", err, p4)
	}

	// A fresh Cache on the same dir finds the avatar via the startup scan.
	c2 := newCache(t, dir, Config{})
	if p5, err := c2.Avatar(ctx, jid, false); err != nil || p5 != p3 {
		t.Fatalf("after restart: %v %s", err, p5)
	}

	// Picture removed: refresh says not found, file is deleted.
	src.set("", "", api.ErrNotFound)
	if _, err := c.Avatar(ctx, jid, true); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("removed: %v", err)
	}
	if exists(p3) {
		t.Fatal("stale avatar kept")
	}
	// HTTP 404 also counts as no picture.
	src.set(srv.URL+"/gone", "333", nil)
	if _, err := c.Avatar(ctx, "x@g.us", false); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
}

func TestAvatarNegativeCache(t *testing.T) {
	src := &fakeAvatars{err: api.ErrNotFound}
	c := newCache(t, "", Config{Avatars: src})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := c.Avatar(ctx, "a@s.whatsapp.net", false); !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	}
	if src.n() != 1 {
		t.Fatalf("lookups = %d, want 1", src.n())
	}
	if _, err := c.Avatar(ctx, "a@s.whatsapp.net", true); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if src.n() != 2 {
		t.Fatalf("refresh should bypass the negative cache, lookups = %d", src.n())
	}
	// Transient errors are not remembered.
	src.set("", "", api.ErrNotConnected)
	c.Avatar(ctx, "b@s.whatsapp.net", false)
	c.Avatar(ctx, "b@s.whatsapp.net", false)
	if src.n() != 4 {
		t.Fatalf("lookups = %d, want 4", src.n())
	}
	// Expiry.
	c.avMu.Lock()
	c.noPic[sanitizeJID("a@s.whatsapp.net")] = time.Now().Add(-time.Second)
	c.avMu.Unlock()
	c.Avatar(ctx, "a@s.whatsapp.net", false)
	if src.n() != 5 {
		t.Fatalf("expired entry should look up again, lookups = %d", src.n())
	}
}

func TestAvatarSingleflightAndLimits(t *testing.T) {
	gate := make(chan struct{})
	var hits atomic.Int32
	big := bytes.Repeat([]byte("x"), avatarMaxBytes+10)
	small := genJPEG(t, 96, 96)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/big" {
			w.Write(big)
			return
		}
		<-gate
		w.Write(small)
	}))
	defer srv.Close()
	src := &fakeAvatars{url: srv.URL + "/s", id: "9"}
	c := newCache(t, "", Config{Avatars: src, HTTP: srv.Client()})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Avatar(context.Background(), "g@g.us", false); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if hits.Load() != 1 || src.n() != 1 {
		t.Fatalf("hits=%d lookups=%d, want 1/1", hits.Load(), src.n())
	}
	// Small JPEG is stored byte-for-byte.
	p, _ := c.Avatar(context.Background(), "g@g.us", false)
	if got, _ := os.ReadFile(p); !bytes.Equal(got, small) {
		t.Fatal("small jpeg was re-encoded")
	}
	src.set(srv.URL+"/big", "10", nil)
	if _, err := c.Avatar(context.Background(), "h@g.us", false); err == nil || errors.Is(err, api.ErrNotFound) {
		t.Fatalf("oversized avatar: err = %v", err)
	}
}

func TestParseAvatarName(t *testing.T) {
	key := sanitizeJID("61400000001-1600000000@g.us")
	k, id, ok := parseAvatarName(key + "-12345.jpg")
	if !ok || k != key || id != "12345" {
		t.Fatalf("got %q %q %v", k, id, ok)
	}
	if _, _, ok := parseAvatarName("foo.png"); ok {
		t.Fatal("non-jpg parsed")
	}
	if got := sanitizeJID("a:1/../b@s.whatsapp.net"); strings.ContainsAny(got, "/:") {
		t.Fatalf("unsafe: %q", got)
	}
}

func TestGiantImageRefused(t *testing.T) {
	// A PNG header claiming 10000x10000 (100 MP): DecodeConfig succeeds,
	// decoding must be refused before allocating anything.
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := []byte("IHDR\x00\x00\x27\x10\x00\x00\x27\x10\x08\x02\x00\x00\x00")
	binary.Write(&buf, binary.BigEndian, uint32(len(ihdr)-4))
	buf.Write(ihdr)
	binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(ihdr))
	data := buf.Bytes()

	if _, _, err := thumbJPEG(bytes.NewReader(data), 100, 70); !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want errTooLarge", err)
	}
	c := newCache(t, "", Config{})
	p := filepath.Join(t.TempDir(), "huge.png")
	os.WriteFile(p, data, 0o600)
	om, err := c.Prepare(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if om.Type != api.MsgImage || om.Width != 10000 || om.Thumb != nil {
		t.Fatalf("huge: %s", summary(om))
	}
}
