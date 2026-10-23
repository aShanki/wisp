package mock

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) Emit(name string, _ any) {
	r.mu.Lock()
	r.events = append(r.events, name)
	r.mu.Unlock()
}

func (r *recorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e == name {
			n++
		}
	}
	return n
}

func newBackend(t *testing.T) (*Backend, *recorder) {
	b := New(Config{Seed: 1})
	rec := &recorder{}
	b.SetEmitter(rec)
	t.Cleanup(func() { b.Close() })
	return b, rec
}

func decodable(t *testing.T, path string) image.Config {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return cfg
}

func TestData(t *testing.T) {
	b, _ := newBackend(t)
	var groups, readOnly int
	types := map[string]bool{}
	for _, c := range b.chats {
		if c.row.Kind == api.ChatGroup {
			groups++
		}
		if c.row.ReadOnly {
			readOnly++
		}
		for i := range c.count() {
			m := c.at(i)
			types[m.Type] = true
			if m.ReplyTo != nil {
				types["reply"] = true
			}
			if len(m.Reactions) > 0 {
				types["reaction"] = true
			}
			if m.Edited {
				types["edit"] = true
			}
		}
	}
	if n := len(b.chats); n < 45 || n > 55 || groups < n/5 || readOnly != 1 {
		t.Fatalf("%d chats, %d groups, %d read-only", n, groups, readOnly)
	}
	for _, want := range []string{api.MsgText, api.MsgImage, api.MsgVoice, api.MsgDocument, api.MsgRevoked,
		api.MsgSystem, "reply", "reaction", "edit"} {
		if !types[want] {
			t.Errorf("no %s messages", want)
		}
	}
	if c := b.chats[bigJID]; c.count() != 10000 {
		t.Errorf("Big Chat has %d messages", c.count())
	}
}

func TestMedia(t *testing.T) {
	b, rec := newBackend(t)
	ctx := context.Background()
	res, _ := b.MessagesList(ctx, api.MessagesListParams{Chat: bigJID, Limit: 200})
	var img api.Message
	for _, m := range res.Messages {
		if m.Type == api.MsgImage {
			img = m
			break
		}
	}
	ref := api.MessageRefParams{Chat: bigJID, ID: img.ID}
	thumb, err := b.MediaThumb(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodable(t, thumb.Path); cfg.Width != thumbWidth {
		t.Fatalf("thumb width %d", cfg.Width)
	}
	full, err := b.MediaFetch(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := decodable(t, full.Path); cfg.Width != img.Media.Width || cfg.Height != img.Media.Height {
		t.Fatalf("full %dx%d, want %dx%d", cfg.Width, cfg.Height, img.Media.Width, img.Media.Height)
	}
	if rec.count(api.EMessageUpdate) != 1 {
		t.Fatal("no message.update after fetch")
	}
	res, _ = b.MessagesList(ctx, api.MessagesListParams{Chat: bigJID, Limit: 200})
	for _, m := range res.Messages {
		if m.ID == img.ID && m.Media.Path != full.Path {
			t.Fatalf("listed path %q, want %q", m.Media.Path, full.Path)
		}
	}
}

func TestSendImage(t *testing.T) {
	b, _ := newBackend(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "photo.png")
	f, _ := os.Create(src)
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 300, 200)))
	f.Close()

	m, err := b.MessagesSendMedia(ctx, api.SendMediaParams{Chat: personJID(0), Path: src, Caption: "look"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != api.MsgImage || m.Media.Width != 300 || m.Status != api.StatusPending || m.Text != "look" {
		t.Fatalf("sent %+v %+v", m, m.Media)
	}
	os.Remove(src) // the mock keeps its own copy
	thumb, err := b.MediaThumb(ctx, api.MessageRefParams{Chat: personJID(0), ID: m.ID})
	if err != nil || thumb.Path == "" {
		t.Fatalf("thumb %v %v", thumb, err)
	}
	decodable(t, thumb.Path)
}

func TestActiveChatStaysRead(t *testing.T) {
	b, _ := newBackend(t)
	ctx := api.WithClientID(context.Background(), 7)
	chat := personJID(1)
	if err := b.UISetActive(ctx, api.UISetActiveParams{Chat: chat, Focused: true}); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.receive(b.chats[chat])
	unread := b.chats[chat].row.Unread
	b.mu.Unlock()
	if unread != 0 {
		t.Fatalf("unread %d in the focused chat", unread)
	}

	b.ClientGone(7)
	b.mu.Lock()
	b.receive(b.chats[chat])
	unread = b.chats[chat].row.Unread
	b.mu.Unlock()
	if unread != 1 {
		t.Fatalf("unread %d after the client left", unread)
	}
}

func TestPairPhone(t *testing.T) {
	b := New(Config{Seed: 1, Unpaired: true})
	defer b.Close()
	res, err := b.AuthPairPhone(context.Background(), api.PairPhoneParams{Phone: "+61400000123"})
	if err != nil || res.Code != "WISP-MOCK" {
		t.Fatalf("%v %v", res, err)
	}
	deadline := time.Now().Add(7 * time.Second)
	for {
		st, _ := b.AuthStatus(context.Background())
		if st.State == api.AuthPaired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s", st.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSetPinned(t *testing.T) {
	b, rec := newBackend(t)
	ctx := context.Background()
	list, err := b.ChatsList(ctx, api.ChatsListParams{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if list.Chats[0].Pinned == 0 || list.Chats[1].Pinned == 0 || list.Chats[2].Pinned != 0 {
		t.Fatalf("want two pinned chats on top: %+v", list.Chats[:3])
	}
	third, fourth := list.Chats[2].JID, list.Chats[3].JID
	if err := b.ChatsSetPinned(ctx, api.SetPinnedParams{Chat: third, Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.ChatsSetPinned(ctx, api.SetPinnedParams{Chat: fourth, Pinned: true}); err != api.ErrPinLimit {
		t.Errorf("4th pin: %v", err)
	}
	if list, _ = b.ChatsList(ctx, api.ChatsListParams{Limit: 1}); list.Chats[0].JID != third {
		t.Errorf("the newest pin is not first: %s", list.Chats[0].JID)
	}
	if rec.count(api.EChatUpdate) != 1 {
		t.Errorf("chat.update events = %d", rec.count(api.EChatUpdate))
	}
}

func TestMentions(t *testing.T) {
	b, rec := newBackend(t)
	ctx := context.Background()
	var others, us int
	for _, c := range b.chats {
		for i := range c.count() {
			m := c.at(i)
			if m.Mentions == nil {
				t.Fatalf("%s: null mentions", m.ID)
			}
			for _, mn := range m.Mentions {
				user, _, _ := strings.Cut(mn.JID, "@")
				if !strings.Contains(m.Text, "@"+user) || mn.Name == "" || mn.Me != (mn.JID == selfJID) {
					t.Fatalf("%s: %+v in %q", m.ID, mn, m.Text)
				}
				if mn.Me {
					us++
				} else {
					others++
				}
			}
		}
	}
	if others == 0 || us == 0 {
		t.Fatalf("%d mentions of others, %d of us", others, us)
	}

	family := groupJID(1)
	res, err := b.GroupsMembers(ctx, api.ChatParams{Chat: family})
	if err != nil || len(res.Members) != 5 {
		t.Fatalf("members %+v %v", res, err)
	}
	if _, err := b.GroupsMembers(ctx, api.ChatParams{Chat: bigJID}); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("members of a 1:1: %v", err)
	}

	sent, err := b.MessagesSend(ctx, api.SendParams{Chat: family, Text: "@61400000003 @61400000000 dinner?",
		Mentions: []string{personJID(2), selfJID, personJID(2)}})
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Mention{{JID: personJID(2), Name: "Mum"}, {JID: selfJID, Name: "You", Me: true}}
	if !reflect.DeepEqual(sent.Mentions, want) {
		t.Fatalf("sent %+v", sent.Mentions)
	}
	page, _ := b.MessagesList(ctx, api.MessagesListParams{Chat: family, Limit: 1})
	if got := page.Messages[0]; got.ID != sent.ID || !reflect.DeepEqual(got.Mentions, want) {
		t.Errorf("listed %+v", got)
	}
	if p := b.chats[family].row.LastMessage.Preview; p != "@Mum @You dinner?" || rec.count(api.EMessageNew) != 1 {
		t.Errorf("preview %q", p)
	}
}
