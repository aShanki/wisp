package rpc

import (
	"testing"
	"time"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/mock"
)

func agentClient(t *testing.T, sock string) (*testClient, api.AgentHelloResult) {
	t.Helper()
	c := dial(t, sock)
	var h api.AgentHelloResult
	c.must(api.MAgentHello, map[string]string{"client": "test"}, &h)
	return c, h
}

func setAccess(t *testing.T, ui *testClient, access string) {
	t.Helper()
	ui.must(api.MSettingsSet, api.SettingsSetParams{Key: api.SettingAgentAccess, Value: access}, nil)
}

func TestAgentAccessLevels(t *testing.T) {
	sock := start(t, mock.Config{Seed: 1})
	ui := dial(t, sock)
	ui.must(api.MDaemonHello, api.HelloParams{Client: "test"}, nil)

	agent, h := agentClient(t, sock)
	if h.Access != api.AgentOff || h.Auth != api.AuthPaired || h.SelfJID == "" {
		t.Fatalf("hello: %+v", h)
	}
	if err := agent.call(api.MChatsList, api.ChatsListParams{}, nil); err == nil || err.Code != api.CodeAgentAccess {
		t.Fatalf("read while off: %v", err)
	}

	setAccess(t, ui, api.AgentRead)
	var chats api.ChatsListResult
	agent.must(api.MChatsList, api.ChatsListParams{Limit: 5}, &chats)
	if len(chats.Chats) != 5 {
		t.Fatalf("chats: %d", len(chats.Chats))
	}
	send := api.SendParams{Chat: bigChat, Text: "hi"}
	if err := agent.call(api.MMessagesSend, send, nil); err == nil || err.Code != api.CodeAgentAccess {
		t.Fatalf("send while read-only: %v", err)
	}
	if err := agent.call(api.MChatsMarkRead, api.ChatParams{Chat: bigChat}, nil); err == nil || err.Code != api.CodeAgentAccess {
		t.Fatalf("mark read while read-only: %v", err)
	}

	setAccess(t, ui, api.AgentSend)
	var res api.MessageResult
	agent.must(api.MMessagesSend, send, &res)
	if !res.Message.FromMe || res.Message.Text != "hi" {
		t.Fatalf("sent: %+v", res.Message)
	}
	if err := agent.call(api.MMessagesSend, api.SendParams{Chat: "61499999999@s.whatsapp.net", Text: "hi"}, nil); err == nil || err.Code != api.CodeNotFound {
		t.Fatalf("send to a new chat: %v", err)
	}
	if err := ui.call(api.MSettingsSet, api.SettingsSetParams{Key: api.SettingAgentAccess, Value: "all"}, nil); err == nil || err.Code != api.CodeInvalidParams {
		t.Fatalf("bad access value: %v", err)
	}
}

func TestAgentScope(t *testing.T) {
	sock := start(t, mock.Config{Seed: 1})
	ui := dial(t, sock)
	setAccess(t, ui, api.AgentSend)
	agent, _ := agentClient(t, sock)
	for _, m := range []string{api.MSettingsSet, api.MSettingsGet, api.MDaemonShutdown, api.MAuthLogout, api.MAuthStartQR,
		api.MDaemonHello, api.MMessagesRevoke, api.MUISetActive} {
		params := map[string]any{"key": api.SettingAgentAccess, "value": api.AgentSend, "chat": bigChat, "id": "x"}
		if err := agent.call(m, params, nil); err == nil || err.Code != api.CodeMethodNotFound {
			t.Errorf("%s: %v", m, err)
		}
	}
	ui.must(api.MAuthStatus, nil, nil) // still up
}

func TestAgentGetsNoEvents(t *testing.T) {
	sock := start(t, mock.Config{Seed: 1})
	ui := dial(t, sock)
	ui.must(api.MDaemonHello, api.HelloParams{Client: "test"}, nil)
	setAccess(t, ui, api.AgentSend)
	agent, _ := agentClient(t, sock)
	agent.must(api.MMessagesSend, api.SendParams{Chat: bigChat, Text: "hi"}, nil)
	ui.waitEvent(api.EMessageNew, 2*time.Second)
	select {
	case e := <-agent.events:
		t.Fatalf("agent got event %s", e.Method)
	case <-time.After(300 * time.Millisecond):
	}
}
