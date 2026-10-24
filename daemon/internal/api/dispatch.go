package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// Dispatch decodes params for method and calls the matching Backend method.
// It is the single source of truth for the method table; the RPC server
// only frames requests/responses around it.
// Returns (result, error); result is JSON-marshalled by the caller.
// Unknown methods return ErrMethodNotFound.
func Dispatch(ctx context.Context, b Backend, method string, raw json.RawMessage) (any, error) {
	h, ok := handlers[method]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMethodNotFound, method)
	}
	return h(ctx, b, raw)
}

// ErrMethodNotFound is returned by Dispatch for unknown methods.
var ErrMethodNotFound = fmt.Errorf("method not found")

type handler func(ctx context.Context, b Backend, raw json.RawMessage) (any, error)

func decode[T any](raw json.RawMessage) (T, error) {
	var p T
	if len(raw) == 0 || string(raw) == "null" {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, InvalidParams("%v", err)
	}
	return p, nil
}

func with[P any, R any](f func(Backend, context.Context, P) (R, error)) handler {
	return func(ctx context.Context, b Backend, raw json.RawMessage) (any, error) {
		p, err := decode[P](raw)
		if err != nil {
			return nil, err
		}
		return f(b, ctx, p)
	}
}

func withOK[P any](f func(Backend, context.Context, P) error) handler {
	return func(ctx context.Context, b Backend, raw json.RawMessage) (any, error) {
		p, err := decode[P](raw)
		if err != nil {
			return nil, err
		}
		if err := f(b, ctx, p); err != nil {
			return nil, err
		}
		return OK{OK: true}, nil
	}
}

func noParams[R any](f func(Backend, context.Context) (R, error)) handler {
	return func(ctx context.Context, b Backend, _ json.RawMessage) (any, error) { return f(b, ctx) }
}

func noParamsOK(f func(Backend, context.Context) error) handler {
	return func(ctx context.Context, b Backend, _ json.RawMessage) (any, error) {
		if err := f(b, ctx); err != nil {
			return nil, err
		}
		return OK{OK: true}, nil
	}
}

func requireChat(chat string) error {
	if chat == "" {
		return InvalidParams("chat is required")
	}
	return nil
}

func requireRef(chat, id string) error {
	if err := requireChat(chat); err != nil {
		return err
	}
	if id == "" {
		return InvalidParams("id is required")
	}
	return nil
}

var handlers = map[string]handler{
	MDaemonHello:    with(Backend.Hello),
	MDaemonShutdown: noParamsOK(Backend.Shutdown),

	MAuthStatus:    noParams(Backend.AuthStatus),
	MAuthStartQR:   noParamsOK(Backend.AuthStartQR),
	MAuthPairPhone: with(Backend.AuthPairPhone),
	MAuthLogout:    noParamsOK(Backend.AuthLogout),

	MChatsList: with(func(b Backend, ctx context.Context, p ChatsListParams) (*ChatsListResult, error) {
		if p.Limit <= 0 {
			p.Limit = 100
		}
		if p.Limit > 1000 {
			p.Limit = 1000
		}
		return b.ChatsList(ctx, p)
	}),
	MChatsGet: with(func(b Backend, ctx context.Context, p ChatParams) (*Chat, error) {
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		return b.ChatsGet(ctx, p)
	}),
	MChatsMarkRead: withOK(func(b Backend, ctx context.Context, p ChatParams) error {
		if err := requireChat(p.Chat); err != nil {
			return err
		}
		return b.ChatsMarkRead(ctx, p)
	}),
	MChatsSetTyping: withOK(Backend.ChatsSetTyping),
	MChatsSetMuted:  withOK(Backend.ChatsSetMuted),
	MChatsSetPinned: withOK(func(b Backend, ctx context.Context, p SetPinnedParams) error {
		if err := requireChat(p.Chat); err != nil {
			return err
		}
		return b.ChatsSetPinned(ctx, p)
	}),

	MMessagesList: with(func(b Backend, ctx context.Context, p MessagesListParams) (*MessagesListResult, error) {
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		if p.Limit <= 0 {
			p.Limit = 50
		}
		if p.Limit > 200 {
			p.Limit = 200
		}
		return b.MessagesList(ctx, p)
	}),
	MMessagesSend: with(func(b Backend, ctx context.Context, p SendParams) (*MessageResult, error) {
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		if p.Text == "" {
			return nil, InvalidParams("text is required")
		}
		m, err := b.MessagesSend(ctx, p)
		if err != nil {
			return nil, err
		}
		return &MessageResult{Message: *m}, nil
	}),
	MMessagesSendMedia: with(func(b Backend, ctx context.Context, p SendMediaParams) (*MessageResult, error) {
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		if p.Path == "" {
			return nil, InvalidParams("path is required")
		}
		m, err := b.MessagesSendMedia(ctx, p)
		if err != nil {
			return nil, err
		}
		return &MessageResult{Message: *m}, nil
	}),

	MMessagesSendSticker: with(func(b Backend, ctx context.Context, p SendStickerParams) (*MessageResult, error) {
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		if p.Sticker == "" {
			return nil, InvalidParams("sticker is required")
		}
		m, err := b.MessagesSendSticker(ctx, p)
		if err != nil {
			return nil, err
		}
		return &MessageResult{Message: *m}, nil
	}),

	MMessagesReact: withOK(func(b Backend, ctx context.Context, p ReactParams) error {
		if err := requireRef(p.Chat, p.ID); err != nil {
			return err
		}
		return b.MessagesReact(ctx, p)
	}),
	MMessagesRevoke: withOK(func(b Backend, ctx context.Context, p MessageRefParams) error {
		if err := requireRef(p.Chat, p.ID); err != nil {
			return err
		}
		return b.MessagesRevoke(ctx, p)
	}),
	MMessagesDeleteForMe: withOK(func(b Backend, ctx context.Context, p MessageRefParams) error {
		if err := requireRef(p.Chat, p.ID); err != nil {
			return err
		}
		return b.MessagesDeleteForMe(ctx, p)
	}),

	MStickersList: noParams(Backend.StickersList),
	MStickersFetch: with(func(b Backend, ctx context.Context, p StickerParams) (*PathResult, error) {
		if p.ID == "" {
			return nil, InvalidParams("id is required")
		}
		return b.StickersFetch(ctx, p)
	}),

	MMediaFetch: with(Backend.MediaFetch),
	MMediaThumb: with(Backend.MediaThumb),

	MSearch: with(func(b Backend, ctx context.Context, p SearchParams) (*SearchResultList, error) {
		if p.Limit <= 0 {
			p.Limit = 50
		}
		if p.Limit > 200 {
			p.Limit = 200
		}
		return b.Search(ctx, p)
	}),

	MAvatarsGet: with(Backend.AvatarsGet),

	MPresenceSubscribe: withOK(Backend.PresenceSubscribe),

	MGroupsMembers: with(func(b Backend, ctx context.Context, p ChatParams) (*GroupMembersResult, error) {
		if err := requireChat(p.Chat); err != nil {
			return nil, err
		}
		return b.GroupsMembers(ctx, p)
	}),

	MUISetActive: withOK(Backend.UISetActive),

	MSettingsGet: noParams(Backend.SettingsGet),
	MSettingsSet: withOK(Backend.SettingsSet),

	MCallsStart: with(func(b Backend, ctx context.Context, p ChatParams) (*CallStartResult, error) {
		if p.Chat == "" {
			return nil, InvalidParams("chat is required")
		}
		return b.CallsStart(ctx, p)
	}),
	MCallsAccept: withOK(callID(Backend.CallsAccept)),
	MCallsReject: withOK(callID(Backend.CallsReject)),
	MCallsEnd:    withOK(callID(Backend.CallsEnd)),
	MCallsMute: withOK(func(b Backend, ctx context.Context, p CallMuteParams) error {
		if p.CallID == "" {
			return InvalidParams("callId is required")
		}
		return b.CallsMute(ctx, p)
	}),
}

func callID(f func(Backend, context.Context, CallParams) error) func(Backend, context.Context, CallParams) error {
	return func(b Backend, ctx context.Context, p CallParams) error {
		if p.CallID == "" {
			return InvalidParams("callId is required")
		}
		return f(b, ctx, p)
	}
}
