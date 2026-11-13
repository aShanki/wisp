package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"wisp/daemon/internal/version"
)

// Agent access: what a local agent may do through `wispctl mcp`. The owner
// picks the level in Settings; an agent connection can never change it.
const (
	SettingAgentAccess = "agent.access" // AgentOff (default), AgentRead or AgentSend

	AgentOff  = "off"
	AgentRead = "read" // chats, messages, search and media
	AgentSend = "send" // AgentRead, plus sending, reacting and marking read
)

// MAgentHello turns the connection into an agent connection for good: it
// receives no events and may only call agentMethods, as far as
// SettingAgentAccess lets it.
const MAgentHello = "agent.hello"

type AgentHelloResult struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	Access   string `json:"access"`
	Auth     string `json:"auth"`
	Conn     string `json:"connection"`
	SelfJID  string `json:"selfJid"`
	SelfName string `json:"selfName"`
}

var (
	ErrAgentAccess = errors.New("agent access")
	ErrAgentRate   = errors.New("agents are sending too fast")
)

// ValidAgentAccess reports whether v is a value of SettingAgentAccess.
func ValidAgentAccess(v any) bool {
	return v == AgentOff || v == AgentRead || v == AgentSend
}

// agentMethods is everything an agent connection may call, with the access
// it needs. Pairing, logout, settings and shutdown are not here.
var agentMethods = map[string]string{
	MAgentHello: AgentOff,

	MChatsList:         AgentRead,
	MChatsGet:          AgentRead,
	MMessagesList:      AgentRead,
	MMessagesReactions: AgentRead,
	MSearch:            AgentRead,
	MGroupsMembers:     AgentRead,
	MMediaFetch:        AgentRead,

	MMessagesSend:      AgentSend,
	MMessagesSendMedia: AgentSend,
	MMessagesReact:     AgentSend,
	MChatsMarkRead:     AgentSend,
}

// AgentSends reports whether method puts something into a chat for others to see.
func AgentSends(method string) bool {
	return method == MMessagesSend || method == MMessagesSendMedia || method == MMessagesReact
}

func accessRank(a string) int {
	switch a {
	case AgentRead:
		return 1
	case AgentSend:
		return 2
	}
	return 0
}

const (
	agentSendLimit  = 20 // sends per agentSendWindow, over all agent connections
	agentSendWindow = time.Minute
)

// AgentGate dispatches the calls of agent connections.
type AgentGate struct {
	mu    sync.Mutex
	sends []time.Time // within agentSendWindow, oldest first
	now   func() time.Time
}

func NewAgentGate() *AgentGate { return &AgentGate{now: time.Now} }

// Dispatch is Dispatch for an agent connection: it refuses what the owner
// has not allowed, sends only to chats that already exist, and caps the rate.
func (g *AgentGate) Dispatch(ctx context.Context, b Backend, method string, raw json.RawMessage) (any, error) {
	need, ok := agentMethods[method]
	if !ok {
		return nil, fmt.Errorf("%w: %s is not available to agents", ErrMethodNotFound, method)
	}
	access := AgentOff
	if s, err := b.SettingsGet(ctx); err != nil {
		return nil, err
	} else if v, _ := s.Settings[SettingAgentAccess].(string); ValidAgentAccess(v) {
		access = v
	}
	if method == MAgentHello {
		st, err := b.AuthStatus(ctx)
		if err != nil {
			return nil, err
		}
		return &AgentHelloResult{Protocol: ProtocolVersion, Version: version.Version, Access: access,
			Auth: st.State, Conn: st.Conn, SelfJID: st.SelfJID, SelfName: st.SelfName}, nil
	}
	if accessRank(access) < accessRank(need) {
		if access == AgentOff {
			return nil, fmt.Errorf("%w is off", ErrAgentAccess)
		}
		return nil, fmt.Errorf("%w is read-only", ErrAgentAccess)
	}
	if AgentSends(method) {
		p, err := decode[ChatParams](raw)
		if err != nil {
			return nil, err
		}
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		chat, err := b.ChatsGet(ctx, p)
		if err != nil {
			return nil, err
		}
		if chat.ReadOnly {
			return nil, fmt.Errorf("%w: only admins can send to %s", ErrUnsupported, chat.Name)
		}
		if err := g.reserve(); err != nil {
			return nil, err
		}
	}
	return Dispatch(ctx, b, method, raw)
}

func (g *AgentGate) reserve() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for len(g.sends) > 0 && now.Sub(g.sends[0]) >= agentSendWindow {
		g.sends = g.sends[1:]
	}
	if len(g.sends) >= agentSendLimit {
		wait := agentSendWindow - now.Sub(g.sends[0])
		return fmt.Errorf("%w: at most %d a minute, try again in %d s", ErrAgentRate, agentSendLimit, int(wait.Seconds())+1)
	}
	g.sends = append(g.sends, now)
	return nil
}
