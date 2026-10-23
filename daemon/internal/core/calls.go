package core

import (
	"context"
	"time"

	"wisp/daemon/internal/api"
)

// ringSeconds: an offer older than this (offline catch-up) is already missed.
const ringSeconds = 60

// call is one call in progress, as last emitted.
type call struct {
	api.CallStateEvent
	ts int64 // when it started ringing
}

func (c *Core) onCallOffer(ctx context.Context, e api.EvCallOffer) {
	r := &call{ts: e.Ts, CallStateEvent: api.CallStateEvent{CallID: e.ID, Chat: e.Chat, From: e.From,
		Name: c.displayName(ctx, e.From), Incoming: true, Video: e.Video, Group: e.Group,
		State: api.CallRinging, Since: time.Now().Unix()}}
	if time.Now().Unix()-e.Ts > ringSeconds {
		c.logCall(ctx, r, api.CallMissed)
		return
	}
	if c.calls == nil {
		c.calls = map[string]*call{}
	}
	c.calls[e.ID] = r
	c.emitEv(api.ECallState, r.CallStateEvent)
	if !c.settingBool(ctx, api.SettingNotifications) {
		return
	}
	n := api.CallNotification{ID: e.ID, Chat: e.Chat, Name: r.Name, Video: e.Video, Group: e.Group}
	if ch, err := c.store.GetChat(ctx, e.Chat); err == nil && !e.Group {
		n.Avatar = ch.Avatar
	}
	if err := c.notify.NotifyCall(ctx, n); err != nil {
		c.log.Warn("core: call notification failed", "err", err)
	}
}

func (c *Core) onCallState(ctx context.Context, e api.EvCallState) {
	r := c.calls[e.ID]
	if r == nil {
		return
	}
	if e.State != r.State {
		r.State, r.Since = e.State, time.Now().Unix()
	}
	r.PeerMuted = e.PeerMuted
	if r.State != api.CallRinging {
		c.notify.ClearCall(e.ID)
	}
	if r.State == api.CallEnded {
		delete(c.calls, e.ID)
		r.Reason = e.Reason
	}
	c.emitEv(api.ECallState, r.CallStateEvent)
	if r.State == api.CallEnded && r.Incoming {
		c.logCall(ctx, r, e.Reason)
	}
}

// logCall leaves a missed or declined call in the chat, and a notification
// for a missed one.
func (c *Core) logCall(ctx context.Context, r *call, reason string) {
	if reason != api.CallMissed && reason != api.CallDeclined {
		return
	}
	text := callText(r.Video, r.Group, reason)
	ts := r.ts
	if ts == 0 {
		ts = time.Now().Unix()
	}
	c.onMessage(ctx, api.EvMessage{Msg: &api.Message{ID: "call-" + r.CallID, Chat: r.Chat, Sender: r.From,
		SenderName: r.Name, Timestamp: ts, Type: api.MsgSystem, Text: text}})
	if reason != api.CallMissed || c.activeFocused(r.Chat) || !c.settingBool(ctx, api.SettingNotifications) {
		return
	}
	n := api.Notification{Chat: r.Chat, ChatName: r.Name, MsgID: "call-" + r.CallID, Text: text, IsGroup: r.Group}
	if ch, err := c.store.GetChat(ctx, r.Chat); err == nil {
		n.ChatName, n.Avatar = ch.Name, ch.Avatar
		if r.Group {
			n.Sender = r.Name
		}
	}
	if err := c.notify.Notify(ctx, n); err != nil {
		c.log.Warn("core: notify failed", "chat", r.Chat, "err", err)
	}
}

func callText(video, group bool, reason string) string {
	kind := "voice call"
	if video {
		kind = "video call"
	}
	if group {
		kind = "group " + kind
	}
	if reason == api.CallDeclined {
		return "Declined " + kind
	}
	return "Missed " + kind
}

func (c *Core) CallsStart(ctx context.Context, p api.ChatParams) (*api.CallStartResult, error) {
	id, err := c.wa.StartCall(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.calls == nil {
		c.calls = map[string]*call{}
	}
	now := time.Now().Unix()
	r := &call{ts: now, CallStateEvent: api.CallStateEvent{CallID: id, Chat: p.Chat, From: c.wa.SelfJID(),
		Name: c.displayName(ctx, p.Chat), State: api.CallRinging, Since: now}}
	c.calls[id] = r
	c.emitEv(api.ECallState, r.CallStateEvent)
	return &api.CallStartResult{CallID: id}, nil
}

func (c *Core) CallsAccept(ctx context.Context, p api.CallParams) error {
	return c.wa.AcceptCall(ctx, p.CallID)
}

func (c *Core) CallsReject(ctx context.Context, p api.CallParams) error {
	return c.wa.RejectCall(ctx, p.CallID)
}

func (c *Core) CallsEnd(ctx context.Context, p api.CallParams) error {
	return c.wa.EndCall(ctx, p.CallID)
}

func (c *Core) CallsMute(ctx context.Context, p api.CallMuteParams) error {
	if err := c.wa.MuteCall(p.CallID, p.Muted); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if r := c.calls[p.CallID]; r != nil && r.Muted != p.Muted {
		r.Muted = p.Muted
		c.emitEv(api.ECallState, r.CallStateEvent)
	}
	return nil
}

func (c *Core) onNotifyDecline(id string) {
	if err := c.wa.RejectCall(c.life, id); err != nil {
		c.logErr("decline call", err, "call", id)
	}
}
