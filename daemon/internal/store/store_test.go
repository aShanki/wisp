package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

const (
	alice = "61400000001@s.whatsapp.net"
	bob   = "61400000002@s.whatsapp.net"
	carol = "61400000003@s.whatsapp.net"
	me    = "61400000000@s.whatsapp.net"
	group = "120363000000000001@g.us"
)

func newStore(t testing.TB) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "wisp.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func txt(chat, id string, ts int64, text string) *api.Message {
	sender := chat
	if kindOf(chat) == api.ChatGroup {
		sender = alice
	}
	return &api.Message{ID: id, Chat: chat, Sender: sender, Timestamp: ts, Type: api.MsgText, Text: text}
}

func mine(chat, id string, ts int64, text, status string) *api.Message {
	return &api.Message{ID: id, Chat: chat, Sender: me, FromMe: true, Timestamp: ts, Type: api.MsgText, Text: text, Status: status}
}

func put(t testing.TB, s *Store, m *api.Message, inc bool) {
	t.Helper()
	if err := s.PutMessage(context.Background(), m, inc); err != nil {
		t.Fatalf("PutMessage %s: %v", m.ID, err)
	}
}

func mustChat(t testing.TB, s *Store, jid string) *api.Chat {
	t.Helper()
	c, err := s.GetChat(context.Background(), jid)
	if err != nil {
		t.Fatalf("GetChat %s: %v", jid, err)
	}
	return c
}

func mustMsg(t testing.TB, s *Store, chat, id string) *api.Message {
	t.Helper()
	m, err := s.GetMessage(context.Background(), chat, id)
	if err != nil {
		t.Fatalf("GetMessage %s: %v", id, err)
	}
	return m
}

func ptr[T any](v T) *T { return &v }

func rx(emoji string, count int, mine bool) api.Reaction {
	return api.Reaction{Emoji: emoji, Count: count, Mine: mine}
}

func ids(ms []api.Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestOpenMigratePragmas(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wisp.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var jm string
	var fk, cache int
	s.w.QueryRow("PRAGMA user_version").Scan(&v)
	s.w.QueryRow("PRAGMA journal_mode").Scan(&jm)
	s.w.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.r.QueryRow("PRAGMA cache_size").Scan(&cache)
	if v != len(migrations) || jm != "wal" || fk != 1 || cache != -readerCacheKiB {
		t.Fatalf("user_version=%d journal=%s fk=%d cache=%d", v, jm, fk, cache)
	}
	put(t, s, txt(alice, "a", 1, "hello"), true)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen: no re-migration, data intact.
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if c := mustChat(t, s, alice); c.Unread != 1 || c.LastMessage == nil || c.LastMessage.ID != "a" {
		t.Fatalf("after reopen: %+v", c)
	}
	// Readers are query-only.
	if _, err := s.r.Exec(`DELETE FROM chats`); err == nil {
		t.Fatal("reader connection accepted a write")
	}
}

func TestMigrateFromV1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wisp.db")
	db, err := sql.Open("sqlite", dsn(path, writerCacheKiB, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{migrations[0], `PRAGMA user_version = 1`,
		`INSERT INTO chats(jid, name, last_seq, unread) VALUES ('` + group + `', 'Climbing', 1, 2)`,
		`INSERT INTO messages(seq, chat, id, sender, ts, type, text, mentions) VALUES (1, '` + group + `', 'M1', '` + alice + `', 100, 'image', 'see the crag', '` + bob + `')`,
		`INSERT INTO media(msg, mime, direct_path, has_thumb) VALUES (1, 'image/jpeg', '/v/x', 1)`,
		`INSERT INTO contacts(jid, full_name, phone) VALUES ('` + alice + `', 'Alice', '61400000001')`,
		`INSERT INTO settings(key, value) VALUES ('notifications.preview', 'false')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, phone int
	s.w.QueryRow(`PRAGMA user_version`).Scan(&v)
	s.w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('contacts') WHERE name = 'phone'`).Scan(&phone)
	if v != len(migrations) || phone != 0 {
		t.Fatalf("user_version %d, contacts.phone left %d", v, phone)
	}
	if c := mustChat(t, s, group); c.Name != "Climbing" || c.Unread != 2 || c.LastMessage == nil || c.LastMessage.ID != "M1" {
		t.Errorf("chat: %+v", c)
	}
	// v1's unused mentions were dropped; v3's start empty.
	if m := mustMsg(t, s, group, "M1"); m.SenderName != "Alice" || m.Media == nil || !m.Media.HasThumb ||
		m.Mentions == nil || len(m.Mentions) != 0 {
		t.Errorf("message: %+v", m)
	}
	put(t, s, &api.Message{ID: "M2", Chat: group, Sender: bob, Timestamp: 101, Type: api.MsgText, Text: "@61400000001 hi",
		Mentions: []api.Mention{{JID: alice}}}, false)
	if m := mustMsg(t, s, group, "M2"); !reflect.DeepEqual(m.Mentions, []api.Mention{{JID: alice, Name: "Alice"}}) {
		t.Errorf("mentions after migration: %+v", m.Mentions)
	}
	if c, err := s.GetContact(ctx, alice); err != nil || c.Phone != "61400000001" {
		t.Errorf("contact: %+v %v", c, err)
	}
	if res, _ := s.Search(ctx, api.SearchParams{Query: "crag"}); len(res) != 1 {
		t.Errorf("search after migration: %+v", res)
	}
	if got, _ := s.AllSettings(ctx); got["notifications.preview"] != "false" {
		t.Errorf("settings: %v", got)
	}
}

func TestWipe(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wisp.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	put(t, s, txt(alice, "a", 1, "secret"), true)
	if err := s.SetSetting(ctx, "notifications.preview", "false"); err != nil {
		t.Fatal(err)
	}
	if err := s.Wipe(ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.ListChats(ctx, api.ChatsListParams{}); len(res.Chats) != 0 {
		t.Errorf("chats after wipe: %+v", res.Chats)
	}
	if res, _ := s.Search(ctx, api.SearchParams{Query: "secret"}); len(res) != 0 {
		t.Errorf("search after wipe: %+v", res)
	}
	if got, _ := s.AllSettings(ctx); got["notifications.preview"] != "false" {
		t.Errorf("settings after wipe: %v", got)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("wisp.db after wipe: %v %v", st.Mode(), err)
	}
	put(t, s, txt(bob, "b", 2, "new account"), true)
	if c := mustChat(t, s, bob); c.Unread != 1 {
		t.Errorf("after wipe: %+v", c)
	}
}

func TestMemory(t *testing.T) {
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	put(t, s, txt(alice, "a", 1, "hi"), false)
	if m := mustMsg(t, s, alice, "a"); m.Text != "hi" {
		t.Fatal(m)
	}
}

func TestChatKindFromJID(t *testing.T) {
	s := newStore(t)
	for _, tc := range []struct{ jid, kind string }{
		{alice, api.ChatUser},
		{"123456789@lid", api.ChatUser},
		{group, api.ChatGroup},
		{"1234@broadcast", api.ChatBroadcast},
		{"120363111@newsletter", api.ChatNewsletter},
	} {
		put(t, s, txt(tc.jid, "x", 1, "hi"), false)
		if c := mustChat(t, s, tc.jid); c.Kind != tc.kind {
			t.Errorf("%s: kind %q want %q", tc.jid, c.Kind, tc.kind)
		}
	}
}

func TestGetMissing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	checks := map[string]error{}
	_, checks["GetChat"] = s.GetChat(ctx, alice)
	_, checks["GetMessage"] = s.GetMessage(ctx, alice, "x")
	_, _, checks["ListMessages before"] = s.ListMessages(ctx, alice, "nope", 10)
	_, checks["ApplyEdit"] = s.ApplyEdit(ctx, alice, "x", "t", nil, 1)
	_, checks["ApplyRevoke"] = s.ApplyRevoke(ctx, alice, "x")
	_, checks["ApplyReaction"] = s.ApplyReaction(ctx, api.EvReaction{Chat: alice, ID: "x", Sender: bob, Emoji: "👍"})
	checks["MarkRead"] = s.MarkRead(ctx, alice)
	_, _, checks["GetMediaRef"] = s.GetMediaRef(ctx, alice, "x")
	checks["SetMediaPath"] = s.SetMediaPath(ctx, alice, "x", "/tmp/x")
	checks["SetMediaDirectPath"] = s.SetMediaDirectPath(ctx, alice, "x", "")
	_, checks["GetContact"] = s.GetContact(ctx, alice)
	for name, err := range checks {
		if !errors.Is(err, api.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	// Empty lists are [] not nil.
	msgs, more, err := s.ListMessages(ctx, alice, "", 10)
	if err != nil || msgs == nil || more {
		t.Errorf("ListMessages empty: %v %v %v", msgs, more, err)
	}
	cl, err := s.ListChats(ctx, api.ChatsListParams{})
	if err != nil || cl.Chats == nil || cl.NextCursor != "" {
		t.Errorf("ListChats empty: %+v %v", cl, err)
	}
	keys, err := s.UnreadIncoming(ctx, alice, 10)
	if err != nil || keys == nil || len(keys) != 0 {
		t.Errorf("UnreadIncoming: %v %v", keys, err)
	}
}

func TestPutMessageUnreadAndLast(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	put(t, s, txt(alice, "m1", 100, "one"), true)
	put(t, s, txt(alice, "m2", 200, "two"), true)
	put(t, s, txt(alice, "m2", 200, "two"), true) // re-delivery
	c := mustChat(t, s, alice)
	if c.Unread != 2 {
		t.Errorf("unread %d want 2", c.Unread)
	}
	if c.LastMessage == nil || c.LastMessage.ID != "m2" || c.LastMessage.Preview != "two" || c.LastActivity != 200 {
		t.Errorf("last: %+v activity %d", c.LastMessage, c.LastActivity)
	}
	// An older message (late delivery) doesn't become lastMessage.
	put(t, s, txt(alice, "m0", 50, "zero"), false)
	if c := mustChat(t, s, alice); c.LastMessage.ID != "m2" || c.LastActivity != 200 || c.Unread != 2 {
		t.Errorf("after old msg: %+v", c)
	}
	// lastActivity never moves backwards through a patch.
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: alice, LastActivity: ptr(int64(10))}); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, alice); c.LastActivity != 200 {
		t.Errorf("activity moved back to %d", c.LastActivity)
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: alice, LastActivity: ptr(int64(300))}); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, alice); c.LastActivity != 300 {
		t.Errorf("activity %d want 300", c.LastActivity)
	}
	// Own message with same timestamp as the newest: insertion order wins.
	put(t, s, mine(alice, "m3", 200, "mine", api.StatusPending), false)
	c = mustChat(t, s, alice)
	if c.LastMessage.ID != "m3" || !c.LastMessage.FromMe || c.LastMessage.Status != api.StatusPending || c.LastMessage.SenderName != "You" {
		t.Errorf("last after own: %+v", c.LastMessage)
	}
}

func TestPutMessageMerge(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Pending -> server copy (sent, server ts) keeps a higher local status.
	put(t, s, mine(alice, "p", 100, "hi", api.StatusPending), false)
	if _, err := s.UpdateStatus(ctx, alice, []string{"p"}, api.StatusDelivered); err != nil {
		t.Fatal(err)
	}
	put(t, s, mine(alice, "p", 101, "hi", api.StatusSent), false)
	m := mustMsg(t, s, alice, "p")
	if m.Status != api.StatusDelivered || m.Timestamp != 101 {
		t.Errorf("merge status: %s ts %d", m.Status, m.Timestamp)
	}
	// A revoked message stays revoked when the original is re-delivered.
	put(t, s, txt(alice, "r", 100, "secret"), false)
	if _, err := s.ApplyRevoke(ctx, alice, "r"); err != nil {
		t.Fatal(err)
	}
	put(t, s, txt(alice, "r", 100, "secret"), false)
	if m := mustMsg(t, s, alice, "r"); !m.Deleted || m.Type != api.MsgRevoked || m.Text != "" {
		t.Errorf("revoked resurrected: %+v", m)
	}
	// An edit survives re-delivery of the original.
	put(t, s, txt(alice, "e", 100, "tpyo"), false)
	if _, err := s.ApplyEdit(ctx, alice, "e", "typo", nil, 110); err != nil {
		t.Fatal(err)
	}
	put(t, s, txt(alice, "e", 100, "tpyo"), false)
	if m := mustMsg(t, s, alice, "e"); m.Text != "typo" || !m.Edited {
		t.Errorf("edit lost: %+v", m)
	}
	// Incoming Deleted flag normalizes.
	put(t, s, &api.Message{ID: "d", Chat: alice, Sender: alice, Timestamp: 5, Type: api.MsgImage, Text: "cap", Deleted: true,
		Media: &api.MediaInfo{Mime: "image/jpeg"}}, false)
	if m := mustMsg(t, s, alice, "d"); m.Type != api.MsgRevoked || m.Text != "" || m.Media != nil {
		t.Errorf("deleted normalize: %+v", m)
	}
	if err := s.PutMessage(ctx, &api.Message{Chat: alice}, false); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("missing id: %v", err)
	}
}

func TestNameResolution(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u1 := "61400000011@s.whatsapp.net"
	u2 := "61400000012@s.whatsapp.net"
	u3 := "61400000013@s.whatsapp.net"
	u4 := "61400000014@s.whatsapp.net"
	u5 := "61400000015@s.whatsapp.net"
	lid := "987654321@lid"
	g2 := "120363000000000002@g.us"
	if err := s.PutContacts(ctx, []api.Contact{
		{JID: u1, FullName: "Full One", PushName: "push1", BusinessName: "Biz1"},
		{JID: u2, PushName: "push2", BusinessName: "Biz2"},
		{JID: u3, BusinessName: "Biz3"},
		{JID: u5, FullName: "Contact Five"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, j := range []string{u1, u2, u3, u4, u5, lid, group, g2} {
		put(t, s, txt(j, "x", 1, "hi"), false)
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: group, Name: ptr("Climbing crew")}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: u5, Name: ptr("Explicit Five")}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ jid, want string }{
		{u1, "Full One"},
		{u2, "push2"},
		{u3, "Biz3"},
		{u4, "+61400000014"},
		{u5, "Explicit Five"},
		{lid, "987654321"},
		{group, "Climbing crew"},
		{g2, "Unnamed group"},
	} {
		if c := mustChat(t, s, tc.jid); c.Name != tc.want {
			t.Errorf("%s: name %q want %q", tc.jid, c.Name, tc.want)
		}
	}
	// Contact.Name follows the same precedence; Phone derived from the JID.
	c, err := s.GetContact(ctx, u2)
	if err != nil || c.Name != "push2" || c.Phone != "61400000012" {
		t.Errorf("contact: %+v %v", c, err)
	}
}

func TestSenderNames(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.PutContacts(ctx, []api.Contact{{JID: alice, FullName: "Alice Nguyen"}}); err != nil {
		t.Fatal(err)
	}
	put(t, s, &api.Message{ID: "1", Chat: group, Sender: alice, PushName: "Ali", Timestamp: 1, Type: api.MsgText, Text: "a"}, false)
	put(t, s, &api.Message{ID: "2", Chat: group, Sender: bob, PushName: "Bobby", Timestamp: 2, Type: api.MsgText, Text: "b",
		ReplyTo: &api.Quote{ID: "1", Sender: alice, Type: api.MsgText, Preview: "a"}}, false)
	put(t, s, &api.Message{ID: "3", Chat: group, Sender: carol, Timestamp: 3, Type: api.MsgText, Text: "c",
		ReplyTo: &api.Quote{ID: "x", Sender: me, FromMe: true, Type: api.MsgImage, Preview: "📷 Photo"}}, false)
	msgs, _, err := s.ListMessages(ctx, group, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*api.Message{}
	for i := range msgs {
		got[msgs[i].ID] = &msgs[i]
	}
	if n := got["1"].SenderName; n != "Alice Nguyen" {
		t.Errorf("contact name: %q", n)
	}
	if n := got["2"].SenderName; n != "Bobby" {
		t.Errorf("push fallback: %q", n)
	}
	if n := got["3"].SenderName; n != "+61400000003" {
		t.Errorf("phone fallback: %q", n)
	}
	if q := got["2"].ReplyTo; q == nil || q.SenderName != "Alice Nguyen" || q.ID != "1" || q.Preview != "a" {
		t.Errorf("reply: %+v", q)
	}
	if q := got["3"].ReplyTo; q == nil || q.SenderName != "You" || !q.FromMe || q.Type != api.MsgImage {
		t.Errorf("reply from me: %+v", q)
	}
	if got["1"].ReplyTo != nil {
		t.Error("ReplyTo should be nil")
	}
	// Contacts learned later apply retroactively.
	if err := s.PutContacts(ctx, []api.Contact{{JID: bob, FullName: "Bob Smith"}}); err != nil {
		t.Fatal(err)
	}
	if m := mustMsg(t, s, group, "2"); m.SenderName != "Bob Smith" {
		t.Errorf("late contact: %q", m.SenderName)
	}
	// Group chat stub carries the resolved sender name.
	if c := mustChat(t, s, group); c.LastMessage.SenderName != "+61400000003" {
		t.Errorf("stub sender: %+v", c.LastMessage)
	}
}

func TestMentions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.PutContacts(ctx, []api.Contact{{JID: alice, FullName: "Alice Nguyen"}}); err != nil {
		t.Fatal(err)
	}
	in := &api.Message{ID: "m", Chat: group, Sender: carol, Timestamp: 10, Type: api.MsgImage, Text: "@61400000001 @61400000002 @61400000000 look",
		Mentions: []api.Mention{{JID: alice, Name: "ignored"}, {JID: bob}, {JID: me, Me: true}}}
	put(t, s, in, false)
	want := []api.Mention{{JID: alice, Name: "Alice Nguyen"}, {JID: bob, Name: "+61400000002"}, {JID: me, Name: "You", Me: true}}
	if m := mustMsg(t, s, group, "m"); !reflect.DeepEqual(m.Mentions, want) || m.Text != in.Text {
		t.Fatalf("mentions %+v text %q", m.Mentions, m.Text)
	}
	if msgs, _, _ := s.ListMessages(ctx, group, "", 10); len(msgs) != 1 || !reflect.DeepEqual(msgs[0].Mentions, want) {
		t.Errorf("listed: %+v", msgs)
	}
	if c := mustChat(t, s, group); c.LastMessage.Preview != "📷 Photo @Alice Nguyen @+61400000002 @You look" {
		t.Errorf("preview: %q", c.LastMessage.Preview)
	}
	// Names follow the contacts, like sender names.
	if err := s.PutContacts(ctx, []api.Contact{{JID: bob, PushName: "Bobby"}}); err != nil {
		t.Fatal(err)
	}
	if m := mustMsg(t, s, group, "m"); m.Mentions[1].Name != "Bobby" {
		t.Errorf("late contact: %+v", m.Mentions)
	}
	// An edit replaces them; a stale copy of the original doesn't bring them back.
	m, err := s.ApplyEdit(ctx, group, "m", "@61400000002 look", []api.Mention{{JID: bob}}, 20)
	if err != nil || !reflect.DeepEqual(m.Mentions, []api.Mention{{JID: bob, Name: "Bobby"}}) {
		t.Fatalf("edit: %+v %v", m, err)
	}
	put(t, s, in, false)
	if m := mustMsg(t, s, group, "m"); len(m.Mentions) != 1 || m.Text != "@61400000002 look" {
		t.Errorf("after stale copy: %q %+v", m.Text, m.Mentions)
	}
	if res, err := s.Search(ctx, api.SearchParams{Query: "look"}); err != nil || len(res) != 1 || res[0].Snippet != "@Bobby look" {
		t.Errorf("search: %+v %v", res, err)
	}
	// A plain message has none, as [].
	put(t, s, txt(group, "plain", 30, "hi"), false)
	if m := mustMsg(t, s, group, "plain"); m.Mentions == nil || len(m.Mentions) != 0 {
		t.Errorf("plain: %#v", m.Mentions)
	}
	// Revoking drops them.
	if m, err := s.ApplyRevoke(ctx, group, "m"); err != nil || len(m.Mentions) != 0 {
		t.Errorf("revoke: %+v %v", m, err)
	}
	var raw string
	s.w.QueryRow(`SELECT mentions FROM messages WHERE id = 'm'`).Scan(&raw)
	if raw != "" {
		t.Errorf("revoked row keeps %q", raw)
	}
}

func TestMessageFieldsRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	in := &api.Message{
		ID: "img", Chat: group, Sender: bob, Timestamp: 1000, Type: api.MsgImage, Text: "Look *here*",
		Forwarded: true, Media: &api.MediaInfo{Mime: "image/jpeg", Size: 1234, Width: 640, Height: 480, HasThumb: true},
		MediaRef: &api.MediaRef{DirectPath: "/v/t62/abc", URL: "https://mmg/abc", MediaKey: []byte{1, 2, 3},
			FileSHA256: []byte{4}, FileEncSHA256: []byte{5}, FileLength: 1234, Mime: "image/jpeg"},
		Thumb: []byte{0xff, 0xd8, 0xff},
	}
	put(t, s, in, false)
	loc := &api.Message{ID: "loc", Chat: group, Sender: bob, Timestamp: 1001, Type: api.MsgLocation,
		Location: &api.Location{Lat: -33.86, Lng: 151.21, Name: "Opera House"}}
	put(t, s, loc, false)
	voice := &api.Message{ID: "v", Chat: group, Sender: bob, Timestamp: 1002, Type: api.MsgVoice,
		Media: &api.MediaInfo{Mime: "audio/ogg", Duration: 75, Waveform: []byte{0, 50, 100}}}
	put(t, s, voice, false)
	if v := mustMsg(t, s, group, "v"); !reflect.DeepEqual(v.Media.Waveform, []byte{0, 50, 100}) {
		t.Errorf("waveform: %v", v.Media.Waveform)
	}

	m := mustMsg(t, s, group, "img")
	want := api.MediaInfo{Mime: "image/jpeg", Size: 1234, Width: 640, Height: 480, HasThumb: true}
	if m.Media == nil || !reflect.DeepEqual(*m.Media, want) {
		t.Errorf("media: %+v", m.Media)
	}
	if !m.Forwarded || m.Text != "Look *here*" || m.Location != nil {
		t.Errorf("fields: %+v", m)
	}
	if m.Reactions == nil || len(m.Reactions) != 0 {
		t.Errorf("reactions must be empty non-nil: %#v", m.Reactions)
	}
	l := mustMsg(t, s, group, "loc")
	if l.Location == nil || *l.Location != *loc.Location || l.Media != nil {
		t.Errorf("location: %+v", l)
	}
	if c := mustChat(t, s, group); c.LastMessage.Preview != "🎤 Voice message (1:15)" {
		t.Errorf("voice preview: %q", c.LastMessage.Preview)
	}

	// Media ref + thumb + cached path.
	ref, thumb, err := s.GetMediaRef(ctx, group, "img")
	if err != nil {
		t.Fatal(err)
	}
	wantRef := api.MediaRef{Chat: group, ID: "img", Sender: bob, Type: api.MsgImage, Mime: "image/jpeg", DirectPath: "/v/t62/abc",
		URL: "https://mmg/abc", MediaKey: []byte{1, 2, 3}, FileSHA256: []byte{4}, FileEncSHA256: []byte{5}, FileLength: 1234}
	if !reflect.DeepEqual(*ref, wantRef) || !reflect.DeepEqual(thumb, []byte{0xff, 0xd8, 0xff}) {
		t.Errorf("ref: %+v thumb %v", ref, thumb)
	}
	if err := s.SetMediaPath(ctx, group, "img", "/cache/ab/abc.jpg"); err != nil {
		t.Fatal(err)
	}
	if m := mustMsg(t, s, group, "img"); m.Media.Path != "/cache/ab/abc.jpg" {
		t.Errorf("path: %q", m.Media.Path)
	}
	// Re-delivery without path/thumb/keys keeps them.
	put(t, s, &api.Message{ID: "img", Chat: group, Sender: bob, Timestamp: 1000, Type: api.MsgImage, Text: "Look *here*",
		Media: &api.MediaInfo{Mime: "image/jpeg"}}, false)
	ref2, thumb2, _ := s.GetMediaRef(ctx, group, "img")
	if m := mustMsg(t, s, group, "img"); m.Media.Path != "/cache/ab/abc.jpg" || m.Media.Width != 640 || !m.Media.HasThumb ||
		!reflect.DeepEqual(ref2, ref) || len(thumb2) != 3 {
		t.Errorf("re-delivery lost media fields: %+v %+v", m.Media, ref2)
	}
	// A media retry's new path replaces the expired one.
	if err := s.SetMediaDirectPath(ctx, group, "img", "/v/t62/new"); err != nil {
		t.Fatal(err)
	}
	if ref3, _, _ := s.GetMediaRef(ctx, group, "img"); ref3.DirectPath != "/v/t62/new" || ref3.URL != "" {
		t.Errorf("after retry: %+v", ref3)
	}
	// Location message has no media.
	if _, _, err := s.GetMediaRef(ctx, group, "loc"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("loc media ref: %v", err)
	}
	// Revoke drops media.
	if _, err := s.ApplyRevoke(ctx, group, "img"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetMediaRef(ctx, group, "img"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("revoked media ref: %v", err)
	}
}

func TestListMessagesPaging(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// 120 messages; groups of 3 share a timestamp (ties broken by insertion).
	var all []string
	for i := 0; i < 120; i++ {
		id := fmt.Sprintf("m%03d", i)
		all = append(all, id)
		put(t, s, txt(alice, id, int64(1000+i/3), "msg "+id), false)
	}
	// Another chat interleaved must not leak in.
	put(t, s, txt(bob, "other", 1010, "x"), false)

	var got []string
	before := ""
	pages := 0
	for {
		page, more, err := s.ListMessages(ctx, alice, before, 50)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		got = append(got, ids(page)...)
		if !more {
			break
		}
		before = page[len(page)-1].ID
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
	}
	if pages != 3 {
		t.Errorf("pages %d want 3", pages)
	}
	if len(got) != 120 {
		t.Fatalf("got %d messages", len(got))
	}
	for i := range got {
		if got[i] != all[119-i] {
			t.Fatalf("order at %d: %s want %s", i, got[i], all[119-i])
		}
	}
	// Exact multiple: hasMore false on the last full page.
	page, more, _ := s.ListMessages(ctx, alice, "m039", 40)
	if len(page) != 39 || more {
		t.Errorf("tail page: %d more=%v", len(page), more)
	}
	page, more, _ = s.ListMessages(ctx, alice, "m040", 40)
	if len(page) != 40 || more {
		t.Errorf("exact page: %d more=%v", len(page), more)
	}
	page, more, _ = s.ListMessages(ctx, alice, "m000", 40)
	if len(page) != 0 || more || page == nil {
		t.Errorf("before oldest: %v more=%v", page, more)
	}
}

func TestUpdateStatus(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		from, to, want string
		changed        bool
	}{
		{api.StatusPending, api.StatusSent, api.StatusSent, true},
		{api.StatusPending, api.StatusRead, api.StatusRead, true},
		{api.StatusSent, api.StatusDelivered, api.StatusDelivered, true},
		{api.StatusDelivered, api.StatusSent, api.StatusDelivered, false},
		{api.StatusRead, api.StatusDelivered, api.StatusRead, false},
		{api.StatusRead, api.StatusRead, api.StatusRead, false},
		{api.StatusRead, api.StatusPlayed, api.StatusPlayed, true},
		{api.StatusPlayed, api.StatusRead, api.StatusPlayed, false},
		{api.StatusPending, api.StatusError, api.StatusError, true},
		{api.StatusSent, api.StatusError, api.StatusSent, false},
		{api.StatusRead, api.StatusError, api.StatusRead, false},
		{api.StatusError, api.StatusSent, api.StatusSent, true},
		{api.StatusError, api.StatusPending, api.StatusPending, true},
		{"", api.StatusSent, api.StatusSent, true},
	} {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			s := newStore(t)
			put(t, s, mine(alice, "x", 1, "hi", tc.from), false)
			put(t, s, txt(alice, "in", 2, "incoming"), false)
			got, err := s.UpdateStatus(ctx, alice, []string{"x", "in", "unknown"}, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			if (len(got) == 1) != tc.changed || (tc.changed && (got[0].ID != "x" || got[0].Status != tc.want)) {
				t.Errorf("returned %+v", got)
			}
			if m := mustMsg(t, s, alice, "x"); m.Status != tc.want {
				t.Errorf("status %q want %q", m.Status, tc.want)
			}
			if m := mustMsg(t, s, alice, "in"); m.Status != "" {
				t.Errorf("incoming message got status %q", m.Status)
			}
		})
	}
	s := newStore(t)
	if _, err := s.UpdateStatus(ctx, alice, []string{"x"}, "bogus"); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("bogus status: %v", err)
	}
	// Chat stub reflects receipts.
	put(t, s, mine(alice, "y", 5, "yo", api.StatusPending), false)
	if _, err := s.UpdateStatus(ctx, alice, []string{"y"}, api.StatusRead); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, alice); c.LastMessage.Status != api.StatusRead {
		t.Errorf("stub status %q", c.LastMessage.Status)
	}
}

func TestEditRevoke(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	put(t, s, txt(alice, "e", 1, "first"), false)
	m, err := s.ApplyEdit(ctx, alice, "e", "second", nil, 20)
	if err != nil || m.Text != "second" || !m.Edited {
		t.Fatalf("edit: %+v %v", m, err)
	}
	// Stale edit ignored.
	m, err = s.ApplyEdit(ctx, alice, "e", "older", nil, 10)
	if err != nil || m.Text != "second" {
		t.Fatalf("stale edit: %+v %v", m, err)
	}
	if c := mustChat(t, s, alice); c.LastMessage.Preview != "second" {
		t.Errorf("stub after edit: %q", c.LastMessage.Preview)
	}
	put(t, s, &api.Message{ID: "q", Chat: alice, Sender: alice, Timestamp: 2, Type: api.MsgText, Text: "quote me",
		ReplyTo: &api.Quote{ID: "e", Sender: alice, Type: api.MsgText, Preview: "second"}}, false)
	if _, err := s.ApplyReaction(ctx, api.EvReaction{Chat: alice, ID: "q", Sender: alice, Emoji: "👍", Ts: 3}); err != nil {
		t.Fatal(err)
	}
	m, err = s.ApplyRevoke(ctx, alice, "q")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Deleted || m.Type != api.MsgRevoked || m.Text != "" || m.ReplyTo != nil || len(m.Reactions) != 0 || m.Reactions == nil {
		t.Errorf("revoked: %+v", m)
	}
	if c := mustChat(t, s, alice); c.LastMessage.Preview != "🚫 This message was deleted" || c.LastMessage.Type != api.MsgRevoked {
		t.Errorf("stub after revoke: %+v", c.LastMessage)
	}
	// Edits of revoked messages are ignored.
	m, err = s.ApplyEdit(ctx, alice, "q", "zombie", nil, 99)
	if err != nil || m.Text != "" || m.Type != api.MsgRevoked {
		t.Errorf("edit after revoke: %+v %v", m, err)
	}
}

func TestReactions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	put(t, s, txt(group, "m", 1, "react to me"), false)
	steps := []struct {
		r    api.EvReaction
		want []api.Reaction
	}{
		{api.EvReaction{Sender: alice, Emoji: "👍", Ts: 10},
			[]api.Reaction{rx("👍", 1, false)}},
		{api.EvReaction{Sender: bob, Emoji: "👍", Ts: 11},
			[]api.Reaction{rx("👍", 2, false)}},
		{api.EvReaction{Sender: me, FromMe: true, Emoji: "❤️", Ts: 12},
			[]api.Reaction{rx("👍", 2, false), rx("❤️", 1, true)}},
		// One reaction per sender: Bob switches.
		{api.EvReaction{Sender: bob, Emoji: "❤️", Ts: 13},
			[]api.Reaction{rx("❤️", 2, true), rx("👍", 1, false)}},
		// Stale reaction from Bob ignored.
		{api.EvReaction{Sender: bob, Emoji: "😂", Ts: 5},
			[]api.Reaction{rx("❤️", 2, true), rx("👍", 1, false)}},
		// Our own reaction from another device (different sender JID) replaces ours.
		{api.EvReaction{Sender: "61400000000:3@s.whatsapp.net", FromMe: true, Emoji: "😂", Ts: 14},
			[]api.Reaction{rx("👍", 1, false), rx("❤️", 1, false), rx("😂", 1, true)}},
		// Removal.
		{api.EvReaction{Sender: alice, Emoji: "", Ts: 15},
			[]api.Reaction{rx("❤️", 1, false), rx("😂", 1, true)}},
		{api.EvReaction{FromMe: true, Emoji: "", Ts: 16},
			[]api.Reaction{rx("❤️", 1, false)}},
		{api.EvReaction{Sender: bob, Emoji: "", Ts: 17},
			[]api.Reaction{}},
	}
	for i, st := range steps {
		st.r.Chat, st.r.ID = group, "m"
		m, err := s.ApplyReaction(ctx, st.r)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if !reflect.DeepEqual(m.Reactions, st.want) {
			t.Errorf("step %d: reactions %+v want %+v", i, m.Reactions, st.want)
		}
	}
	// Reactions show up in ListMessages too.
	if _, err := s.ApplyReaction(ctx, api.EvReaction{Chat: group, ID: "m", Sender: carol, Emoji: "🔥", Ts: 20}); err != nil {
		t.Fatal(err)
	}
	page, _, _ := s.ListMessages(ctx, group, "", 10)
	if len(page) != 1 || !reflect.DeepEqual(page[0].Reactions, []api.Reaction{rx("🔥", 1, false)}) {
		t.Errorf("list reactions: %+v", page)
	}
}

func TestMarkReadUnreadIncoming(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	put(t, s, txt(group, "1", 1, "a"), true)
	put(t, s, &api.Message{ID: "2", Chat: group, Sender: bob, Timestamp: 2, Type: api.MsgText, Text: "b"}, true)
	put(t, s, mine(group, "3", 3, "c", api.StatusSent), false)
	put(t, s, &api.Message{ID: "sys", Chat: group, Timestamp: 3, Type: api.MsgSystem, Text: "Bob joined"}, false)
	put(t, s, txt(group, "4", 4, "d"), true)

	keys, err := s.UnreadIncoming(ctx, group, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []api.MsgKey{{Chat: group, ID: "1", Sender: alice}, {Chat: group, ID: "2", Sender: bob}, {Chat: group, ID: "4", Sender: alice}}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("unread incoming %+v", keys)
	}
	// limit keeps the newest, still oldest first.
	keys, _ = s.UnreadIncoming(ctx, group, 2)
	if len(keys) != 2 || keys[0].ID != "2" || keys[1].ID != "4" {
		t.Errorf("limited: %+v", keys)
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: group, MarkedUnread: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, group); c.Unread != 3 || !c.MarkedUnread {
		t.Errorf("before read: %+v", c)
	}
	if err := s.MarkRead(ctx, group); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, group); c.Unread != 0 || c.MarkedUnread {
		t.Errorf("after read: %+v", c)
	}
	if keys, _ := s.UnreadIncoming(ctx, group, 10); len(keys) != 0 {
		t.Errorf("after read: %+v", keys)
	}
	put(t, s, txt(group, "5", 5, "e"), true)
	if keys, _ := s.UnreadIncoming(ctx, group, 10); len(keys) != 1 || keys[0].ID != "5" {
		t.Errorf("new after read: %+v", keys)
	}
	// Read on another device (patch unread=0) also advances the marker.
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: group, Unread: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.UnreadIncoming(ctx, group, 10); len(keys) != 0 {
		t.Errorf("after remote read: %+v", keys)
	}
	// 1:1 incoming with empty sender reports the chat as sender.
	put(t, s, &api.Message{ID: "n", Chat: alice, Timestamp: 1, Type: api.MsgText, Text: "x"}, true)
	if keys, _ := s.UnreadIncoming(ctx, alice, 10); len(keys) != 1 || keys[0].Sender != alice {
		t.Errorf("1:1 sender: %+v", keys)
	}
}

func TestListChats(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// 30 chats: some pinned, some archived, several with equal activity.
	type exp struct {
		jid      string
		pinned   int64
		act      int64
		archived bool
	}
	var all []exp
	for i := 0; i < 30; i++ {
		jid := fmt.Sprintf("614000001%02d@s.whatsapp.net", i)
		e := exp{jid: jid, act: int64(1000 + (i%10)*10), archived: i%9 == 4}
		if i%7 == 0 {
			e.pinned = int64(100 - i) // the newest pin sorts first, whatever the activity
		}
		all = append(all, e)
		put(t, s, txt(jid, "m", e.act, "hi"), false)
		if err := s.UpsertChat(ctx, api.ChatPatch{JID: jid, Pinned: ptr(e.pinned), Archived: ptr(e.archived)}); err != nil {
			t.Fatal(err)
		}
	}
	put(t, s, txt("status@broadcast", "s", 5000, "status"), false)

	expect := func(archived bool) []string {
		var list []exp
		for _, e := range all {
			if e.archived == archived {
				list = append(list, e)
			}
		}
		// Reference ordering.
		for i := 1; i < len(list); i++ {
			for j := i; j > 0; j-- {
				a, b := list[j-1], list[j]
				less := b.pinned > a.pinned ||
					b.pinned == a.pinned && (b.act > a.act || b.act == a.act && b.jid < a.jid)
				if !less {
					break
				}
				list[j-1], list[j] = b, a
			}
		}
		out := make([]string, len(list))
		for i, e := range list {
			out[i] = e.jid
		}
		return out
	}
	for _, archived := range []bool{false, true} {
		for _, limit := range []int{1, 4, 7, 100} {
			var got []string
			cursor := ""
			for n := 0; ; n++ {
				res, err := s.ListChats(ctx, api.ChatsListParams{Limit: limit, Cursor: cursor, Archived: archived})
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range res.Chats {
					got = append(got, c.JID)
					if c.Archived != archived {
						t.Errorf("archived filter: %+v", c)
					}
					if c.LastMessage == nil || c.LastMessage.Preview != "hi" {
						t.Errorf("stub: %+v", c.LastMessage)
					}
				}
				if res.NextCursor == "" {
					break
				}
				if len(res.Chats) != limit || n > 40 {
					t.Fatalf("page size %d (limit %d)", len(res.Chats), limit)
				}
				cursor = res.NextCursor
			}
			if want := expect(archived); !reflect.DeepEqual(got, want) {
				t.Errorf("archived=%v limit=%d:\n got %v\nwant %v", archived, limit, got, want)
			}
		}
	}
	if _, err := s.ListChats(ctx, api.ChatsListParams{Cursor: "!!garbage"}); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("bad cursor: %v", err)
	}
}

func TestChatFlags(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()
	for _, tc := range []struct {
		until     int64
		muted     bool
		wantUntil int64
	}{
		{0, false, 0}, {-1, true, -1}, {future, true, future}, {past, false, 0},
	} {
		if err := s.UpsertChat(ctx, api.ChatPatch{JID: alice, MutedUntil: ptr(tc.until)}); err != nil {
			t.Fatal(err)
		}
		if c := mustChat(t, s, alice); c.Muted != tc.muted || c.MutedUntil != tc.wantUntil {
			t.Errorf("mute %d: muted=%v until=%d", tc.until, c.Muted, c.MutedUntil)
		}
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: alice, Pinned: ptr(int64(1761000000)), Archived: ptr(true), ReadOnly: ptr(true),
		Unread: ptr(4), MarkedUnread: ptr(true), Kind: ptr(api.ChatUser)}); err != nil {
		t.Fatal(err)
	}
	c := mustChat(t, s, alice)
	if c.Pinned != 1761000000 || !c.Archived || !c.ReadOnly || c.Unread != 4 || !c.MarkedUnread || c.LastMessage != nil || c.Avatar != "" {
		t.Errorf("flags: %+v", c)
	}
	// Nil fields are left alone.
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: alice, Name: ptr("Ally")}); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, alice); c.Pinned != 1761000000 || !c.Archived || c.Unread != 4 || c.Name != "Ally" {
		t.Errorf("partial patch: %+v", c)
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{}); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("empty jid: %v", err)
	}
}

func TestSearch(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.PutContacts(ctx, []api.Contact{{JID: alice, FullName: "Alice Nguyen"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: group, Name: ptr("Climbing crew")}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("filler words go here ", 20) + "the climbing gym at six " + strings.Repeat("more trailing text ", 20)
	put(t, s, txt(alice, "a1", 100, "See you at the climbing gym"), false)
	put(t, s, txt(alice, "a2", 300, "Café crème later?"), false)
	put(t, s, &api.Message{ID: "g1", Chat: group, Sender: bob, PushName: "Bobby", Timestamp: 200, Type: api.MsgText, Text: long}, false)
	put(t, s, &api.Message{ID: "g2", Chat: group, Sender: alice, Timestamp: 250, Type: api.MsgImage, Text: "climbers photo",
		Media: &api.MediaInfo{Mime: "image/jpeg"}}, false)
	put(t, s, mine(group, "g3", 260, "AND OR NOT \"quoted\" (paren) col:on *star", api.StatusSent), false)
	put(t, s, txt(bob, "b1", 400, "multi\nline\nclimbing\nnotes"), false)

	for _, tc := range []struct {
		q, chat string
		want    []string
	}{
		{"climb", "", []string{"b1", "g2", "g1", "a1"}}, // prefix on last term, newest first
		{"climbing", "", []string{"b1", "g1", "a1"}},
		{"climbing gym", "", []string{"g1", "a1"}},
		{"gym climbing", "", []string{"g1", "a1"}}, // AND, not phrase
		{"CLIMB", group, []string{"g2", "g1"}},
		{"cafe creme", "", []string{"a2"}}, // diacritics folded
		{"crème", "", []string{"a2"}},
		{`AND OR NOT "quoted" (paren) col:on *star`, "", []string{"g3"}},
		{`"quot`, "", []string{"g3"}},
		{"col:", "", []string{"g3"}},
		{"NOT", "", []string{"b1", "g3"}}, // literal, and a prefix of "notes"
		{"***", "", nil},
		{"   ", "", nil},
		{"zzz", "", nil},
	} {
		res, err := s.Search(ctx, api.SearchParams{Query: tc.q, Chat: tc.chat})
		if err != nil {
			t.Errorf("%q: %v", tc.q, err)
			continue
		}
		if res == nil {
			t.Errorf("%q: nil results", tc.q)
		}
		var got []string
		for _, r := range res {
			got = append(got, r.ID)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q chat=%q: got %v want %v", tc.q, tc.chat, got, tc.want)
		}
	}
	res, _ := s.Search(ctx, api.SearchParams{Query: "gym six"})
	if len(res) != 1 {
		t.Fatalf("gym six: %+v", res)
	}
	r := res[0]
	if r.Chat != group || r.ChatName != "Climbing crew" || r.SenderName != "Bobby" || r.Timestamp != 200 || r.FromMe {
		t.Errorf("result fields: %+v", r)
	}
	if !strings.Contains(r.Snippet, "climbing gym at six") || len([]rune(r.Snippet)) > 160 || !strings.Contains(r.Snippet, "…") {
		t.Errorf("snippet %q (%d runes)", r.Snippet, len([]rune(r.Snippet)))
	}
	res, _ = s.Search(ctx, api.SearchParams{Query: "notes"})
	if len(res) != 1 || strings.Contains(res[0].Snippet, "\n") || res[0].ChatName != "+61400000002" {
		t.Errorf("multiline snippet: %+v", res)
	}
	res, _ = s.Search(ctx, api.SearchParams{Query: "see you"})
	if len(res) != 1 || res[0].ChatName != "Alice Nguyen" || res[0].SenderName != "Alice Nguyen" {
		t.Errorf("names: %+v", res)
	}
	res, _ = s.Search(ctx, api.SearchParams{Query: "climb", Limit: 2})
	if len(res) != 2 {
		t.Errorf("limit: %d", len(res))
	}

	// Edits re-index; revokes un-index.
	if _, err := s.ApplyEdit(ctx, alice, "a1", "See you at the bouldering wall", nil, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRevoke(ctx, group, "g2"); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Search(ctx, api.SearchParams{Query: "climb"})
	if len(res) != 2 || res[0].ID != "b1" || res[1].ID != "g1" {
		t.Errorf("after edit/revoke: %+v", res)
	}
	res, _ = s.Search(ctx, api.SearchParams{Query: "boulder"})
	if len(res) != 1 || res[0].ID != "a1" {
		t.Errorf("edited text: %+v", res)
	}
	// Re-delivery (upsert) keeps a single index entry.
	put(t, s, &api.Message{ID: "g1", Chat: group, Sender: bob, Timestamp: 200, Type: api.MsgText, Text: long}, false)
	res, _ = s.Search(ctx, api.SearchParams{Query: "climbing"})
	if len(res) != 2 {
		t.Errorf("duplicate index entries: %+v", res)
	}
	// FTS index stays consistent with its content table.
	if _, err := s.w.Exec(`INSERT INTO messages_fts(messages_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		t.Errorf("fts integrity: %v", err)
	}
}

func TestContacts(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.PutContacts(ctx, []api.Contact{
		{JID: alice, FullName: "Alice Nguyen", PushName: "Ali"},
		{JID: bob, PushName: "bobby"},
		{JID: carol, BusinessName: "Carol's Café", Phone: "+61400000003"},
		{JID: "61400000004@s.whatsapp.net", Name: "+61400000004"}, // derived Name is not stored
		{JID: ""}, // ignored
	}))
	must(s.PutContacts(ctx, []api.Contact{{JID: "61400000004@s.whatsapp.net", PushName: "Dee"}}))
	if d, _ := s.GetContact(ctx, "61400000004@s.whatsapp.net"); d.Name != "Dee" || d.FullName != "" {
		t.Errorf("push name after phone-only contact: %+v", d)
	}
	// Merge: empty fields don't clobber.
	must(s.PutContacts(ctx, []api.Contact{{JID: alice, PushName: "Alice N"}, {JID: bob, FullName: "Bob Smith"}}))
	a, err := s.GetContact(ctx, alice)
	must(err)
	if want := (api.Contact{JID: alice, Name: "Alice Nguyen", FullName: "Alice Nguyen", PushName: "Alice N", Phone: "61400000001"}); *a != want {
		t.Errorf("merged: %+v", a)
	}
	if c, _ := s.GetContact(ctx, carol); c.Phone != "61400000003" || c.Name != "Carol's Café" {
		t.Errorf("phone normalize: %+v", c)
	}

	// Avatars: chat row and contact row.
	put(t, s, txt(alice, "m", 1, "hi"), false)
	put(t, s, txt(group, "m", 1, "hi"), false)
	must(s.SetAvatar(ctx, alice, "/av/alice.jpg"))
	must(s.SetAvatar(ctx, group, "/av/group.jpg"))
	must(s.SetAvatar(ctx, "61400000077@s.whatsapp.net", "/av/x.jpg")) // unknown person: contact row created
	if c := mustChat(t, s, alice); c.Avatar != "/av/alice.jpg" {
		t.Errorf("chat avatar %q", c.Avatar)
	}
	if c := mustChat(t, s, group); c.Avatar != "/av/group.jpg" {
		t.Errorf("group avatar %q", c.Avatar)
	}
	if c, err := s.GetContact(ctx, "61400000077@s.whatsapp.net"); err != nil || c.Avatar != "/av/x.jpg" {
		t.Errorf("contact avatar %+v %v", c, err)
	}
	// A chat created after the contact got its avatar inherits it.
	put(t, s, txt("61400000077@s.whatsapp.net", "m", 1, "hi"), false)
	if c := mustChat(t, s, "61400000077@s.whatsapp.net"); c.Avatar != "/av/x.jpg" {
		t.Errorf("inherited avatar %q", c.Avatar)
	}
	must(s.SetAvatar(ctx, alice, ""))
	if c := mustChat(t, s, alice); c.Avatar != "" {
		t.Errorf("cleared avatar %q", c.Avatar)
	}
}

func TestSettings(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if all, err := s.AllSettings(ctx); len(all) != 0 || err != nil {
		t.Fatalf("unset: %v %v", all, err)
	}
	if err := s.SetSetting(ctx, api.SettingMediaCacheMB, "512"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, api.SettingNotifications, "false"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, api.SettingMediaCacheMB, "256"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "x", "{not json"); !errors.Is(err, api.ErrInvalidParams) {
		t.Errorf("invalid json: %v", err)
	}
	all, err := s.AllSettings(ctx)
	if err != nil || !reflect.DeepEqual(all, map[string]string{api.SettingMediaCacheMB: "256", api.SettingNotifications: "false"}) {
		t.Errorf("all: %v %v", all, err)
	}
}

func TestPutHistory(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	// Live state before the history batch.
	put(t, s, txt(alice, "live", 5000, "live msg"), true)

	var msgs []*api.Message
	// Out of order on purpose.
	for i := 9; i >= 0; i-- {
		msgs = append(msgs, txt(alice, fmt.Sprintf("h%d", i), int64(1000+i), fmt.Sprintf("history %d", i)))
	}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, &api.Message{ID: fmt.Sprintf("g%d", i), Chat: group, Sender: bob, Timestamp: int64(2000 + i),
			Type: api.MsgText, Text: fmt.Sprintf("group %d", i)})
	}
	msgs = append(msgs, &api.Message{ID: "gsys", Chat: group, Sender: bob, Timestamp: 2005, Type: api.MsgSystem, Text: "Bob changed the group icon"},
		mine(group, "gme", 2010, "mine", api.StatusRead), nil, &api.Message{Chat: group})
	h := &api.EvHistory{
		Chats: []api.ChatPatch{
			{JID: group, Name: ptr("Hist group"), Unread: ptr(2), Pinned: ptr(int64(1500)), LastActivity: ptr(int64(2010))},
			{JID: carol, Unread: ptr(0), Archived: ptr(true), LastActivity: ptr(int64(10))},
		},
		Messages: msgs,
		Contacts: []api.Contact{{JID: bob, FullName: "Bob Smith"}},
		Reactions: []api.EvReaction{
			{Chat: group, ID: "g1", Sender: alice, Emoji: "👍", Ts: 1},
			{Chat: group, ID: "g1", FromMe: true, Emoji: "👍", Ts: 1},
			{Chat: group, ID: "unknown", Sender: alice, Emoji: "👍", Ts: 1},
		},
	}
	if err := s.PutHistory(ctx, h); err != nil {
		t.Fatal(err)
	}
	// Re-applying the same batch is idempotent and never inflates unread.
	if err := s.PutHistory(ctx, h); err != nil {
		t.Fatal(err)
	}
	a := mustChat(t, s, alice)
	if a.Unread != 1 || a.LastMessage.ID != "live" || a.LastActivity != 5000 {
		t.Errorf("alice: %+v", a)
	}
	page, more, _ := s.ListMessages(ctx, alice, "", 5)
	if !reflect.DeepEqual(ids(page), []string{"live", "h9", "h8", "h7", "h6"}) || !more {
		t.Errorf("alice page: %v", ids(page))
	}
	g := mustChat(t, s, group)
	if g.Name != "Hist group" || g.Unread != 2 || g.Pinned != 1500 || g.LastMessage.ID != "gme" || g.LastMessage.Status != api.StatusRead || g.Kind != api.ChatGroup {
		t.Errorf("group: %+v %+v", g, g.LastMessage)
	}
	if m := mustMsg(t, s, group, "g1"); m.SenderName != "Bob Smith" || !reflect.DeepEqual(m.Reactions, []api.Reaction{rx("👍", 2, true)}) {
		t.Errorf("g1: %+v", m)
	}
	// Read marker: the newest 2 incoming are unread; system messages don't count.
	keys, _ := s.UnreadIncoming(ctx, group, 10)
	if len(keys) != 2 || keys[0].ID != "g3" || keys[1].ID != "g4" {
		t.Errorf("group unread incoming: %+v", keys)
	}
	if c := mustChat(t, s, carol); !c.Archived || c.LastMessage != nil || c.LastActivity != 10 {
		t.Errorf("carol: %+v", c)
	}
	res, _ := s.Search(ctx, api.SearchParams{Query: "history"})
	if len(res) != 10 {
		t.Errorf("history searchable: %d", len(res))
	}
	if err := s.PutHistory(ctx, nil); err != nil {
		t.Error(err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			chat := fmt.Sprintf("6140000050%d@s.whatsapp.net", w)
			for i := 0; i < 50; i++ {
				if err := s.PutMessage(ctx, txt(chat, fmt.Sprintf("%d", i), int64(i), "concurrent text"), true); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := s.ListChats(ctx, api.ChatsListParams{}); err != nil {
					errs <- err
					return
				}
				if _, err := s.Search(ctx, api.SearchParams{Query: "concurrent"}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	res, _ := s.ListChats(ctx, api.ChatsListParams{})
	if len(res.Chats) != 4 {
		t.Fatalf("chats: %d", len(res.Chats))
	}
	for _, c := range res.Chats {
		if c.Unread != 50 {
			t.Errorf("%s unread %d", c.JID, c.Unread)
		}
	}
}

func TestFTSQuery(t *testing.T) {
	for in, want := range map[string]string{
		"climb":          `"climb"*`,
		"see  you":       `"see" "you"*`,
		`say "hi"`:       `"say" """hi"""*`,
		"a AND b":        `"a" "AND" "b"*`,
		"*** ---":        ``,
		"col:x":          `"col:x"*`,
		"":               ``,
		"日本語":            `"日本語"*`,
		"trailing ! ?? ": `"trailing"*`,
	} {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %s want %s", in, got, want)
		}
	}
}

// history builds n messages spread over chats, with some media and replies.
func history(n, chats int) *api.EvHistory {
	h := &api.EvHistory{}
	for i := 0; i < n; i++ {
		chat := fmt.Sprintf("614%08d@s.whatsapp.net", i%chats)
		m := txt(chat, fmt.Sprintf("m%06d", i), int64(1_700_000_000+i), fmt.Sprintf("message number %d about something", i))
		switch {
		case i%10 == 0:
			m.Type = api.MsgImage
			m.Media = &api.MediaInfo{Mime: "image/jpeg", Width: 1280, Height: 960, HasThumb: true}
			m.MediaRef = &api.MediaRef{DirectPath: "/v/t62/x", MediaKey: make([]byte, 32), FileLength: 180_000}
			m.Thumb = make([]byte, 2000)
		case i%7 == 0:
			m.ReplyTo = &api.Quote{ID: fmt.Sprintf("m%06d", i-chats), Sender: chat, Type: api.MsgText, Preview: "earlier"}
		case i%3 == 0:
			m.Sender, m.FromMe, m.Status = me, true, api.StatusRead
		}
		h.Messages = append(h.Messages, m)
	}
	return h
}

func BenchmarkListMessages10k(b *testing.B) {
	s := newStore(b)
	ctx := context.Background()
	if err := s.PutHistory(ctx, history(10_000, 1)); err != nil {
		b.Fatal(err)
	}
	chat := fmt.Sprintf("614%08d@s.whatsapp.net", 0)
	for _, before := range []string{"", "m005000"} {
		b.Run("before="+before, func(b *testing.B) {
			for b.Loop() {
				page, more, err := s.ListMessages(ctx, chat, before, 50)
				if err != nil || len(page) != 50 || !more {
					b.Fatal(len(page), more, err)
				}
			}
		})
	}
}

func BenchmarkPutHistory20k(b *testing.B) {
	ctx := context.Background()
	h := history(20_000, 200)
	for b.Loop() {
		b.StopTimer()
		s, err := Open(ctx, filepath.Join(b.TempDir(), "wisp.db"))
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := s.PutHistory(ctx, h); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		s.Close()
		b.StartTimer()
	}
}

// App state settings that arrived first survive a history batch that
// doesn't carry them (wa/history.go only sets what it knows).
func TestHistoryKeepsChatSettings(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.UpsertChat(ctx, api.ChatPatch{JID: group, Pinned: ptr(int64(1700)), MutedUntil: ptr(int64(-1)),
		Archived: ptr(true), MarkedUnread: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	h := &api.EvHistory{Chats: []api.ChatPatch{{JID: group, Unread: ptr(0), LastActivity: ptr(int64(2000))}}}
	if err := s.PutHistory(ctx, h); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, group); c.Pinned != 1700 || !c.Muted || !c.Archived || !c.MarkedUnread {
		t.Errorf("chat %+v", c)
	}
}

func TestDeleteMessageUnreadCount(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	put(t, s, txt(alice, "read", 100, "read"), true)
	if err := s.MarkRead(ctx, alice); err != nil {
		t.Fatal(err)
	}
	put(t, s, txt(alice, "unread", 200, "unread"), true)
	if err := s.DeleteMessage(ctx, alice, "read"); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, alice); c.Unread != 1 {
		t.Fatalf("deleting read message changed unread to %d", c.Unread)
	}
	if err := s.DeleteMessage(ctx, alice, "unread"); err != nil {
		t.Fatal(err)
	}
	if c := mustChat(t, s, alice); c.Unread != 0 || c.LastMessage != nil {
		t.Fatalf("deleted last unread message: %+v", c)
	}
}
