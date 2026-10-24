package core

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"wisp/daemon/internal/api"
)

// enqueue is the WA event handler: it hands events to the serial loop and
// only blocks if the loop is far behind (backpressure on the WA layer).
// History batches are big, so they are handled right here: the WA layer
// waits rather than queueing them.
func (c *Core) enqueue(ev api.WAEvent) {
	if _, ok := ev.(api.EvHistory); ok {
		if c.life.Err() == nil {
			c.handle(ev)
		}
		return
	}
	select {
	case c.events <- ev:
	case <-c.life.Done():
	}
}

func (c *Core) loop() {
	defer c.wg.Done()
	for {
		select {
		case ev := <-c.events:
			c.handle(ev)
		case <-c.life.Done():
			return
		}
	}
}

// handle processes one WA event synchronously.
func (c *Core) handle(ev api.WAEvent) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("core: panic handling WA event", "event", fmt.Sprintf("%T", ev), "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(c.life, 2*time.Minute)
	defer cancel()

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	switch ev.(type) {
	case api.EvMessage, api.EvHistory, api.EvReceipt, api.EvReaction, api.EvEdit, api.EvRevoke, api.EvDeleteForMe,
		api.EvChatMeta, api.EvContacts, api.EvAvatarChanged, api.EvCallOffer, api.EvFavoriteSticker:
		if !c.wa.IsLoggedIn() {
			return // left over from a session that was logged out and wiped
		}
	}

	switch e := ev.(type) {
	case api.EvQR:
		c.emitEv(api.EAuthQR, api.QREvent{Code: e.Code, Timeout: e.Timeout})
	case api.EvPaired:
		c.emitEv(api.EAuthState, api.AuthStateEvent{State: api.AuthPaired, SelfJID: e.JID, SelfName: e.PushName})
		c.applyOnline(true) // presence carries our name, which WhatsApp needs once per connection
	case api.EvLoggedOut:
		c.wipe(ctx)
		c.emitEv(api.EAuthState, api.AuthStateEvent{State: api.AuthLoggedOut, Reason: e.Reason})
	case api.EvConnState:
		c.emitEv(api.EConnectionState, api.ConnectionStateEvent{State: e.State})
		if e.State == api.ConnConnected {
			c.applyOnline(true)
			c.resendReceipts()
			if c.setting(ctx, settingResynced) == nil {
				c.resyncAppState()
			}
		}
		if !c.wa.IsLoggedIn() {
			c.emitAuthState() // e.g. QR codes ran out: back to unpaired
		}
	case api.EvMessage:
		c.onMessage(ctx, e)
	case api.EvHistory:
		c.onHistory(ctx, &e)
	case api.EvReceipt:
		msgs, err := c.store.UpdateStatus(ctx, e.Chat, e.IDs, e.Status)
		if err != nil {
			c.logErr("receipt", err, "chat", e.Chat)
			return
		}
		c.emitUpdates(ctx, e.Chat, msgs...)
	case api.EvReaction:
		m, err := c.store.ApplyReaction(ctx, e)
		if err != nil {
			c.logErr("reaction", err, "chat", e.Chat, "id", e.ID)
			return
		}
		c.emitUpdates(ctx, e.Chat, m)
	case api.EvEdit:
		m, err := c.store.ApplyEdit(ctx, e.Chat, e.ID, e.Text, e.Mentions, e.Ts)
		if err != nil {
			c.logErr("edit", err, "chat", e.Chat, "id", e.ID)
			return
		}
		c.emitUpdates(ctx, e.Chat, m)
	case api.EvRevoke:
		m, err := c.store.ApplyRevoke(ctx, e.Chat, e.ID)
		if err != nil {
			c.logErr("revoke", err, "chat", e.Chat, "id", e.ID)
			return
		}
		c.emitUpdates(ctx, e.Chat, m)
	case api.EvDeleteForMe:
		if err := c.removeMessage(ctx, e.Chat, e.ID); err != nil {
			c.logErr("delete for me", err, "chat", e.Chat, "id", e.ID)
		}
	case api.EvChatMeta:
		if e.Patch.JID == "" {
			return
		}
		if err := c.store.UpsertChat(ctx, e.Patch); err != nil {
			c.logErr("chat meta", err, "chat", e.Patch.JID)
			return
		}
		if e.Patch.Unread != nil && *e.Patch.Unread == 0 {
			// Read on another device.
			c.notify.Clear(e.Patch.JID)
		}
		c.emitChat(ctx, e.Patch.JID)
	case api.EvContacts:
		c.onContacts(ctx, e.Contacts)
	case api.EvMediaPath:
		if err := c.store.SetMediaDirectPath(ctx, e.Chat, e.ID, e.DirectPath); err != nil {
			c.logErr("media path", err, "chat", e.Chat, "id", e.ID)
		}
	case api.EvCallOffer:
		c.onCallOffer(ctx, e)
	case api.EvCallState:
		c.onCallState(ctx, e)
	case api.EvPresence:
		c.emitEv(api.EPresence, api.PresenceEvent{JID: e.JID, Online: e.Online, LastSeen: e.LastSeen})
	case api.EvTyping:
		kind := e.Kind
		if kind == "" {
			kind = "text"
		}
		who := e.JID
		if who == "" {
			who = e.Chat
		}
		c.emitEv(api.ETyping, api.TypingEvent{
			Chat: e.Chat, JID: who, Name: c.displayName(ctx, who), Typing: e.Typing, Kind: kind,
		})
	case api.EvFavoriteSticker:
		if err := c.store.PutFavoriteSticker(ctx, e); err != nil {
			c.logErr("favourite sticker", err)
		}
	case api.EvAvatarChanged:
		// Network fetch: don't hold up the event loop.
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.refreshAvatar(e.JID)
		}()
	default:
		c.log.Debug("core: unhandled WA event", "event", fmt.Sprintf("%T", ev))
	}
}

// onMessage: store → message.new → (mark read if open) → chat.update → notify.
func (c *Core) onMessage(ctx context.Context, e api.EvMessage) {
	m := e.Msg
	if m == nil || m.Chat == "" || m.ID == "" {
		return
	}
	if !m.FromMe {
		c.learnPushName(ctx, m)
	}
	active := c.activeFocused(m.Chat)
	incoming := !m.FromMe && m.Type != api.MsgSystem
	if err := c.store.PutMessage(ctx, m, incoming && !active); err != nil {
		c.logErr("put message", err, "chat", m.Chat, "id", m.ID)
		return
	}
	// Emit the stored row: merged with any earlier copy, names resolved.
	if stored, err := c.store.GetMessage(ctx, m.Chat, m.ID); err == nil {
		m = stored
	}
	if m.Reactions == nil {
		m.Reactions = []api.Reaction{}
	}
	if m.Mentions == nil {
		m.Mentions = []api.Mention{}
	}
	c.emitEv(api.EMessageNew, api.MessageEvent{Message: *m})

	if incoming && active {
		// Chat is on screen in a focused window: it's read already.
		c.markRead(ctx, m.Chat, false)
	}
	chat := c.emitChat(ctx, m.Chat)

	if !e.Live || !incoming || active || chat == nil {
		return
	}
	// Like WhatsApp, a mention of us notifies even in a muted chat.
	mentioned := slices.ContainsFunc(m.Mentions, func(mn api.Mention) bool { return mn.Me })
	if !c.settingBool(ctx, api.SettingNotifications) || (chat.Muted && !mentioned) {
		return
	}
	n := api.Notification{
		Chat:     m.Chat,
		ChatName: chat.Name,
		MsgID:    m.ID,
		Text:     api.PreviewText(m),
		IsGroup:  chat.Kind == api.ChatGroup,
		Avatar:   chat.Avatar,
	}
	if n.IsGroup {
		n.Sender = m.SenderName
	}
	if !c.settingBool(ctx, api.SettingNotifyPreview) {
		n.Text = "New message"
		n.Sender = ""
	}
	if err := c.notify.Notify(ctx, n); err != nil {
		c.log.Warn("core: notify failed", "chat", m.Chat, "err", err)
	}
}

func (c *Core) onHistory(ctx context.Context, h *api.EvHistory) {
	if err := c.store.PutHistory(ctx, h); err != nil {
		c.logErr("put history", err)
		return
	}
	touched := map[string]struct{}{}
	for _, p := range h.Chats {
		touched[p.JID] = struct{}{}
	}
	for _, m := range h.Messages {
		if m != nil {
			touched[m.Chat] = struct{}{}
		}
	}
	delete(touched, "")
	for j := range touched {
		c.emitChat(ctx, j)
		c.histChats[j] = struct{}{}
	}
	c.histMsgs += len(h.Messages)
	c.emitEv(api.EHistorySyncProgress, api.HistorySyncEvent{
		Percent: h.Percent, Chats: len(c.histChats), Messages: c.histMsgs, Done: h.Final})
	if h.Final {
		// A later sync (e.g. after re-linking) counts afresh.
		c.histChats = map[string]struct{}{}
		c.histMsgs = 0
		// The first app state sync raced the LID mappings history brings.
		c.resyncAppState()
	}
}

// settingResynced marks a database whose chat settings were fetched again
// after history sync stopped clearing them, and again (v2) to pick up the
// favourite stickers synced before wisp stored them. Not shown by settings.get.
const settingResynced = "internal.appStateResynced2"

// resyncAppState re-fetches pin, mute and archive state in the background,
// then returns the sync's garbage to the OS.
func (c *Core) resyncAppState() {
	if !c.resyncing.CompareAndSwap(false, true) {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.resyncing.Store(false)
		defer debug.FreeOSMemory()
		ctx, cancel := context.WithTimeout(c.life, 2*time.Minute)
		defer cancel()
		if err := c.wa.ResyncAppState(ctx); err != nil {
			c.log.Warn("core: app state resync failed", "err", err)
			return
		}
		if err := c.saveSetting(ctx, settingResynced, true); err != nil {
			c.logErr("save resync mark", err)
		}
	}()
}

// onContacts stores names and emits chat.update for 1:1 chats whose
// displayed name changed.
func (c *Core) onContacts(ctx context.Context, cs []api.Contact) {
	before := map[string]string{}
	for _, ct := range cs {
		if ch, err := c.store.GetChat(ctx, ct.JID); err == nil {
			before[ct.JID] = ch.Name
		}
	}
	if err := c.store.PutContacts(ctx, cs); err != nil {
		c.logErr("put contacts", err)
		return
	}
	for jid, name := range before {
		if ch, err := c.store.GetChat(ctx, jid); err == nil && ch.Name != name {
			c.emitEv(api.EChatUpdate, api.ChatEvent{Chat: *ch})
		}
	}
}

// learnPushName records the sender's self-chosen name if it is new to us.
func (c *Core) learnPushName(ctx context.Context, m *api.Message) {
	if m.PushName == "" || m.Sender == "" {
		return
	}
	ct, err := c.store.GetContact(ctx, m.Sender)
	if err == nil && ct != nil && ct.PushName == m.PushName {
		return
	}
	if err := c.store.PutContacts(ctx, []api.Contact{{JID: m.Sender, PushName: m.PushName}}); err != nil {
		c.logErr("put pushname", err)
	}
}

func (c *Core) refreshAvatar(jid string) {
	ctx, cancel := context.WithTimeout(c.life, time.Minute)
	defer cancel()
	path, err := c.media.Avatar(ctx, jid, true)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		c.logErr("avatar refresh", err, "jid", jid)
		return
	}
	if err := c.store.SetAvatar(ctx, jid, path); err != nil {
		c.logErr("set avatar", err, "jid", jid)
		return
	}
	c.emitChat(ctx, jid)
}

// emitUpdates emits message.update for each message, and chat.update when
// one of them is the chat's last message.
func (c *Core) emitUpdates(ctx context.Context, chat string, msgs ...*api.Message) {
	ids := make(map[string]struct{}, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		ids[m.ID] = struct{}{}
		c.emitEv(api.EMessageUpdate, api.MessageEvent{Message: *m})
	}
	if len(ids) == 0 {
		return
	}
	ch, err := c.store.GetChat(ctx, chat)
	if err != nil {
		return
	}
	if ch.LastMessage != nil {
		if _, ok := ids[ch.LastMessage.ID]; ok {
			c.emitEv(api.EChatUpdate, api.ChatEvent{Chat: *ch})
		}
	}
}

// emitChat reads the chat row and emits chat.update. Returns the row.
func (c *Core) emitChat(ctx context.Context, jid string) *api.Chat {
	ch, err := c.store.GetChat(ctx, jid)
	if err != nil {
		c.logErr("get chat", err, "chat", jid)
		return nil
	}
	c.emitEv(api.EChatUpdate, api.ChatEvent{Chat: *ch})
	return ch
}

// displayName resolves a JID to the best known name: our contacts, then
// whatsmeow's contact store, then the phone number.
func (c *Core) displayName(ctx context.Context, jid string) string {
	if ct, err := c.store.GetContact(ctx, jid); err == nil && ct.Name != "" {
		return ct.Name
	}
	if c.wa.IsLoggedIn() {
		if ct, err := c.wa.ResolveContact(ctx, jid); err == nil && ct != nil && ct.Name != "" {
			return ct.Name
		}
	}
	return jidUser(jid)
}

func jidUser(jid string) string {
	user := jid
	if i := strings.IndexByte(user, '@'); i >= 0 {
		user = user[:i]
	}
	if i := strings.IndexByte(user, ':'); i >= 0 { // device suffix
		user = user[:i]
	}
	if strings.HasSuffix(jid, "@s.whatsapp.net") && user != "" {
		return "+" + user
	}
	return user
}

func (c *Core) logErr(what string, err error, args ...any) {
	if errors.Is(err, api.ErrNotFound) {
		c.log.Debug("core: "+what+": not found", args...)
		return
	}
	c.log.Warn("core: "+what+" failed", append(args, "err", err)...)
}
