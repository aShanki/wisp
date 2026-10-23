package wa

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
)

func TestMembers(t *testing.T) {
	bobPN := types.NewJID("61400000002", types.DefaultUserServer)
	bobLID := types.NewJID("902", types.HiddenUserServer)
	stranger := types.NewJID("999", types.HiddenUserServer)
	g := &types.GroupInfo{JID: group, Participants: []types.GroupParticipant{
		{JID: selfLID, PhoneNumber: selfPN, LID: selfLID, IsAdmin: true},
		{JID: aliceLID, IsSuperAdmin: true},
		{JID: bobLID, PhoneNumber: bobPN, LID: bobLID},
		{JID: stranger},
	}}
	want := []api.GroupMember{{JID: alicePN.String(), Admin: true}, {JID: bobPN.String()}, {JID: stranger.String()}}
	if got := testConverter().members(g); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

// Group metadata is cached until a change to the group is seen.
func TestGroupCache(t *testing.T) {
	ctx := context.Background()
	c, err := New(ctx, Config{DBPath: filepath.Join(t.TempDir(), "wa.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.GroupMembers(ctx, group.String()); !errors.Is(err, api.ErrNotLoggedIn) {
		t.Fatalf("members while unpaired: %v", err)
	}
	cli := c.client()
	g := &types.GroupInfo{JID: group}
	c.groups[group] = g
	if got, err := c.groupInfo(ctx, cli, group); got != g || err != nil {
		t.Fatalf("cached: %v %v", got, err)
	}
	c.onEvent(cli, &events.GroupInfo{JID: group, Join: []types.JID{alicePN}})
	if _, err := c.groupInfo(ctx, cli, group); !errors.Is(err, api.ErrNotConnected) {
		t.Fatalf("after a change: %v", err)
	}
	c.groups[group] = g
	c.onEvent(cli, &events.JoinedGroup{GroupInfo: types.GroupInfo{JID: group}})
	if len(c.groups) != 0 {
		t.Fatal("joining kept the old copy")
	}
}
