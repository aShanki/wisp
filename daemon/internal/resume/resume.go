// Package resume reports when the machine wakes from suspend, from
// systemd-logind's PrepareForSleep signal on the system bus. Nothing polls.
package resume

import (
	"context"
	"log/slog"

	"github.com/godbus/dbus/v5"
)

const (
	logindPath   = dbus.ObjectPath("/org/freedesktop/login1")
	logindIface  = "org.freedesktop.login1.Manager"
	logindSignal = "PrepareForSleep"
)

// Watch calls fn each time the system resumes from sleep, until ctx ends.
// It returns at once; without a system bus or logind it logs and does nothing.
func Watch(ctx context.Context, log *slog.Logger, fn func()) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Warn("resume: no system bus; reconnecting after sleep relies on keepalives", "err", err)
		return
	}
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(logindPath),
		dbus.WithMatchInterface(logindIface), dbus.WithMatchMember(logindSignal)); err != nil {
		conn.Close()
		log.Warn("resume: can't watch logind; reconnecting after sleep relies on keepalives", "err", err)
		return
	}
	sigs := make(chan *dbus.Signal, 4)
	conn.Signal(sigs)
	go func() {
		<-ctx.Done()
		conn.Close() // closes sigs
	}()
	go func() {
		for s := range sigs {
			if isResume(s) {
				fn()
			}
		}
	}()
}

// isResume reports whether s is logind's PrepareForSleep(false), sent right
// after the system wakes (PrepareForSleep(true) is sent before it sleeps).
func isResume(s *dbus.Signal) bool {
	if s == nil || s.Path != logindPath || s.Name != logindIface+"."+logindSignal || len(s.Body) != 1 {
		return false
	}
	sleeping, ok := s.Body[0].(bool)
	return ok && !sleeping
}
