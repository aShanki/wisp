//go:build !darwin

package notify

import (
	"context"
	"slices"
	"testing"

	"wisp/daemon/internal/api"
)

func TestCallNotification(t *testing.T) {
	n, srv, rec, _ := setup(t, kdeCaps)
	declined := make(chan string, 1)
	a := rec.actions()
	a.Decline = func(id string) { declined <- id }
	n.SetActions(a)
	ctx := context.Background()

	n.NotifyCall(ctx, api.CallNotification{ID: "C1", Chat: alice, Name: "Alice", Video: true})
	c := srv.nextCall()
	if c.Summary != "Alice" || c.Body != "Video call" || !slices.Contains(c.Actions, "decline") ||
		c.hint("category") != "call.incoming" || c.hint("resident") != true || c.Timeout != 0 {
		t.Fatalf("call notification = %+v", c)
	}
	srv.emit("ActionInvoked", c.ID, "decline")
	if got := recv(t, declined, "Decline"); got != "C1" {
		t.Fatalf("Decline(%q)", got)
	}
	if id := srv.nextClose(); id != c.ID {
		t.Fatalf("closed %d, want %d", id, c.ID)
	}
	none(t, rec.open, "Open")

	n.NotifyCall(ctx, api.CallNotification{ID: "C2", Chat: alice, Name: "Alice"})
	c = srv.nextCall()
	n.ClearCall("C2")
	if id := srv.nextClose(); id != c.ID {
		t.Fatalf("ClearCall closed %d, want %d", id, c.ID)
	}
	n.ClearCall("C2")
	srv.noClose()
}
