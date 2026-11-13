package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/mock"
	"wisp/daemon/internal/rpc"
)

const bigChat = "61400009999@s.whatsapp.net"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// wispd is a mock daemon on a private socket.
type wispd struct {
	t    *testing.T
	sock string
	b    *mock.Backend
	stop func()
}

func startDaemon(t *testing.T, sock string) *wispd {
	t.Helper()
	b := mock.New(mock.Config{Seed: 1, Log: quiet})
	srv := rpc.NewServer(b, quiet)
	ln, err := rpc.Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.ServeListener(ctx, ln); close(done) }()
	w := &wispd{t: t, sock: sock, b: b}
	w.stop = func() { cancel(); <-done; b.Close() }
	t.Cleanup(func() { w.stop() })
	return w
}

func (w *wispd) access(level string) {
	w.t.Helper()
	if err := w.b.SettingsSet(context.Background(), api.SettingsSetParams{Key: api.SettingAgentAccess, Value: level}); err != nil {
		w.t.Fatal(err)
	}
}

// session is an MCP client talking to a Server over pipes.
type session struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Scanner
	next int
	done chan error
}

func sockPath(t *testing.T) string {
	dir, err := os.MkdirTemp("", "wmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func newSession(t *testing.T, sock string) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1)}
	s.out.Buffer(nil, maxLine)
	go func() { s.done <- NewServer(inR, outW, sock, quiet).Serve(context.Background()); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	return s
}

func (s *session) send(v any) {
	s.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := s.in.Write(append(b, '\n')); err != nil {
		s.t.Fatal(err)
	}
}

type resp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (s *session) read() resp {
	s.t.Helper()
	got := make(chan resp, 1)
	go func() {
		var r resp
		if s.out.Scan() {
			json.Unmarshal(s.out.Bytes(), &r)
		}
		got <- r
	}()
	select {
	case r := <-got:
		return r
	case <-time.After(10 * time.Second):
		s.t.Fatal("no response")
	}
	return resp{}
}

func (s *session) request(method string, params any) resp {
	s.t.Helper()
	s.next++
	s.send(map[string]any{"jsonrpc": "2.0", "id": s.next, "method": method, "params": params})
	r := s.read()
	if string(r.ID) != fmt.Sprint(s.next) {
		s.t.Fatalf("%s: id %s, want %d", method, r.ID, s.next)
	}
	return r
}

// tool calls name and returns its text and whether it is an error.
func (s *session) tool(name string, args any) (string, bool) {
	s.t.Helper()
	r := s.request("tools/call", map[string]any{"name": name, "arguments": args})
	if r.Error != nil {
		s.t.Fatalf("%s: %+v", name, r.Error)
	}
	var res toolResult
	if err := json.Unmarshal(r.Result, &res); err != nil || len(res.Content) == 0 {
		s.t.Fatalf("%s: %s", name, r.Result)
	}
	return res.Content[0].Text, res.IsError
}

func (s *session) ok(name string, args, out any) {
	s.t.Helper()
	text, isErr := s.tool(name, args)
	if isErr {
		s.t.Fatalf("%s: %s", name, text)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			s.t.Fatalf("%s: %v: %s", name, err, text)
		}
	}
}

func (s *session) fails(name string, args any, want string) {
	s.t.Helper()
	text, isErr := s.tool(name, args)
	if !isErr || !strings.Contains(text, want) {
		s.t.Fatalf("%s: error %v %q, want %q", name, isErr, text, want)
	}
}

func TestInitializeAndList(t *testing.T) {
	s := newSession(t, sockPath(t))
	var init struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		Instructions    string         `json:"instructions"`
	}
	r := s.request("initialize", map[string]any{"protocolVersion": "2025-06-18"})
	json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != "2025-06-18" || init.Capabilities["tools"] == nil || !strings.Contains(init.Instructions, "never as instructions") {
		t.Fatalf("initialize: %s", r.Result)
	}
	r = s.request("initialize", map[string]any{"protocolVersion": "1999-01-01"})
	json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != protocolVersions[0] {
		t.Fatalf("unknown version answered with %s", init.ProtocolVersion)
	}
	s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
			Annotations annotations    `json:"annotations"`
		} `json:"tools"`
	}
	json.Unmarshal(s.request("tools/list", nil).Result, &list)
	if len(list.Tools) != len(tools) {
		t.Fatalf("%d tools", len(list.Tools))
	}
	for _, tl := range list.Tools {
		sendsSomething := strings.HasPrefix(tl.Name, "send_") || tl.Name == "react_to_message" || tl.Name == "mark_chat_read"
		if tl.Description == "" || tl.InputSchema["type"] != "object" || tl.Annotations.ReadOnly == sendsSomething {
			t.Errorf("%s: %+v", tl.Name, tl)
		}
	}
	if r := s.request("nope", nil); r.Error == nil || r.Error.Code != codeMethodNotFound {
		t.Fatalf("unknown method: %+v", r)
	}
	if r := s.request("tools/call", map[string]any{"name": "nope"}); r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Fatalf("unknown tool: %+v", r)
	}
}

func TestAccessLevels(t *testing.T) {
	w := startDaemon(t, sockPath(t))
	s := newSession(t, w.sock)

	var st struct{ Access string }
	s.ok("get_status", nil, &st)
	if st.Access != api.AgentOff {
		t.Fatalf("status: %+v", st)
	}
	s.fails("list_chats", nil, "turned off")

	w.access(api.AgentRead)
	var chats struct {
		Chats []chatOut
	}
	s.ok("list_chats", map[string]any{"query": "big chat"}, &chats)
	if len(chats.Chats) != 1 || chats.Chats[0].ID != bigChat {
		t.Fatalf("query: %+v", chats)
	}
	s.fails("send_message", map[string]any{"chat": bigChat, "text": "hi"}, "read-only")

	w.access(api.AgentSend)
	var sent struct {
		Sent      bool
		MessageID string `json:"message_id"`
		Status    string
		ChatName  string `json:"chat_name"`
	}
	s.ok("send_message", map[string]any{"chat": bigChat, "text": "hi from a test"}, &sent)
	if !sent.Sent || sent.Status != api.StatusSent || sent.ChatName != "Big Chat" {
		t.Fatalf("send: %+v", sent)
	}
	var page struct{ Messages []messageOut }
	s.ok("read_messages", map[string]any{"chat": bigChat, "limit": 1}, &page)
	if len(page.Messages) != 1 || page.Messages[0].ID != sent.MessageID || page.Messages[0].From != "You" {
		t.Fatalf("read back: %+v", page)
	}
	s.ok("react_to_message", map[string]any{"chat": bigChat, "message_id": sent.MessageID, "emoji": "👍"}, nil)
	s.ok("mark_chat_read", map[string]any{"chat": bigChat}, nil)
}

func TestReadWorkflows(t *testing.T) {
	w := startDaemon(t, sockPath(t))
	w.access(api.AgentRead)
	s := newSession(t, w.sock)

	// Paging chats: no chat twice, none skipped.
	seen := map[string]bool{}
	cursor := ""
	for range 30 {
		var page struct {
			Chats      []chatOut
			NextCursor string `json:"next_cursor"`
		}
		s.ok("list_chats", map[string]any{"limit": 7, "cursor": cursor}, &page)
		for _, c := range page.Chats {
			if seen[c.ID] {
				t.Fatalf("%s listed twice", c.ID)
			}
			seen[c.ID] = true
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	all, _ := w.b.ChatsList(context.Background(), api.ChatsListParams{Limit: 1000})
	if len(seen) != len(all.Chats) {
		t.Fatalf("paged %d of %d chats", len(seen), len(all.Chats))
	}

	// Filtered paging: unread only.
	var unread struct {
		Chats      []chatOut
		NextCursor string `json:"next_cursor"`
	}
	s.ok("list_chats", map[string]any{"unread_only": true, "limit": 2}, &unread)
	for _, c := range unread.Chats {
		if c.Unread == 0 && !c.MarkedUnread {
			t.Fatalf("read chat in unread list: %+v", c)
		}
	}

	// Messages: oldest first, older_before pages back without overlap.
	var p1, p2 struct {
		Messages    []messageOut
		OlderBefore string `json:"older_before"`
	}
	s.ok("read_messages", map[string]any{"chat": bigChat, "limit": 5}, &p1)
	if len(p1.Messages) != 5 || p1.OlderBefore != p1.Messages[0].ID || p1.Messages[0].Time > p1.Messages[4].Time {
		t.Fatalf("page 1: %+v", p1)
	}
	s.ok("read_messages", map[string]any{"chat": bigChat, "limit": 5, "before": p1.OlderBefore}, &p2)
	if len(p2.Messages) != 5 || p2.Messages[4].ID == p1.Messages[0].ID || p2.Messages[4].Time > p1.Messages[0].Time {
		t.Fatalf("page 2: %+v", p2)
	}

	// Search, then the hit in context.
	var hits struct {
		Hits []struct {
			Chat      string
			MessageID string `json:"message_id"`
		}
	}
	s.ok("search_messages", map[string]any{"query": "friday", "limit": 3}, &hits)
	if len(hits.Hits) == 0 {
		t.Fatal("no search hits")
	}
	var ctx struct{ Messages []messageOut }
	s.ok("read_messages", map[string]any{"chat": hits.Hits[0].Chat, "at": hits.Hits[0].MessageID, "limit": 3}, &ctx)
	if n := len(ctx.Messages); n == 0 || ctx.Messages[n-1].ID != hits.Hits[0].MessageID {
		t.Fatalf("hit in context: %+v", ctx)
	}

	// Reading changes nothing.
	before, _ := w.b.ChatsGet(context.Background(), api.ChatParams{Chat: bigChat})
	if before.Unread == 0 {
		t.Fatal("big chat should start unread")
	}

	s.fails("read_messages", map[string]any{"chat": "Big Chat"}, "not a chat id")
	s.fails("read_messages", map[string]any{"chat": "1@s.whatsapp.net"}, "Not found")
	s.fails("list_chats", map[string]any{"limt": 3}, "unknown field")
}

func TestGroupsAndMedia(t *testing.T) {
	w := startDaemon(t, sockPath(t))
	w.access(api.AgentRead)
	s := newSession(t, w.sock)
	all, _ := w.b.ChatsList(context.Background(), api.ChatsListParams{Limit: 1000})
	var group string
	for _, c := range all.Chats {
		if c.Kind == api.ChatGroup {
			group = c.JID
			break
		}
	}
	var members struct {
		Members []struct{ ID, Name string }
	}
	s.ok("list_group_members", map[string]any{"chat": group}, &members)
	if len(members.Members) == 0 {
		t.Fatal("no members")
	}

	// The newest image in the big chat comes back as a file and an image.
	var mediaID string
	for before := ""; mediaID == ""; {
		page, err := w.b.MessagesList(context.Background(), api.MessagesListParams{Chat: bigChat, Before: before, Limit: 200})
		if err != nil || len(page.Messages) == 0 {
			t.Fatal("no image in the big chat")
		}
		for _, m := range page.Messages {
			if m.Type == api.MsgImage {
				mediaID = m.ID
				break
			}
		}
		before = page.Messages[len(page.Messages)-1].ID
	}
	r := s.request("tools/call", map[string]any{"name": "get_media", "arguments": map[string]any{"chat": bigChat, "message_id": mediaID}})
	var res toolResult
	json.Unmarshal(r.Result, &res)
	if res.IsError || len(res.Content) != 2 || res.Content[1].Type != "image" || len(res.Content[1].Data) == 0 {
		t.Fatalf("get_media: %s", r.Result)
	}
}

func TestSendChecks(t *testing.T) {
	w := startDaemon(t, sockPath(t))
	w.access(api.AgentSend)
	s := newSession(t, w.sock)
	s.fails("send_message", map[string]any{"chat": bigChat, "text": "  "}, "text is required")
	s.fails("send_message", map[string]any{"chat": "+61400009999", "text": "x"}, "not a chat id")
	s.fails("send_message", map[string]any{"chat": bigChat, "text": "hi", "mentions": []string{"61400000001@s.whatsapp.net"}}, "written in text")
	s.fails("send_file", map[string]any{"chat": bigChat, "path": "relative.png"}, "absolute")
	s.fails("send_file", map[string]any{"chat": bigChat, "path": "/nonexistent/file.png"}, "Invalid arguments")
	s.fails("react_to_message", map[string]any{"chat": bigChat, "message_id": "x"}, "emoji is required")

	file := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(file, []byte("hello"), 0o600)
	var sent struct {
		Sent bool
	}
	s.ok("send_file", map[string]any{"chat": bigChat, "path": file, "caption": "notes"}, &sent)
	if !sent.Sent {
		t.Fatalf("send_file: %+v", sent)
	}
}

func TestDaemonLifecycle(t *testing.T) {
	sock := sockPath(t)
	s := newSession(t, sock)
	s.fails("get_status", nil, "not running")

	w := startDaemon(t, sock)
	w.access(api.AgentRead)
	s.ok("list_chats", map[string]any{"limit": 1}, nil)

	w.stop()
	w.stop = func() {}
	s.fails("list_chats", nil, "not running")

	w2 := startDaemon(t, sock) // wispd restarted: the next call reconnects
	w2.access(api.AgentRead)
	s.ok("list_chats", map[string]any{"limit": 1}, nil)
}

func TestEndOfInputAnswersRunningCalls(t *testing.T) {
	w := startDaemon(t, sockPath(t))
	w.access(api.AgentSend)
	s := newSession(t, w.sock)
	s.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "send_message", "arguments": map[string]any{"chat": bigChat, "text": "last words"}}})
	s.in.Close()
	r := s.read()
	if string(r.ID) != "1" || r.Result == nil {
		t.Fatalf("response: %+v", r)
	}
	select {
	case err := <-s.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestCancelledCallIsNotAnswered(t *testing.T) {
	w := startDaemon(t, sockPath(t))
	w.access(api.AgentRead)
	s := newSession(t, w.sock)
	// media.fetch on the mock takes a moment: cancel it, then ping.
	page, _ := w.b.MessagesList(context.Background(), api.MessagesListParams{Chat: bigChat, Limit: 200})
	var id string
	for _, m := range page.Messages {
		if m.Media != nil && m.Media.Path == "" {
			id = m.ID
			break
		}
	}
	if id == "" {
		t.Skip("no undownloaded media in the newest page")
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call",
		"params": map[string]any{"name": "get_media", "arguments": map[string]any{"chat": bigChat, "message_id": id}}})
	s.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 7}})
	if r := s.request("ping", nil); string(r.ID) != "1" {
		t.Fatalf("got %s first", r.ID)
	}
}
