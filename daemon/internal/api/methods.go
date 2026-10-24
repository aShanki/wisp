package api

// RPC method names (UI -> daemon).
const (
	MDaemonHello    = "daemon.hello"
	MDaemonShutdown = "daemon.shutdown"

	MAuthStatus    = "auth.status"
	MAuthStartQR   = "auth.startQR"
	MAuthPairPhone = "auth.pairPhone"
	MAuthLogout    = "auth.logout"

	MChatsList      = "chats.list"
	MChatsGet       = "chats.get"
	MChatsMarkRead  = "chats.markRead"
	MChatsSetTyping = "chats.setTyping"
	MChatsSetMuted  = "chats.setMuted"
	MChatsSetPinned = "chats.setPinned"

	MMessagesList        = "messages.list"
	MMessagesSend        = "messages.send"
	MMessagesSendMedia   = "messages.sendMedia"
	MMessagesSendSticker = "messages.sendSticker"
	MMessagesReact       = "messages.react"
	MMessagesRevoke      = "messages.revoke"
	MMessagesDeleteForMe = "messages.deleteForMe"

	MStickersList  = "stickers.list"
	MStickersFetch = "stickers.fetch"

	MMediaFetch = "media.fetch"
	MMediaThumb = "media.thumb"

	MSearch = "search"

	MAvatarsGet = "avatars.get"

	MPresenceSubscribe = "presence.subscribe"

	MGroupsMembers = "groups.members"

	MUISetActive = "ui.setActive"

	MSettingsGet = "settings.get"
	MSettingsSet = "settings.set"

	MCallsStart  = "calls.start"
	MCallsAccept = "calls.accept"
	MCallsReject = "calls.reject"
	MCallsEnd    = "calls.end"
	MCallsMute   = "calls.mute"
)

type HelloParams struct {
	Client  string `json:"client"`  // e.g. "wisp-ui"
	Version string `json:"version"` // client version string
}

type HelloResult struct {
	PID      int    `json:"pid"`      // process to wait for during an update
	Protocol int    `json:"protocol"` // ProtocolVersion
	Version  string `json:"version"`  // daemon version
	Mock     bool   `json:"mock"`
	Auth     string `json:"auth"`       // current auth state
	Conn     string `json:"connection"` // current connection state
	SelfJID  string `json:"selfJid"`
	SelfName string `json:"selfName"`
	Calls    bool   `json:"calls"` // voice calls can be placed and answered
}

type AuthStatusResult struct {
	State    string `json:"state"`
	SelfJID  string `json:"selfJid"`
	SelfName string `json:"selfName"`
	Conn     string `json:"connection"`
}

type PairPhoneParams struct {
	Phone string `json:"phone"` // international format, digits only or with '+'
}

type PairPhoneResult struct {
	Code string `json:"code"` // e.g. "ABCD-EFGH", typed on the phone
}

type ChatsListParams struct {
	Limit    int    `json:"limit"`    // default 100, max 1000
	Cursor   string `json:"cursor"`   // opaque, from previous NextCursor; "" = first page
	Archived bool   `json:"archived"` // true = only archived chats, false = only non-archived
}

type ChatsListResult struct {
	Chats      []Chat `json:"chats"`
	NextCursor string `json:"nextCursor"` // "" when no more
}

type ChatParams struct {
	Chat string `json:"chat"`
}

type SetTypingParams struct {
	Chat   string `json:"chat"`
	Typing bool   `json:"typing"`
}

type SetMutedParams struct {
	Chat  string `json:"chat"`
	Muted bool   `json:"muted"`
	Until int64  `json:"until"` // unix seconds; 0 with muted=true means forever
}

type SetPinnedParams struct {
	Chat   string `json:"chat"`
	Pinned bool   `json:"pinned"`
}

type MessagesListParams struct {
	Chat   string `json:"chat"`
	Before string `json:"before"` // message id; "" = newest page
	Limit  int    `json:"limit"`  // default 50, max 200
}

type MessagesListResult struct {
	Messages []Message `json:"messages"` // newest first
	HasMore  bool      `json:"hasMore"`  // older messages exist
}

type MessageRefParams struct {
	Chat string `json:"chat"`
	ID   string `json:"id"`
}

type ReactParams struct {
	Chat  string `json:"chat"`
	ID    string `json:"id"`
	Emoji string `json:"emoji"` // "" removes our reaction
}

type SendParams struct {
	Chat     string   `json:"chat"`
	Text     string   `json:"text"`
	ReplyTo  string   `json:"replyTo"`  // message id in the same chat, or ""
	Mentions []string `json:"mentions"` // JIDs, each written in text as "@" and its user part
}

type SendMediaParams struct {
	Chat    string `json:"chat"`
	Path    string `json:"path"` // absolute local file path
	Caption string `json:"caption"`
	ReplyTo string `json:"replyTo"`
}

type SendStickerParams struct {
	Chat    string `json:"chat"`
	Sticker string `json:"sticker"` // Sticker.ID from stickers.list
	ReplyTo string `json:"replyTo"`
}

type StickerParams struct {
	ID string `json:"id"`
}

// Sticker is one sticker in the picker: a favourite synced from the phone or
// one we sent recently.
type Sticker struct {
	ID       string `json:"id"` // hex SHA-256 of the encrypted file
	Favorite bool   `json:"favorite"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

type StickersListResult struct {
	Stickers []Sticker `json:"stickers"` // favourites first, then the most recent
}

type MessageResult struct {
	Message Message `json:"message"`
}

type PathResult struct {
	Path string `json:"path"` // "" if not available
	Mime string `json:"mime,omitempty"`
}

type SearchParams struct {
	Query string `json:"query"`
	Chat  string `json:"chat"`  // "" = all chats
	Limit int    `json:"limit"` // default 50, max 200
}

type SearchResultList struct {
	Results []SearchResult `json:"results"`
}

type JIDParams struct {
	JID string `json:"jid"`
}

type GroupMembersResult struct {
	Members []GroupMember `json:"members"`
}

// GroupMember is a participant of a group other than us.
type GroupMember struct {
	JID   string `json:"jid"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}

type UISetActiveParams struct {
	Chat    string `json:"chat"`    // chat currently open, "" for none
	Focused bool   `json:"focused"` // window is active
}

type SettingsGetResult struct {
	Settings map[string]any `json:"settings"`
}

type SettingsSetParams struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

type CallParams struct {
	CallID string `json:"callId"`
}

type CallMuteParams struct {
	CallID string `json:"callId"`
	Muted  bool   `json:"muted"`
}

type CallStartResult struct {
	CallID string `json:"callId"`
}

// OK is the result of methods that return nothing interesting.
type OK struct {
	OK bool `json:"ok"`
}
