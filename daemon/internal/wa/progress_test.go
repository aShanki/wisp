package wa

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"wisp/daemon/internal/api"
)

func TestMediaHTTPProgress(t *testing.T) {
	body := strings.Repeat("x", 100000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		io.WriteString(w, body)
	}))
	defer srv.Close()

	var last, total int64
	ctx := api.WithProgress(context.Background(), func(d, t int64) { last, total = d, t })
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := newMediaHTTP().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || len(data) != len(body) {
		t.Fatalf("read %d bytes, err %v", len(data), err)
	}
	if last != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("progress %d/%d, want %d/%d", last, total, len(body), len(body))
	}

	// Without a callback on the context the body is left alone.
	resp, err = newMediaHTTP().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, ok := resp.Body.(*progressBody); ok {
		t.Fatal("body wrapped without a progress callback")
	}
}

func TestMediaHTTPHashMismatch(t *testing.T) {
	body := strings.Repeat("y", 50000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body)
	}))
	defer srv.Close()

	fetch := func(want []byte) bool {
		ctx, mismatch, cancel := withHashCheck(context.Background())
		defer cancel()
		u := srv.URL + "/f?hash=" + base64.URLEncoding.EncodeToString(want) + "&mms-type=video"
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := newMediaHTTP().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		if mismatch() != (ctx.Err() != nil) {
			t.Fatalf("mismatch %v but ctx err %v", mismatch(), ctx.Err())
		}
		return mismatch()
	}
	right := sha256.Sum256([]byte(body))
	if fetch(right[:]) {
		t.Fatal("matching file reported as a mismatch")
	}
	wrong := sha256.Sum256([]byte("other"))
	if !fetch(wrong[:]) {
		t.Fatal("mismatching file not reported")
	}
}
