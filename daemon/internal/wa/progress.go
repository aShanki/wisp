package wa

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"hash"
	"io"
	"net/http"
	"sync/atomic"

	"wisp/daemon/internal/api"
)

// newMediaHTTP is whatsmeow's default media client, plus progress: a
// download whose context carries an api.ProgressFunc hears of each read.
// whatsmeow has no progress hook of its own.
func newMediaHTTP() *http.Client {
	return &http.Client{Transport: progressTransport{http.DefaultTransport.(*http.Transport).Clone()}}
}

type progressTransport struct{ next http.RoundTripper }

func (t progressTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if err != nil || req.Method != http.MethodGet || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	if fn := api.ProgressFrom(req.Context()); fn != nil {
		resp.Body = &progressBody{ReadCloser: resp.Body, fn: fn, total: resp.ContentLength}
	}
	if bad, ok := req.Context().Value(hashCheckKey{}).(func()); ok {
		if want, err := base64.URLEncoding.DecodeString(req.URL.Query().Get("hash")); err == nil && len(want) == sha256.Size {
			resp.Body = &hashBody{ReadCloser: resp.Body, h: sha256.New(), want: want, bad: bad}
		}
	}
	return resp, nil
}

type progressBody struct {
	io.ReadCloser
	fn          api.ProgressFunc
	done, total int64
}

func (b *progressBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.done += int64(n)
		b.fn(b.done, b.total)
	}
	return n, err
}

type hashCheckKey struct{}

// withHashCheck returns a context for a download that is cancelled as soon
// as a whole file arrives that doesn't match the hash its URL asks for, and
// a func reporting whether that happened. Every host serves the same bytes,
// so whatsmeow trying the others would only fetch the same wrong file again.
func withHashCheck(ctx context.Context) (context.Context, func() bool, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	var mismatch atomic.Bool
	ctx = context.WithValue(ctx, hashCheckKey{}, func() {
		mismatch.Store(true)
		cancel()
	})
	return ctx, mismatch.Load, cancel
}

type hashBody struct {
	io.ReadCloser
	h    hash.Hash
	want []byte
	bad  func()
	done bool
}

func (b *hashBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.h.Write(p[:n])
	if err == io.EOF && !b.done {
		b.done = true
		if !bytes.Equal(b.h.Sum(nil), b.want) {
			b.bad()
		}
	}
	return n, err
}
