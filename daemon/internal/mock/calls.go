package mock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"wisp/daemon/internal/api"
)

// How long a mock call rings before it is missed (or unanswered), how long an
// outgoing one rings before the other side picks up, and how long connecting takes.
const (
	ringFor    = 30 * time.Second
	pickUpIn   = 4 * time.Second
	connectFor = time.Second
)

type mockCall struct {
	c     *chat
	ev    api.CallStateEvent
	timer *time.Timer
}

// ring starts an incoming voice call from the most recent 1:1 chat.
func (b *Backend) ring() {
	if b.auth != api.AuthPaired {
		return
	}
	for _, c := range b.sorted() {
		if c.row.Kind == api.ChatUser {
			b.newCall(c, true)
			return
		}
	}
}

func (b *Backend) newCall(c *chat, incoming bool) *mockCall {
	b.seq++
	id := fmt.Sprintf("MOCKCALL%04d", b.seq)
	from, _ := b.self()
	if incoming {
		from = c.spec.jid
	}
	mc := &mockCall{c: c, ev: api.CallStateEvent{CallID: id, Chat: c.spec.jid, From: from, Name: c.row.Name,
		Incoming: incoming, State: api.CallRinging, Since: time.Now().Unix()}}
	b.calls[id] = mc
	if incoming {
		mc.timer = b.after(ringFor, func() { b.endCall(id, api.CallMissed) })
	} else {
		mc.timer = b.after(pickUpIn, func() { b.connect(id) })
	}
	b.emit(api.ECallState, mc.ev)
	return mc
}

func (b *Backend) setState(mc *mockCall, state string) {
	mc.ev.State, mc.ev.Since = state, time.Now().Unix()
	b.emit(api.ECallState, mc.ev)
}

// connect: the call was answered; it is active a moment later.
func (b *Backend) connect(id string) {
	mc := b.calls[id]
	if mc == nil {
		return
	}
	b.setState(mc, api.CallConnecting)
	mc.timer = b.after(connectFor, func() {
		if b.calls[id] == mc {
			b.setState(mc, api.CallActive)
		}
	})
}

func (b *Backend) endCall(id, reason string) {
	mc := b.calls[id]
	if mc == nil {
		return
	}
	delete(b.calls, id)
	mc.timer.Stop()
	mc.ev.Reason = reason
	b.setState(mc, api.CallEnded)
	if reason != api.CallMissed && reason != api.CallDeclined {
		return
	}
	text := "Missed voice call"
	if reason == api.CallDeclined {
		text = "Declined voice call"
	}
	b.add(mc.c, &api.Message{Type: api.MsgSystem, Text: text, Sender: mc.c.spec.jid, SenderName: mc.c.row.Name})
}

func (b *Backend) CallsStart(ctx context.Context, p api.ChatParams) (*api.CallStartResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.chats[p.Chat]
	if c == nil {
		return nil, api.ErrNotFound
	}
	if c.row.Kind != api.ChatUser {
		return nil, api.ErrUnsupported
	}
	if len(b.calls) > 0 {
		return nil, errors.New("already in a call")
	}
	return &api.CallStartResult{CallID: b.newCall(c, false).ev.CallID}, nil
}

func (b *Backend) CallsAccept(ctx context.Context, p api.CallParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	mc := b.calls[p.CallID]
	if mc == nil || !mc.ev.Incoming || mc.ev.State != api.CallRinging {
		return api.ErrNotFound
	}
	mc.timer.Stop()
	b.connect(p.CallID)
	return nil
}

func (b *Backend) CallsReject(ctx context.Context, p api.CallParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	mc := b.calls[p.CallID]
	if mc == nil || !mc.ev.Incoming || mc.ev.State != api.CallRinging {
		return api.ErrNotFound
	}
	b.endCall(p.CallID, api.CallDeclined)
	return nil
}

func (b *Backend) CallsEnd(ctx context.Context, p api.CallParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	mc := b.calls[p.CallID]
	if mc == nil {
		return api.ErrNotFound
	}
	reason := api.CallHungUp
	if mc.ev.Incoming && mc.ev.State == api.CallRinging {
		reason = api.CallDeclined
	}
	b.endCall(p.CallID, reason)
	return nil
}

func (b *Backend) CallsMute(ctx context.Context, p api.CallMuteParams) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	mc := b.calls[p.CallID]
	if mc == nil || mc.ev.State == api.CallRinging {
		return api.ErrNotFound
	}
	if mc.ev.Muted != p.Muted {
		mc.ev.Muted = p.Muted
		b.emit(api.ECallState, mc.ev)
	}
	return nil
}
