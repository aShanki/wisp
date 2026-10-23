package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/store"
)

type sendCall struct {
	chat, id, text string
	reply          *api.ReplyRef
	mentions       []string
	media          *api.OutgoingMedia
}

type fakeWA struct {
	mu           sync.Mutex
	handler      func(api.WAEvent)
	loggedIn     bool
	nextID       int
	sends        []sendCall
	sendGate     chan struct{} // if non-nil, sends wait for a value
	sendErr      error
	ephemeralErr error // SetTyping, SubscribePresence
	marked       [][]api.MsgKey
	private      []bool
	markErr      error
	online       []bool
	connected    bool
	pins         []api.SetPinnedParams
	resyncs      int
	rejected     []string
	members      map[string][]api.GroupMember
}

var _ api.WA = (*fakeWA)(nil)

const selfJID = "61400000000@s.whatsapp.net"

func (w *fakeWA) Download(ctx context.Context, ref *api.MediaRef) ([]byte, error) {
	return []byte("data"), nil
}
func (w *fakeWA) AvatarURL(ctx context.Context, jid string) (string, string, error) {
	return "", "", api.ErrNotFound
}
func (w *fakeWA) SetEventHandler(h func(api.WAEvent)) { w.handler = h }
func (w *fakeWA) Connect(ctx context.Context) error {
	w.mu.Lock()
	w.connected = true
	w.mu.Unlock()
	return nil
}
func (w *fakeWA) isConnected() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.connected
}
func (w *fakeWA) Disconnect() {}
func (w *fakeWA) IsLoggedIn() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.loggedIn
}
func (w *fakeWA) SelfJID() string  { return selfJID }
func (w *fakeWA) SelfName() string { return "Me" }
func (w *fakeWA) AuthState() string {
	if w.IsLoggedIn() {
		return api.AuthPaired
	}
	return api.AuthUnpaired
}
func (w *fakeWA) ConnState() string                 { return api.ConnConnected }
func (w *fakeWA) StartQR(ctx context.Context) error { return nil }
func (w *fakeWA) PairPhone(ctx context.Context, phone string) (string, error) {
	return "ABCD-EFGH", nil
}
func (w *fakeWA) Logout(ctx context.Context) error {
	w.setLoggedIn(false)
	return nil
}
func (w *fakeWA) setLoggedIn(v bool) {
	w.mu.Lock()
	w.loggedIn = v
	w.mu.Unlock()
}
func (w *fakeWA) NewMessageID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextID++
	return "OUT" + string(rune('A'+w.nextID))
}
func (w *fakeWA) waitGate() {
	w.mu.Lock()
	g := w.sendGate
	w.mu.Unlock()
	if g != nil {
		<-g
	}
}
func (w *fakeWA) SendText(ctx context.Context, chat, id, text string, reply *api.ReplyRef, mentions []string) error {
	w.mu.Lock()
	w.sends = append(w.sends, sendCall{chat: chat, id: id, text: text, reply: reply, mentions: mentions})
	err := w.sendErr
	w.mu.Unlock()
	w.waitGate()
	return err
}
func (w *fakeWA) SendMedia(ctx context.Context, chat, id string, m *api.OutgoingMedia, caption string, reply *api.ReplyRef) (*api.MediaRef, error) {
	w.mu.Lock()
	w.sends = append(w.sends, sendCall{chat: chat, id: id, text: caption, reply: reply, media: m})
	err := w.sendErr
	w.mu.Unlock()
	w.waitGate()
	if err != nil {
		return nil, err
	}
	return &api.MediaRef{DirectPath: "/v/t62/abc", MediaKey: []byte{1, 2, 3}}, nil
}
func (w *fakeWA) MarkRead(ctx context.Context, keys []api.MsgKey, private bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.markErr != nil {
		return w.markErr
	}
	w.marked = append(w.marked, keys)
	w.private = append(w.private, private)
	return nil
}
func (w *fakeWA) SetTyping(ctx context.Context, chat string, typing bool) error {
	return w.ephemeralErr
}
func (w *fakeWA) SubscribePresence(ctx context.Context, jid string) error { return w.ephemeralErr }
func (w *fakeWA) SetOnline(ctx context.Context, online bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.online = append(w.online, online)
	return nil
}
func (w *fakeWA) SetMuted(ctx context.Context, chat string, until int64) error { return nil }
func (w *fakeWA) SetPinned(ctx context.Context, chat string, pinned bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pins = append(w.pins, api.SetPinnedParams{Chat: chat, Pinned: pinned})
	return nil
}
func (w *fakeWA) ResyncAppState(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.resyncs++
	return nil
}
func (w *fakeWA) resyncCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.resyncs
}
func (w *fakeWA) ResolveContact(ctx context.Context, jid string) (*api.Contact, error) {
	return nil, api.ErrNotFound
}

func (w *fakeWA) GroupMembers(ctx context.Context, chat string) ([]api.GroupMember, error) {
	ms, ok := w.members[chat]
	if !ok {
		return nil, api.ErrNotFound
	}
	return append([]api.GroupMember(nil), ms...), nil
}

func (w *fakeWA) RejectCall(ctx context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rejected = append(w.rejected, id)
	return nil
}

func (w *fakeWA) CallsAvailable() bool { return true }
func (w *fakeWA) StartCall(ctx context.Context, chat string) (string, error) {
	return "OUT1", nil
}
func (w *fakeWA) AcceptCall(ctx context.Context, id string) error { return nil }
func (w *fakeWA) EndCall(ctx context.Context, id string) error    { return nil }
func (w *fakeWA) MuteCall(id string, muted bool) error            { return nil }

type fakeMedia struct {
	mu    sync.Mutex
	dir   string
	capB  int64
	wiped int
}

var _ api.Media = (*fakeMedia)(nil)

func (m *fakeMedia) Fetch(ctx context.Context, ref *api.MediaRef) (string, error) {
	return m.dir + "/" + ref.ID + ".jpg", nil
}
func (m *fakeMedia) Thumb(ctx context.Context, ref *api.MediaRef, embedded []byte) (string, error) {
	if len(embedded) == 0 {
		return "", api.ErrNotFound
	}
	return m.dir + "/thumb-" + ref.ID + ".jpg", nil
}
func (m *fakeMedia) Avatar(ctx context.Context, jid string, refresh bool) (string, error) {
	return m.dir + "/avatar-" + jid + ".jpg", nil
}
func (m *fakeMedia) Prepare(ctx context.Context, path string) (*api.OutgoingMedia, error) {
	return &api.OutgoingMedia{Type: api.MsgImage, Mime: "image/jpeg", Data: []byte("jpegdata"), Thumb: []byte{0xff}, Width: 10, Height: 20}, nil
}
func (m *fakeMedia) Put(ctx context.Context, ref *api.MediaRef, data []byte) (string, error) {
	return m.dir + "/put-" + ref.ID, nil
}
func (m *fakeMedia) SetCap(b int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.capB = b
}
func (m *fakeMedia) Wipe() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wiped++
	return nil
}

type fakeNotifier struct {
	mu      sync.Mutex
	actions api.NotifyActions
	shown   []api.Notification
	cleared []string
	calls   []api.CallNotification
}

var _ api.Notifier = (*fakeNotifier)(nil)

func (n *fakeNotifier) SetActions(a api.NotifyActions) { n.actions = a }
func (n *fakeNotifier) Notify(ctx context.Context, x api.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.shown = append(n.shown, x)
	return nil
}
func (n *fakeNotifier) Clear(chat string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cleared = append(n.cleared, chat)
}
func (n *fakeNotifier) ClearAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cleared = append(n.cleared, "*")
}
func (n *fakeNotifier) Close() error { return nil }
func (n *fakeNotifier) NotifyCall(ctx context.Context, c api.CallNotification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, c)
	return nil
}
func (n *fakeNotifier) ClearCall(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cleared = append(n.cleared, "call:"+id)
}

type event struct {
	name   string
	params any
}

type recorder struct {
	mu     sync.Mutex
	events []event
	signal chan struct{}
}

func newRecorder() *recorder { return &recorder{signal: make(chan struct{}, 1000)} }

func (r *recorder) Emit(name string, params any) {
	// Round-trip through JSON like the RPC server does, to catch
	// unmarshalable params and null arrays.
	if _, err := json.Marshal(params); err != nil {
		panic(err)
	}
	r.mu.Lock()
	r.events = append(r.events, event{name, params})
	r.mu.Unlock()
	r.signal <- struct{}{}
}

func (r *recorder) all() []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event(nil), r.events...)
}

func (r *recorder) names() []string {
	var out []string
	for _, e := range r.all() {
		out = append(out, e.name)
	}
	return out
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
}

// waitFor waits until an event matching pred has been emitted.
func (r *recorder) waitFor(t *testing.T, what string, pred func(event) bool) event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		for _, e := range r.all() {
			if pred(e) {
				return e
			}
		}
		select {
		case <-r.signal:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; got %v", what, r.names())
			return event{}
		}
	}
}

type harness struct {
	c  *Core
	st api.Store
	wa *fakeWA
	md *fakeMedia
	nt *fakeNotifier
	ev *recorder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "wisp.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		st: st,
		wa: &fakeWA{loggedIn: true},
		md: &fakeMedia{dir: t.TempDir()},
		nt: &fakeNotifier{},
		ev: newRecorder(),
	}
	h.c = NewWith(Deps{Store: h.st, Media: h.md, WA: h.wa, Notifier: h.nt})
	h.c.SetEmitter(h.ev)
	h.c.Start(context.Background())
	t.Cleanup(func() { h.c.Close() })
	return h
}

func clientCtx(id int64) context.Context {
	return api.WithClientID(context.Background(), id)
}
