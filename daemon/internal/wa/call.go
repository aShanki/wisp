package wa

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/purpshell/meowcaller"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/audio"
)

const ringTimeout = 90 * time.Second

// live is a 1:1 voice call meowcaller carries: one we placed, or an incoming one
// it can answer. Its audio streams exist only once it is answered.
type live struct {
	mc        *meowcaller.Call
	incoming  bool
	answered  bool
	state     string
	peerMuted bool
	mic       *audio.Mic
	speaker   *audio.Speaker
	player    *meowcaller.Player
}

// newCaller attaches meowcaller to a new whatsmeow client, before it connects.
func (c *Client) newCaller(cli *whatsmeow.Client) *meowcaller.Client {
	mc := meowcaller.NewClient(cli)
	mc.OnIncomingCall(func(call *meowcaller.Call) {
		if c.client() == cli {
			c.track(call, true)
		}
	})
	return mc
}

func (c *Client) caller() *meowcaller.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mcaller
}

// onCall translates whatsmeow's call signalling for incoming calls.
func (c *Client) onCall(cli *whatsmeow.Client, raw any) {
	switch v := raw.(type) {
	case *events.CallOffer:
		_, video := v.Data.GetOptionalChildByTag("video")
		c.ring(cli, v.BasicCallMeta, video)
	case *events.CallOfferNotice:
		c.ring(cli, v.BasicCallMeta, v.Media == "video")
	case *events.CallTerminate:
		c.endCall(v.CallID, terminateReason(v.Reason))
	case *events.CallReject: // from another of our devices
		c.endCall(v.CallID, api.CallDeclined)
	case *events.CallAccept:
		c.endCall(v.CallID, api.CallAnswered)
	}
}

func (c *Client) ring(cli *whatsmeow.Client, m types.BasicCallMeta, video bool) {
	creator := m.CallCreator
	if creator.IsEmpty() {
		creator = m.From
	}
	cv := c.converterFor(cli)
	from := creator
	if from.Server == types.HiddenUserServer && m.CallCreatorAlt.Server == types.DefaultUserServer {
		from = m.CallCreatorAlt
	}
	from = cv.canon(from)
	if cv.isSelf(from) {
		return // our own call, ringing our other devices
	}
	chat, group := from, !m.GroupJID.IsEmpty()
	if group {
		chat = m.GroupJID.ToNonAD()
	}
	c.callMu.Lock()
	if c.calls == nil {
		c.calls = make(map[string]types.JID)
	}
	_, seen := c.calls[m.CallID]
	c.calls[m.CallID] = creator
	c.callMu.Unlock()
	if !seen {
		// WhatsApp ends a call that rings out; this covers a lost terminate.
		time.AfterFunc(ringTimeout, func() { c.endCall(m.CallID, api.CallMissed) })
		c.startTone(m.CallID, audio.Ringtone)
		c.emit(api.EvCallOffer{ID: m.CallID, Chat: chat.String(), From: from.String(),
			Video: video, Group: group, Ts: unix(m.Timestamp)})
	}
}

// endCall ends an incoming call that is still ringing.
func (c *Client) endCall(id, reason string) {
	c.callMu.Lock()
	_, ok := c.calls[id]
	delete(c.calls, id)
	if l := c.live[id]; l != nil && !l.answered {
		delete(c.live, id)
	}
	c.callMu.Unlock()
	if ok {
		c.stopTone(id)
		c.emit(api.EvCallState{ID: id, State: api.CallEnded, Reason: reason})
	}
}

func terminateReason(r string) string {
	switch {
	case strings.Contains(r, "accept"):
		return api.CallAnswered
	case strings.Contains(r, "reject"), strings.Contains(r, "decline"):
		return api.CallDeclined
	}
	return api.CallMissed
}

// track follows a call meowcaller carries until it ends.
func (c *Client) track(mc *meowcaller.Call, incoming bool) *live {
	id := mc.ID()
	l := &live{mc: mc, incoming: incoming, state: api.CallRinging}
	c.callMu.Lock()
	if c.live == nil {
		c.live = make(map[string]*live)
	}
	c.live[id] = l
	c.callMu.Unlock()
	mc.OnPeerAccept(func() { c.answered(id) })
	mc.OnReady(func() { c.moved(id, api.CallActive, nil) })
	mc.OnMuteState(func(m bool) { c.moved(id, "", &m) })
	mc.OnEnd(func(reason string) { c.finish(id, reason) })
	return l
}

// moved emits a call's new state ("" keeps it), or its peer's mute.
func (c *Client) moved(id, state string, peerMuted *bool) {
	c.callMu.Lock()
	l := c.live[id]
	if l == nil || !l.answered {
		c.callMu.Unlock()
		return
	}
	if state != "" {
		l.state = state
	}
	if peerMuted != nil {
		l.peerMuted = *peerMuted
	}
	ev := api.EvCallState{ID: id, State: l.state, PeerMuted: l.peerMuted}
	c.callMu.Unlock()
	c.emit(ev)
}

// answered: either side picked up. The call's audio opens now.
func (c *Client) answered(id string) {
	c.callMu.Lock()
	l := c.live[id]
	if l == nil || l.answered {
		c.callMu.Unlock()
		return
	}
	l.answered = true
	delete(c.calls, id)
	c.callMu.Unlock()
	c.stopTone(id)
	mic, err := audio.OpenMic()
	if err != nil {
		c.log.Warn("wa: call microphone", "err", err)
	}
	speaker, err := audio.OpenSpeaker()
	if err != nil {
		c.log.Warn("wa: call speaker", "err", err)
	}
	c.callMu.Lock()
	l.mic, l.speaker = mic, speaker
	if mic != nil {
		l.player = l.mc.Play(mic)
	}
	if speaker != nil {
		l.mc.Receive(speaker)
	}
	c.callMu.Unlock()
	c.moved(id, api.CallConnecting, nil)
}

func (c *Client) finish(id, reason string) {
	c.callMu.Lock()
	l := c.live[id]
	delete(c.live, id)
	c.callMu.Unlock()
	if l == nil {
		return
	}
	if l.player != nil {
		l.player.Stop()
	}
	if l.speaker != nil {
		_ = l.speaker.Close()
	}
	if l.incoming && !l.answered {
		return // endCall reports a call that only rang
	}
	c.stopTone(id)
	c.emit(api.EvCallState{ID: id, State: api.CallEnded, Reason: endReason(reason, l.answered), PeerMuted: l.peerMuted})
}

// endReason maps meowcaller's reason for a call we placed or answered.
func endReason(r string, answered bool) string {
	switch {
	case r == "offer_failed", strings.HasPrefix(r, "server:"):
		return api.CallFailed
	case answered, r == "hangup":
		return api.CallHungUp
	case r == "rejected":
		return api.CallRejected
	}
	return api.CallUnanswered
}

func (c *Client) CallsAvailable() bool { return audio.Available() }

// StartCall rings a 1:1 chat. One call at a time.
func (c *Client) StartCall(ctx context.Context, chat string) (string, error) {
	if _, err := c.requireConnected(); err != nil {
		return "", err
	}
	jid, err := types.ParseJID(chat)
	if err != nil || (jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer) {
		return "", api.ErrUnsupported
	}
	c.callMu.Lock()
	busy := false
	for _, l := range c.live {
		busy = busy || l.answered || !l.incoming
	}
	c.callMu.Unlock()
	if busy {
		return "", errors.New("already in a call")
	}
	mc, err := c.caller().Call(ctx, jid.String())
	if err != nil {
		return "", err
	}
	c.track(mc, false)
	c.startTone(mc.ID(), audio.Ringback)
	return mc.ID(), nil
}

// AcceptCall answers an incoming 1:1 voice call that is ringing.
func (c *Client) AcceptCall(ctx context.Context, id string) error {
	if _, err := c.requireConnected(); err != nil {
		return err
	}
	c.callMu.Lock()
	l := c.live[id]
	c.callMu.Unlock()
	if l == nil || !l.incoming || l.answered {
		return api.ErrNotFound
	}
	if l.mc.IsVideo() {
		return api.ErrUnsupported
	}
	if err := l.mc.Answer(); err != nil {
		return err
	}
	c.answered(id)
	return nil
}

// RejectCall declines a ringing incoming call on all our devices.
func (c *Client) RejectCall(ctx context.Context, id string) error {
	cli, err := c.requireConnected()
	if err != nil {
		return err
	}
	c.callMu.Lock()
	creator, ok := c.calls[id]
	l := c.live[id]
	c.callMu.Unlock()
	if !ok {
		return api.ErrNotFound
	}
	if l != nil {
		err = l.mc.Reject()
	} else {
		err = cli.RejectCall(ctx, creator, id)
	}
	if err != nil {
		return err
	}
	c.endCall(id, api.CallDeclined)
	return nil
}

// EndCall hangs up a call we placed or answered; a ringing incoming one is declined.
func (c *Client) EndCall(ctx context.Context, id string) error {
	c.callMu.Lock()
	l := c.live[id]
	_, ringing := c.calls[id]
	c.callMu.Unlock()
	if ringing {
		return c.RejectCall(ctx, id)
	}
	if l == nil {
		return api.ErrNotFound
	}
	return l.mc.Hangup()
}

// MuteCall stops sending the microphone (silence goes instead).
func (c *Client) MuteCall(id string, muted bool) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	l := c.live[id]
	if l == nil || l.player == nil {
		return api.ErrNotFound
	}
	if muted {
		l.player.Pause()
	} else {
		l.player.Resume()
	}
	return nil
}

// startTone loops a ringing tone for a call, unless one is already playing.
func (c *Client) startTone(id string, t audio.Tone) {
	c.toneMu.Lock()
	defer c.toneMu.Unlock()
	if c.tone != nil {
		return
	}
	r, err := audio.Ring(t)
	if err != nil {
		c.log.Debug("wa: ringing tone", "err", err)
		return
	}
	c.tone, c.toneID = r, id
}

func (c *Client) stopTone(id string) {
	c.toneMu.Lock()
	defer c.toneMu.Unlock()
	if c.tone != nil && c.toneID == id {
		_ = c.tone.Close()
		c.tone = nil
	}
}
