package resume

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestIsResume(t *testing.T) {
	sig := func(path dbus.ObjectPath, name string, body ...any) *dbus.Signal {
		return &dbus.Signal{Path: path, Name: name, Body: body}
	}
	name := logindIface + "." + logindSignal
	for _, tc := range []struct {
		s    *dbus.Signal
		want bool
	}{
		{sig(logindPath, name, false), true},
		{sig(logindPath, name, true), false}, // going to sleep
		{sig(logindPath, name), false},
		{sig(logindPath, name, "false"), false},
		{sig("/elsewhere", name, false), false},
		{sig(logindPath, logindIface+".PrepareForShutdown", false), false},
		{nil, false},
	} {
		if got := isResume(tc.s); got != tc.want {
			t.Errorf("isResume(%v) = %v, want %v", tc.s, got, tc.want)
		}
	}
}
