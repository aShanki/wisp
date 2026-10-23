package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"wisp/daemon/internal/api"
)

func webMsg(chat, id string, fromMe bool, t int64, m *waE2E.Message) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{RemoteJID: new(chat), FromMe: new(fromMe), ID: new(id)},
		MessageTimestamp: new(uint64(t)),
		Message:          m,
	}}
}

func TestHistory(t *testing.T) {
	now := time.Unix(1761200000, 0)
	cli := whatsmeow.NewClient(&store.Device{ID: &selfPN, LID: selfLID}, nil)

	read := webMsg(alicePN.String(), "H2", true, 1761190010, &waE2E.Message{Conversation: new("ok")})
	read.Message.Status = waWeb.WebMessageInfo_READ.Enum()
	read.Message.Reactions = []*waWeb.Reaction{{
		Key:               &waCommon.MessageKey{RemoteJID: new(alicePN.String()), ID: new("R")},
		Text:              new("❤️"),
		SenderTimestampMS: new(int64(1761190020000)),
	}}
	revoked := webMsg(alicePN.String(), "H3", false, 1761190030, nil)
	revoked.Message.MessageStubType = waWeb.WebMessageInfo_REVOKE.Enum()

	data := &waHistorySync.HistorySync{
		SyncType: waHistorySync.HistorySync_INITIAL_BOOTSTRAP.Enum(),
		Progress: new(uint32(40)),
		Conversations: []*waHistorySync.Conversation{
			{
				ID:                    new(aliceLID.String()),
				PnJID:                 new(alicePN.String()),
				UnreadCount:           new(uint32(2)),
				ConversationTimestamp: new(uint64(1761190030)),
				MuteEndTime:           new(uint64(now.Add(time.Hour).Unix())),
				Pinned:                new(uint32(1761000000)),
				Messages: []*waHistorySync.HistorySyncMsg{
					webMsg(aliceLID.String(), "H1", false, 1761190000, &waE2E.Message{Conversation: new("hello")}),
					read, revoked,
				},
			},
			{ID: new(group.String()), Name: new("Climbing"), Archived: new(true)},
			{ID: new("status@broadcast"), Messages: []*waHistorySync.HistorySyncMsg{
				webMsg("status@broadcast", "S1", false, 1761190000, &waE2E.Message{Conversation: new("story")}),
			}},
		},
		Pushnames: []*waHistorySync.Pushname{
			{ID: new(aliceLID.String()), Pushname: new("ally")},
			{ID: new("61400000002@s.whatsapp.net"), Pushname: new("-")},
		},
	}
	h := convertHistory(testConverter(), cli.ParseWebMessage, data, now)
	if h == nil {
		t.Fatal("nil history")
	}
	if h.Percent != 40 || h.Final {
		t.Errorf("progress %d final %v", h.Percent, h.Final)
	}

	if len(h.Chats) != 2 {
		t.Fatalf("chats %+v", h.Chats)
	}
	c := h.Chats[0]
	if c.JID != alicePN.String() || *c.Kind != api.ChatUser || *c.Unread != 2 || *c.Pinned != 1761000000 || c.Archived != nil ||
		*c.MutedUntil != now.Add(time.Hour).Unix() || *c.LastActivity != 1761190030 {
		t.Errorf("chat %+v", c)
	}
	// App state owns pin, mute, archive and marked-unread: history never clears them.
	g := h.Chats[1]
	if g.JID != group.String() || *g.Kind != api.ChatGroup || *g.Name != "Climbing" || !*g.Archived ||
		g.MutedUntil != nil || g.Pinned != nil || g.MarkedUnread != nil || g.ReadOnly != nil || *g.Unread != 0 {
		t.Errorf("group %+v", g)
	}

	if len(h.Messages) != 3 {
		t.Fatalf("messages %+v", h.Messages)
	}
	m1, m2, m3 := h.Messages[0], h.Messages[1], h.Messages[2]
	if m1.Chat != alicePN.String() || m1.Sender != alicePN.String() || m1.Text != "hello" || m1.SenderName != "Alice" {
		t.Errorf("m1 %+v", m1)
	}
	if !m2.FromMe || m2.Sender != selfPN.String() || m2.Status != api.StatusRead {
		t.Errorf("m2 %+v", m2)
	}
	if m3.Type != api.MsgRevoked || !m3.Deleted || m3.Chat != alicePN.String() {
		t.Errorf("m3 %+v", m3)
	}

	want := api.EvReaction{Chat: alicePN.String(), ID: "H2", Sender: alicePN.String(), Emoji: "❤️", Ts: 1761190020}
	if len(h.Reactions) != 1 || h.Reactions[0] != want {
		t.Errorf("reactions %+v", h.Reactions)
	}
	if len(h.Contacts) != 1 || h.Contacts[0] != (api.Contact{JID: alicePN.String(), Name: "ally", PushName: "ally", Phone: alicePN.User}) {
		t.Errorf("contacts %+v", h.Contacts)
	}
}

func TestHistoryMuteUntil(t *testing.T) {
	now := time.Unix(1761200000, 0)
	for in, want := range map[uint64]int64{0: 0, 1761100000: 0, 1761300000: 1761300000, 1 << 63: -1} {
		if got := historyMuteUntil(in, now); got != want {
			t.Errorf("historyMuteUntil(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestHistoryLIDPairs(t *testing.T) {
	bob := types.NewJID("902", types.HiddenUserServer)
	data := &waHistorySync.HistorySync{
		PhoneNumberToLidMappings: []*waHistorySync.PhoneNumberToLIDMapping{
			{PnJID: new("61400000002@c.us"), LidJID: new(bob.String())},
		},
		Conversations: []*waHistorySync.Conversation{
			{ID: new(aliceLID.String()), PnJID: new(alicePN.String())},
			{ID: new(group.String())},
		},
	}
	got := historyLIDPairs(data)
	want := []store.LIDMapping{
		{LID: bob, PN: types.NewJID("61400000002", types.DefaultUserServer)},
		{LID: aliceLID, PN: alicePN},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v", got)
	}
}
