package api

// Event names (daemon -> UI JSON-RPC notifications).
const (
	EAuthQR              = "auth.qr"
	EAuthState           = "auth.state"
	EConnectionState     = "connection.state"
	EMessageNew          = "message.new"
	EMessageUpdate       = "message.update"
	EChatUpdate          = "chat.update"
	EPresence            = "presence"
	ETyping              = "typing"
	EHistorySyncProgress = "historySync.progress"
	EUIOpenChat          = "ui.openChat"
	EUIQuit              = "ui.quit"
	ECallState           = "call.state"
)

type QREvent struct {
	Code    string `json:"code"`    // text to encode in the QR image
	Timeout int    `json:"timeout"` // seconds until this code expires
}

type AuthStateEvent struct {
	State    string `json:"state"`
	SelfJID  string `json:"selfJid"`
	SelfName string `json:"selfName"`
	Reason   string `json:"reason,omitempty"` // e.g. why we were logged out
}

type ConnectionStateEvent struct {
	State string `json:"state"`
}

type MessageEvent struct {
	Message Message `json:"message"`
}

type ChatEvent struct {
	Chat Chat `json:"chat"`
}

type PresenceEvent struct {
	JID      string `json:"jid"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"lastSeen"` // unix seconds, 0 if unknown/hidden
}

type TypingEvent struct {
	Chat   string `json:"chat"`
	JID    string `json:"jid"`  // who is typing (sender), equals chat for 1:1
	Name   string `json:"name"` // display name of who is typing
	Typing bool   `json:"typing"`
	Kind   string `json:"kind"` // "text" | "audio" (recording a voice note)
}

type HistorySyncEvent struct {
	Percent  int  `json:"percent"` // 0-100, -1 if unknown
	Chats    int  `json:"chats"`   // chats ingested so far
	Messages int  `json:"messages"`
	Done     bool `json:"done"`
}

type OpenChatEvent struct {
	Chat  string `json:"chat"`            // "" = just raise the window
	Token string `json:"token,omitempty"` // xdg-activation token for the window
}

type CallStateEvent struct {
	CallID    string `json:"callId"`
	Chat      string `json:"chat"` // the other side, or the group
	From      string `json:"from"` // who started the call
	Name      string `json:"name"` // the other side's name
	Incoming  bool   `json:"incoming"`
	Video     bool   `json:"video"`
	Group     bool   `json:"group"`
	State     string `json:"state"` // Call* state
	Since     int64  `json:"since"` // unix seconds the state began
	Muted     bool   `json:"muted"`
	PeerMuted bool   `json:"peerMuted"`
	Reason    string `json:"reason"` // why it ended
}

// call.state states.
const (
	CallRinging    = "ringing"
	CallConnecting = "connecting"
	CallActive     = "active"
	CallEnded      = "ended"
)

// call.state reasons, once ended.
const (
	CallMissed     = "missed"     // incoming: the caller gave up, or it rang out
	CallDeclined   = "declined"   // incoming: declined here or on another of our devices
	CallAnswered   = "answered"   // incoming: answered on another of our devices
	CallRejected   = "rejected"   // outgoing: the other side declined
	CallUnanswered = "unanswered" // outgoing: nobody answered
	CallHungUp     = "hungup"     // either side hung up after answering
	CallFailed     = "failed"     // signalling or media failed
)

// WAEvent is implemented by every event the WA layer hands to core (not on
// the wire).
type WAEvent interface{ isWAEvent() }

// EvQR: a new QR code to display during pairing.
type EvQR struct {
	Code    string
	Timeout int
}

// EvPaired: pairing succeeded, or our own name arrived (first sync after linking).
type EvPaired struct {
	JID      string
	PushName string
}

// EvLoggedOut: session revoked or invalid; must pair again.
type EvLoggedOut struct{ Reason string }

// EvConnState: websocket connection state changed.
type EvConnState struct{ State string }

// EvMessage: a new message (incoming, or sent from another of our devices).
// Live is false during offline catch-up after reconnect (no notifications
// for very old ones).
type EvMessage struct {
	Msg  *Message
	Live bool
}

// EvHistory: one history-sync batch. Chats carry names/unread/mute/pin;
// Messages are oldest-or-newest in any order; store sorts by timestamp.
type EvHistory struct {
	Chats     []ChatPatch
	Messages  []*Message
	Contacts  []Contact
	Reactions []EvReaction
	Percent   int
	Final     bool
}

// EvReceipt: delivery/read receipts for our outgoing messages.
type EvReceipt struct {
	Chat   string
	IDs    []string
	Status string
}

// EvReaction: someone (maybe us, from another device) reacted.
type EvReaction struct {
	Chat   string
	ID     string // target message id
	Sender string
	FromMe bool
	Emoji  string // "" = reaction removed
	Ts     int64
}

// EvEdit: a message was edited.
type EvEdit struct {
	Chat     string
	ID       string
	Text     string
	Mentions []Mention
	Ts       int64
}

// EvRevoke: a message was deleted for everyone.
type EvRevoke struct {
	Chat string
	ID   string
}

// EvChatMeta: chat metadata changed (app state sync from the phone, group
// rename, read on another device, ...).
type EvChatMeta struct{ Patch ChatPatch }

// EvContacts: contact names learned or changed.
type EvContacts struct{ Contacts []Contact }

// EvPresence: online/last seen of a contact.
type EvPresence struct {
	JID      string
	Online   bool
	LastSeen int64
}

// EvTyping: chat state (composing/recording/paused).
type EvTyping struct {
	Chat   string
	JID    string
	Typing bool
	Kind   string
}

// EvAvatarChanged: someone changed their profile picture; cached file is stale.
type EvAvatarChanged struct{ JID string }

// EvMediaPath: the phone re-uploaded expired media to DirectPath ("" = the
// phone no longer has it either; don't ask again).
type EvMediaPath struct {
	Chat       string
	ID         string
	DirectPath string
}

// EvCallOffer: an incoming call is ringing.
type EvCallOffer struct {
	ID    string
	Chat  string
	From  string
	Video bool
	Group bool
	Ts    int64
}

// EvCallState: a call moved on. State is a Call* state, Reason a Call* reason
// once ended.
type EvCallState struct {
	ID        string
	State     string
	Reason    string
	PeerMuted bool
}

func (EvQR) isWAEvent()            {}
func (EvPaired) isWAEvent()        {}
func (EvLoggedOut) isWAEvent()     {}
func (EvConnState) isWAEvent()     {}
func (EvMessage) isWAEvent()       {}
func (EvHistory) isWAEvent()       {}
func (EvReceipt) isWAEvent()       {}
func (EvReaction) isWAEvent()      {}
func (EvEdit) isWAEvent()          {}
func (EvRevoke) isWAEvent()        {}
func (EvChatMeta) isWAEvent()      {}
func (EvContacts) isWAEvent()      {}
func (EvPresence) isWAEvent()      {}
func (EvTyping) isWAEvent()        {}
func (EvAvatarChanged) isWAEvent() {}
func (EvMediaPath) isWAEvent()     {}
func (EvCallOffer) isWAEvent()     {}
func (EvCallState) isWAEvent()     {}
