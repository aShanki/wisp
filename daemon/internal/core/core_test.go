package core

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

const (
	alice = "61400000001@s.whatsapp.net"
	group = "120363000000000001@g.us"
)

func incoming(chat, id, sender, text string) *api.Message {
	return &api.Message{ID: id, Chat: chat, Sender: sender, Timestamp: time.Now().Unix(), Type: api.MsgText, Text: text, PushName: "Ali"}
}

// mentioning is a group message from Alice that mentions jid.
func mentioning(jid string) *api.Message {
	m := incoming(group, "X", alice, "@"+strings.TrimSuffix(jid, "@s.whatsapp.net")+" look")
	m.Mentions = []api.Mention{{JID: jid, Me: jid == selfJID}}
	return m
}

func chatEvents(evs []event) []api.Chat {
	var out []api.Chat
	for _, e := range evs {
		if e.name == api.EChatUpdate {
			out = append(out, e.params.(api.ChatEvent).Chat)
		}
	}
	return out
}

func TestIncomingMessageEmitsAndNotifies(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "hello there"), Live: true})

	if got, want := h.ev.names(), []string{api.EMessageNew, api.EChatUpdate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	msg := h.ev.all()[0].params.(api.MessageEvent).Message
	if msg.Reactions == nil {
		t.Error("reactions must be [] not null")
	}
	if msg.SenderName != "Ali" {
		t.Errorf("senderName = %q, want push name", msg.SenderName)
	}
	ch := chatEvents(h.ev.all())[0]
	if ch.Unread != 1 || ch.LastMessage == nil || ch.LastMessage.ID != "M1" {
		t.Errorf("chat = %+v", ch)
	}
	if ch.Name != "Ali" {
		t.Errorf("chat name = %q, want resolved push name", ch.Name)
	}
	if len(h.nt.shown) != 1 {
		t.Fatalf("notifications = %d, want 1", len(h.nt.shown))
	}
	n := h.nt.shown[0]
	if n.Chat != alice || n.Text != "hello there" || n.IsGroup || n.Sender != "" || n.MsgID != "M1" {
		t.Errorf("notification = %+v", n)
	}
	if ct, _ := h.st.GetContact(context.Background(), alice); ct == nil || ct.PushName != "Ali" {
		t.Errorf("push name not learned: %+v", ct)
	}
}

func TestGroupNotificationHasSender(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(group, "G1", alice, "yo"), Live: true})
	if len(h.nt.shown) != 1 {
		t.Fatalf("notifications = %d", len(h.nt.shown))
	}
	if n := h.nt.shown[0]; !n.IsGroup || n.Sender != "Ali" {
		t.Errorf("notification = %+v", n)
	}
}

func TestNotificationRules(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		setup  func(h *harness)
		msg    func() *api.Message
		live   bool
		notify bool
	}{
		{"live incoming", nil, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, true},
		{"not live", nil, func() *api.Message { return incoming(alice, "X", alice, "hi") }, false, false},
		{"from me", nil, func() *api.Message {
			m := incoming(alice, "X", selfJID, "hi")
			m.FromMe = true
			return m
		}, true, false},
		{"system", nil, func() *api.Message {
			m := incoming(group, "X", alice, "Alice added Bob")
			m.Type = api.MsgSystem
			return m
		}, true, false},
		{"muted forever", func(h *harness) {
			v := int64(-1)
			h.st.UpsertChat(ctx, api.ChatPatch{JID: alice, MutedUntil: &v})
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, false},
		{"muted until future", func(h *harness) {
			v := time.Now().Add(time.Hour).Unix()
			h.st.UpsertChat(ctx, api.ChatPatch{JID: alice, MutedUntil: &v})
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, false},
		{"mute expired", func(h *harness) {
			v := time.Now().Add(-time.Hour).Unix()
			h.st.UpsertChat(ctx, api.ChatPatch{JID: alice, MutedUntil: &v})
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, true},
		{"notifications disabled", func(h *harness) {
			if err := h.c.SettingsSet(ctx, api.SettingsSetParams{Key: api.SettingNotifications, Value: false}); err != nil {
				panic(err)
			}
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, false},
		{"muted, mentions us", func(h *harness) {
			v := int64(-1)
			h.st.UpsertChat(ctx, api.ChatPatch{JID: group, MutedUntil: &v})
		}, func() *api.Message { return mentioning(selfJID) }, true, true},
		{"muted, mentions someone else", func(h *harness) {
			v := int64(-1)
			h.st.UpsertChat(ctx, api.ChatPatch{JID: group, MutedUntil: &v})
		}, func() *api.Message { return mentioning("61400000002@s.whatsapp.net") }, true, false},
		{"notifications disabled, mentions us", func(h *harness) {
			if err := h.c.SettingsSet(ctx, api.SettingsSetParams{Key: api.SettingNotifications, Value: false}); err != nil {
				panic(err)
			}
		}, func() *api.Message { return mentioning(selfJID) }, true, false},
		{"active and focused", func(h *harness) {
			h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: alice, Focused: true})
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, false},
		{"active but unfocused", func(h *harness) {
			h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: alice, Focused: false})
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, true},
		{"focused on other chat", func(h *harness) {
			h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: group, Focused: true})
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, true},
		{"active client gone", func(h *harness) {
			h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: alice, Focused: true})
			h.c.ClientGone(1)
		}, func() *api.Message { return incoming(alice, "X", alice, "hi") }, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if tc.setup != nil {
				tc.setup(h)
			}
			h.c.handle(api.EvMessage{Msg: tc.msg(), Live: tc.live})
			if got := len(h.nt.shown) == 1; got != tc.notify {
				t.Errorf("notified = %v, want %v", got, tc.notify)
			}
		})
	}
}

func TestPreviewSettingHidesText(t *testing.T) {
	h := newHarness(t)
	if err := h.c.SettingsSet(context.Background(), api.SettingsSetParams{Key: api.SettingNotifyPreview, Value: false}); err != nil {
		t.Fatal(err)
	}
	h.c.handle(api.EvMessage{Msg: incoming(group, "G1", alice, "secret"), Live: true})
	if len(h.nt.shown) != 1 {
		t.Fatal("expected a notification")
	}
	if n := h.nt.shown[0]; n.Text == "secret" || n.Sender != "" {
		t.Errorf("preview leaked: %+v", n)
	}
}

func TestActiveFocusedChatIsMarkedRead(t *testing.T) {
	h := newHarness(t)
	h.c.UISetActive(clientCtx(7), api.UISetActiveParams{Chat: alice, Focused: true})
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "hi"), Live: true})

	chats := chatEvents(h.ev.all())
	if len(chats) != 1 || chats[0].Unread != 0 {
		t.Fatalf("chat updates = %+v, want one with unread 0", chats)
	}
	if len(h.wa.marked) != 1 || len(h.wa.marked[0]) != 1 || h.wa.marked[0][0].ID != "M1" {
		t.Errorf("read receipts = %+v", h.wa.marked)
	}
}

func TestSetActiveMarksRead(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "hi"), Live: true})
	h.ev.reset()
	h.c.UISetActive(clientCtx(7), api.UISetActiveParams{Chat: alice, Focused: false})
	if len(h.ev.all()) != 0 {
		t.Fatalf("unfocused setActive emitted %v", h.ev.names())
	}
	h.c.UISetActive(clientCtx(7), api.UISetActiveParams{Chat: alice, Focused: true})
	if chats := chatEvents(h.ev.all()); len(chats) != 1 || chats[0].Unread != 0 {
		t.Fatalf("chat updates = %+v", chats)
	}
	if len(h.wa.marked) != 1 || h.wa.marked[0][0].ID != "M1" {
		t.Errorf("read receipts = %+v", h.wa.marked)
	}
}

func TestMarkRead(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "a"), Live: true})
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M2", alice, "b"), Live: true})
	h.ev.reset()

	if err := h.c.ChatsMarkRead(ctx, api.ChatParams{Chat: alice}); err != nil {
		t.Fatal(err)
	}
	if len(h.wa.marked) != 1 || len(h.wa.marked[0]) != 2 {
		t.Errorf("receipts = %+v, want 2 keys", h.wa.marked)
	}
	if len(h.nt.cleared) == 0 || h.nt.cleared[len(h.nt.cleared)-1] != alice {
		t.Errorf("notification not cleared: %v", h.nt.cleared)
	}
	chats := chatEvents(h.ev.all())
	if len(chats) != 1 || chats[0].Unread != 0 {
		t.Errorf("chat.update = %+v", chats)
	}

	// With read receipts disabled: private receipts, so the phone still
	// learns the chat was read.
	h.c.SettingsSet(ctx, api.SettingsSetParams{Key: api.SettingSendReceipts, Value: false})
	h.c.handle(api.EvMessage{Msg: &api.Message{ID: "M3", Chat: alice, Sender: alice, Timestamp: time.Now().Unix() + 5, Type: api.MsgText, Text: "c"}, Live: true})
	if err := h.c.ChatsMarkRead(ctx, api.ChatParams{Chat: alice}); err != nil {
		t.Fatal(err)
	}
	if len(h.wa.marked) != 2 || len(h.wa.marked[1]) != 1 || h.wa.private[0] || !h.wa.private[1] {
		t.Errorf("receipts %+v private %v", h.wa.marked, h.wa.private)
	}
	if ch, _ := h.st.GetChat(ctx, alice); ch.Unread != 0 {
		t.Errorf("unread = %d", ch.Unread)
	}
}

// Receipts that fail while reconnecting are sent on the next connect.
func TestMarkReadWhileDisconnected(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "a"), Live: true})
	h.wa.mu.Lock()
	h.wa.markErr = api.ErrNotConnected
	h.wa.mu.Unlock()
	if err := h.c.ChatsMarkRead(ctx, api.ChatParams{Chat: alice}); err != nil {
		t.Fatal(err)
	}
	if ch, _ := h.st.GetChat(ctx, alice); ch.Unread != 0 {
		t.Errorf("unread = %d", ch.Unread)
	}
	h.wa.mu.Lock()
	h.wa.markErr = nil
	h.wa.mu.Unlock()
	h.c.handle(api.EvConnState{State: api.ConnConnected})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		h.wa.mu.Lock()
		marked := h.wa.marked
		h.wa.mu.Unlock()
		if len(marked) == 1 && len(marked[0]) == 1 && marked[0][0].ID == "M1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resent receipts = %+v", marked)
		}
	}
}

func TestSendPendingThenSent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	gate := make(chan struct{})
	h.wa.sendGate = gate

	m, err := h.c.MessagesSend(ctx, api.SendParams{Chat: alice, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != api.StatusPending || !m.FromMe || m.ID == "" || m.Sender != selfJID {
		t.Fatalf("returned = %+v", m)
	}
	if got, want := h.ev.names(), []string{api.EMessageNew, api.EChatUpdate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if ch := chatEvents(h.ev.all())[0]; ch.LastMessage == nil || ch.LastMessage.Status != api.StatusPending || ch.Unread != 0 {
		t.Errorf("chat = %+v", ch)
	}
	stored, err := h.st.GetMessage(ctx, alice, m.ID)
	if err != nil || stored.Status != api.StatusPending {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	close(gate)
	h.ev.waitFor(t, "message.update sent", func(e event) bool {
		me, ok := e.params.(api.MessageEvent)
		return e.name == api.EMessageUpdate && ok && me.Message.ID == m.ID && me.Message.Status == api.StatusSent
	})
	h.ev.waitFor(t, "chat.update sent", func(e event) bool {
		ce, ok := e.params.(api.ChatEvent)
		return ok && ce.Chat.LastMessage != nil && ce.Chat.LastMessage.Status == api.StatusSent
	})
	if len(h.wa.sends) != 1 || h.wa.sends[0].id != m.ID || h.wa.sends[0].text != "hi" {
		t.Errorf("sends = %+v", h.wa.sends)
	}
	if len(h.nt.shown) != 0 {
		t.Error("own message must not notify")
	}
}

func TestSendErrorMarksError(t *testing.T) {
	h := newHarness(t)
	h.wa.sendErr = errors.New("boom")
	m, err := h.c.MessagesSend(context.Background(), api.SendParams{Chat: alice, Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	h.ev.waitFor(t, "message.update error", func(e event) bool {
		me, ok := e.params.(api.MessageEvent)
		return e.name == api.EMessageUpdate && ok && me.Message.ID == m.ID && me.Message.Status == api.StatusError
	})
}

// Typing and presence are ephemeral: a reconnect is no error worth a toast.
func TestEphemeralWhileReconnecting(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.wa.ephemeralErr = api.ErrNotConnected
	if err := h.c.ChatsSetTyping(ctx, api.SetTypingParams{Chat: alice, Typing: true}); err != nil {
		t.Fatalf("typing: %v", err)
	}
	if err := h.c.PresenceSubscribe(ctx, api.JIDParams{JID: alice}); err != nil {
		t.Fatalf("presence: %v", err)
	}
}

func TestSendReply(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "Q1", alice, "where?"), Live: true})
	m, err := h.c.MessagesSend(ctx, api.SendParams{Chat: alice, Text: "here", ReplyTo: "Q1"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ReplyTo == nil || m.ReplyTo.ID != "Q1" || m.ReplyTo.Preview != "where?" || m.ReplyTo.Sender != alice {
		t.Errorf("quote = %+v", m.ReplyTo)
	}
	h.ev.waitFor(t, "sent", func(e event) bool { return e.name == api.EMessageUpdate })
	r := h.wa.sends[0].reply
	if r == nil || r.ID != "Q1" || r.Sender != alice || r.Text != "where?" {
		t.Errorf("reply ref = %+v", r)
	}
}

func TestSendMentions(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(group, "Q1", alice, "who's in?"), Live: true})
	h.ev.reset()
	bob := "61400000002@s.whatsapp.net"
	m, err := h.c.MessagesSend(ctx, api.SendParams{Chat: group, Text: "@61400000001 @61400000002 me", ReplyTo: "Q1",
		Mentions: []string{alice, bob, alice, ""}})
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Mention{{JID: alice, Name: "Ali"}, {JID: bob, Name: "+61400000002"}}
	if !reflect.DeepEqual(m.Mentions, want) || m.ReplyTo == nil || m.ReplyTo.ID != "Q1" || m.Text != "@61400000001 @61400000002 me" {
		t.Fatalf("returned %+v", m)
	}
	if e := h.ev.all()[0]; e.name != api.EMessageNew || !reflect.DeepEqual(e.params.(api.MessageEvent).Message.Mentions, want) {
		t.Errorf("emitted %+v", e)
	}
	if c := chatEvents(h.ev.all())[0]; c.LastMessage.Preview != "@Ali @+61400000002 me" {
		t.Errorf("preview %q", c.LastMessage.Preview)
	}
	h.ev.waitFor(t, "sent", func(e event) bool { return e.name == api.EMessageUpdate })
	s := h.wa.sends[0]
	if !reflect.DeepEqual(s.mentions, []string{alice, bob}) || s.reply == nil || s.reply.ID != "Q1" {
		t.Errorf("send %+v reply %+v", s, s.reply)
	}
	if st, err := h.st.GetMessage(ctx, group, m.ID); err != nil || !reflect.DeepEqual(st.Mentions, want) {
		t.Errorf("stored %+v %v", st, err)
	}
}

func TestEditMentions(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(group, "E1", alice, "hi all"), Live: true})
	h.c.handle(api.EvEdit{Chat: group, ID: "E1", Text: "hi @61400000000", Mentions: []api.Mention{{JID: selfJID, Me: true}}, Ts: time.Now().Unix()})
	e := h.ev.waitFor(t, "edit", func(e event) bool { return e.name == api.EMessageUpdate })
	if m := e.params.(api.MessageEvent).Message; m.Text != "hi @61400000000" ||
		!reflect.DeepEqual(m.Mentions, []api.Mention{{JID: selfJID, Name: "You", Me: true}}) {
		t.Errorf("edited %+v", m)
	}
}

func TestGroupsMembers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	bob, lid := "61400000002@s.whatsapp.net", "999@lid"
	h.wa.members = map[string][]api.GroupMember{group: {{JID: bob}, {JID: lid}, {JID: alice, Admin: true}}}
	if err := h.st.PutContacts(ctx, []api.Contact{{JID: alice, FullName: "alice Nguyen"}}); err != nil {
		t.Fatal(err)
	}
	res, err := h.c.GroupsMembers(ctx, api.ChatParams{Chat: group})
	if err != nil {
		t.Fatal(err)
	}
	want := []api.GroupMember{{JID: alice, Name: "alice Nguyen", Admin: true}, {JID: bob, Name: "+61400000002"}, {JID: lid, Name: "999"}}
	if !reflect.DeepEqual(res.Members, want) {
		t.Errorf("members %+v", res.Members)
	}
	h.wa.members[group] = nil
	if res, err := h.c.GroupsMembers(ctx, api.ChatParams{Chat: group}); err != nil || res.Members == nil {
		t.Errorf("empty group: %#v %v", res, err)
	}
	if _, err := h.c.GroupsMembers(ctx, api.ChatParams{Chat: alice}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown group: %v", err)
	}
	h.wa.setLoggedIn(false)
	if _, err := h.c.GroupsMembers(ctx, api.ChatParams{Chat: group}); !errors.Is(err, api.ErrNotLoggedIn) {
		t.Errorf("logged out: %v", err)
	}
}

func TestSendMedia(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	m, err := h.c.MessagesSendMedia(ctx, api.SendMediaParams{Chat: alice, Path: "/tmp/x.jpg",
		Caption: "look @61400000001", Mentions: []string{alice, alice}})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Mentions) != 1 || m.Mentions[0].JID != alice {
		t.Errorf("caption mentions = %+v", m.Mentions)
	}
	if m.Type != api.MsgImage || m.Media == nil || m.Media.Path == "" || !m.Media.HasThumb || m.Text != "look @61400000001" {
		t.Fatalf("pending = %+v media=%+v", m, m.Media)
	}
	h.ev.waitFor(t, "media sent", func(e event) bool {
		me, ok := e.params.(api.MessageEvent)
		return e.name == api.EMessageUpdate && ok && me.Message.Status == api.StatusSent
	})
	ref, _, err := h.st.GetMediaRef(ctx, alice, m.ID)
	if err != nil || ref.DirectPath != "/v/t62/abc" || ref.Mime != "image/jpeg" || ref.ID != m.ID {
		t.Errorf("stored ref = %+v, %v", ref, err)
	}
	if st, _ := h.st.GetMessage(ctx, alice, m.ID); st.Media == nil || st.Media.Path != m.Media.Path {
		t.Errorf("stored media = %+v", st.Media)
	}
}

// A favourite from the phone is listed and sent as a fresh sticker upload.
func TestSendSticker(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	enc := bytes.Repeat([]byte{7}, 32)
	id := hex.EncodeToString(enc)
	h.c.handle(api.EvFavoriteSticker{Favorite: true, TS: 5, Width: 256, Height: 256,
		Ref: api.MediaRef{Mime: "image/webp", DirectPath: "/v/s", MediaKey: []byte{1}, FileEncSHA256: enc}})
	list, err := h.c.StickersList(ctx)
	if err != nil || len(list.Stickers) != 1 || list.Stickers[0].ID != id || !list.Stickers[0].Favorite {
		t.Fatalf("list = %+v, %v", list, err)
	}
	// An animated WebP: RIFF, size, WEBP, then a VP8X chunk with the animation flag.
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x02"), make([]byte, 16)...)
	if err := os.WriteFile(filepath.Join(h.md.dir, id+".jpg"), webp, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := h.c.MessagesSendSticker(ctx, api.SendStickerParams{Chat: alice, Sticker: id})
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != api.MsgSticker || m.Media == nil || m.Media.Width != 256 {
		t.Fatalf("pending = %+v media=%+v", m, m.Media)
	}
	h.ev.waitFor(t, "sticker sent", func(e event) bool {
		me, ok := e.params.(api.MessageEvent)
		return e.name == api.EMessageUpdate && ok && me.Message.Status == api.StatusSent
	})
	h.wa.mu.Lock()
	sent := h.wa.sends[len(h.wa.sends)-1].media
	h.wa.mu.Unlock()
	if sent.Type != api.MsgSticker || sent.Mime != "image/webp" || !sent.Animated || !bytes.Equal(sent.Data, webp) {
		t.Fatalf("sent = %+v", sent)
	}
	if _, err := h.c.MessagesSendSticker(ctx, api.SendStickerParams{Chat: alice, Sticker: "00"}); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("unknown sticker: %v", err)
	}
}

func TestSendRequiresLogin(t *testing.T) {
	h := newHarness(t)
	h.wa.loggedIn = false
	_, err := h.c.MessagesSend(context.Background(), api.SendParams{Chat: alice, Text: "hi"})
	if !errors.Is(err, api.ErrNotLoggedIn) {
		t.Fatalf("err = %v, want ErrNotLoggedIn", err)
	}
	if api.ErrorCode(err) != api.CodeNotLoggedIn {
		t.Error("wrong code")
	}
}

func TestHistoryBatch(t *testing.T) {
	h := newHarness(t)
	name := "Climbing"
	unread := 4
	now := time.Now().Unix()
	h.c.handle(api.EvHistory{
		Chats: []api.ChatPatch{{JID: group, Name: &name, Unread: &unread}},
		Messages: []*api.Message{
			{ID: "H1", Chat: group, Sender: alice, Timestamp: now - 100, Type: api.MsgText, Text: "old"},
			{ID: "H2", Chat: alice, Sender: alice, Timestamp: now - 50, Type: api.MsgText, Text: "older"},
		},
		Percent: 30,
	})
	// One chat.update per chat and one progress event per batch, never per message.
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.EChatUpdate, api.EChatUpdate, api.EHistorySyncProgress}) {
		t.Fatalf("events = %v", got)
	}
	byJID := map[string]api.Chat{}
	for _, ch := range chatEvents(h.ev.all()) {
		byJID[ch.JID] = ch
	}
	if g := byJID[group]; len(byJID) != 2 || g.Name != "Climbing" || g.Unread != 4 || byJID[alice].JID != alice {
		t.Fatalf("chat updates = %+v", byJID)
	}
	h.c.handle(api.EvHistory{
		Messages: []*api.Message{{ID: "H3", Chat: group, Sender: alice, Timestamp: now - 10, Type: api.MsgText, Text: "x"}},
		Percent:  100,
		Final:    true,
	})
	var prog []api.HistorySyncEvent
	for _, e := range h.ev.all() {
		if e.name == api.EHistorySyncProgress {
			prog = append(prog, e.params.(api.HistorySyncEvent))
		}
	}
	want := []api.HistorySyncEvent{
		{Percent: 30, Chats: 2, Messages: 2},
		{Percent: 100, Chats: 2, Messages: 3, Done: true},
	}
	if !reflect.DeepEqual(prog, want) {
		t.Errorf("progress = %+v, want %+v", prog, want)
	}
	if len(h.nt.shown) != 0 {
		t.Error("history must not notify")
	}
}

func TestReceiptEditRevokeReaction(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	m, _ := h.c.MessagesSend(ctx, api.SendParams{Chat: alice, Text: "hi"})
	h.ev.waitFor(t, "sent", func(e event) bool {
		ce, ok := e.params.(api.ChatEvent)
		return ok && ce.Chat.LastMessage.Status == api.StatusSent
	})
	h.ev.reset()

	h.c.handle(api.EvReceipt{Chat: alice, IDs: []string{m.ID}, Status: api.StatusRead})
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.EMessageUpdate, api.EChatUpdate}) {
		t.Fatalf("receipt events = %v", got)
	}
	if s := h.ev.all()[0].params.(api.MessageEvent).Message.Status; s != api.StatusRead {
		t.Errorf("status = %s", s)
	}
	h.ev.reset()
	h.c.handle(api.EvReceipt{Chat: alice, IDs: []string{m.ID}, Status: api.StatusDelivered})
	if got := h.ev.names(); len(got) != 0 {
		t.Errorf("downgrade emitted %v", got)
	}

	h.c.handle(api.EvEdit{Chat: alice, ID: m.ID, Text: "hello", Ts: time.Now().Unix()})
	ev := h.ev.waitFor(t, "edit", func(e event) bool { return e.name == api.EMessageUpdate })
	if mm := ev.params.(api.MessageEvent).Message; !mm.Edited || mm.Text != "hello" {
		t.Errorf("edited = %+v", mm)
	}
	h.ev.reset()
	h.c.handle(api.EvReaction{Chat: alice, ID: m.ID, Sender: alice, Emoji: "👍"})
	ev = h.ev.waitFor(t, "reaction", func(e event) bool { return e.name == api.EMessageUpdate })
	if mm := ev.params.(api.MessageEvent).Message; len(mm.Reactions) != 1 {
		t.Errorf("reactions = %+v", mm.Reactions)
	}
	h.ev.reset()
	h.c.handle(api.EvRevoke{Chat: alice, ID: m.ID})
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.EMessageUpdate, api.EChatUpdate}) {
		t.Fatalf("revoke events = %v", got)
	}
	if ch := chatEvents(h.ev.all())[0]; ch.LastMessage.Preview != "🚫 This message was deleted" {
		t.Errorf("preview = %q", ch.LastMessage.Preview)
	}
	// Unknown message: no events, no panic.
	h.ev.reset()
	h.c.handle(api.EvRevoke{Chat: alice, ID: "nope"})
	if got := h.ev.names(); len(got) != 0 {
		t.Errorf("unknown revoke emitted %v", got)
	}
}

func TestMediaFetchAndThumb(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	m := incoming(alice, "IMG", alice, "")
	m.Type = api.MsgImage
	m.Media = &api.MediaInfo{Mime: "image/jpeg", HasThumb: true}
	m.MediaRef = &api.MediaRef{Chat: alice, ID: "IMG", Mime: "image/jpeg"}
	m.Thumb = []byte{1}
	h.c.handle(api.EvMessage{Msg: m})
	h.ev.reset()

	res, err := h.c.MediaFetch(ctx, api.MessageRefParams{Chat: alice, ID: "IMG"})
	if err != nil || res.Path == "" || res.Mime != "image/jpeg" {
		t.Fatalf("fetch = %+v, %v", res, err)
	}
	ev := h.ev.waitFor(t, "media update", func(e event) bool { return e.name == api.EMessageUpdate })
	if p := ev.params.(api.MessageEvent).Message.Media.Path; p != res.Path {
		t.Errorf("update path = %q", p)
	}
	th, err := h.c.MediaThumb(ctx, api.MessageRefParams{Chat: alice, ID: "IMG"})
	if err != nil || th.Path == "" {
		t.Errorf("thumb = %+v, %v", th, err)
	}
	th, err = h.c.MediaThumb(ctx, api.MessageRefParams{Chat: alice, ID: "missing"})
	if err != nil || th.Path != "" {
		t.Errorf("missing thumb = %+v, %v", th, err)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	if h.md.capB != 1024<<20 {
		t.Errorf("initial cap = %d", h.md.capB)
	}
	res, err := h.c.SettingsGet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Settings[api.SettingNotifications] != true || res.Settings[api.SettingMediaCacheMB] != float64(1024) {
		t.Errorf("defaults = %+v", res.Settings)
	}
	if err := h.c.SettingsSet(ctx, api.SettingsSetParams{Key: api.SettingMediaCacheMB, Value: float64(512)}); err != nil {
		t.Fatal(err)
	}
	if h.md.capB != 512<<20 {
		t.Errorf("cap = %d", h.md.capB)
	}
	if err := h.c.SettingsSet(ctx, api.SettingsSetParams{Key: api.SettingNotifications, Value: "yes"}); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("bad type err = %v", err)
	}
	// Persisted and reloaded by a fresh core.
	c2 := NewWith(Deps{Store: h.st, Media: h.md, WA: h.wa, Notifier: h.nt})
	res, _ = c2.SettingsGet(ctx)
	if res.Settings[api.SettingMediaCacheMB] != float64(512) {
		t.Errorf("reloaded = %+v", res.Settings)
	}
}

func TestOnlineFollowsFocus(t *testing.T) {
	h := newHarness(t)
	waitOnline := func(want ...bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			h.wa.mu.Lock()
			got := append([]bool(nil), h.wa.online...)
			h.wa.mu.Unlock()
			if reflect.DeepEqual(got, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("online calls = %v, want %v", got, want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: alice, Focused: true})
	waitOnline(true)
	h.c.UISetActive(clientCtx(2), api.UISetActiveParams{Chat: "", Focused: true})
	h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: alice, Focused: false})
	h.c.ClientGone(2)
	waitOnline(true, false)
	// Our name arriving after linking re-sends presence, which carries it.
	h.wa.handler(api.EvPaired{JID: "61400000000@s.whatsapp.net", PushName: "Me"})
	waitOnline(true, false, false)
}

func TestNotifyActions(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "hi"), Live: true})

	// Open with a connected UI → ui.openChat.
	h.c.Hello(clientCtx(3), api.HelloParams{Client: "wisp-ui"})
	h.ev.reset()
	h.nt.actions.Open(alice)
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.EUIOpenChat}) {
		t.Errorf("open events = %v", got)
	}

	// Reply → sent as a reply, chat marked read.
	h.nt.actions.Reply(alice, "M1", "ok!")
	h.ev.waitFor(t, "reply sent", func(e event) bool {
		me, ok := e.params.(api.MessageEvent)
		return e.name == api.EMessageUpdate && ok && me.Message.Text == "ok!" && me.Message.Status == api.StatusSent
	})
	h.wa.mu.Lock()
	sends := h.wa.sends
	h.wa.mu.Unlock()
	if len(sends) != 1 || sends[0].reply == nil || sends[0].reply.ID != "M1" {
		t.Errorf("sends = %+v", sends)
	}
	if ch, _ := h.st.GetChat(context.Background(), alice); ch.Unread != 0 {
		t.Errorf("unread after reply = %d", ch.Unread)
	}
}

func TestEventLoopViaHandler(t *testing.T) {
	h := newHarness(t)
	if h.wa.handler == nil {
		t.Fatal("Start must set the handler")
	}
	for i := 0; !h.wa.isConnected(); i++ {
		if i == 100 {
			t.Fatal("Start must connect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.wa.handler(api.EvMessage{Msg: incoming(alice, "L1", alice, "async"), Live: true})
	h.wa.handler(api.EvQR{Code: "2@abc", Timeout: 20})
	h.ev.waitFor(t, "auth.qr", func(e event) bool { return e.name == api.EAuthQR })
	names := h.ev.names()
	if len(names) != 3 || names[0] != api.EMessageNew || names[2] != api.EAuthQR {
		t.Errorf("order = %v", names)
	}
}

func TestHelloAndAuth(t *testing.T) {
	ctx := clientCtx(1)
	h := newHarness(t)
	res, err := h.c.Hello(ctx, api.HelloParams{})
	if err != nil || res.Protocol != api.ProtocolVersion || res.Mock || res.Auth != api.AuthPaired || res.SelfJID != selfJID {
		t.Errorf("hello = %+v, %v", res, err)
	}
	pc, err := h.c.AuthPairPhone(ctx, api.PairPhoneParams{Phone: "+61400000000"})
	if err != nil || pc.Code != "ABCD-EFGH" {
		t.Errorf("pair = %+v %v", pc, err)
	}
	select {
	case <-h.c.Done():
		t.Fatal("done before shutdown")
	default:
	}
	h.c.Shutdown(ctx)
	<-h.c.Done()
}

func TestContactRenameUpdatesChat(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "hi")})
	h.ev.reset()
	h.c.handle(api.EvContacts{Contacts: []api.Contact{{JID: alice, FullName: "Alice A"}, {JID: "61400000002@s.whatsapp.net", FullName: "Bob"}}})
	chats := chatEvents(h.ev.all())
	if len(chats) != 1 || chats[0].JID != alice || chats[0].Name != "Alice A" {
		t.Fatalf("chat updates = %+v", chats)
	}
	h.ev.reset()
	h.c.handle(api.EvContacts{Contacts: []api.Contact{{JID: alice, FullName: "Alice A"}}})
	if got := h.ev.names(); len(got) != 0 {
		t.Errorf("unchanged name emitted %v", got)
	}
}

func TestPairingTimeoutEmitsAuthState(t *testing.T) {
	h := newHarness(t)
	h.wa.setLoggedIn(false)
	h.c.handle(api.EvConnState{State: api.ConnOffline})
	ev := h.ev.waitFor(t, "auth.state", func(e event) bool { return e.name == api.EAuthState })
	if st := ev.params.(api.AuthStateEvent).State; st != api.AuthUnpaired {
		t.Errorf("auth state = %s", st)
	}
}

func TestLogoutWipesAccountData(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "logout", true: "unlinked"}[remote], func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t)
			if err := h.c.SettingsSet(ctx, api.SettingsSetParams{Key: api.SettingNotifyPreview, Value: false}); err != nil {
				t.Fatal(err)
			}
			h.c.handle(api.EvMessage{Msg: incoming(alice, "M1", alice, "hello"), Live: true})
			h.c.handle(api.EvHistory{Messages: []*api.Message{incoming(group, "H1", alice, "hello")}, Percent: 10})
			h.ev.reset()

			want := api.AuthUnpaired
			if remote {
				h.wa.setLoggedIn(false) // whatsmeow deletes the session
				h.c.handle(api.EvLoggedOut{Reason: "unlinked"})
				want = api.AuthLoggedOut
			} else if err := h.c.AuthLogout(ctx); err != nil {
				t.Fatal(err)
			}
			if ev := h.ev.waitFor(t, "auth.state", func(e event) bool { return e.name == api.EAuthState }); ev.params.(api.AuthStateEvent).State != want {
				t.Errorf("auth.state = %+v, want %s", ev.params, want)
			}
			// A straggler from the old session is dropped.
			h.c.handle(api.EvMessage{Msg: incoming(alice, "M2", alice, "late"), Live: true})

			list, err := h.c.ChatsList(ctx, api.ChatsListParams{})
			if err != nil || len(list.Chats) != 0 {
				t.Fatalf("chats after logout = %+v, %v", list, err)
			}
			if rs, err := h.c.Search(ctx, api.SearchParams{Query: "hello"}); err != nil || len(rs.Results) != 0 {
				t.Errorf("search after logout = %+v, %v", rs, err)
			}
			if h.md.wiped != 1 {
				t.Errorf("media wiped %d times", h.md.wiped)
			}
			h.nt.mu.Lock()
			if !slices.Contains(h.nt.cleared, "*") {
				t.Errorf("notifications not cleared: %v", h.nt.cleared)
			}
			h.nt.mu.Unlock()
			if res, _ := h.c.SettingsGet(ctx); res.Settings[api.SettingNotifyPreview] != false {
				t.Errorf("settings = %+v, want kept", res.Settings)
			}

			// The next account starts from an empty store, history counts included.
			h.wa.setLoggedIn(true)
			h.c.handle(api.EvHistory{Messages: []*api.Message{incoming(alice, "N1", alice, "new")}, Percent: 50})
			list, _ = h.c.ChatsList(ctx, api.ChatsListParams{})
			if len(list.Chats) != 1 || list.Chats[0].LastMessage.ID != "N1" {
				t.Errorf("chats of the next account = %+v", list.Chats)
			}
			ev := h.ev.waitFor(t, "progress", func(e event) bool { return e.name == api.EHistorySyncProgress })
			if p := ev.params.(api.HistorySyncEvent); p.Chats != 1 || p.Messages != 1 {
				t.Errorf("progress = %+v", p)
			}
		})
	}
}

func TestReconnectCycles(t *testing.T) {
	h := newHarness(t)
	h.c.UISetActive(clientCtx(1), api.UISetActiveParams{Chat: group, Focused: true})
	for !h.wa.isConnected() {
		time.Sleep(5 * time.Millisecond)
	}
	settle := func() int {
		n := runtime.NumGoroutine()
		for range 50 {
			time.Sleep(10 * time.Millisecond)
			n = min(n, runtime.NumGoroutine())
		}
		return n
	}
	before := settle()
	h.c.Start(context.Background()) // a second Start adds nothing

	const cycles = 50
	for i := range cycles {
		h.wa.handler(api.EvConnState{State: api.ConnDisconnected})
		h.wa.handler(api.EvConnState{State: api.ConnConnecting})
		h.wa.handler(api.EvConnState{State: api.ConnConnected})
		// Offline catch-up after the reconnect.
		h.wa.handler(api.EvMessage{Msg: incoming(alice, fmt.Sprintf("C%d", i), alice, "while away"), Live: false})
	}
	h.ev.waitFor(t, "catch-up", func(e event) bool {
		me, ok := e.params.(api.MessageEvent)
		return ok && me.Message.ID == fmt.Sprintf("C%d", cycles-1)
	})
	if after := settle(); after > before {
		t.Errorf("goroutines %d -> %d after %d reconnects", before, after, cycles)
	}
	h.wa.mu.Lock()
	online := append([]bool(nil), h.wa.online...)
	h.wa.mu.Unlock()
	if len(online) == 0 || len(online) > cycles+1 {
		t.Errorf("SetOnline called %d times for %d connects", len(online), cycles)
	}
	for _, v := range online {
		if !v {
			t.Errorf("SetOnline(false) with a focused window: %v", online)
			break
		}
	}
	if ch, _ := h.st.GetChat(context.Background(), alice); ch.Unread != cycles {
		t.Errorf("unread = %d, want %d", ch.Unread, cycles)
	}
	if len(h.nt.shown) != 0 {
		t.Errorf("catch-up notified %d times", len(h.nt.shown))
	}
}

func TestSetPinned(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	chats := []string{alice, group, "61400000002@s.whatsapp.net", "61400000003@s.whatsapp.net"}
	for _, j := range chats {
		if err := h.st.UpsertChat(ctx, api.ChatPatch{JID: j}); err != nil {
			t.Fatal(err)
		}
	}
	for _, j := range chats[:3] {
		if err := h.c.ChatsSetPinned(ctx, api.SetPinnedParams{Chat: j, Pinned: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.c.ChatsSetPinned(ctx, api.SetPinnedParams{Chat: chats[3], Pinned: true}); !errors.Is(err, api.ErrPinLimit) {
		t.Fatalf("4th pin: err = %v", err)
	}
	if err := h.c.ChatsSetPinned(ctx, api.SetPinnedParams{Chat: alice, Pinned: false}); err != nil {
		t.Fatal(err)
	}
	want := []api.SetPinnedParams{{Chat: group, Pinned: true}, {Chat: "61400000002@s.whatsapp.net", Pinned: true}, {Chat: alice}}
	if got := h.wa.pins; !reflect.DeepEqual(got[1:], want) || len(got) != 4 {
		t.Errorf("wa pins = %v", got)
	}
	evs := chatEvents(h.ev.all())
	if len(evs) != 4 || evs[0].JID != alice || evs[0].Pinned == 0 || evs[3].JID != alice || evs[3].Pinned != 0 {
		t.Errorf("chat.update = %+v", evs)
	}
}

func TestHistoryKeepsAppStateSettings(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	at, forever := int64(1761000000), int64(-1)
	h.c.handle(api.EvChatMeta{Patch: api.ChatPatch{JID: group, Pinned: &at, MutedUntil: &forever}})
	// History carries no pin or mute (wa/history.go leaves them nil).
	h.c.handle(api.EvHistory{Percent: 100, Final: true, Chats: []api.ChatPatch{{JID: group, Unread: new(0)}}})
	c, err := h.st.GetChat(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	if c.Pinned != at || !c.Muted {
		t.Errorf("chat = %+v", c)
	}
}

func TestAppStateResyncOnce(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	waitResyncs := func(n int) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); h.wa.resyncCount() < n || h.c.resyncing.Load(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("resyncs = %d, want %d", h.wa.resyncCount(), n)
			}
		}
	}
	h.c.handle(api.EvConnState{State: api.ConnConnected})
	waitResyncs(1)
	h.c.handle(api.EvConnState{State: api.ConnConnected})
	h.c.handle(api.EvHistory{Percent: 100, Final: true})
	waitResyncs(2)
	if n := h.wa.resyncCount(); n != 2 {
		t.Errorf("resyncs = %d, want 2 (first connect, then full history)", n)
	}
	s, _ := h.c.SettingsGet(ctx)
	if _, ok := s.Settings[settingResynced]; ok {
		t.Errorf("settings.get shows %s", settingResynced)
	}
}

func TestReactRevokeDeleteForMe(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	in := incoming(alice, "IN1", alice, "hello")
	h.c.handle(api.EvMessage{Msg: in})
	mine, _ := h.c.MessagesSend(ctx, api.SendParams{Chat: alice, Text: "hi"})
	h.ev.waitFor(t, "sent", func(e event) bool {
		ce, ok := e.params.(api.ChatEvent)
		return ok && ce.Chat.LastMessage.Status == api.StatusSent
	})
	h.ev.reset()

	if err := h.c.MessagesReact(ctx, api.ReactParams{Chat: alice, ID: in.ID, Emoji: "👍"}); err != nil {
		t.Fatal(err)
	}
	ev := h.ev.waitFor(t, "react", func(e event) bool { return e.name == api.EMessageUpdate })
	if rs := ev.params.(api.MessageEvent).Message.Reactions; len(rs) != 1 || !rs[0].Mine || rs[0].Emoji != "👍" {
		t.Errorf("reactions = %+v", rs)
	}
	h.ev.reset()
	if err := h.c.MessagesReact(ctx, api.ReactParams{Chat: alice, ID: in.ID}); err != nil {
		t.Fatal(err)
	}
	ev = h.ev.waitFor(t, "unreact", func(e event) bool { return e.name == api.EMessageUpdate })
	if rs := ev.params.(api.MessageEvent).Message.Reactions; len(rs) != 0 {
		t.Errorf("reactions after removing = %+v", rs)
	}

	if err := h.c.MessagesRevoke(ctx, api.MessageRefParams{Chat: alice, ID: in.ID}); !errors.Is(err, api.ErrUnsupported) {
		t.Errorf("revoke of their message: err = %v", err)
	}
	h.ev.reset()
	if err := h.c.MessagesRevoke(ctx, api.MessageRefParams{Chat: alice, ID: mine.ID}); err != nil {
		t.Fatal(err)
	}
	ev = h.ev.waitFor(t, "revoke", func(e event) bool { return e.name == api.EMessageUpdate })
	if m := ev.params.(api.MessageEvent).Message; !m.Deleted || m.Text != "" {
		t.Errorf("revoked = %+v", m)
	}

	h.ev.reset()
	if err := h.c.MessagesDeleteForMe(ctx, api.MessageRefParams{Chat: alice, ID: mine.ID}); err != nil {
		t.Fatal(err)
	}
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.EMessageRemove, api.EChatUpdate}) {
		t.Fatalf("delete events = %v", got)
	}
	if ch := chatEvents(h.ev.all())[0]; ch.LastMessage == nil || ch.LastMessage.ID != in.ID {
		t.Errorf("last message after delete = %+v", ch.LastMessage)
	}
	if _, err := h.st.GetMessage(ctx, alice, mine.ID); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("deleted message still stored: %v", err)
	}

	// Deleted on another device.
	h.ev.reset()
	h.c.handle(api.EvDeleteForMe{Chat: alice, ID: in.ID})
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.EMessageRemove, api.EChatUpdate}) {
		t.Fatalf("remote delete events = %v", got)
	}

	want := []string{"react IN1 👍", "react IN1 ", "revoke " + mine.ID, "deleteForMe " + mine.ID}
	if !reflect.DeepEqual(h.wa.acts, want) {
		t.Errorf("wa calls = %q", h.wa.acts)
	}
}

func TestRevokeTooOld(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	old := incoming(alice, "OLD", selfJID, "old")
	old.FromMe, old.Timestamp = true, time.Now().Add(-revokeWindow-time.Hour).Unix()
	h.c.handle(api.EvMessage{Msg: old})
	if err := h.c.MessagesRevoke(ctx, api.MessageRefParams{Chat: alice, ID: "OLD"}); !errors.Is(err, api.ErrUnsupported) {
		t.Errorf("err = %v", err)
	}
	if len(h.wa.acts) != 0 {
		t.Errorf("wa calls = %q", h.wa.acts)
	}
}

func TestDeleteForMeRejectsPendingSend(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	m := incoming(alice, "PENDING", selfJID, "uploading")
	m.FromMe, m.Status, m.Type = true, api.StatusPending, api.MsgImage
	if err := h.st.PutMessage(ctx, m, false); err != nil {
		t.Fatal(err)
	}
	if err := h.c.MessagesDeleteForMe(ctx, api.MessageRefParams{Chat: alice, ID: m.ID}); !errors.Is(err, api.ErrUnsupported) {
		t.Fatalf("pending delete = %v", err)
	}
	if len(h.wa.acts) != 0 {
		t.Fatalf("pending delete reached WhatsApp: %v", h.wa.acts)
	}
	if _, err := h.st.GetMessage(ctx, alice, m.ID); err != nil {
		t.Fatalf("pending message removed: %v", err)
	}
}

func TestUploadCompletionKeepsRemoteDeletion(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	m := incoming(alice, "UPLOAD", selfJID, "uploading")
	m.FromMe, m.Status, m.Type = true, api.StatusPending, api.MsgImage
	if err := h.st.PutMessage(ctx, m, false); err != nil {
		t.Fatal(err)
	}
	h.c.handle(api.EvDeleteForMe{Chat: alice, ID: m.ID})
	h.c.finishSend(m, &api.MediaRef{}, nil)
	if _, err := h.st.GetMessage(ctx, alice, m.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("upload completion restored deleted message: %v", err)
	}
}

func TestEdit(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	in := incoming(alice, "IN1", alice, "hello")
	h.c.handle(api.EvMessage{Msg: in})
	mine, _ := h.c.MessagesSend(ctx, api.SendParams{Chat: alice, Text: "hi"})
	h.ev.waitFor(t, "sent", func(e event) bool {
		ce, ok := e.params.(api.ChatEvent)
		return ok && ce.Chat.LastMessage.Status == api.StatusSent
	})
	h.ev.reset()

	if err := h.c.MessagesEdit(ctx, api.EditParams{Chat: alice, ID: in.ID, Text: "x"}); !errors.Is(err, api.ErrUnsupported) {
		t.Errorf("editing their message: err = %v", err)
	}
	if err := h.c.MessagesEdit(ctx, api.EditParams{Chat: alice, ID: mine.ID, Text: "hi @61400000001", Mentions: []string{alice}}); err != nil {
		t.Fatal(err)
	}
	ev := h.ev.waitFor(t, "edit", func(e event) bool { return e.name == api.EMessageUpdate })
	if m := ev.params.(api.MessageEvent).Message; !m.Edited || m.Text != "hi @61400000001" || len(m.Mentions) != 1 {
		t.Errorf("edited = %+v", m)
	}
	h.ev.reset()
	if err := h.c.MessagesRevoke(ctx, api.MessageRefParams{Chat: alice, ID: mine.ID}); err != nil {
		t.Fatal(err)
	}
	if err := h.c.MessagesEdit(ctx, api.EditParams{Chat: alice, ID: mine.ID, Text: "y"}); !errors.Is(err, api.ErrUnsupported) {
		t.Errorf("editing a deleted message: err = %v", err)
	}
}
