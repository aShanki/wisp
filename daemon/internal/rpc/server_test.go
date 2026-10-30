package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/mock"
)

const bigChat = "61400009999@s.whatsapp.net"

type testClient struct {
	t      *testing.T
	conn   net.Conn
	next   int
	resps  chan reply
	events chan reply
}

type reply struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func tempSock(t *testing.T, name string) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), name)
	if len(sock) >= 100 {
		if d, err := os.MkdirTemp("", "wisp-*"); err == nil {
			t.Cleanup(func() { _ = os.RemoveAll(d) })
			sock = filepath.Join(d, name)
		}
	}
	return sock
}

func start(t *testing.T, cfg mock.Config) string {
	t.Helper()
	sock := tempSock(t, "wisp.sock")
	b := mock.New(cfg)
	srv := NewServer(b, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { done <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		b.Close()
	})
	return sock
}

func dial(t *testing.T, sock string) *testClient {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &testClient{t: t, conn: conn, resps: make(chan reply, 16), events: make(chan reply, 256)}
	go func() {
		sc := bufio.NewScanner(conn)
		sc.Buffer(nil, maxLine)
		for sc.Scan() {
			var r reply
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				t.Errorf("bad line %q: %v", sc.Bytes(), err)
				return
			}
			if r.Method != "" {
				c.events <- r
			} else {
				c.resps <- r
			}
		}
	}()
	return c
}

func (c *testClient) raw(line string) reply {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		c.t.Fatal(err)
	}
	select {
	case r := <-c.resps:
		return r
	case <-time.After(5 * time.Second):
		c.t.Fatalf("no response to %s", line)
	}
	return reply{}
}

// call returns the result of method, or its error.
func (c *testClient) call(method string, params any, out any) *Error {
	c.t.Helper()
	c.next++
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params})
	r := c.raw(string(req))
	if r.ID == nil || *r.ID != c.next {
		c.t.Fatalf("%s: response id %v, want %d", method, r.ID, c.next)
	}
	if r.Error != nil {
		return r.Error
	}
	if out != nil {
		if err := json.Unmarshal(r.Result, out); err != nil {
			c.t.Fatalf("%s: %v", method, err)
		}
	}
	return nil
}

func (c *testClient) must(method string, params any, out any) {
	c.t.Helper()
	if err := c.call(method, params, out); err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
}

// waitEvent returns the next event named name, skipping others.
func (c *testClient) waitEvent(name string, d time.Duration) json.RawMessage {
	c.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case e := <-c.events:
			if e.Method == name {
				return e.Params
			}
		case <-deadline:
			c.t.Fatalf("no %s event within %s", name, d)
		}
	}
}

func hello(c *testClient) api.HelloResult {
	var h api.HelloResult
	c.must(api.MDaemonHello, api.HelloParams{Client: "test"}, &h)
	return h
}

func TestHelloAndEvents(t *testing.T) {
	sock := start(t, mock.Config{Seed: 1})
	quiet, c := dial(t, sock), dial(t, sock)

	h := hello(c)
	if h.Protocol != api.ProtocolVersion || !h.Mock || h.Auth != api.AuthPaired || h.SelfJID == "" {
		t.Fatalf("hello = %+v", h)
	}
	quiet.must(api.MChatsMarkRead, api.ChatParams{Chat: bigChat}, nil)
	c.waitEvent(api.EChatUpdate, time.Second)
	select {
	case e := <-quiet.events:
		t.Fatalf("client without hello got event %s", e.Method)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPageBigChat(t *testing.T) {
	c := dial(t, start(t, mock.Config{Seed: 1}))
	hello(c)
	seen := map[string]bool{}
	before, last := "", int64(1<<62)
	for {
		var page api.MessagesListResult
		c.must(api.MMessagesList, api.MessagesListParams{Chat: bigChat, Before: before, Limit: 200}, &page)
		for _, m := range page.Messages {
			if seen[m.ID] {
				t.Fatalf("duplicate %s", m.ID)
			}
			if m.Timestamp >= last {
				t.Fatalf("%s not older than the previous message", m.ID)
			}
			if m.Reactions == nil || m.Mentions == nil {
				t.Fatalf("%s: null reactions or mentions", m.ID)
			}
			seen[m.ID], last = true, m.Timestamp
		}
		if !page.HasMore {
			break
		}
		before = page.Messages[len(page.Messages)-1].ID
	}
	if len(seen) != 10000 {
		t.Fatalf("paged %d messages, want 10000", len(seen))
	}
}

func TestSendFlow(t *testing.T) {
	sock := start(t, mock.Config{Seed: 1})
	c, reader := dial(t, sock), dial(t, sock)
	hello(c)
	stop, stopped := make(chan struct{}), make(chan struct{})
	defer func() { close(stop); <-stopped }()
	go func() { // concurrent reads while statuses change (for -race)
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				reader.call(api.MChatsList, nil, nil)
			}
		}
	}()
	var sent api.MessageResult
	c.must(api.MMessagesSend, api.SendParams{Chat: bigChat, Text: "hi"}, &sent)
	if sent.Message.Status != api.StatusPending || !sent.Message.FromMe {
		t.Fatalf("sent = %+v", sent.Message)
	}
	var ev api.MessageEvent
	json.Unmarshal(c.waitEvent(api.EMessageNew, time.Second), &ev)
	if ev.Message.ID != sent.Message.ID {
		t.Fatalf("message.new for %s, want %s", ev.Message.ID, sent.Message.ID)
	}
	for _, want := range []string{api.StatusSent, api.StatusDelivered, api.StatusRead} {
		json.Unmarshal(c.waitEvent(api.EMessageUpdate, 3*time.Second), &ev)
		if ev.Message.Status != want {
			t.Fatalf("status %q, want %q", ev.Message.Status, want)
		}
	}
	var typing api.TypingEvent
	json.Unmarshal(c.waitEvent(api.ETyping, 2*time.Second), &typing)
	if !typing.Typing || typing.Chat != bigChat {
		t.Fatalf("typing = %+v", typing)
	}
	json.Unmarshal(c.waitEvent(api.EMessageNew, 4*time.Second), &ev)
	if ev.Message.FromMe || ev.Message.Chat != bigChat {
		t.Fatalf("reply = %+v", ev.Message)
	}
	var page api.MessagesListResult
	c.must(api.MMessagesList, api.MessagesListParams{Chat: bigChat, Limit: 2}, &page)
	if page.Messages[0].ID != ev.Message.ID || page.Messages[1].ID != sent.Message.ID {
		t.Fatalf("newest page = %s, %s", page.Messages[0].ID, page.Messages[1].ID)
	}
}

func TestGroupMembers(t *testing.T) {
	c := dial(t, start(t, mock.Config{Seed: 1}))
	self := hello(c).SelfJID
	const family = "120363000000000002@g.us"
	var res api.GroupMembersResult
	c.must(api.MGroupsMembers, api.ChatParams{Chat: family}, &res)
	var names []string
	for _, m := range res.Members {
		if m.JID == self {
			t.Fatal("members include us")
		}
		names = append(names, m.Name)
	}
	if want := []string{"Charlotte Wright", "Dad", "Grandma 💐", "Mum", "Noah Williams"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("members %v, want %v", names, want)
	}
	if !res.Members[3].Admin || res.Members[0].Admin {
		t.Errorf("admins: %+v", res.Members)
	}

	// The picked member, mentioned in a reply, comes back named.
	var page api.MessagesListResult
	c.must(api.MMessagesList, api.MessagesListParams{Chat: family, Limit: 1}, &page)
	mum := res.Members[3]
	var sent api.MessageResult
	c.must(api.MMessagesSend, api.SendParams{Chat: family, Text: "@61400000003 dinner?", ReplyTo: page.Messages[0].ID,
		Mentions: []string{mum.JID}}, &sent)
	if !reflect.DeepEqual(sent.Message.Mentions, []api.Mention{{JID: mum.JID, Name: "Mum"}}) || sent.Message.ReplyTo == nil {
		t.Fatalf("sent %+v", sent.Message)
	}
	var chat api.ChatEvent
	json.Unmarshal(c.waitEvent(api.EChatUpdate, time.Second), &chat)
	if chat.Chat.LastMessage.Preview != "@Mum dinner?" {
		t.Errorf("preview %q", chat.Chat.LastMessage.Preview)
	}

	for _, p := range []any{api.ChatParams{Chat: bigChat}, nil} {
		if err := c.call(api.MGroupsMembers, p, nil); err == nil || err.Code != api.CodeInvalidParams {
			t.Errorf("groups.members %v: %v", p, err)
		}
	}
}

func TestErrors(t *testing.T) {
	c := dial(t, start(t, mock.Config{Seed: 1}))
	for _, tc := range []struct {
		line string
		code int
	}{
		{`{bad`, api.CodeParseError},
		{`{"jsonrpc":"2.0","id":1}`, api.CodeInvalidRequest},
		{`[{"jsonrpc":"2.0","id":1,"method":"daemon.hello"}]`, api.CodeInvalidRequest},
		{`{"jsonrpc":"2.0","id":1,"method":"nope"}`, api.CodeMethodNotFound},
		{`{"jsonrpc":"2.0","id":1,"method":"messages.list","params":{"chat":5}}`, api.CodeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"messages.list","params":{}}`, api.CodeInvalidParams},
		{`{"jsonrpc":"2.0","id":1,"method":"chats.get","params":{"chat":"x@s.whatsapp.net"}}`, api.CodeNotFound},
	} {
		r := c.raw(tc.line)
		if r.Error == nil || r.Error.Code != tc.code {
			t.Errorf("%s: error %+v, want code %d", tc.line, r.Error, tc.code)
		}
	}
}

func TestUnpaired(t *testing.T) {
	c := dial(t, start(t, mock.Config{Seed: 1, Unpaired: true}))
	if h := hello(c); h.Auth != api.AuthUnpaired {
		t.Fatalf("auth = %s", h.Auth)
	}
	if err := c.call(api.MChatsList, nil, nil); err == nil || err.Code != api.CodeNotLoggedIn {
		t.Fatalf("chats.list unpaired: %v", err)
	}
	c.must(api.MAuthStartQR, nil, nil)
	var qr api.QREvent
	json.Unmarshal(c.waitEvent(api.EAuthQR, time.Second), &qr)
	if qr.Code == "" || qr.Timeout != 4 {
		t.Fatalf("qr = %+v", qr)
	}
}

func TestSingleInstance(t *testing.T) {
	sock := start(t, mock.Config{Seed: 1})
	if _, err := Listen(sock); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Listen: %v", err)
	}
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", fi.Mode(), err)
	}

	stale := tempSock(t, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	ln2, err := Listen(stale)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	ln2.Close()
}
