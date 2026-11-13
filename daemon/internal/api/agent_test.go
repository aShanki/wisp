package api

import (
	"errors"
	"testing"
	"time"
)

func TestAgentMethodsLeaveOutAccountControl(t *testing.T) {
	for _, m := range []string{MDaemonHello, MDaemonShutdown, MAuthStartQR, MAuthPairPhone, MAuthLogout,
		MSettingsGet, MSettingsSet, MUISetActive, MMessagesRevoke, MMessagesDeleteForMe, MMessagesEdit, MChatsSetPinned, MChatsSetMuted} {
		if _, ok := agentMethods[m]; ok {
			t.Errorf("agents may call %s", m)
		}
	}
	for m, need := range agentMethods {
		if _, ok := handlers[m]; !ok && m != MAgentHello {
			t.Errorf("%s has no handler", m)
		}
		if AgentSends(m) && need != AgentSend {
			t.Errorf("%s sends but needs only %q", m, need)
		}
	}
}

func TestAgentSendRate(t *testing.T) {
	now := time.Unix(1000, 0)
	g := &AgentGate{now: func() time.Time { return now }}
	for i := range agentSendLimit {
		if err := g.reserve(); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		now = now.Add(time.Second)
	}
	if err := g.reserve(); !errors.Is(err, ErrAgentRate) || ErrorCode(err) != CodeAgentRate {
		t.Fatalf("over the limit: %v", err)
	}
	now = now.Add(agentSendWindow - agentSendLimit*time.Second + time.Second) // the first send is a minute old
	if err := g.reserve(); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}
