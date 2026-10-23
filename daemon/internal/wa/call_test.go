package wa

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"wisp/daemon/internal/api"
)

func TestTerminateReason(t *testing.T) {
	for in, want := range map[string]string{
		"":                   api.CallMissed,
		"timeout":            api.CallMissed,
		"accepted_elsewhere": api.CallAnswered,
		"rejected_elsewhere": api.CallDeclined,
	} {
		if got := terminateReason(in); got != want {
			t.Errorf("terminateReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEndCallOnce(t *testing.T) {
	c := &Client{calls: map[string]types.JID{"C1": {}}}
	var got []api.WAEvent
	h := func(ev api.WAEvent) { got = append(got, ev) }
	c.handler.Store(&h)
	c.endCall("C1", api.CallMissed)
	c.endCall("C1", api.CallMissed)
	c.endCall("C2", api.CallMissed)
	if len(got) != 1 || got[0] != (api.EvCallState{ID: "C1", State: api.CallEnded, Reason: api.CallMissed}) {
		t.Errorf("events = %+v", got)
	}
}

func TestEndReason(t *testing.T) {
	for _, tc := range []struct {
		in       string
		answered bool
		want     string
	}{
		{"rejected", false, api.CallRejected},
		{"hangup", false, api.CallHungUp},
		{"timeout", false, api.CallUnanswered},
		{"", true, api.CallHungUp},
		{"offer_failed", false, api.CallFailed},
		{"server:403", true, api.CallFailed},
	} {
		if got := endReason(tc.in, tc.answered); got != tc.want {
			t.Errorf("endReason(%q, %v) = %q, want %q", tc.in, tc.answered, got, tc.want)
		}
	}
}

// The caller exists from the start, but costs nothing until a call: no
// goroutines, no audio streams.
func TestCallerIsIdle(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, Config{DBPath: filepath.Join(t.TempDir(), "wa.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.caller() == nil {
		t.Fatal("no caller")
	}
	before := runtime.NumGoroutine()
	c.newCaller(c.client())
	if n := runtime.NumGoroutine(); n > before {
		t.Errorf("%d goroutines after a new caller, %d before", n, before)
	}
	if _, err := c.StartCall(ctx, alicePN.String()); !errors.Is(err, api.ErrNotLoggedIn) {
		t.Errorf("start offline: %v", err)
	}
	if err := c.MuteCall("X", true); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("mute unknown: %v", err)
	}
	if c.live != nil || c.tone != nil {
		t.Error("call state before any call")
	}
}
