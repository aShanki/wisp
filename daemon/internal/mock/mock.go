// Package mock is an api.Backend serving generated data (wispd --mock).
package mock

import (
	"cmp"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wisp/daemon/internal/api"
)

type Config struct {
	Seed     int64
	Traffic  time.Duration // mean gap between incoming messages; 0 = none
	Unpaired bool          // start unpaired and walk through the QR flow
	Call     time.Duration // ring an incoming call this long after start; 0 = none
	Log      *slog.Logger
}

type Backend struct {
	traffic time.Duration
	dir     string // generated media, removed by Close
	chats   map[string]*chat

	mu       sync.Mutex
	emitter  api.Emitter
	auth     string
	pairing  *time.Timer
	active   map[int64]api.UISetActiveParams
	calls    map[string]*mockCall
	settings map[string]any
	seq      int
	closed   bool

	done     chan struct{}
	doneOnce sync.Once
}

type chat struct {
	spec     *chatSpec
	row      api.Chat
	extra    []*api.Message          // sent and received at runtime, oldest first
	extraIdx map[string]int          // id -> position in extra
	paths    map[string]string       // id -> fetched media file
	edits    map[string]*api.Message // id -> generated message as changed since
	gone     map[string]bool         // ids deleted for us
	replying bool
}

var _ api.Backend = (*Backend)(nil)

func New(cfg Config) *Backend {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	dir, err := os.MkdirTemp("", "wisp-mock-")
	if err != nil {
		log.Error("mock: temp dir", "err", err)
	}
	b := &Backend{
		traffic: cfg.Traffic,
		dir:     dir,
		chats:   map[string]*chat{},
		auth:    api.AuthPaired,
		active:  map[int64]api.UISetActiveParams{},
		calls:   map[string]*mockCall{},
		settings: map[string]any{
			api.SettingNotifications: true,
			api.SettingNotifyPreview: true,
			api.SettingSendReceipts:  true,
			api.SettingMediaCacheMB:  1024,
		},
		done: make(chan struct{}),
	}
	if cfg.Unpaired {
		b.auth = api.AuthUnpaired
	}
	for _, cs := range buildSpecs(uint64(cfg.Seed), time.Now().Unix()) {
		last := cs.gen(cs.n - 1)
		var mutedUntil int64
		if cs.muted {
			mutedUntil = -1
		}
		b.chats[cs.jid] = &chat{
			spec: cs,
			row: api.Chat{
				JID: cs.jid, Name: cs.name, Kind: cs.kind,
				LastMessage: api.Stub(&last), LastActivity: last.Timestamp,
				Unread: cs.unread, Muted: cs.muted, MutedUntil: mutedUntil,
				Pinned: cs.pinned, Archived: cs.archived, ReadOnly: cs.readOnly,
			},
			extraIdx: map[string]int{},
			paths:    map[string]string{},
			edits:    map[string]*api.Message{},
			gone:     map[string]bool{},
		}
	}
	if cfg.Traffic > 0 {
		b.after(cfg.Traffic, b.incomingTraffic)
	}
	if cfg.Call > 0 {
		b.after(cfg.Call, b.ring)
	}
	return b
}

// Done is closed after daemon.shutdown.
func (b *Backend) Done() <-chan struct{} { return b.done }

// Close stops timers and removes generated files.
func (b *Backend) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return os.RemoveAll(b.dir)
}

// after runs f under the lock in d, unless the backend has closed.
func (b *Backend) after(d time.Duration, f func()) *time.Timer {
	return time.AfterFunc(d, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if !b.closed {
			f()
		}
	})
}

func (b *Backend) emit(event string, params any) {
	if b.emitter != nil {
		b.emitter.Emit(event, params)
	}
}

func (b *Backend) SetEmitter(e api.Emitter) {
	b.mu.Lock()
	b.emitter = e
	b.mu.Unlock()
}

func (b *Backend) ClientGone(id int64) {
	b.mu.Lock()
	delete(b.active, id)
	b.mu.Unlock()
}

func (b *Backend) connState() string {
	if b.auth == api.AuthPaired {
		return api.ConnConnected
	}
	return api.ConnOffline
}

func (b *Backend) self() (string, string) {
	if b.auth == api.AuthPaired {
		return selfJID, selfName
	}
	return "", ""
}

func (b *Backend) Hello(ctx context.Context, p api.HelloParams) (*api.HelloResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	jid, name := b.self()
	return &api.HelloResult{Protocol: api.ProtocolVersion, Version: "mock", Mock: true,
		Auth: b.auth, Conn: b.connState(), SelfJID: jid, SelfName: name, Calls: true}, nil
}

func (b *Backend) Shutdown(ctx context.Context) error {
	b.doneOnce.Do(func() { close(b.done) })
	return nil
}

func (b *Backend) AuthStatus(ctx context.Context) (*api.AuthStatusResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	jid, name := b.self()
	return &api.AuthStatusResult{State: b.auth, SelfJID: jid, SelfName: name, Conn: b.connState()}, nil
}

const qrCodes = 3

func (b *Backend) AuthStartQR(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.auth == api.AuthPaired {
		return nil
	}
	b.setAuth(api.AuthPairing, "")
	b.showQR(0)
	return nil
}

// showQR emits code n, then the next one 4 s later; after the last, pairs.
func (b *Backend) showQR(n int) {
	if n == qrCodes {
		b.paired()
		return
	}
	code := fmt.Sprintf("2@wisp-mock-%d,%016x", n+1, mix(uint64(time.Now().UnixNano())))
	b.emit(api.EAuthQR, api.QREvent{Code: code, Timeout: 4})
	b.stopPairing()
	b.pairing = b.after(4*time.Second, func() {
		if b.auth == api.AuthPairing {
			b.showQR(n + 1)
		}
	})
}

func (b *Backend) AuthPairPhone(ctx context.Context, p api.PairPhoneParams) (*api.PairPhoneResult, error) {
	digits := strings.TrimPrefix(strings.ReplaceAll(p.Phone, " ", ""), "+")
	if _, err := strconv.ParseUint(digits, 10, 64); err != nil || len(digits) < 8 {
		return nil, api.InvalidParams("phone must be digits in international format")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.auth == api.AuthPaired {
		return nil, api.InvalidParams("already paired")
	}
	b.setAuth(api.AuthPairing, "")
	b.stopPairing()
	b.pairing = b.after(5*time.Second, func() {
		if b.auth == api.AuthPairing {
			b.paired()
		}
	})
	return &api.PairPhoneResult{Code: "WISP-MOCK"}, nil
}

func (b *Backend) AuthLogout(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopPairing()
	b.setAuth(api.AuthUnpaired, "logged out")
	b.emit(api.EConnectionState, api.ConnectionStateEvent{State: api.ConnOffline})
	return nil
}

func (b *Backend) stopPairing() {
	if b.pairing != nil {
		b.pairing.Stop()
		b.pairing = nil
	}
}

func (b *Backend) setAuth(state, reason string) {
	b.auth = state
	jid, name := b.self()
	b.emit(api.EAuthState, api.AuthStateEvent{State: state, SelfJID: jid, SelfName: name, Reason: reason})
}

func (b *Backend) paired() {
	b.stopPairing()
	b.setAuth(api.AuthPaired, "")
	b.emit(api.EConnectionState, api.ConnectionStateEvent{State: api.ConnConnecting})
	b.emit(api.EConnectionState, api.ConnectionStateEvent{State: api.ConnConnected})
	b.emit(api.EHistorySyncProgress, api.HistorySyncEvent{Percent: 40, Chats: len(b.chats) / 2})
	b.after(time.Second, func() {
		b.emit(api.EHistorySyncProgress, api.HistorySyncEvent{Percent: 100, Chats: len(b.chats), Messages: b.total(), Done: true})
	})
}

func (b *Backend) total() int {
	n := 0
	for _, c := range b.chats {
		n += c.count()
	}
	return n
}

// chat returns the chat for jid; the caller holds the lock.
func (b *Backend) chat(jid string) (*chat, error) {
	if b.auth != api.AuthPaired {
		return nil, api.ErrNotLoggedIn
	}
	c, ok := b.chats[jid]
	if !ok {
		return nil, fmt.Errorf("chat %s: %w", jid, api.ErrNotFound)
	}
	return c, nil
}

func (b *Backend) sorted() []*chat {
	cs := make([]*chat, 0, len(b.chats))
	for _, c := range b.chats {
		cs = append(cs, c)
	}
	slices.SortFunc(cs, func(x, y *chat) int {
		return cmp.Or(cmp.Compare(y.row.Pinned, x.row.Pinned),
			cmp.Compare(y.row.LastActivity, x.row.LastActivity), cmp.Compare(x.row.JID, y.row.JID))
	})
	return cs
}

// ChatsList pages by offset into the sorted list; the cursor is the offset.
func (b *Backend) ChatsList(ctx context.Context, p api.ChatsListParams) (*api.ChatsListResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.auth != api.AuthPaired {
		return nil, api.ErrNotLoggedIn
	}
	off := 0
	if p.Cursor != "" {
		var err error
		if off, err = strconv.Atoi(p.Cursor); err != nil || off < 0 {
			return nil, api.InvalidParams("bad cursor")
		}
	}
	res := &api.ChatsListResult{Chats: []api.Chat{}}
	n := 0
	for _, c := range b.sorted() {
		if c.row.Archived != p.Archived {
			continue
		}
		if n++; n <= off {
			continue
		}
		if len(res.Chats) == p.Limit {
			res.NextCursor = strconv.Itoa(off + p.Limit)
			break
		}
		res.Chats = append(res.Chats, c.row)
	}
	return res, nil
}

func (b *Backend) ChatsGet(ctx context.Context, p api.ChatParams) (*api.Chat, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return nil, err
	}
	row := c.row
	return &row, nil
}

func (b *Backend) ChatsMarkRead(ctx context.Context, p api.ChatParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return err
	}
	b.markRead(c)
	return nil
}

func (b *Backend) markRead(c *chat) {
	if c.row.Unread == 0 && !c.row.MarkedUnread {
		return
	}
	c.row.Unread, c.row.MarkedUnread = 0, false
	b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
}

func (b *Backend) ChatsSetTyping(ctx context.Context, p api.SetTypingParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.chat(p.Chat)
	return err
}

func (b *Backend) ChatsSetMuted(ctx context.Context, p api.SetMutedParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return err
	}
	c.row.Muted, c.row.MutedUntil = p.Muted, 0
	if p.Muted {
		c.row.MutedUntil = cmp.Or(p.Until, -1)
	}
	b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
	return nil
}

func (b *Backend) ChatsSetPinned(ctx context.Context, p api.SetPinnedParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return err
	}
	if p.Pinned == (c.row.Pinned > 0) {
		return nil
	}
	c.row.Pinned = 0
	if p.Pinned {
		n := 0
		for _, o := range b.chats {
			if o.row.Pinned > 0 && !o.row.Archived {
				n++
			}
		}
		if n >= 3 {
			return api.ErrPinLimit
		}
		c.row.Pinned = time.Now().Unix()
	}
	b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
	return nil
}

func (c *chat) count() int { return c.spec.n + len(c.extra) }

// at returns the message at position p (0 = oldest).
func (c *chat) at(p int) api.Message {
	var m api.Message
	if p < c.spec.n {
		m = c.spec.gen(p)
		if e, ok := c.edits[m.ID]; ok {
			m = *e
		}
	} else {
		m = *c.extra[p-c.spec.n]
	}
	if path, ok := c.paths[m.ID]; ok && m.Media != nil {
		media := *m.Media
		media.Path = path
		m.Media = &media
	}
	return m
}

func (c *chat) pos(id string) (int, bool) {
	if i, ok := c.spec.index(id); ok {
		return i, true
	}
	i, ok := c.extraIdx[id]
	return c.spec.n + i, ok
}

func (b *Backend) MessagesList(ctx context.Context, p api.MessagesListParams) (*api.MessagesListResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return nil, err
	}
	end := c.count()
	if p.Before != "" {
		var ok bool
		if end, ok = c.pos(p.Before); !ok {
			return nil, fmt.Errorf("message %s: %w", p.Before, api.ErrNotFound)
		}
	}
	start := max(0, end-p.Limit)
	res := &api.MessagesListResult{Messages: make([]api.Message, 0, end-start), HasMore: start > 0}
	for i := end - 1; i >= start; i-- {
		if m := c.at(i); !c.gone[m.ID] {
			res.Messages = append(res.Messages, m)
		}
	}
	return res, nil
}

// change applies f to message id, then emits it and the chat if it was the
// last message.
func (b *Backend) change(jid, id string, f func(m *api.Message) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(jid)
	if err != nil {
		return err
	}
	p, ok := c.pos(id)
	if !ok || c.gone[id] {
		return fmt.Errorf("message %s: %w", id, api.ErrNotFound)
	}
	m := c.at(p)
	if err := f(&m); err != nil {
		return err
	}
	if p < c.spec.n {
		c.edits[id] = &m
	} else {
		*c.extra[p-c.spec.n] = m
	}
	b.emit(api.EMessageUpdate, api.MessageEvent{Message: m})
	if c.row.LastMessage != nil && c.row.LastMessage.ID == id {
		c.row.LastMessage = api.Stub(&m)
		b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
	}
	return nil
}

func (b *Backend) MessagesReact(ctx context.Context, p api.ReactParams) error {
	return b.change(p.Chat, p.ID, func(m *api.Message) error {
		if m.Deleted {
			return fmt.Errorf("message was deleted: %w", api.ErrUnsupported)
		}
		rs := []api.Reaction{}
		for _, r := range m.Reactions {
			if r.Mine {
				r.Mine, r.Count = false, r.Count-1
			}
			if r.Emoji == p.Emoji {
				r.Mine, r.Count = true, r.Count+1
			}
			if r.Count > 0 {
				rs = append(rs, r)
			}
		}
		if p.Emoji != "" && !slices.ContainsFunc(rs, func(r api.Reaction) bool { return r.Mine }) {
			rs = append(rs, api.Reaction{Emoji: p.Emoji, Count: 1, Mine: true})
		}
		m.Reactions = rs
		return nil
	})
}

func (b *Backend) MessagesRevoke(ctx context.Context, p api.MessageRefParams) error {
	return b.change(p.Chat, p.ID, func(m *api.Message) error {
		if !m.FromMe {
			return fmt.Errorf("only your own messages can be deleted for everyone: %w", api.ErrUnsupported)
		}
		*m = api.Message{ID: m.ID, Chat: m.Chat, Sender: m.Sender, SenderName: m.SenderName, FromMe: true,
			Timestamp: m.Timestamp, Type: api.MsgRevoked, Status: m.Status, Deleted: true,
			Reactions: []api.Reaction{}, Mentions: []api.Mention{}}
		return nil
	})
}

func (b *Backend) MessagesDeleteForMe(ctx context.Context, p api.MessageRefParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return err
	}
	if _, ok := c.pos(p.ID); !ok || c.gone[p.ID] {
		return fmt.Errorf("message %s: %w", p.ID, api.ErrNotFound)
	}
	c.gone[p.ID] = true
	b.emit(api.EMessageRemove, api.MessageRemoveEvent{Chat: p.Chat, ID: p.ID})
	if c.row.LastMessage != nil && c.row.LastMessage.ID == p.ID {
		c.row.LastMessage = nil
		for i := c.count() - 1; i >= 0; i-- {
			if m := c.at(i); !c.gone[m.ID] {
				c.row.LastMessage = api.Stub(&m)
				break
			}
		}
		b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
	}
	return nil
}

func (b *Backend) MessagesSend(ctx context.Context, p api.SendParams) (*api.Message, error) {
	m := &api.Message{Type: api.MsgText, Text: p.Text, Mentions: []api.Mention{}}
	for _, j := range p.Mentions {
		if j != "" && !slices.ContainsFunc(m.Mentions, func(mn api.Mention) bool { return mn.JID == j }) {
			m.Mentions = append(m.Mentions, api.Mention{JID: j, Name: nameOf(j), Me: j == selfJID})
		}
	}
	return b.send(p.Chat, p.ReplyTo, m)
}

func (b *Backend) MessagesSendMedia(ctx context.Context, p api.SendMediaParams) (*api.Message, error) {
	m := &api.Message{Type: api.MsgDocument, Text: p.Caption}
	f, err := os.Open(p.Path)
	if err != nil {
		return nil, api.InvalidParams("%v", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, api.InvalidParams("%s is not a file", p.Path)
	}
	ext := strings.ToLower(filepath.Ext(p.Path))
	m.Media = &api.MediaInfo{Mime: cmp.Or(mime.TypeByExtension(ext), "application/octet-stream"),
		Size: st.Size(), FileName: filepath.Base(p.Path)}
	if cfg, format, err := image.DecodeConfig(f); err == nil {
		m.Type = api.MsgImage
		m.Media = &api.MediaInfo{Mime: "image/" + format, Size: st.Size(), Width: cfg.Width, Height: cfg.Height, HasThumb: true}
	}
	// Keep a copy: the caller may delete its file (e.g. a pasted image).
	dst := filepath.Join(b.dir, fmt.Sprintf("sent-%d%s", time.Now().UnixNano(), ext))
	if err := copyFile(f, dst); err != nil {
		return nil, err
	}
	m.Media.Path = dst
	return b.send(p.Chat, p.ReplyTo, m)
}

func copyFile(src *os.File, dst string) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// send stores m as our pending message, then walks it to read in about 2 s.
func (b *Backend) send(jid, replyTo string, m *api.Message) (*api.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(jid)
	if err != nil {
		return nil, err
	}
	if c.row.ReadOnly {
		return nil, fmt.Errorf("only admins can send to %s: %w", c.row.Name, api.ErrUnsupported)
	}
	if replyTo != "" {
		i, ok := c.pos(replyTo)
		if !ok {
			return nil, fmt.Errorf("reply to %s: %w", replyTo, api.ErrNotFound)
		}
		q := c.at(i)
		m.ReplyTo = quoteOf(&q)
	}
	m.FromMe, m.Sender, m.SenderName, m.Status = true, selfJID, selfName, api.StatusPending
	b.add(c, m)
	id := m.ID
	b.after(300*time.Millisecond, func() { b.setStatus(c, id, api.StatusSent) })
	b.after(900*time.Millisecond, func() { b.setStatus(c, id, api.StatusDelivered) })
	b.after(2*time.Second, func() { b.setStatus(c, id, api.StatusRead) })
	if c.row.Kind == api.ChatUser && !c.replying {
		c.replying = true
		b.after(2500*time.Millisecond, func() { b.typing(c, true) })
		b.after(5*time.Second, func() {
			c.replying = false
			b.typing(c, false)
			b.receive(c)
		})
	}
	out := *m
	return &out, nil
}

// add appends a new message to c and emits it with the updated chat.
func (b *Backend) add(c *chat, m *api.Message) {
	b.seq++
	m.ID = fmt.Sprintf("3EB1%08X%08X", b.seq, uint32(mix(uint64(time.Now().UnixNano()))))
	m.Chat = c.spec.jid
	m.Timestamp = max(time.Now().Unix(), c.row.LastActivity)
	if m.Reactions == nil {
		m.Reactions = []api.Reaction{}
	}
	if m.Mentions == nil {
		m.Mentions = []api.Mention{}
	}
	c.extraIdx[m.ID] = len(c.extra)
	c.extra = append(c.extra, m)
	c.row.LastMessage, c.row.LastActivity = api.Stub(m), m.Timestamp
	if !m.FromMe && !b.viewing(c) {
		c.row.Unread++
	}
	b.emit(api.EMessageNew, api.MessageEvent{Message: c.at(c.count() - 1)})
	b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
}

// viewing reports whether some client has c open in a focused window.
func (b *Backend) viewing(c *chat) bool {
	for _, a := range b.active {
		if a.Focused && a.Chat == c.spec.jid {
			return true
		}
	}
	return false
}

func (b *Backend) setStatus(c *chat, id, status string) {
	i, ok := c.extraIdx[id]
	if !ok {
		return
	}
	m := c.extra[i]
	m.Status = status
	b.emit(api.EMessageUpdate, api.MessageEvent{Message: c.at(c.spec.n + i)})
	if c.row.LastMessage != nil && c.row.LastMessage.ID == id {
		c.row.LastMessage = api.Stub(m) // replace, never mutate: copies of row share it
		b.emit(api.EChatUpdate, api.ChatEvent{Chat: c.row})
	}
}

func (b *Backend) typing(c *chat, on bool) {
	b.emit(api.ETyping, api.TypingEvent{Chat: c.spec.jid, JID: c.spec.jid, Name: c.row.Name, Typing: on, Kind: "text"})
}

// receive adds an incoming message from the other side of c.
func (b *Backend) receive(c *chat) {
	if b.auth != api.AuthPaired {
		return
	}
	r := rng(uint64(time.Now().UnixNano()), uint64(b.seq))
	m := &api.Message{Type: api.MsgText, Text: pick(r, replyBank), Sender: c.spec.jid, SenderName: c.row.Name}
	if c.spec.isGroup() {
		p := pick(r, c.spec.members)
		m.Sender, m.SenderName = personJID(p), personName(p)
	}
	b.add(c, m)
}

// incomingTraffic delivers a message to a recently active chat, then reschedules.
func (b *Backend) incomingTraffic() {
	cs := b.sorted()
	r := rng(uint64(time.Now().UnixNano()), 0x7a)
	c := cs[min(r.IntN(8), len(cs)-1)]
	if c.row.Kind == api.ChatUser && b.auth == api.AuthPaired {
		b.typing(c, true)
		b.after(2*time.Second, func() {
			b.typing(c, false)
			b.receive(c)
		})
	} else {
		b.receive(c)
	}
	b.after(b.traffic/2+time.Duration(r.Int64N(int64(b.traffic))), b.incomingTraffic)
}

func (b *Backend) Search(ctx context.Context, p api.SearchParams) (*api.SearchResultList, error) {
	q := strings.ToLower(strings.TrimSpace(p.Query))
	if q == "" {
		return nil, api.InvalidParams("query is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.auth != api.AuthPaired {
		return nil, api.ErrNotLoggedIn
	}
	// Newest matches per chat, then newest overall.
	res := &api.SearchResultList{Results: []api.SearchResult{}}
	for _, c := range b.chats {
		if p.Chat != "" && c.spec.jid != p.Chat {
			continue
		}
		for i, n := c.count()-1, 0; i >= 0 && n < p.Limit; i-- {
			m := c.at(i)
			if m.Type == api.MsgSystem || !strings.Contains(strings.ToLower(m.Text), q) {
				continue
			}
			n++
			res.Results = append(res.Results, api.SearchResult{Chat: m.Chat, ChatName: c.row.Name, ID: m.ID,
				Sender: m.Sender, SenderName: m.SenderName, FromMe: m.FromMe, Timestamp: m.Timestamp,
				Snippet: snippet(m.Text, q)})
		}
	}
	slices.SortFunc(res.Results, func(x, y api.SearchResult) int { return cmp.Compare(y.Timestamp, x.Timestamp) })
	res.Results = res.Results[:min(len(res.Results), p.Limit)]
	return res, nil
}

// snippet returns about 100 characters of text around the match of q.
func snippet(text, q string) string {
	text = strings.Join(strings.Fields(text), " ")
	at := 0
	if lower := strings.ToLower(text); len(lower) == len(text) { // same byte offsets
		at = utf8.RuneCountInString(text[:max(0, strings.Index(lower, q))])
	}
	r := []rune(text)
	start := max(0, at-40)
	end := min(len(r), start+100)
	s := string(r[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(r) {
		s += "…"
	}
	return s
}

func (b *Backend) PresenceSubscribe(ctx context.Context, p api.JIDParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.JID)
	if err != nil || c.row.Kind != api.ChatUser {
		return err
	}
	h := mix(c.spec.seed ^ 0x9e5)
	ev := api.PresenceEvent{JID: p.JID, Online: h%3 == 0}
	if !ev.Online {
		ev.LastSeen = time.Now().Unix() - int64(h%(2*day))
	}
	b.emit(api.EPresence, ev)
	return nil
}

func (b *Backend) GroupsMembers(ctx context.Context, p api.ChatParams) (*api.GroupMembersResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return nil, err
	}
	if !c.spec.isGroup() {
		return nil, api.InvalidParams("%s is not a group", p.Chat)
	}
	res := &api.GroupMembersResult{Members: make([]api.GroupMember, 0, len(c.spec.members))}
	for i, p := range c.spec.members {
		creator := i == 0 && !groupDefs[c.spec.group].owner
		res.Members = append(res.Members, api.GroupMember{JID: personJID(p), Name: personName(p), Admin: creator})
	}
	slices.SortFunc(res.Members, func(x, y api.GroupMember) int {
		return cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	return res, nil
}

func (b *Backend) UISetActive(ctx context.Context, p api.UISetActiveParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.active[api.ClientIDFrom(ctx)] = p
	if c, ok := b.chats[p.Chat]; ok && p.Focused && b.auth == api.AuthPaired {
		b.markRead(c)
	}
	return nil
}

func (b *Backend) SettingsGet(ctx context.Context) (*api.SettingsGetResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := make(map[string]any, len(b.settings))
	for k, v := range b.settings {
		s[k] = v
	}
	return &api.SettingsGetResult{Settings: s}, nil
}

func (b *Backend) SettingsSet(ctx context.Context, p api.SettingsSetParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch p.Key {
	case api.SettingNotifications, api.SettingNotifyPreview, api.SettingSendReceipts:
		if _, ok := p.Value.(bool); !ok {
			return api.InvalidParams("%s must be a bool", p.Key)
		}
		b.settings[p.Key] = p.Value
	case api.SettingMediaCacheMB:
		v, ok := p.Value.(float64)
		if !ok || v < 1 {
			return api.InvalidParams("%s must be a positive number", p.Key)
		}
		b.settings[p.Key] = int(v)
	default:
		return api.InvalidParams("unknown setting %q", p.Key)
	}
	return nil
}
