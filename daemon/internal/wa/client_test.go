package wa

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"wisp/daemon/internal/api"
)

// A fresh store is unpaired and stays offline: no network without a session.
func TestNewUnpaired(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, Config{DBPath: filepath.Join(t.TempDir(), "wa.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if c.AuthState() != api.AuthUnpaired || c.ConnState() != api.ConnOffline || c.IsLoggedIn() || c.SelfJID() != "" {
		t.Fatalf("auth %s conn %s", c.AuthState(), c.ConnState())
	}
	if err := c.SendText(ctx, alicePN.String(), c.NewMessageID(), "hi", nil, nil); !errors.Is(err, api.ErrNotLoggedIn) {
		t.Fatalf("send: %v", err)
	}
}
