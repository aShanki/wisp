package wa

import (
	"cmp"
	"context"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
)

// converterFor builds a converter backed by the client's LID and contact
// stores (both cached in memory by whatsmeow's sqlstore).
func (c *Client) converterFor(cli *whatsmeow.Client) *converter {
	cv := &converter{
		pnForLID: func(lid types.JID) types.JID {
			pn, err := cli.Store.LIDs.GetPNForLID(c.ctx, lid)
			if err != nil {
				return types.JID{}
			}
			return pn
		},
		name: func(j types.JID) string {
			info, err := cli.Store.Contacts.GetContact(c.ctx, j)
			if err != nil || !info.Found {
				return ""
			}
			return bestName(info)
		},
	}
	if id := cli.Store.ID; id != nil {
		cv.selfPN = id.ToNonAD()
	}
	if lid := cli.Store.LID; !lid.IsEmpty() {
		cv.selfLID = lid.ToNonAD()
	}
	return cv
}

func bestName(info types.ContactInfo) string {
	switch {
	case info.FullName != "":
		return info.FullName
	case info.FirstName != "":
		return info.FirstName
	case info.PushName != "":
		return info.PushName
	case info.BusinessName != "":
		return info.BusinessName
	}
	return ""
}

// contactFor builds a full api.Contact for canonical JID j from whatsmeow's
// contact store, merging in data stored under alternative JIDs (e.g. the LID).
func (c *Client) contactFor(cli *whatsmeow.Client, j types.JID, alts ...types.JID) api.Contact {
	ct := api.Contact{JID: j.String()}
	if j.Server == types.DefaultUserServer {
		ct.Phone = j.User
	}
	merge := func(info types.ContactInfo) {
		if ct.FullName == "" {
			ct.FullName = info.FullName
			if ct.FullName == "" {
				ct.FullName = info.FirstName
			}
		}
		if ct.PushName == "" {
			ct.PushName = info.PushName
		}
		if ct.BusinessName == "" {
			ct.BusinessName = info.BusinessName
		}
	}
	for _, x := range append([]types.JID{j}, alts...) {
		if x.IsEmpty() {
			continue
		}
		if info, err := cli.Store.Contacts.GetContact(c.ctx, x.ToNonAD()); err == nil && info.Found {
			merge(info)
		}
	}
	switch {
	case ct.FullName != "":
		ct.Name = ct.FullName
	case ct.PushName != "":
		ct.Name = ct.PushName
	case ct.BusinessName != "":
		ct.Name = ct.BusinessName
	case ct.Phone != "":
		ct.Name = "+" + ct.Phone
	}
	return ct
}

// onEvent translates whatsmeow events into api.WAEvents. It runs on
// whatsmeow's event goroutine and must stay cheap.
func (c *Client) onEvent(cli *whatsmeow.Client, raw any) {
	switch v := raw.(type) {
	case *events.Connected:
		c.offlineSync.Store(false)
		c.mu.Lock()
		if c.cli == cli {
			c.auth = api.AuthPaired
		}
		first := !c.groupsFetched
		c.groupsFetched = true
		c.mu.Unlock()
		c.setConn(api.ConnConnected)
		if first {
			go c.fetchGroups(cli)
		}
	case *events.Disconnected:
		c.setConn(api.ConnDisconnected)
	case *events.KeepAliveTimeout:
		if v.ErrorCount >= 2 {
			c.setConn(api.ConnConnecting)
		}
	case *events.KeepAliveRestored:
		c.setConn(api.ConnConnected)
	case *events.StreamReplaced:
		c.log.Warn("connection replaced by another client using this session")
		c.setConn(api.ConnOffline)
	case *events.LoggedOut:
		reason := v.Reason.String()
		c.log.Warn("logged out", "reason", reason, "onConnect", v.OnConnect)
		c.mu.Lock()
		c.auth = api.AuthLoggedOut
		c.conn = api.ConnOffline
		c.mu.Unlock()
		c.emit(api.EvLoggedOut{Reason: reason})
		c.emit(api.EvConnState{State: api.ConnOffline})
	case *events.TemporaryBan:
		c.log.Error("temporarily banned", "ban", v.String())
		c.setConn(api.ConnOffline)
	case *events.ConnectFailure:
		c.log.Warn("connect failure", "reason", v.Reason.String(), "message", v.Message)
		c.setConn(api.ConnDisconnected)
	case *events.ClientOutdated:
		c.log.Error("WhatsApp says this client is outdated; update whatsmeow")
		c.setConn(api.ConnOffline)
	case *events.PairSuccess:
		c.setAuth(api.AuthPaired)
		c.emit(api.EvPaired{JID: v.ID.ToNonAD().String(), PushName: cli.Store.PushName})
	case *events.PushNameSetting:
		if id := cli.Store.ID; id != nil {
			c.emit(api.EvPaired{JID: id.ToNonAD().String(), PushName: v.Action.GetName()})
		}
	case *events.PairError:
		c.log.Warn("pairing failed", "err", v.Error)
	case *events.OfflineSyncPreview:
		c.offlineSync.Store(true)
	case *events.OfflineSyncCompleted:
		c.offlineSync.Store(false)

	case *events.Message:
		c.handleMessage(cli, v)
	case *events.UndecryptableMessage:
		c.log.Debug("undecryptable message", "id", v.Info.ID, "chat", v.Info.Chat)
	case *events.HistorySync:
		cv := c.converterFor(cli)
		h := c.convertHistory(cli, cv, v.Data)
		if h != nil {
			c.emit(*h)
		}
	case *events.Receipt:
		c.handleReceipt(cli, v)

	case *events.Mute:
		var until int64
		if v.Action.GetMuted() {
			until = muteUntil(v.Action.GetMuteEndTimestamp())
		}
		c.emitMeta(cli, v.JID, func(p *api.ChatPatch) { p.MutedUntil = &until })
	case *events.Pin:
		at := pinTime(v.Action.GetPinned(), v.Timestamp)
		c.emitMeta(cli, v.JID, func(p *api.ChatPatch) { p.Pinned = &at })
	case *events.DeleteForMe:
		c.emit(api.EvDeleteForMe{Chat: c.converterFor(cli).canon(v.ChatJID).String(), ID: v.MessageID})
	case *events.Archive:
		archived := v.Action.GetArchived()
		c.emitMeta(cli, v.JID, func(p *api.ChatPatch) { p.Archived = &archived })
	case *events.MarkChatAsRead:
		c.emitMeta(cli, v.JID, func(p *api.ChatPatch) { markChatRead(p, v.Action.GetRead(), v.FromFullSync) })
	case *events.Contact:
		if !v.FromFullSync { // full syncs are batched on AppStateSyncComplete
			c.emitContact(cli, v.JID)
		}
	case *events.PushName:
		c.emitContact(cli, v.JID, v.JIDAlt)
	case *events.BusinessName:
		c.emitContact(cli, v.JID)
	case *events.AppState:
		// The phone keys 1:1 chats by LID and names their phone number here.
		// Store it before the chat's other settings (next in the same patch).
		if m, ok := lidChatMapping(v); ok {
			if err := cli.Store.LIDs.PutLIDMapping(c.ctx, m.LID, m.PN); err != nil {
				c.log.Debug("store lid chat mapping", "err", err)
			}
		}
		if f, ok := favoriteSticker(v); ok {
			c.emit(f)
		}
	case *events.AppStateSyncComplete:
		if v.Name == appstate.WAPatchCriticalUnblockLow {
			go c.emitAllContacts(cli)
		}

	case *events.GroupInfo:
		c.forgetGroup(v.JID)
		if v.Name != nil {
			name := v.Name.Name
			kind := api.ChatGroup
			c.emit(api.EvChatMeta{Patch: api.ChatPatch{JID: v.JID.ToNonAD().String(), Name: &name, Kind: &kind}})
		}
		if v.Announce != nil {
			ro := v.Announce.IsAnnounce
			if ro {
				// Admins can still post; check our own role lazily.
				go c.refreshGroupMeta(cli, v.JID)
			} else {
				c.emit(api.EvChatMeta{Patch: api.ChatPatch{JID: v.JID.ToNonAD().String(), ReadOnly: &ro}})
			}
		}
	case *events.JoinedGroup:
		c.forgetGroup(v.JID)
		c.emit(api.EvChatMeta{Patch: groupPatch(&v.GroupInfo, c.converterFor(cli))})

	case *events.Presence:
		cv := c.converterFor(cli)
		c.emit(api.EvPresence{JID: cv.canon(v.From).String(), Online: !v.Unavailable, LastSeen: unix(v.LastSeen)})
	case *events.ChatPresence:
		cv := c.converterFor(cli)
		chat := cv.chatOf(&types.MessageInfo{MessageSource: v.MessageSource})
		kind := "text"
		if v.Media == types.ChatPresenceMediaAudio {
			kind = "audio"
		}
		c.emit(api.EvTyping{
			Chat:   chat.String(),
			JID:    cv.senderOf(&types.MessageInfo{MessageSource: v.MessageSource}).String(),
			Typing: v.State == types.ChatPresenceComposing,
			Kind:   kind,
		})
	case *events.Picture:
		cv := c.converterFor(cli)
		c.emit(api.EvAvatarChanged{JID: cv.canon(v.JID).String()})
	case *events.CallOffer, *events.CallOfferNotice, *events.CallTerminate, *events.CallReject, *events.CallAccept:
		c.onCall(cli, raw)
	case *events.MediaRetry:
		c.retryMu.Lock()
		if ch := c.retries[v.MessageID]; ch != nil {
			select {
			case ch <- v:
			default:
			}
		}
		c.retryMu.Unlock()
	}
}

// markChatRead applies a mark-as-read/unread action. A full sync replays the
// latest action per chat, which may predate newer messages, so a replayed
// read leaves the unread count (from history sync) alone.
func markChatRead(p *api.ChatPatch, read, fullSync bool) {
	unread := !read
	p.MarkedUnread = &unread
	if read && !fullSync {
		zero := 0
		p.Unread = &zero
	}
}

// muteUntil converts a MuteAction end timestamp (ms, -1 = forever) to the
// api convention (unix seconds, -1 = forever).
func muteUntil(endMS int64) int64 {
	if endMS < 0 || endMS == 0 {
		return -1
	}
	if endMS > 1e14 { // far future (store.MutedForever etc.)
		return -1
	}
	return endMS / 1000
}

// favoriteSticker reads a sticker the phone (un)favourited. Lottie stickers
// are left out: the UI can't draw them.
func favoriteSticker(v *events.AppState) (api.EvFavoriteSticker, bool) {
	if len(v.Index) < 1 || v.Index[0] != appstate.IndexFavoriteSticker || v.SyncActionValue == nil {
		return api.EvFavoriteSticker{}, false
	}
	a := v.GetStickerAction()
	if a == nil || a.GetIsLottie() || len(a.GetFileEncSHA256()) != 32 || len(a.GetMediaKey()) == 0 {
		return api.EvFavoriteSticker{}, false
	}
	return api.EvFavoriteSticker{
		Favorite: a.GetIsFavorite(),
		TS:       v.GetTimestamp() / 1000,
		Width:    int(a.GetWidth()),
		Height:   int(a.GetHeight()),
		Ref: api.MediaRef{Type: api.MsgSticker, Mime: cmp.Or(a.GetMimetype(), "image/webp"),
			DirectPath: a.GetDirectPath(), URL: a.GetURL(), MediaKey: a.GetMediaKey(),
			FileEncSHA256: a.GetFileEncSHA256(), FileLength: a.GetFileLength()},
	}, true
}

func lidChatMapping(v *events.AppState) (store.LIDMapping, bool) {
	if len(v.Index) < 2 || v.Index[0] != appstate.IndexPNForLIDChat || v.SyncActionValue == nil {
		return store.LIDMapping{}, false
	}
	lid, err1 := types.ParseJID(v.Index[1])
	pn, err2 := types.ParseJID(v.GetPnForLidChatAction().GetPnJID())
	if err1 != nil || err2 != nil || lid.Server != types.HiddenUserServer || pn.Server != types.DefaultUserServer {
		return store.LIDMapping{}, false
	}
	return store.LIDMapping{LID: lid.ToNonAD(), PN: pn.ToNonAD()}, true
}

// pinTime is the api pin value: when it was pinned (unix seconds), 0 = not.
func pinTime(pinned bool, at time.Time) int64 {
	if !pinned {
		return 0
	}
	return max(at.Unix(), 1)
}

func (c *Client) emitMeta(cli *whatsmeow.Client, jid types.JID, set func(p *api.ChatPatch)) {
	cv := c.converterFor(cli)
	p := api.ChatPatch{JID: cv.canon(jid).String()}
	set(&p)
	c.emit(api.EvChatMeta{Patch: p})
}

// emitContact re-reads a contact that whatsmeow has just updated in its store.
func (c *Client) emitContact(cli *whatsmeow.Client, jid types.JID, alts ...types.JID) {
	j := c.converterFor(cli).canon(jid)
	for _, a := range alts {
		if a.Server == types.DefaultUserServer {
			j = a.ToNonAD()
		}
	}
	c.emit(api.EvContacts{Contacts: []api.Contact{c.contactFor(cli, j, append(alts, jid)...)}})
}

func (c *Client) handleMessage(cli *whatsmeow.Client, evt *events.Message) {
	cv := c.converterFor(cli)
	p := cv.convertEvent(evt)
	switch {
	case p.msg != nil:
		age := time.Since(evt.Info.Timestamp)
		live := !c.offlineSync.Load() || age < 2*time.Minute
		c.emit(api.EvMessage{Msg: p.msg, Live: live})
	case p.reaction != nil:
		c.emit(*p.reaction)
	case p.edit != nil:
		c.emit(*p.edit)
	case p.revoke != nil:
		c.emit(*p.revoke)
	}
}

func (c *Client) handleReceipt(cli *whatsmeow.Client, r *events.Receipt) {
	if ev := receiptEvent(c.converterFor(cli), r); ev != nil {
		c.emit(ev)
	}
}

// receiptEvent translates a receipt (nil = nothing to report). A read on
// another of our devices ("read", or "read-self" with receipts off) clears
// the chat's unread state.
func receiptEvent(cv *converter, r *events.Receipt) api.WAEvent {
	if len(r.MessageIDs) == 0 {
		return nil
	}
	chat := cv.chatOf(&types.MessageInfo{MessageSource: r.MessageSource})
	if r.Type == types.ReceiptTypeReadSelf || (r.IsFromMe && (r.Type == types.ReceiptTypeRead || r.Type == types.ReceiptTypePlayed)) {
		zero, f := 0, false
		return api.EvChatMeta{Patch: api.ChatPatch{JID: chat.String(), Unread: &zero, MarkedUnread: &f}}
	}
	status := receiptStatus(r)
	if status == "" || r.IsFromMe { // IsFromMe: our own devices acknowledging
		return nil
	}
	ids := make([]string, len(r.MessageIDs))
	copy(ids, r.MessageIDs)
	return api.EvReceipt{Chat: chat.String(), IDs: ids, Status: status}
}

// receiptStatus maps someone's receipt to our message's status ("" = none).
// In groups and broadcast lists a read receipt is one member's, and "read by
// everyone" would need the member list, so those messages stop at delivered.
func receiptStatus(r *events.Receipt) string {
	switch r.Type {
	case types.ReceiptTypeDelivered:
		return api.StatusDelivered
	case types.ReceiptTypeRead, types.ReceiptTypePlayed:
		if r.IsGroup {
			return api.StatusDelivered
		}
		if r.Type == types.ReceiptTypePlayed {
			return api.StatusPlayed
		}
		return api.StatusRead
	}
	return ""
}

// emitAllContacts sends the whole whatsmeow contact store as one batch
// (after the initial app state sync of the address book).
func (c *Client) emitAllContacts(cli *whatsmeow.Client) {
	all, err := cli.Store.Contacts.GetAllContacts(c.ctx)
	if err != nil {
		c.log.Warn("load contacts", "err", err)
		return
	}
	cv := c.converterFor(cli)
	seen := make(map[string]int, len(all))
	out := make([]api.Contact, 0, len(all))
	for j := range all {
		if j.Server != types.DefaultUserServer && j.Server != types.HiddenUserServer {
			continue
		}
		cj := cv.canon(j)
		ct := c.contactFor(cli, cj, j)
		if i, ok := seen[ct.JID]; ok {
			// keep the richer entry
			if out[i].FullName == "" && ct.FullName != "" {
				out[i] = ct
			}
			continue
		}
		seen[ct.JID] = len(out)
		out = append(out, ct)
	}
	if len(out) > 0 {
		c.emit(api.EvContacts{Contacts: out})
	}
}

func groupPatch(g *types.GroupInfo, cv *converter) api.ChatPatch {
	kind := api.ChatGroup
	p := api.ChatPatch{JID: g.JID.ToNonAD().String(), Kind: &kind}
	if g.Name != "" {
		name := g.Name
		p.Name = &name
	}
	ro := false
	if g.IsAnnounce {
		ro = true
		for _, pt := range g.Participants {
			if cv.isSelfParticipant(pt) && (pt.IsAdmin || pt.IsSuperAdmin) {
				ro = false
				break
			}
		}
	}
	p.ReadOnly = &ro
	return p
}

// fetchGroups emits names of all joined groups once after the first connect.
func (c *Client) fetchGroups(cli *whatsmeow.Client) {
	ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
	defer cancel()
	groups, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		c.log.Warn("fetch joined groups", "err", err)
		c.mu.Lock()
		c.groupsFetched = false // retry on next connect
		c.mu.Unlock()
		return
	}
	cv := c.converterFor(cli)
	for _, g := range groups {
		c.emit(api.EvChatMeta{Patch: groupPatch(g, cv)})
	}
}

func (c *Client) refreshGroupMeta(cli *whatsmeow.Client, jid types.JID) {
	ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
	defer cancel()
	g, err := c.groupInfo(ctx, cli, jid.ToNonAD())
	if err != nil {
		c.log.Debug("group info", "jid", jid, "err", err)
		return
	}
	c.emit(api.EvChatMeta{Patch: groupPatch(g, c.converterFor(cli))})
}
