package api

import (
	"context"
	"errors"
	"fmt"
)

// Backend is what the RPC server calls; core and mock implement it. Dispatch
// maps each RPC method onto one of these.
type Backend interface {
	// SetEmitter is called once by the RPC server before serving.
	SetEmitter(e Emitter)
	// ClientGone is called when a UI connection closes (clears its active chat).
	ClientGone(clientID int64)

	Hello(ctx context.Context, p HelloParams) (*HelloResult, error)
	Shutdown(ctx context.Context) error

	AuthStatus(ctx context.Context) (*AuthStatusResult, error)
	AuthStartQR(ctx context.Context) error
	AuthPairPhone(ctx context.Context, p PairPhoneParams) (*PairPhoneResult, error)
	AuthLogout(ctx context.Context) error

	ChatsList(ctx context.Context, p ChatsListParams) (*ChatsListResult, error)
	ChatsGet(ctx context.Context, p ChatParams) (*Chat, error)
	ChatsMarkRead(ctx context.Context, p ChatParams) error
	ChatsSetTyping(ctx context.Context, p SetTypingParams) error
	ChatsSetMuted(ctx context.Context, p SetMutedParams) error
	ChatsSetPinned(ctx context.Context, p SetPinnedParams) error

	MessagesList(ctx context.Context, p MessagesListParams) (*MessagesListResult, error)
	MessagesSend(ctx context.Context, p SendParams) (*Message, error)
	MessagesSendMedia(ctx context.Context, p SendMediaParams) (*Message, error)
	MessagesSendSticker(ctx context.Context, p SendStickerParams) (*Message, error)
	MessagesReact(ctx context.Context, p ReactParams) error
	// MessagesEdit changes the text of one of our text messages for everyone.
	MessagesEdit(ctx context.Context, p EditParams) error
	// MessagesRevoke deletes one of our messages for everyone.
	MessagesRevoke(ctx context.Context, p MessageRefParams) error
	// MessagesDeleteForMe removes a message here and on our other devices.
	MessagesDeleteForMe(ctx context.Context, p MessageRefParams) error

	StickersList(ctx context.Context) (*StickersListResult, error)
	StickersFetch(ctx context.Context, p StickerParams) (*PathResult, error)

	MediaFetch(ctx context.Context, p MessageRefParams) (*PathResult, error)
	MediaThumb(ctx context.Context, p MessageRefParams) (*PathResult, error)

	Search(ctx context.Context, p SearchParams) (*SearchResultList, error)

	AvatarsGet(ctx context.Context, p JIDParams) (*PathResult, error)

	PresenceSubscribe(ctx context.Context, p JIDParams) error

	GroupsMembers(ctx context.Context, p ChatParams) (*GroupMembersResult, error)

	// UISetActive: clientID comes from ClientIDFrom(ctx).
	UISetActive(ctx context.Context, p UISetActiveParams) error

	SettingsGet(ctx context.Context) (*SettingsGetResult, error)
	SettingsSet(ctx context.Context, p SettingsSetParams) error

	CallsStart(ctx context.Context, p ChatParams) (*CallStartResult, error)
	CallsAccept(ctx context.Context, p CallParams) error
	CallsReject(ctx context.Context, p CallParams) error
	CallsEnd(ctx context.Context, p CallParams) error
	CallsMute(ctx context.Context, p CallMuteParams) error
}

// Emitter broadcasts an event to every connected UI client. Implemented by
// the RPC server. Must be safe for concurrent use and must never block on a
// slow client. params is marshalled to JSON (use the *Event structs).
type Emitter interface {
	Emit(event string, params any)
}

type ctxKey int

const clientIDKey ctxKey = 1

// WithClientID tags a request context with the UI connection it came from.
func WithClientID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, clientIDKey, id)
}

// ClientIDFrom returns the connection id set by the RPC server (0 if none).
func ClientIDFrom(ctx context.Context) int64 {
	v, _ := ctx.Value(clientIDKey).(int64)
	return v
}

// Store is implemented by internal/store (SQLite, wisp.db).
type Store interface {
	Close() error

	// Chats.
	UpsertChat(ctx context.Context, p ChatPatch) error
	GetChat(ctx context.Context, jid string) (*Chat, error) // ErrNotFound if missing
	ListChats(ctx context.Context, p ChatsListParams) (*ChatsListResult, error)

	// PutMessage inserts or replaces one message (matched by chat+id), bumps
	// the chat's lastActivity/lastMessage if newer, creates the chat row if
	// missing, and increments unread when incUnread is true.
	PutMessage(ctx context.Context, m *Message, incUnread bool) error
	// PutHistory ingests a history-sync batch in ONE transaction: chat
	// patches, messages (never bumping unread; unread comes from patches),
	// contacts and reactions. Must handle tens of thousands of rows fast.
	PutHistory(ctx context.Context, h *EvHistory) error

	GetMessage(ctx context.Context, chat, id string) (*Message, error) // ErrNotFound
	// ListMessages returns up to limit messages older than beforeID (or the
	// newest if beforeID == ""), newest first, plus whether older ones exist.
	ListMessages(ctx context.Context, chat, beforeID string, limit int) ([]Message, bool, error)

	// Mutations return the updated message so core can emit message.update.
	UpdateStatus(ctx context.Context, chat string, ids []string, status string) ([]*Message, error) // never downgrades
	ApplyEdit(ctx context.Context, chat, id, text string, mentions []Mention, ts int64) (*Message, error)
	ApplyRevoke(ctx context.Context, chat, id string) (*Message, error)
	ApplyReaction(ctx context.Context, r EvReaction) (*Message, error)
	// DeleteMessage removes a message, its media row and reactions, and
	// repoints the chat's last message.
	DeleteMessage(ctx context.Context, chat, id string) error

	MarkRead(ctx context.Context, chat string) error // unread = 0, markedUnread = false
	// UnreadIncoming lists incoming messages after the last one we marked
	// read (for sending read receipts). Up to limit, oldest first.
	UnreadIncoming(ctx context.Context, chat string, limit int) ([]MsgKey, error)

	Search(ctx context.Context, p SearchParams) ([]SearchResult, error)

	GetMediaRef(ctx context.Context, chat, id string) (*MediaRef, []byte, error) // ref, embedded thumb; ErrNotFound
	SetMediaPath(ctx context.Context, chat, id, path string) error               // cached decrypted file
	SetMediaDirectPath(ctx context.Context, chat, id, path string) error         // after a media retry; clears the URL

	// Stickers: favourites (from the phone's app state) and recent sticker messages.
	ListStickers(ctx context.Context, recent int) ([]Sticker, error)       // favourites, then up to `recent` others
	StickerRef(ctx context.Context, id string) (*MediaRef, Sticker, error) // ErrNotFound
	PutFavoriteSticker(ctx context.Context, f EvFavoriteSticker) error     // or removes it when !f.Favorite

	PutContacts(ctx context.Context, cs []Contact) error // merge non-empty fields
	GetContact(ctx context.Context, jid string) (*Contact, error)
	SetAvatar(ctx context.Context, jid, path string) error // updates chats+contacts avatar column

	SetSetting(ctx context.Context, key, jsonValue string) error
	AllSettings(ctx context.Context) (map[string]string, error)

	// Wipe deletes all account data (after logout), keeping settings.
	Wipe(ctx context.Context) error
}

// Downloader fetches and decrypts a media blob (implemented by internal/wa).
type Downloader interface {
	Download(ctx context.Context, ref *MediaRef) ([]byte, error)
}

// AvatarSource resolves a profile picture URL (implemented by internal/wa).
// id changes whenever the picture changes; url is fetched with plain HTTP GET.
// Returns ErrNotFound if the person has no (visible) picture.
type AvatarSource interface {
	AvatarURL(ctx context.Context, jid string) (url, id string, err error)
}

// Media is implemented by internal/media (disk cache under data/media).
type Media interface {
	// Fetch returns the path of the decrypted file, downloading via the
	// Downloader on a cache miss. Concurrent calls for the same ref share
	// one download.
	Fetch(ctx context.Context, ref *MediaRef) (string, error)
	// Thumb returns a small JPEG path for the message: from the embedded
	// thumbnail bytes if given, else generated from the cached full file,
	// else ErrNotFound.
	Thumb(ctx context.Context, ref *MediaRef, embedded []byte) (string, error)
	// Avatar returns a cached avatar path for jid, refreshing via
	// AvatarSource when missing or when refresh is true. ErrNotFound if none.
	Avatar(ctx context.Context, jid string, refresh bool) (string, error)
	// Prepare reads a local file for sending: detects type/mime, reads
	// dimensions, makes a thumbnail.
	Prepare(ctx context.Context, path string) (*OutgoingMedia, error)
	// Put stores bytes we already have (e.g. our own sent media) under ref.
	Put(ctx context.Context, ref *MediaRef, data []byte) (string, error)
	// SetCap changes the LRU size cap in bytes and evicts if needed.
	SetCap(bytes int64)
	// Wipe deletes every cached file (after logout).
	Wipe() error
}

// WA is implemented by internal/wa (whatsmeow).
type WA interface {
	Downloader
	AvatarSource

	// SetEventHandler must be called before Connect. Handlers run on the
	// WA layer's goroutines; the handler must not block for long.
	SetEventHandler(h func(WAEvent))
	// Connect connects if a session exists (auto-reconnects with backoff
	// afterwards). If there is no session it returns nil and stays offline
	// until StartQR/PairPhone.
	Connect(ctx context.Context) error
	Disconnect()
	IsLoggedIn() bool
	SelfJID() string
	SelfName() string
	AuthState() string // Auth* constant
	ConnState() string // Conn* constant

	StartQR(ctx context.Context) error // emits EvQR repeatedly until paired or timed out
	PairPhone(ctx context.Context, phone string) (string, error)
	Logout(ctx context.Context) error

	// NewMessageID generates a WhatsApp message id. core uses it to store
	// and emit a "pending" message before calling SendText/SendMedia.
	NewMessageID() string
	// Sends block until the server acks; SendMedia returns the upload's
	// MediaRef so our own media can be downloaded again. On error the caller
	// marks it StatusError. mentions are JIDs written in text as "@<user>".
	SendText(ctx context.Context, chat, id, text string, reply *ReplyRef, mentions []string) error
	SendMedia(ctx context.Context, chat, id string, m *OutgoingMedia, caption string, reply *ReplyRef, mentions []string) (*MediaRef, error)
	MarkRead(ctx context.Context, keys []MsgKey, private bool) error // private: read-self, no blue ticks
	SetTyping(ctx context.Context, chat string, typing bool) error
	SubscribePresence(ctx context.Context, jid string) error
	SetOnline(ctx context.Context, online bool) error
	SetMuted(ctx context.Context, chat string, until int64) error // 0 unmute, -1 forever
	// React, Revoke and DeleteForMe act on the message key m.Chat/m.ID;
	// m.Sender and m.FromMe say whose message it is.
	React(ctx context.Context, m *Message, emoji string) error
	Revoke(ctx context.Context, m *Message) error
	// Edit replaces the text of our message m; mentions as in SendText.
	Edit(ctx context.Context, m *Message, text string, mentions []string) error
	DeleteForMe(ctx context.Context, m *Message) error
	SetPinned(ctx context.Context, chat string, pinned bool) error
	// ResyncAppState fetches the chat settings (pin, mute, archive) and
	// favourite stickers from scratch, re-emitting them. It does nothing
	// before the first sync.
	ResyncAppState(ctx context.Context) error

	ResolveContact(ctx context.Context, jid string) (*Contact, error) // from whatsmeow's contact store
	// GroupMembers lists a group's participants other than us, canonical
	// like senders. Names are left to the caller.
	GroupMembers(ctx context.Context, chat string) ([]GroupMember, error)

	// Calls: 1:1 voice only. StartCall returns once the offer is sent; progress
	// arrives as EvCallState. RejectCall declines an incoming call still ringing.
	CallsAvailable() bool
	StartCall(ctx context.Context, chat string) (string, error)
	AcceptCall(ctx context.Context, id string) error
	RejectCall(ctx context.Context, id string) error
	EndCall(ctx context.Context, id string) error
	MuteCall(id string, muted bool) error
}

// NotifyActions are callbacks from notification buttons (set by core).
type NotifyActions struct {
	Open     func(chat string)
	Reply    func(chat, msgID, text string)
	MarkRead func(chat string)
	Decline  func(callID string)
}

// Notifier is implemented by internal/notify (freedesktop D-Bus).
type Notifier interface {
	SetActions(a NotifyActions)
	// Notify shows or replaces the notification for n.Chat (one per chat,
	// newest message wins; body may summarize "3 new messages").
	Notify(ctx context.Context, n Notification) error
	// Clear closes any notification shown for chat.
	Clear(chat string)
	// NotifyCall shows a ringing call, with Decline, until ClearCall.
	NotifyCall(ctx context.Context, c CallNotification) error
	ClearCall(id string)
	ClearAll() // e.g. on logout
	Close() error
}

// Errors backends return (wrapped is fine); the RPC server maps them to
// JSON-RPC error codes.

var (
	ErrNotFound      = errors.New("not found")
	ErrNotLoggedIn   = errors.New("not logged in")
	ErrNotConnected  = errors.New("not connected")
	ErrMediaExpired  = errors.New("media no longer available")
	ErrUnsupported   = errors.New("not supported")
	ErrInvalidParams = errors.New("invalid params")
	ErrPinLimit      = errors.New("only 3 chats can be pinned") // WhatsApp's limit
)

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeNotLoggedIn    = 1001
	CodeNotFound       = 1002
	CodeNotConnected   = 1003
	CodeMediaExpired   = 1004
	CodeUnsupported    = 1005
)

// ErrorCode maps an error to a JSON-RPC error code.
func ErrorCode(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return CodeNotFound
	case errors.Is(err, ErrNotLoggedIn):
		return CodeNotLoggedIn
	case errors.Is(err, ErrNotConnected):
		return CodeNotConnected
	case errors.Is(err, ErrMediaExpired):
		return CodeMediaExpired
	case errors.Is(err, ErrUnsupported):
		return CodeUnsupported
	case errors.Is(err, ErrInvalidParams), errors.Is(err, ErrPinLimit):
		return CodeInvalidParams
	}
	return CodeInternal
}

// InvalidParams wraps ErrInvalidParams with a message.
func InvalidParams(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidParams, fmt.Sprintf(format, a...))
}
