//go:build !darwin

package notify

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"wisp/daemon/internal/api"
)

var kdeCaps = []string{"body", "body-hyperlinks", "body-markup", "body-images", "icon-static", "actions", "inline-reply", "persistence"}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestMain hides any installed notification sound from the tests.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "wisp-notify-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}

// fakeExec records the commands a Notifier starts; names in fail fail to start.
type fakeExec struct {
	calls chan []string
	fail  map[string]bool
}

func newFakeExec(fail ...string) *fakeExec {
	f := &fakeExec{calls: make(chan []string, 16), fail: map[string]bool{}}
	for _, name := range fail {
		f.fail[name] = true
	}
	return f
}

func (f *fakeExec) exec(name string, arg ...string) error {
	if f.fail[name] {
		return exec.ErrNotFound
	}
	f.calls <- append([]string{name}, arg...)
	return nil
}

type actionRec struct {
	open, markRead chan string
	reply          chan [3]string
}

func newRec() *actionRec {
	return &actionRec{open: make(chan string, 8), markRead: make(chan string, 8), reply: make(chan [3]string, 8)}
}

func (r *actionRec) actions() api.NotifyActions {
	return api.NotifyActions{
		Open:     func(chat string) { r.open <- chat },
		MarkRead: func(chat string) { r.markRead <- chat },
		Reply:    func(chat, msgID, text string) { r.reply <- [3]string{chat, msgID, text} },
	}
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

func none[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %v", what, v)
	case <-time.After(150 * time.Millisecond):
	}
}

func setup(t *testing.T, caps []string) (*Notifier, *fakeServer, *actionRec, string) {
	t.Helper()
	addr := privateBus(t)
	srv := newFakeServer(t, addr, caps)
	n := New(Config{Log: quietLog(), BusAddress: addr, Exec: newFakeExec().exec})
	t.Cleanup(func() { n.Close() })
	rec := newRec()
	n.SetActions(rec.actions())
	return n, srv, rec, addr
}

const alice = "61400000001@s.whatsapp.net"
const family = "120363000000000001@g.us"

func dm(id, text string) api.Notification {
	return api.Notification{Chat: alice, ChatName: "Alice", MsgID: id, Text: text}
}

func TestNotifyReplacesPerChat(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	ctx := context.Background()

	if err := n.Notify(ctx, dm("m1", "hi there")); err != nil {
		t.Fatal(err)
	}
	c1 := srv.nextCall()
	if c1.ReplacesID != 0 || c1.Summary != "Alice" || c1.Body != "hi there" || c1.AppName != "Wisp" || c1.Icon != "wisp" || c1.Timeout != -1 {
		t.Fatalf("first call: %+v", c1)
	}
	for _, a := range []string{"default", "mark-read", "inline-reply"} {
		if !c1.hasAction(a) {
			t.Errorf("missing action %q in %v", a, c1.Actions)
		}
	}
	if c1.hint("desktop-entry") != "wisp" || c1.hint("category") != "im.received" || c1.hint("urgency") != byte(1) ||
		c1.hint("x-kde-reply-placeholder-text") != "Reply…" || c1.hint("image-path") != "wisp" {
		t.Errorf("hints: %v", c1.Hints)
	}

	// Second message replaces the first and summarises.
	n.Notify(ctx, dm("m2", "second line\nmore"))
	c2 := srv.nextCall()
	if c2.ReplacesID != c1.ID || c2.ID != c1.ID {
		t.Fatalf("replaces_id = %d, want %d", c2.ReplacesID, c1.ID)
	}
	if c2.Body != "2 new messages\nsecond line\nmore" {
		t.Fatalf("body = %q", c2.Body)
	}
	// Same message again (e.g. edit) does not bump the count.
	n.Notify(ctx, dm("m2", "second line edited"))
	if c := srv.nextCall(); c.Body != "2 new messages\nsecond line edited" {
		t.Fatalf("body = %q", c.Body)
	}

	// A different chat gets its own notification.
	n.Notify(ctx, api.Notification{Chat: "61400000002@s.whatsapp.net", MsgID: "x", Text: "yo"})
	c3 := srv.nextCall()
	if c3.ReplacesID != 0 || c3.ID == c1.ID || c3.Summary != "+61400000002" {
		t.Fatalf("other chat: %+v", c3)
	}
}

func TestSound(t *testing.T) {
	addr := privateBus(t)
	srv := newFakeServer(t, addr, kdeCaps)
	sound := filepath.Join(t.TempDir(), "notification.mp3")
	player := newFakeExec()
	n := New(Config{Log: quietLog(), BusAddress: addr, Sound: sound, Exec: player.exec})
	defer n.Close()
	ctx := context.Background()

	n.Notify(ctx, dm("m1", "one"))
	if c := srv.nextCall(); c.hint("suppress-sound") != true {
		t.Fatalf("suppress-sound hint: %v", c.Hints)
	}
	if got := recv(t, player.calls, "pw-play"); !slices.Equal(got, []string{"pw-play", "--media-role=Notification", sound}) {
		t.Fatalf("played %v", got)
	}
	// Within 2 s: shown, not played. An edit of a shown message: not played.
	n.Notify(ctx, dm("m2", "two"))
	srv.nextCall()
	n.mu.Lock()
	n.played = time.Now().Add(-soundGap)
	n.mu.Unlock()
	n.Notify(ctx, dm("m2", "two, edited"))
	srv.nextCall()
	none(t, player.calls, "sound")

	// Do Not Disturb: shown, not played.
	srv.setInhibited(true)
	n.Notify(ctx, dm("m3", "three"))
	srv.nextCall()
	none(t, player.calls, "sound during Do Not Disturb")
	srv.setInhibited(false)
	n.Notify(ctx, dm("m4", "four"))
	srv.nextCall()
	recv(t, player.calls, "sound after Do Not Disturb")
}

func TestSoundFallsBackToPaplay(t *testing.T) {
	addr := privateBus(t)
	srv := newFakeServer(t, addr, kdeCaps)
	player := newFakeExec("pw-play")
	n := New(Config{Log: quietLog(), BusAddress: addr, Sound: "/s/notification.oga", Exec: player.exec})
	defer n.Close()
	n.Notify(context.Background(), dm("m1", "hi"))
	srv.nextCall()
	if got := recv(t, player.calls, "paplay"); !slices.Equal(got, []string{"paplay", "--property=media.role=event", "/s/notification.oga"}) {
		t.Fatalf("played %v", got)
	}
}

func TestNoSoundFile(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	if n.sound != "" {
		t.Fatalf("found sound %q", n.sound)
	}
	n.Notify(context.Background(), dm("m1", "hi"))
	if c := srv.nextCall(); c.hint("suppress-sound") != nil {
		t.Fatalf("suppress-sound without a sound: %v", c.Hints)
	}
}

func TestFindSound(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	if s := findSound(); s != "" {
		t.Fatalf("findSound() = %q, want none", s)
	}
	put := func(dir string) string {
		p := filepath.Join(dir, "wisp", "notification.mp3")
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte("x"), 0o600)
		return p
	}
	inHome := put(filepath.Join(home, ".local", "share"))
	if s := findSound(); s != inHome {
		t.Fatalf("findSound() = %q, want %q", s, inHome)
	}
	inData := put(data)
	if s := findSound(); s != inData {
		t.Fatalf("findSound() = %q, want %q", s, inData)
	}
}

func TestGroupRenderingAndEscaping(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	ctx := context.Background()
	g := api.Notification{Chat: family, ChatName: "Family", IsGroup: true, Sender: "Bob", MsgID: "g1", Text: "a < b && c > d"}
	n.Notify(ctx, g)
	c := srv.nextCall()
	if c.Summary != "Bob @ Family" || c.Body != "a &lt; b &amp;&amp; c &gt; d" {
		t.Fatalf("group single: %q / %q", c.Summary, c.Body)
	}
	g.MsgID, g.Sender, g.Text = "g2", "Carol", "<b>hey</b>"
	n.Notify(ctx, g)
	c = srv.nextCall()
	if c.Summary != "Family" || c.Body != "2 new messages\nCarol: &lt;b&gt;hey&lt;/b&gt;" {
		t.Fatalf("group multi: %q / %q", c.Summary, c.Body)
	}
}

func TestNoMarkupNoInlineReply(t *testing.T) {
	n, srv, _, _ := setup(t, []string{"body", "actions"})
	n.Notify(context.Background(), dm("m1", "x < y"))
	c := srv.nextCall()
	if c.Body != "x < y" {
		t.Fatalf("body = %q (server has no body-markup)", c.Body)
	}
	if c.hasAction("inline-reply") || c.hint("x-kde-reply-placeholder-text") != nil {
		t.Fatalf("inline reply offered without capability: %v", c.Actions)
	}
}

func TestAvatarImagePath(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	av := filepath.Join(t.TempDir(), "a b.jpg")
	os.WriteFile(av, []byte("x"), 0o600)
	m := dm("m1", "hi")
	m.Avatar = av
	n.Notify(context.Background(), m)
	c := srv.nextCall()
	got, _ := c.hint("image-path").(string)
	if !strings.HasPrefix(got, "file:///") || !strings.HasSuffix(got, "/a%20b.jpg") {
		t.Fatalf("image-path = %q", got)
	}
}

func TestDefaultActionOpens(t *testing.T) {
	n, srv, rec, _ := setup(t, kdeCaps)
	n.Notify(context.Background(), dm("m1", "hi"))
	c := srv.nextCall()
	srv.emit("ActivationToken", c.ID, "tok123")
	srv.emit("ActionInvoked", c.ID, "default")
	if got := recv(t, rec.open, "Open"); got != alice {
		t.Fatalf("Open(%q)", got)
	}
	if id := srv.nextClose(); id != c.ID {
		t.Fatalf("closed %d, want %d", id, c.ID)
	}
	if tok := n.TakeActivationToken(); tok != "tok123" {
		t.Fatalf("token = %q", tok)
	}
	if tok := n.TakeActivationToken(); tok != "" {
		t.Fatalf("token not consumed: %q", tok)
	}
	// Next message starts a fresh notification.
	n.Notify(context.Background(), dm("m2", "again"))
	if c2 := srv.nextCall(); c2.ReplacesID != 0 || c2.Body != "again" {
		t.Fatalf("after open: %+v", c2)
	}
}

func TestMarkReadAction(t *testing.T) {
	n, srv, rec, _ := setup(t, kdeCaps)
	n.Notify(context.Background(), dm("m1", "hi"))
	c := srv.nextCall()
	srv.emit("ActionInvoked", c.ID, "mark-read")
	if got := recv(t, rec.markRead, "MarkRead"); got != alice {
		t.Fatalf("MarkRead(%q)", got)
	}
	if id := srv.nextClose(); id != c.ID {
		t.Fatalf("closed %d", id)
	}
	none(t, rec.open, "Open")
}

func TestInlineReply(t *testing.T) {
	n, srv, rec, _ := setup(t, kdeCaps)
	n.Notify(context.Background(), dm("m1", "hi"))
	n.Notify(context.Background(), dm("m2", "you there?"))
	srv.nextCall()
	c := srv.nextCall()
	srv.emit("NotificationReplied", c.ID, "yes!")
	got := recv(t, rec.reply, "Reply")
	if got != [3]string{alice, "m2", "yes!"} {
		t.Fatalf("Reply%v", got)
	}
	srv.nextClose()
	// Signals for unknown ids are ignored.
	srv.emit("NotificationReplied", uint32(9999), "nope")
	srv.emit("ActionInvoked", uint32(9999), "default")
	none(t, rec.reply, "Reply")
	none(t, rec.open, "Open")
}

func TestClear(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	ctx := context.Background()
	n.Notify(ctx, dm("m1", "one"))
	n.Notify(ctx, dm("m2", "two"))
	srv.nextCall()
	c := srv.nextCall()
	n.Clear(alice)
	if id := srv.nextClose(); id != c.ID {
		t.Fatalf("Clear closed %d, want %d", id, c.ID)
	}
	n.Clear(alice) // no-op
	n.Clear("nobody@s.whatsapp.net")
	srv.noClose()
	n.Notify(ctx, dm("m3", "three"))
	if c := srv.nextCall(); c.ReplacesID != 0 || c.Body != "three" {
		t.Fatalf("after Clear: %+v", c)
	}
}

func TestClosedByServer(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	ctx := context.Background()

	// Expired: id is gone but the count carries on.
	n.Notify(ctx, dm("m1", "one"))
	c := srv.nextCall()
	srv.emit("NotificationClosed", c.ID, uint32(1))
	waitFor(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return len(n.byID) == 0 })
	n.Notify(ctx, dm("m2", "two"))
	c = srv.nextCall()
	if c.ReplacesID != 0 || c.Body != "2 new messages\ntwo" {
		t.Fatalf("after expiry: %+v", c)
	}

	// Dismissed by the user: start over.
	srv.emit("NotificationClosed", c.ID, uint32(2))
	waitFor(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return len(n.byChat) == 0 })
	n.Notify(ctx, dm("m3", "three"))
	if c := srv.nextCall(); c.ReplacesID != 0 || c.Body != "three" {
		t.Fatalf("after dismiss: %+v", c)
	}
}

func TestSpoofedSignalIgnored(t *testing.T) {
	n, srv, rec, addr := setup(t, kdeCaps)
	n.Notify(context.Background(), dm("m1", "hi"))
	c := srv.nextCall()

	evil, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer evil.Close()
	n.mu.Lock()
	target := n.conn.Names()[0]
	n.mu.Unlock()
	msg := &dbus.Message{
		Type: dbus.TypeSignal,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:        dbus.MakeVariant(objPath),
			dbus.FieldInterface:   dbus.MakeVariant(iface),
			dbus.FieldMember:      dbus.MakeVariant("NotificationReplied"),
			dbus.FieldDestination: dbus.MakeVariant(target),
			dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(uint32(0), "")),
		},
		Body: []any{c.ID, "send money"},
	}
	if err := evil.Send(msg, nil).Err; err != nil {
		t.Fatal(err)
	}
	none(t, rec.reply, "Reply from spoofed signal")
	// The real server still works.
	srv.emit("NotificationReplied", c.ID, "ok")
	if got := recv(t, rec.reply, "Reply"); got[2] != "ok" {
		t.Fatalf("Reply%v", got)
	}
}

func TestServerRestart(t *testing.T) {
	n, srv, rec, addr := setup(t, kdeCaps)
	ctx := context.Background()
	n.Notify(ctx, dm("m1", "hi"))
	srv.nextCall()

	srv.conn.Close() // server dies; ids are void
	waitFor(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return len(n.byID) == 0 })
	srv2 := newFakeServer(t, addr, kdeCaps, 500)
	n.Notify(ctx, dm("m2", "after restart"))
	c := srv2.nextCall()
	if c.ReplacesID != 0 || c.Body != "2 new messages\nafter restart" {
		t.Fatalf("after restart: %+v", c)
	}
	srv2.emit("ActionInvoked", c.ID, "default")
	if got := recv(t, rec.open, "Open"); got != alice {
		t.Fatalf("Open(%q)", got)
	}
}

func TestBusLossRedials(t *testing.T) {
	n, srv, rec, _ := setup(t, kdeCaps)
	ctx := context.Background()
	n.Notify(ctx, dm("m1", "hi"))
	srv.nextCall()
	n.mu.Lock()
	old := n.conn
	n.mu.Unlock()
	old.Close() // simulate losing the bus
	waitFor(t, func() bool { n.mu.Lock(); defer n.mu.Unlock(); return n.conn == nil })
	if err := n.Notify(ctx, dm("m2", "back")); err != nil {
		t.Fatal(err)
	}
	c := srv.nextCall()
	if c.ReplacesID != 0 {
		t.Fatalf("stale replaces_id after redial: %+v", c)
	}
	srv.emit("ActionInvoked", c.ID, "mark-read")
	recv(t, rec.markRead, "MarkRead")
}

func TestCloseClosesNotifications(t *testing.T) {
	n, srv, _, _ := setup(t, kdeCaps)
	ctx := context.Background()
	n.Notify(ctx, dm("m1", "hi"))
	c := srv.nextCall()
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	if id := srv.nextClose(); id != c.ID {
		t.Fatalf("closed %d", id)
	}
	if err := n.Notify(ctx, dm("m2", "late")); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-srv.calls:
		t.Fatalf("Notify after Close reached server: %+v", c)
	case <-time.After(100 * time.Millisecond):
	}
	n.Clear(alice)
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNoServerReturnsError(t *testing.T) {
	addr := privateBus(t) // bus but nobody owns org.freedesktop.Notifications
	n := New(Config{Log: quietLog(), BusAddress: addr})
	defer n.Close()
	if err := n.Notify(context.Background(), dm("m1", "hi")); err == nil {
		t.Fatal("expected error without a notification server")
	}
	n.Clear(alice)
}

// No bus at all: a working no-op. Runs everywhere (no dbus-daemon needed).
func TestNoBusIsNoop(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, addr := range []string{"", "unix:path=/nonexistent/wisp-test-bus"} {
		n := New(Config{Log: quietLog(), BusAddress: addr})
		n.SetActions(api.NotifyActions{})
		if err := n.Notify(context.Background(), dm("m1", "hi")); err != nil {
			t.Fatalf("Notify on no bus: %v", err)
		}
		n.Clear(alice)
		if n.TakeActivationToken() != "" {
			t.Fatal("token")
		}
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRender(t *testing.T) {
	cases := []struct {
		n      api.Notification
		count  int
		markup bool
		s, b   string
	}{
		{api.Notification{Chat: alice, ChatName: "Alice", Text: "hi"}, 1, true, "Alice", "hi"},
		{api.Notification{Chat: alice, Text: ""}, 1, true, "+61400000001", "New message"},
		{api.Notification{Chat: alice, ChatName: "Alice", Text: "x&y"}, 3, true, "Alice", "3 new messages\nx&amp;y"},
		{api.Notification{Chat: family, ChatName: "Fam", IsGroup: true, Text: "t"}, 1, false, "Fam", "t"},
		{api.Notification{Chat: family, ChatName: "Fam", IsGroup: true, Sender: "Bo", Text: "t"}, 2, false, "Fam", "2 new messages\nBo: t"},
	}
	for i, c := range cases {
		s, b := render(c.n, c.count, c.markup)
		if s != c.s || b != c.b {
			t.Errorf("%d: got %q / %q, want %q / %q", i, s, b, c.s, c.b)
		}
	}
	long := strings.Repeat("é", 1000)
	_, b := render(api.Notification{Chat: alice, Text: long}, 1, false)
	if r := []rune(b); len(r) != maxBodyRunes || r[len(r)-1] != '…' {
		t.Errorf("truncate: %d runes", len(r))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
