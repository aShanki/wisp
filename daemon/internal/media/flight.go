package media

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// flight is a minimal singleflight: concurrent calls with the same key share
// one execution of fn. fn runs on its own goroutine with a context detached
// from any single caller (so one caller giving up doesn't abort the shared
// work, which still lands in the cache); each caller waits on its own ctx.
type flight struct {
	mu sync.Mutex
	m  map[string]*flightCall
}

type flightCall struct {
	done chan struct{}
	val  string
	err  error
}

func (f *flight) do(ctx context.Context, key string, timeout time.Duration, fn func(ctx context.Context) (string, error)) (string, error) {
	f.mu.Lock()
	if f.m == nil {
		f.m = make(map[string]*flightCall)
	}
	call, ok := f.m[key]
	if !ok {
		call = &flightCall{done: make(chan struct{})}
		f.m[key] = call
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		go func() {
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					call.err = fmt.Errorf("media: panic: %v", r)
				}
				f.mu.Lock()
				delete(f.m, key)
				f.mu.Unlock()
				close(call.done)
			}()
			call.val, call.err = fn(runCtx)
		}()
	}
	f.mu.Unlock()
	select {
	case <-call.done:
		return call.val, call.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
