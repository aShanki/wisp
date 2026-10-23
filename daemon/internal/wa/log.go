package wa

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// slogAdapter adapts whatsmeow's printf-style logger to slog. whatsmeow is
// chatty at debug/info level; by default only warnings and errors pass (the
// handler's own level decides, with info demoted to debug).
type slogAdapter struct {
	l *slog.Logger
}

func newWALog(l *slog.Logger, module string) waLog.Logger {
	return &slogAdapter{l: l.With("wa", module)}
}

func (a *slogAdapter) log(level slog.Level, msg string, args []any) {
	if !a.l.Enabled(context.Background(), level) {
		return
	}
	a.l.Log(context.Background(), level, fmt.Sprintf(msg, args...))
}

// WhatsApp drops the connection without answering the close frame, so every
// clean disconnect would otherwise warn.
func (a *slogAdapter) Warnf(msg string, args ...any) {
	level := slog.LevelWarn
	if strings.HasPrefix(msg, "Error sending close to websocket") {
		level = slog.LevelDebug
	}
	a.log(level, msg, args)
}

func (a *slogAdapter) Errorf(msg string, args ...any) { a.log(slog.LevelError, msg, args) }

// whatsmeow's info logs are routine protocol chatter; keep them at debug.
func (a *slogAdapter) Infof(msg string, args ...any)  { a.log(slog.LevelDebug, msg, args) }
func (a *slogAdapter) Debugf(msg string, args ...any) { a.log(slog.LevelDebug-4, msg, args) }

func (a *slogAdapter) Sub(module string) waLog.Logger {
	return &slogAdapter{l: a.l.With("sub", module)}
}
