package wa

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/reflect/protoreflect"

	"wisp/daemon/internal/api"
)

// converter turns whatsmeow messages into api types. It is a plain struct
// with function hooks so it can be unit-tested without a live client.
type converter struct {
	// pnForLID returns the phone-number JID for a LID, or the empty JID.
	pnForLID func(lid types.JID) types.JID
	// name returns the best known display name of a (canonical) user JID, or "".
	name func(jid types.JID) string
	// selfPN / selfLID are our own JIDs (non-AD); either may be empty.
	selfPN  types.JID
	selfLID types.JID
}

// parsed is the result of converting one WhatsApp message. At most one
// field is set; all nil means "ignore".
type parsed struct {
	msg      *api.Message
	reaction *api.EvReaction
	edit     *api.EvEdit
	revoke   *api.EvRevoke
}

// canon maps a JID to its canonical non-AD form: LIDs become phone JIDs
// when the mapping is known, so one person is one chat.
func (cv *converter) canon(j types.JID) types.JID {
	if j.IsEmpty() {
		return j
	}
	j = j.ToNonAD()
	if j.Server == types.HiddenUserServer {
		if cv.selfLID.User != "" && j.User == cv.selfLID.User && !cv.selfPN.IsEmpty() {
			return cv.selfPN
		}
		if cv.pnForLID != nil {
			if pn := cv.pnForLID(j); !pn.IsEmpty() {
				return pn.ToNonAD()
			}
		}
	}
	return j
}

func (cv *converter) canonStr(s string) string {
	if s == "" {
		return ""
	}
	j, err := types.ParseJID(s)
	if err != nil {
		return s
	}
	return cv.canon(j).String()
}

func (cv *converter) isSelf(j types.JID) bool {
	if j.IsEmpty() {
		return false
	}
	switch j.Server {
	case types.DefaultUserServer:
		return cv.selfPN.User != "" && j.User == cv.selfPN.User
	case types.HiddenUserServer:
		return cv.selfLID.User != "" && j.User == cv.selfLID.User
	}
	return false
}

// chatOf returns the canonical chat JID for a message.
func (cv *converter) chatOf(info *types.MessageInfo) types.JID {
	chat := info.Chat.ToNonAD()
	if chat.Server == types.HiddenUserServer && !info.IsGroup {
		if !info.IsFromMe && info.SenderAlt.Server == types.DefaultUserServer {
			return info.SenderAlt.ToNonAD()
		}
		if info.IsFromMe && info.RecipientAlt.Server == types.DefaultUserServer {
			return info.RecipientAlt.ToNonAD()
		}
	}
	return cv.canon(chat)
}

// senderOf returns the canonical sender JID for a message.
func (cv *converter) senderOf(info *types.MessageInfo) types.JID {
	if info.IsFromMe && !cv.selfPN.IsEmpty() {
		return cv.selfPN
	}
	s := info.Sender.ToNonAD()
	if s.Server == types.HiddenUserServer && info.SenderAlt.Server == types.DefaultUserServer {
		return info.SenderAlt.ToNonAD()
	}
	return cv.canon(s)
}

func chatKind(j types.JID) string {
	switch j.Server {
	case types.GroupServer:
		return api.ChatGroup
	case types.BroadcastServer:
		return api.ChatBroadcast
	case types.NewsletterServer:
		return api.ChatNewsletter
	}
	return api.ChatUser
}

// unwrap strips container messages (ephemeral, view-once, document with
// caption, edited, device-sent, ...) until the real content is reached.
// It reports whether an EditedMessage wrapper was seen.
func unwrap(m *waE2E.Message) (out *waE2E.Message, edited bool) {
	for i := 0; m != nil && i < 8; i++ {
		var inner *waE2E.Message
		switch {
		case m.GetDeviceSentMessage().GetMessage() != nil:
			inner = m.GetDeviceSentMessage().GetMessage()
		case m.GetEphemeralMessage().GetMessage() != nil:
			inner = m.GetEphemeralMessage().GetMessage()
		case m.GetViewOnceMessage().GetMessage() != nil:
			inner = m.GetViewOnceMessage().GetMessage()
		case m.GetViewOnceMessageV2().GetMessage() != nil:
			inner = m.GetViewOnceMessageV2().GetMessage()
		case m.GetViewOnceMessageV2Extension().GetMessage() != nil:
			inner = m.GetViewOnceMessageV2Extension().GetMessage()
		case m.GetDocumentWithCaptionMessage().GetMessage() != nil:
			inner = m.GetDocumentWithCaptionMessage().GetMessage()
		case m.GetEditedMessage().GetMessage() != nil:
			inner = m.GetEditedMessage().GetMessage()
			edited = true
		case m.GetBotInvokeMessage().GetMessage() != nil:
			inner = m.GetBotInvokeMessage().GetMessage()
		case m.GetLottieStickerMessage().GetMessage() != nil:
			inner = m.GetLottieStickerMessage().GetMessage()
		case m.GetGroupMentionedMessage().GetMessage() != nil:
			inner = m.GetGroupMentionedMessage().GetMessage()
		case m.GetSpoilerMessage().GetMessage() != nil:
			inner = m.GetSpoilerMessage().GetMessage()
		case m.GetAssociatedChildMessage().GetMessage() != nil:
			inner = m.GetAssociatedChildMessage().GetMessage()
		case m.GetPollCreationMessageV4().GetMessage() != nil:
			inner = m.GetPollCreationMessageV4().GetMessage()
		}
		if inner == nil {
			return m, edited
		}
		m = inner
	}
	return m, edited
}

// convertEvent converts a whatsmeow message event (live or from history
// sync via ParseWebMessage).
func (cv *converter) convertEvent(evt *events.Message) parsed {
	m := evt.Message
	if m == nil {
		m = evt.RawMessage
	}
	p := cv.convert(&evt.Info, m)
	if p.msg != nil && evt.IsEdit {
		p.msg.Edited = true
	}
	return p
}

// convert is the single message-parsing function used for live messages
// and history sync.
func (cv *converter) convert(info *types.MessageInfo, raw *waE2E.Message) parsed {
	if info.Chat.Server == types.BroadcastServer && info.Chat.User == types.StatusBroadcastJID.User {
		return parsed{} // status updates are out of scope
	}
	m, edited := unwrap(raw)
	if m == nil {
		return parsed{}
	}
	chat := cv.chatOf(info)
	sender := cv.senderOf(info)
	ts := unix(info.Timestamp)

	if pm := m.GetProtocolMessage(); pm != nil {
		target := pm.GetKey().GetID()
		switch pm.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			if target == "" {
				return parsed{}
			}
			return parsed{revoke: &api.EvRevoke{Chat: chat.String(), ID: target}}
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			if target == "" {
				return parsed{}
			}
			tmp := &api.Message{}
			eci := fillContent(tmp, pm.GetEditedMessage())
			mentions := cv.mentions(eci, &tmp.Text)
			ets := ts
			if ms := pm.GetTimestampMS(); ms > 0 {
				ets = ms / 1000
			}
			return parsed{edit: &api.EvEdit{Chat: chat.String(), ID: target, Text: tmp.Text, Mentions: mentions, Ts: ets}}
		}
		return parsed{}
	}
	if r := m.GetReactionMessage(); r != nil {
		target := r.GetKey().GetID()
		if target == "" {
			return parsed{}
		}
		rts := ts
		if ms := r.GetSenderTimestampMS(); ms > 0 {
			rts = ms / 1000
		}
		return parsed{reaction: &api.EvReaction{
			Chat: chat.String(), ID: target, Sender: sender.String(),
			FromMe: info.IsFromMe, Emoji: r.GetText(), Ts: rts,
		}}
	}

	msg := &api.Message{
		ID:        info.ID,
		Chat:      chat.String(),
		Sender:    sender.String(),
		FromMe:    info.IsFromMe,
		Timestamp: ts,
		Reactions: []api.Reaction{},
		PushName:  info.PushName,
		Edited:    edited,
	}
	ci := fillContent(msg, m)
	if msg.Type == "" {
		return parsed{} // nothing displayable (e.g. sender key distribution only)
	}
	if msg.FromMe {
		msg.Status = api.StatusSent
	} else {
		if cv.name != nil {
			msg.SenderName = cv.name(sender)
		}
		if msg.SenderName == "" {
			msg.SenderName = info.PushName
		}
	}
	if msg.MediaRef != nil {
		msg.MediaRef.Chat = msg.Chat
		msg.MediaRef.ID = msg.ID
	}
	if ci != nil {
		msg.Forwarded = ci.GetIsForwarded()
		msg.ReplyTo = cv.quote(ci, chat)
		msg.Mentions = cv.mentions(ci, &msg.Text)
	}
	return parsed{msg: msg}
}

// mentions reads whom a message @mentions, canonical like senders. Tokens
// in text follow their JID from LID to phone number.
func (cv *converter) mentions(ci *waE2E.ContextInfo, text *string) []api.Mention {
	var out []api.Mention
	for _, s := range ci.GetMentionedJID() {
		raw, err := types.ParseJID(s)
		if err != nil || raw.User == "" {
			continue
		}
		j := cv.canon(raw)
		if j.User != raw.User {
			*text = api.ReplaceMention(*text, raw.User, j.User)
		}
		if slices.ContainsFunc(out, func(m api.Mention) bool { return m.JID == j.String() }) {
			continue
		}
		name := cv.personName(raw)
		if name == "" {
			name = j.User
		}
		out = append(out, api.Mention{JID: j.String(), Name: name, Me: cv.isSelf(raw)})
	}
	return out
}

// personName names someone in text the daemon writes: "You", their contact
// name or their phone number; "" if none is known.
func (cv *converter) personName(j types.JID) string {
	if cv.isSelf(j) {
		return "You"
	}
	cj := cv.canon(j)
	if cv.name != nil {
		if n := cv.name(cj); n != "" {
			return n
		}
	}
	if cj.Server == types.DefaultUserServer {
		return "+" + cj.User
	}
	return ""
}

// quote builds the reply preview from a message's ContextInfo.
func (cv *converter) quote(ci *waE2E.ContextInfo, chat types.JID) *api.Quote {
	id := ci.GetStanzaID()
	if id == "" {
		return nil
	}
	q := &api.Quote{ID: id, Type: api.MsgText}
	var sender types.JID
	if p := ci.GetParticipant(); p != "" {
		if j, err := types.ParseJID(p); err == nil {
			sender = j
		}
	} else if chat.Server == types.DefaultUserServer || chat.Server == types.HiddenUserServer {
		sender = chat
	}
	if !sender.IsEmpty() {
		q.FromMe = cv.isSelf(sender.ToNonAD())
		if q.FromMe && !cv.selfPN.IsEmpty() {
			sender = cv.selfPN
		} else {
			sender = cv.canon(sender)
		}
		q.Sender = sender.String()
		if !q.FromMe && cv.name != nil {
			q.SenderName = cv.name(sender)
		}
	}
	if qm := ci.GetQuotedMessage(); qm != nil {
		inner, _ := unwrap(qm)
		tmp := &api.Message{}
		qci := fillContent(tmp, inner)
		tmp.Mentions = cv.mentions(qci, &tmp.Text)
		if tmp.Type != "" {
			q.Type = tmp.Type
			q.Preview = api.PreviewText(tmp)
		}
	}
	return q
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// mediaFields is the common shape of downloadable media protos.
type mediaFields interface {
	GetURL() string
	GetDirectPath() string
	GetMediaKey() []byte
	GetFileSHA256() []byte
	GetFileEncSHA256() []byte
	GetFileLength() uint64
	GetMimetype() string
}

func setMedia(msg *api.Message, typ string, f mediaFields, w, h, dur uint32, fileName string, thumb []byte) {
	msg.Type = typ
	msg.Media = &api.MediaInfo{
		Mime:     f.GetMimetype(),
		Size:     int64(f.GetFileLength()),
		Width:    int(w),
		Height:   int(h),
		Duration: int(dur),
		FileName: fileName,
		HasThumb: len(thumb) > 0,
	}
	if len(thumb) > 0 {
		msg.Thumb = thumb
	}
	if f.GetDirectPath() != "" || f.GetURL() != "" {
		msg.MediaRef = &api.MediaRef{
			Type:          typ,
			Mime:          f.GetMimetype(),
			DirectPath:    f.GetDirectPath(),
			URL:           f.GetURL(),
			MediaKey:      f.GetMediaKey(),
			FileSHA256:    f.GetFileSHA256(),
			FileEncSHA256: f.GetFileEncSHA256(),
			FileLength:    f.GetFileLength(),
			FileName:      fileName,
		}
	}
}

// fillContent sets Type, Text, Media, MediaRef, Thumb and Location from the
// (already unwrapped) message content and returns its ContextInfo. Type is
// left "" for messages that carry nothing displayable.
func fillContent(msg *api.Message, m *waE2E.Message) *waE2E.ContextInfo {
	if m == nil {
		return nil
	}
	switch {
	case m.Conversation != nil:
		msg.Type = api.MsgText
		msg.Text = m.GetConversation()
		return nil
	case m.ExtendedTextMessage != nil:
		x := m.GetExtendedTextMessage()
		msg.Type = api.MsgText
		msg.Text = x.GetText()
		return x.GetContextInfo()
	case m.ImageMessage != nil || m.InstantImageMessage != nil:
		x := m.GetImageMessage()
		if x == nil {
			x = m.GetInstantImageMessage()
		}
		setMedia(msg, api.MsgImage, x, x.GetWidth(), x.GetHeight(), 0, "", x.GetJPEGThumbnail())
		msg.Text = x.GetCaption()
		return x.GetContextInfo()
	case m.VideoMessage != nil || m.PtvMessage != nil:
		x := m.GetVideoMessage()
		if x == nil {
			x = m.GetPtvMessage()
		}
		typ := api.MsgVideo
		if x.GetGifPlayback() {
			typ = api.MsgGIF
		}
		setMedia(msg, typ, x, x.GetWidth(), x.GetHeight(), x.GetSeconds(), "", x.GetJPEGThumbnail())
		msg.Text = x.GetCaption()
		return x.GetContextInfo()
	case m.AudioMessage != nil:
		x := m.GetAudioMessage()
		typ := api.MsgAudio
		if x.GetPTT() {
			typ = api.MsgVoice
		}
		setMedia(msg, typ, x, 0, 0, x.GetSeconds(), "", nil)
		if w := x.GetWaveform(); len(w) <= 256 {
			msg.Media.Waveform = w
		}
		return x.GetContextInfo()
	case m.DocumentMessage != nil:
		x := m.GetDocumentMessage()
		name := x.GetFileName()
		if name == "" {
			name = x.GetTitle()
		}
		setMedia(msg, api.MsgDocument, x, x.GetThumbnailWidth(), x.GetThumbnailHeight(), 0, name, x.GetJPEGThumbnail())
		// Width/height of a document are those of its preview only.
		msg.Media.Width, msg.Media.Height = 0, 0
		msg.Text = x.GetCaption()
		return x.GetContextInfo()
	case m.StickerMessage != nil:
		x := m.GetStickerMessage()
		mime := x.GetMimetype()
		setMedia(msg, api.MsgSticker, x, x.GetWidth(), x.GetHeight(), 0, "", nil)
		if mime == "" {
			msg.Media.Mime = "image/webp"
			if msg.MediaRef != nil {
				msg.MediaRef.Mime = "image/webp"
			}
		}
		return x.GetContextInfo()
	case m.LocationMessage != nil:
		x := m.GetLocationMessage()
		msg.Type = api.MsgLocation
		name := x.GetName()
		if name == "" {
			name = x.GetAddress()
		}
		msg.Location = &api.Location{Lat: x.GetDegreesLatitude(), Lng: x.GetDegreesLongitude(), Name: name}
		if t := x.GetJPEGThumbnail(); len(t) > 0 {
			msg.Thumb = t
		}
		if x.GetAddress() != "" && x.GetName() != "" {
			msg.Text = x.GetAddress()
		}
		return x.GetContextInfo()
	case m.LiveLocationMessage != nil:
		x := m.GetLiveLocationMessage()
		msg.Type = api.MsgLocation
		msg.Location = &api.Location{Lat: x.GetDegreesLatitude(), Lng: x.GetDegreesLongitude(), Name: "Live location"}
		msg.Text = x.GetCaption()
		if t := x.GetJPEGThumbnail(); len(t) > 0 {
			msg.Thumb = t
		}
		return x.GetContextInfo()
	case m.ContactMessage != nil:
		x := m.GetContactMessage()
		msg.Type = api.MsgContact
		msg.Text = x.GetDisplayName()
		return x.GetContextInfo()
	case m.ContactsArrayMessage != nil:
		x := m.GetContactsArrayMessage()
		msg.Type = api.MsgContact
		msg.Text = x.GetDisplayName()
		if msg.Text == "" {
			msg.Text = fmt.Sprintf("%d contacts", len(x.GetContacts()))
		}
		return x.GetContextInfo()
	case m.PollCreationMessage != nil || m.PollCreationMessageV2 != nil || m.PollCreationMessageV3 != nil ||
		m.PollCreationMessageV5 != nil || m.PollCreationMessageV6 != nil:
		x := m.GetPollCreationMessage()
		for _, alt := range []*waE2E.PollCreationMessage{m.GetPollCreationMessageV2(), m.GetPollCreationMessageV3(), m.GetPollCreationMessageV5(), m.GetPollCreationMessageV6()} {
			if x == nil {
				x = alt
			}
		}
		msg.Type = api.MsgPoll
		var b strings.Builder
		b.WriteString(x.GetName())
		for _, o := range x.GetOptions() {
			b.WriteString("\n• ")
			b.WriteString(o.GetOptionName())
		}
		msg.Text = b.String()
		return x.GetContextInfo()
	case m.Call != nil || m.CallLogMesssage != nil:
		msg.Type = api.MsgSystem
		msg.Text = "Call"
		return nil
	}
	if isIgnorable(m) {
		return nil
	}
	msg.Type = api.MsgUnsupported
	return nil
}

// isIgnorable reports messages that carry no user-visible content.
func isIgnorable(m *waE2E.Message) bool {
	if m.SenderKeyDistributionMessage != nil ||
		m.FastRatchetKeySenderKeyDistributionMessage != nil ||
		m.ProtocolMessage != nil ||
		m.ReactionMessage != nil ||
		m.PollUpdateMessage != nil ||
		m.EncReactionMessage != nil ||
		m.KeepInChatMessage != nil ||
		m.PinInChatMessage != nil ||
		m.EncEventResponseMessage != nil ||
		m.EncCommentMessage != nil ||
		m.SecretEncryptedMessage != nil ||
		m.StickerSyncRmrMessage != nil ||
		m.PlaceholderMessage != nil ||
		m.AlbumMessage != nil ||
		m.MessageHistoryBundle != nil ||
		m.MessageHistoryNotice != nil ||
		m.GroupRootKeyShare != nil ||
		m.RootSecretDistributeMessage != nil {
		return true
	}
	n := 0
	m.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if fd.Name() != "messageContextInfo" {
			n++
		}
		return true
	})
	return n == 0
}
