// Package api holds the types shared by every daemon package and the JSON
// shapes of the UI <-> daemon protocol.
//
// Field names and JSON tags here ARE the wire contract. Change them only
// together with the UI.
package api

// ProtocolVersion is reported by daemon.hello. Bump on breaking changes.
const ProtocolVersion = 1

// Chat kinds.
const (
	ChatUser       = "user"
	ChatGroup      = "group"
	ChatBroadcast  = "broadcast"
	ChatNewsletter = "newsletter"
)

// Message types.
const (
	MsgText        = "text"
	MsgImage       = "image"
	MsgVideo       = "video"
	MsgGIF         = "gif"
	MsgAudio       = "audio"
	MsgVoice       = "voice" // push-to-talk voice note
	MsgDocument    = "document"
	MsgSticker     = "sticker"
	MsgLocation    = "location"
	MsgContact     = "contact"
	MsgPoll        = "poll"
	MsgSystem      = "system" // group events, security code changes, etc.
	MsgRevoked     = "revoked"
	MsgUnsupported = "unsupported"
)

// Outgoing message status. Ordered: a status only ever moves forward,
// except "error".
const (
	StatusPending   = "pending"
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusRead      = "read"
	StatusPlayed    = "played" // a voice note listened to
	StatusError     = "error"
)

// StatusRank orders statuses so receipts never move a message backwards.
func StatusRank(s string) int {
	switch s {
	case StatusError:
		return -1
	case StatusPending:
		return 0
	case StatusSent:
		return 1
	case StatusDelivered:
		return 2
	case StatusRead:
		return 3
	case StatusPlayed:
		return 4
	}
	return 0
}

// Auth states.
const (
	AuthUnpaired  = "unpaired"  // no session; call auth.startQR or auth.pairPhone
	AuthPairing   = "pairing"   // QR / pair code flow in progress
	AuthPaired    = "paired"    // logged in
	AuthLoggedOut = "loggedOut" // session revoked from the phone; must pair again
)

// Connection states (WhatsApp websocket, not the UI socket).
const (
	ConnConnecting   = "connecting"
	ConnConnected    = "connected"
	ConnDisconnected = "disconnected" // will retry
	ConnOffline      = "offline"      // no session / not trying
)

// Chat is one row of the chat list.
type Chat struct {
	JID          string       `json:"jid"`
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	Avatar       string       `json:"avatar"` // local file path if cached, else ""
	LastMessage  *MessageStub `json:"lastMessage"`
	LastActivity int64        `json:"lastActivity"` // unix seconds; sort key
	Unread       int          `json:"unread"`
	MarkedUnread bool         `json:"markedUnread"`
	Muted        bool         `json:"muted"`
	MutedUntil   int64        `json:"mutedUntil"` // unix seconds, -1 = forever, 0 = not muted
	Pinned       int64        `json:"pinned"`     // when it was pinned (unix seconds), 0 = not pinned
	Archived     bool         `json:"archived"`
	ReadOnly     bool         `json:"readOnly"` // e.g. announcement groups where we can't post
}

// MessageStub is the compact last-message preview shown in the chat list.
type MessageStub struct {
	ID         string    `json:"id"`
	Sender     string    `json:"sender"`
	SenderName string    `json:"senderName"`
	FromMe     bool      `json:"fromMe"`
	Type       string    `json:"type"`
	Preview    string    `json:"preview"` // one line, already formatted (see PreviewText)
	Timestamp  int64     `json:"timestamp"`
	Status     string    `json:"status"`
	Mentions   []Mention `json:"mentions,omitempty"`
}

// Message is one chat message as sent to the UI.
type Message struct {
	ID         string     `json:"id"`
	Chat       string     `json:"chat"`
	Sender     string     `json:"sender"`
	SenderName string     `json:"senderName"`
	FromMe     bool       `json:"fromMe"`
	Timestamp  int64      `json:"timestamp"` // unix seconds
	Type       string     `json:"type"`
	Text       string     `json:"text"` // body, or caption for media
	Status     string     `json:"status"`
	Edited     bool       `json:"edited"`
	Deleted    bool       `json:"deleted"` // revoked; Text is "" and Type is "revoked"
	Forwarded  bool       `json:"forwarded"`
	ReplyTo    *Quote     `json:"replyTo"`
	Reactions  []Reaction `json:"reactions"` // aggregated, never null on the wire (use [])
	Mentions   []Mention  `json:"mentions"`  // never null on the wire (use [])
	Media      *MediaInfo `json:"media"`
	Location   *Location  `json:"location"`

	// Daemon-internal, never serialized.
	MediaRef *MediaRef `json:"-"` // keys needed to download the media
	Thumb    []byte    `json:"-"` // embedded JPEG thumbnail, if any
	PushName string    `json:"-"` // sender's push name as seen on this message
}

// Quote is the replied-to message preview embedded in a message.
type Quote struct {
	ID         string `json:"id"`
	Sender     string `json:"sender"`
	SenderName string `json:"senderName"`
	FromMe     bool   `json:"fromMe"`
	Type       string `json:"type"`
	Preview    string `json:"preview"`
}

// Mention is someone @mentioned in a message. The text carries the mention
// as "@" and the user part of JID (see MentionText).
type Mention struct {
	JID  string `json:"jid"`
	Name string `json:"name"`
	Me   bool   `json:"-"` // us, named "You"
}

// Reaction is one aggregated emoji on a message.
type Reaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
}

// MediaInfo is the UI-visible description of a message attachment.
type MediaInfo struct {
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`     // bytes, 0 if unknown
	Width    int    `json:"width"`    // pixels, 0 if unknown
	Height   int    `json:"height"`   // pixels, 0 if unknown
	Duration int    `json:"duration"` // seconds, audio/video
	FileName string `json:"fileName"` // documents
	HasThumb bool   `json:"hasThumb"`
	Path     string `json:"path"`               // local decrypted file if already cached, else ""
	Waveform []byte `json:"waveform,omitempty"` // voice notes: levels 0-100, base64 in JSON
}

// Location payload for MsgLocation.
type Location struct {
	Lat  float64 `json:"lat"`
	Lng  float64 `json:"lng"`
	Name string  `json:"name"`
}

// MediaRef holds everything needed to download and decrypt a WhatsApp
// media blob. Persisted by the store, never sent to the UI.
type MediaRef struct {
	Chat          string
	ID            string
	Sender        string // for a media retry request
	FromMe        bool
	Type          string // message type (MsgImage, MsgVoice, ...)
	Mime          string
	DirectPath    string
	URL           string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    uint64
	FileName      string
}

// Contact is a resolved person.
type Contact struct {
	JID          string
	Name         string // best display name (address book > push name > business > phone)
	FullName     string // address book name from the phone
	PushName     string // name the person set themselves
	BusinessName string // verified business name
	Phone        string // E.164 without '+', "" for LID-only contacts
	Avatar       string // local path if cached
}

// SearchResult is one full-text search hit.
type SearchResult struct {
	Chat       string `json:"chat"`
	ChatName   string `json:"chatName"`
	ID         string `json:"id"`
	Sender     string `json:"sender"`
	SenderName string `json:"senderName"`
	FromMe     bool   `json:"fromMe"`
	Timestamp  int64  `json:"timestamp"`
	Snippet    string `json:"snippet"` // plain text, ~100 chars around the match, no markup
}

// ChatPatch is a partial update of a chat row. Nil fields are left alone.
type ChatPatch struct {
	JID          string
	Name         *string
	Kind         *string
	LastActivity *int64 // only moves forward
	Unread       *int
	MarkedUnread *bool
	MutedUntil   *int64 // 0 = unmuted, -1 = forever
	Pinned       *int64 // pin time (unix seconds), 0 = unpinned
	Archived     *bool
	ReadOnly     *bool
}

// MsgKey identifies a message for receipts.
type MsgKey struct {
	Chat   string
	ID     string
	Sender string
	FromMe bool
}

// ReplyRef is what the WA layer needs to send a quoted reply.
type ReplyRef struct {
	ID     string
	Sender string // JID of the original sender (our own JID for our messages)
	Text   string // original body/caption, used to build the quoted stub
}

// OutgoingMedia is a prepared file ready for upload (built by media.Prepare).
type OutgoingMedia struct {
	Type     string // MsgImage, MsgSticker or MsgDocument
	Mime     string
	FileName string
	Data     []byte
	Thumb    []byte // small JPEG, may be nil
	Width    int
	Height   int
	Animated bool // MsgSticker only
}

// Notification is a desktop notification request from core to notify.
type Notification struct {
	Chat     string
	ChatName string
	MsgID    string
	Sender   string // display name of the sender ("" for 1:1)
	Text     string // preview text
	IsGroup  bool
	Avatar   string // local image path, may be ""
}

type CallNotification struct {
	ID     string
	Chat   string
	Name   string
	Video  bool
	Group  bool
	Avatar string
}

// Settings keys (settings.get / settings.set).
const (
	SettingNotifications = "notifications.enabled" // bool, default true
	SettingNotifyPreview = "notifications.preview" // bool, default true (show message text)
	SettingMediaCacheMB  = "media.cacheMB"         // int, default 1024
	SettingSendReceipts  = "privacy.readReceipts"  // bool, default true
)
