package api

import "context"

// ProgressFunc is told how many bytes of a download have arrived, out of
// total (-1 if unknown).
type ProgressFunc func(done, total int64)

type progressKey struct{}

// WithProgress carries fn on ctx to whatever performs the download.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ProgressFrom returns the ProgressFunc on ctx, or nil.
func ProgressFrom(ctx context.Context) ProgressFunc {
	fn, _ := ctx.Value(progressKey{}).(ProgressFunc)
	return fn
}
