package wa

import (
	"math"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
)

// webParser parses a history WebMessageInfo into a message event
// (whatsmeow.Client.ParseWebMessage in production).
type webParser func(chat types.JID, wm *waWeb.WebMessageInfo) (*events.Message, error)

func (c *Client) convertHistory(cli *whatsmeow.Client, cv *converter, data *waHistorySync.HistorySync) *api.EvHistory {
	if cli != nil && data != nil {
		// whatsmeow stores the blob's PN/LID mappings and push names in the
		// background, racing this conversion; store the mappings first so
		// LID senders resolve. Push names fall back to the blob below.
		if err := cli.Store.LIDs.PutManyLIDMappings(c.ctx, historyLIDPairs(data)); err != nil {
			c.log.Debug("history: store lid mappings", "err", err)
		}
	}
	h := convertHistory(cv, cli.ParseWebMessage, data, time.Now())
	if h != nil && cli != nil {
		// Enrich push names with address-book names whatsmeow may know.
		for i := range h.Contacts {
			j, err := types.ParseJID(h.Contacts[i].JID)
			if err != nil {
				continue
			}
			ct := c.contactFor(cli, j)
			if ct.PushName == "" {
				ct.PushName = h.Contacts[i].PushName
			}
			if ct.Name == "" || (ct.FullName == "" && ct.PushName != "") {
				ct.Name = ct.PushName
			}
			h.Contacts[i] = ct
		}
	}
	return h
}

// historyLIDPairs collects a blob's LID/PN pairs: its mapping table plus
// each conversation's own pair. Invalid pairs are dropped by the store.
func historyLIDPairs(data *waHistorySync.HistorySync) []store.LIDMapping {
	norm := func(s string) types.JID {
		j, _ := types.ParseJID(s)
		j = j.ToNonAD()
		if j.Server == types.LegacyUserServer {
			j.Server = types.DefaultUserServer
		}
		return j
	}
	var pairs []store.LIDMapping
	for _, m := range data.GetPhoneNumberToLidMappings() {
		pairs = append(pairs, store.LIDMapping{LID: norm(m.GetLidJID()), PN: norm(m.GetPnJID())})
	}
	for _, conv := range data.GetConversations() {
		id := norm(conv.GetID())
		lid, pn := norm(conv.GetLidJID()), norm(conv.GetPnJID())
		switch id.Server {
		case types.HiddenUserServer:
			lid = id
		case types.DefaultUserServer:
			pn = id
		}
		if !lid.IsEmpty() && !pn.IsEmpty() {
			pairs = append(pairs, store.LIDMapping{LID: lid, PN: pn})
		}
	}
	return pairs
}

func webStatus(s waWeb.WebMessageInfo_Status) string {
	switch s {
	case waWeb.WebMessageInfo_ERROR:
		return api.StatusError
	case waWeb.WebMessageInfo_PENDING:
		return api.StatusPending
	case waWeb.WebMessageInfo_SERVER_ACK:
		return api.StatusSent
	case waWeb.WebMessageInfo_DELIVERY_ACK:
		return api.StatusDelivered
	case waWeb.WebMessageInfo_READ, waWeb.WebMessageInfo_PLAYED:
		return api.StatusRead
	}
	return api.StatusSent
}

// historyMuteUntil converts a conversation's muteEndTime (unix seconds;
// huge values mean forever) to the api convention.
func historyMuteUntil(v uint64, now time.Time) int64 {
	if v == 0 {
		return 0
	}
	if v > math.MaxInt64/2 || int64(v) > now.AddDate(50, 0, 0).Unix() {
		return -1
	}
	if int64(v) < now.Unix() {
		return 0 // expired
	}
	return int64(v)
}

// convertHistory turns one history-sync blob into an api.EvHistory.
func convertHistory(cv *converter, parse webParser, data *waHistorySync.HistorySync, now time.Time) *api.EvHistory {
	if data == nil {
		return nil
	}
	h := &api.EvHistory{Percent: -1}
	if p := data.GetProgress(); p > 0 {
		h.Percent = int(p)
		h.Final = p >= 100
	}
	syncType := data.GetSyncType()
	withMeta := syncType == waHistorySync.HistorySync_INITIAL_BOOTSTRAP ||
		syncType == waHistorySync.HistorySync_RECENT ||
		syncType == waHistorySync.HistorySync_FULL

	for _, conv := range data.GetConversations() {
		rawChat, err := types.ParseJID(conv.GetID())
		if err != nil || rawChat.IsEmpty() {
			continue
		}
		if rawChat.Server == types.BroadcastServer && rawChat.User == types.StatusBroadcastJID.User {
			continue
		}
		chat := cv.canon(rawChat)
		if chat.Server == types.HiddenUserServer {
			if pn, err := types.ParseJID(conv.GetPnJID()); err == nil && pn.Server == types.DefaultUserServer {
				chat = pn.ToNonAD()
			}
		}
		chatStr := chat.String()
		kind := chatKind(chat)
		patch := api.ChatPatch{JID: chatStr, Kind: &kind}
		name := conv.GetName()
		if name == "" && kind == api.ChatGroup {
			name = conv.GetDisplayName()
		}
		if name != "" {
			patch.Name = &name
		}
		if ts := conv.GetConversationTimestamp(); ts > 0 {
			v := int64(ts)
			patch.LastActivity = &v
		} else if ts := conv.GetLastMsgTimestamp(); ts > 0 {
			v := int64(ts)
			patch.LastActivity = &v
		}
		if withMeta {
			patch.Unread = new(int(conv.GetUnreadCount()))
			// App state (and group info) own the rest and may have arrived
			// first: history only adds what it knows, it never clears.
			if conv.GetMarkedAsUnread() {
				patch.MarkedUnread = new(true)
			}
			if m := historyMuteUntil(conv.GetMuteEndTime(), now); m != 0 {
				patch.MutedUntil = &m
			}
			if p := conv.GetPinned(); p > 0 {
				patch.Pinned = new(int64(p))
			}
			if conv.GetArchived() {
				patch.Archived = new(true)
			}
			if conv.GetReadOnly() {
				patch.ReadOnly = new(true)
			}
		}
		h.Chats = append(h.Chats, patch)

		for _, hm := range conv.GetMessages() {
			wm := hm.GetMessage()
			if wm == nil || wm.GetKey() == nil {
				continue
			}
			evt, err := parse(rawChat, wm)
			if err != nil || evt == nil {
				continue
			}
			var p parsed
			if evt.Message == nil && evt.RawMessage == nil {
				p = cv.stubMessage(&evt.Info, wm)
			} else {
				p = cv.convertEvent(evt)
			}
			if p.msg != nil {
				p.msg.Chat = chatStr
				if p.msg.MediaRef != nil {
					p.msg.MediaRef.Chat = chatStr
				}
				if p.msg.FromMe {
					p.msg.Status = webStatus(wm.GetStatus())
				}
				h.Messages = append(h.Messages, p.msg)
			} else if p.reaction != nil {
				p.reaction.Chat = chatStr
				h.Reactions = append(h.Reactions, *p.reaction)
			}
			for _, r := range wm.GetReactions() {
				if ev, ok := cv.historyReaction(chat, rawChat, wm, r); ok {
					h.Reactions = append(h.Reactions, ev)
				}
			}
		}
	}

	for _, pn := range data.GetPushnames() {
		j, err := types.ParseJID(pn.GetID())
		if err != nil || pn.GetPushname() == "" || pn.GetPushname() == "-" {
			continue
		}
		if j.Server != types.DefaultUserServer && j.Server != types.HiddenUserServer {
			continue
		}
		cj := cv.canon(j)
		ct := api.Contact{JID: cj.String(), PushName: pn.GetPushname(), Name: pn.GetPushname()}
		if cj.Server == types.DefaultUserServer {
			ct.Phone = cj.User
		}
		h.Contacts = append(h.Contacts, ct)
	}

	if len(h.Chats) == 0 && len(h.Messages) == 0 && len(h.Contacts) == 0 && len(h.Reactions) == 0 && h.Percent < 0 {
		return nil
	}
	return h
}

// historyReaction converts a reaction attached to a history message.
func (cv *converter) historyReaction(chat, rawChat types.JID, target *waWeb.WebMessageInfo, r *waWeb.Reaction) (api.EvReaction, bool) {
	key := r.GetKey()
	if key == nil || target.GetKey().GetID() == "" {
		return api.EvReaction{}, false
	}
	ev := api.EvReaction{
		Chat:   chat.String(),
		ID:     target.GetKey().GetID(),
		FromMe: key.GetFromMe(),
		Emoji:  r.GetText(),
		Ts:     r.GetSenderTimestampMS() / 1000,
	}
	switch {
	case ev.FromMe && !cv.selfPN.IsEmpty():
		ev.Sender = cv.selfPN.String()
	case key.GetParticipant() != "":
		ev.Sender = cv.canonStr(key.GetParticipant())
	case rawChat.Server == types.DefaultUserServer || rawChat.Server == types.HiddenUserServer:
		ev.Sender = chat.String()
	default:
		ev.Sender = cv.canonStr(key.GetRemoteJID())
	}
	return ev, true
}

// stubMessage converts a history message without content (WhatsApp "stub"
// such as revoked messages or group events) into a system/revoked message.
func (cv *converter) stubMessage(info *types.MessageInfo, wm *waWeb.WebMessageInfo) parsed {
	msg := &api.Message{
		ID:        info.ID,
		Chat:      cv.chatOf(info).String(),
		Sender:    cv.senderOf(info).String(),
		FromMe:    info.IsFromMe,
		Timestamp: unix(info.Timestamp),
		Reactions: []api.Reaction{},
		Type:      api.MsgSystem,
	}
	params := wm.GetMessageStubParameters()
	names := func() string {
		out := make([]string, 0, len(params))
		for _, p := range params {
			n := ""
			if j, err := types.ParseJID(p); err == nil && j.User != "" {
				n = cv.personName(j)
			}
			if n == "" {
				n = p
			}
			out = append(out, n)
		}
		return strings.Join(out, ", ")
	}
	actor := "Someone"
	if info.IsFromMe {
		actor = "You"
	} else if cv.name != nil {
		if n := cv.name(cv.senderOf(info)); n != "" {
			actor = n
		} else if info.PushName != "" {
			actor = info.PushName
		}
	}
	switch wm.GetMessageStubType() {
	case waWeb.WebMessageInfo_REVOKE, waWeb.WebMessageInfo_ADMIN_REVOKE:
		msg.Type = api.MsgRevoked
		msg.Deleted = true
	case waWeb.WebMessageInfo_GROUP_CREATE:
		msg.Text = actor + " created this group"
	case waWeb.WebMessageInfo_GROUP_CHANGE_SUBJECT:
		subj := ""
		if len(params) > 0 {
			subj = params[0]
		}
		msg.Text = actor + " changed the group name to \"" + subj + "\""
	case waWeb.WebMessageInfo_GROUP_CHANGE_ICON:
		msg.Text = actor + " changed the group icon"
	case waWeb.WebMessageInfo_GROUP_CHANGE_DESCRIPTION:
		msg.Text = actor + " changed the group description"
	case waWeb.WebMessageInfo_GROUP_PARTICIPANT_ADD:
		msg.Text = actor + " added " + names()
	case waWeb.WebMessageInfo_GROUP_PARTICIPANT_REMOVE:
		msg.Text = actor + " removed " + names()
	case waWeb.WebMessageInfo_GROUP_PARTICIPANT_LEAVE:
		msg.Text = names() + " left"
	case waWeb.WebMessageInfo_GROUP_PARTICIPANT_INVITE:
		msg.Text = names() + " joined using an invite link"
	case waWeb.WebMessageInfo_GROUP_PARTICIPANT_PROMOTE:
		msg.Text = names() + " is now an admin"
	case waWeb.WebMessageInfo_GROUP_PARTICIPANT_DEMOTE:
		msg.Text = names() + " is no longer an admin"
	case waWeb.WebMessageInfo_CALL_MISSED_VOICE:
		msg.Text = "Missed voice call"
	case waWeb.WebMessageInfo_CALL_MISSED_VIDEO:
		msg.Text = "Missed video call"
	default:
		return parsed{}
	}
	return parsed{msg: msg}
}
