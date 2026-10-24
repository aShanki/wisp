package core

import (
	"cmp"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"wisp/daemon/internal/api"
)

func (c *Core) ChatsList(ctx context.Context, p api.ChatsListParams) (*api.ChatsListResult, error) {
	return c.store.ListChats(ctx, p)
}

func (c *Core) ChatsGet(ctx context.Context, p api.ChatParams) (*api.Chat, error) {
	return c.store.GetChat(ctx, p.Chat)
}

func (c *Core) ChatsMarkRead(ctx context.Context, p api.ChatParams) error {
	return c.markRead(ctx, p.Chat, true)
}

// markRead sends read receipts for unread incoming messages (private ones,
// without blue ticks, if privacy.readReceipts is off), zeroes unread, clears
// the notification and optionally emits chat.update. Receipts that fail
// (e.g. reconnecting) are sent again on the next connect.
func (c *Core) markRead(ctx context.Context, chat string, emit bool) error {
	keys, err := c.store.UnreadIncoming(ctx, chat, 500)
	if err != nil && !errors.Is(err, api.ErrNotFound) {
		return err
	}
	if len(keys) > 0 && c.wa.IsLoggedIn() {
		if err := c.wa.MarkRead(ctx, keys, !c.settingBool(ctx, api.SettingSendReceipts)); err != nil {
			c.log.Warn("core: send read receipts failed", "chat", chat, "n", len(keys), "err", err)
			c.unsentMu.Lock()
			c.unsent = append(c.unsent, keys...)
			c.unsentMu.Unlock()
		}
	}
	if err := c.store.MarkRead(ctx, chat); err != nil {
		return err
	}
	c.notify.Clear(chat)
	if emit {
		c.emitChat(ctx, chat)
	}
	return nil
}

// resendReceipts retries the read receipts that failed while disconnected.
func (c *Core) resendReceipts() {
	c.unsentMu.Lock()
	keys := c.unsent
	c.unsent = nil
	c.unsentMu.Unlock()
	if len(keys) == 0 {
		return
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.wa.MarkRead(c.life, keys, !c.settingBool(c.life, api.SettingSendReceipts)); err != nil {
			c.log.Warn("core: resend read receipts failed", "n", len(keys), "err", err)
			if errors.Is(err, api.ErrNotConnected) {
				c.unsentMu.Lock()
				c.unsent = append(c.unsent, keys...)
				c.unsentMu.Unlock()
			}
		}
	}()
}

func (c *Core) ChatsSetTyping(ctx context.Context, p api.SetTypingParams) error {
	if p.Chat == "" {
		return api.InvalidParams("chat is required")
	}
	if !c.wa.IsLoggedIn() {
		return nil // ephemeral; nothing to do offline
	}
	if err := c.wa.SetTyping(ctx, p.Chat, p.Typing); err != nil && !errors.Is(err, api.ErrNotConnected) {
		return err
	}
	return nil
}

func (c *Core) ChatsSetMuted(ctx context.Context, p api.SetMutedParams) error {
	if p.Chat == "" {
		return api.InvalidParams("chat is required")
	}
	until := int64(0)
	if p.Muted {
		until = p.Until
		if until <= 0 {
			until = -1
		}
	}
	if err := c.requireLogin(); err != nil {
		return err
	}
	if err := c.wa.SetMuted(ctx, p.Chat, until); err != nil {
		return err
	}
	return c.patchChat(ctx, api.ChatPatch{JID: p.Chat, MutedUntil: &until})
}

// maxPinned is WhatsApp's limit (api.ErrPinLimit).
const maxPinned = 3

func (c *Core) ChatsSetPinned(ctx context.Context, p api.SetPinnedParams) error {
	if err := c.requireLogin(); err != nil {
		return err
	}
	ch, err := c.store.GetChat(ctx, p.Chat)
	if err != nil {
		return err
	}
	if p.Pinned == (ch.Pinned > 0) {
		return nil
	}
	if p.Pinned {
		top, err := c.store.ListChats(ctx, api.ChatsListParams{Limit: maxPinned})
		if err != nil {
			return err
		}
		if len(top.Chats) == maxPinned && top.Chats[maxPinned-1].Pinned > 0 {
			return api.ErrPinLimit
		}
	}
	if err := c.wa.SetPinned(ctx, p.Chat, p.Pinned); err != nil {
		return err
	}
	var at int64
	if p.Pinned {
		at = time.Now().Unix()
	}
	return c.patchChat(ctx, api.ChatPatch{JID: p.Chat, Pinned: &at})
}

func (c *Core) patchChat(ctx context.Context, p api.ChatPatch) error {
	if err := c.store.UpsertChat(ctx, p); err != nil {
		return err
	}
	c.emitChat(ctx, p.JID)
	return nil
}

func (c *Core) MessagesList(ctx context.Context, p api.MessagesListParams) (*api.MessagesListResult, error) {
	msgs, more, err := c.store.ListMessages(ctx, p.Chat, p.Before, p.Limit)
	if err != nil {
		return nil, err
	}
	return &api.MessagesListResult{Messages: msgs, HasMore: more}, nil
}

// MediaFetch downloads on first call and emits message.update with the path.
func (c *Core) MediaFetch(ctx context.Context, p api.MessageRefParams) (*api.PathResult, error) {
	m, err := c.store.GetMessage(ctx, p.Chat, p.ID)
	if err != nil {
		return nil, err
	}
	if m.Media == nil {
		return nil, api.ErrNotFound
	}
	if m.Media.Path != "" && fileExists(m.Media.Path) {
		return &api.PathResult{Path: m.Media.Path, Mime: m.Media.Mime}, nil
	}
	ref, _, err := c.store.GetMediaRef(ctx, p.Chat, p.ID)
	if err != nil {
		return nil, err
	}
	last := -1
	progress := func(done, total int64) {
		if total <= 0 {
			return
		}
		if pct := int(min(100, done*100/total)); pct != last {
			last = pct
			c.emitEv(api.EMediaProgress, api.MediaProgressEvent{Chat: p.Chat, ID: p.ID, Percent: pct})
		}
	}
	path, err := c.media.Fetch(api.WithProgress(ctx, progress), ref)
	if err != nil {
		return nil, err
	}
	if err := c.store.SetMediaPath(ctx, p.Chat, p.ID, path); err != nil {
		c.logErr("set media path", err, "chat", p.Chat, "id", p.ID)
	}
	m.Media.Path = path
	c.emitEv(api.EMessageUpdate, api.MessageEvent{Message: *m})
	return &api.PathResult{Path: path, Mime: m.Media.Mime}, nil
}

// stickerListRecent caps the recent stickers in the picker, as WhatsApp does.
const stickerListRecent = 60

func (c *Core) StickersList(ctx context.Context) (*api.StickersListResult, error) {
	sts, err := c.store.ListStickers(ctx, stickerListRecent)
	if err != nil {
		return nil, err
	}
	return &api.StickersListResult{Stickers: sts}, nil
}

func (c *Core) StickersFetch(ctx context.Context, p api.StickerParams) (*api.PathResult, error) {
	path, _, err := c.stickerFile(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	return &api.PathResult{Path: path, Mime: "image/webp"}, nil
}

// stickerFile returns the cached file of sticker id, downloading it if needed.
func (c *Core) stickerFile(ctx context.Context, id string) (string, api.Sticker, error) {
	ref, st, err := c.store.StickerRef(ctx, id)
	if err != nil {
		return "", st, err
	}
	path, err := c.media.Fetch(ctx, ref)
	return path, st, err
}

func (c *Core) MediaThumb(ctx context.Context, p api.MessageRefParams) (*api.PathResult, error) {
	ref, thumb, err := c.store.GetMediaRef(ctx, p.Chat, p.ID)
	if errors.Is(err, api.ErrNotFound) {
		return &api.PathResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	path, err := c.media.Thumb(ctx, ref, thumb)
	if errors.Is(err, api.ErrNotFound) {
		return &api.PathResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &api.PathResult{Path: path, Mime: "image/jpeg"}, nil
}

func (c *Core) Search(ctx context.Context, p api.SearchParams) (*api.SearchResultList, error) {
	rs, err := c.store.Search(ctx, p)
	if err != nil {
		return nil, err
	}
	return &api.SearchResultList{Results: rs}, nil
}

func (c *Core) AvatarsGet(ctx context.Context, p api.JIDParams) (*api.PathResult, error) {
	if p.JID == "" {
		return nil, api.InvalidParams("jid is required")
	}
	path, err := c.media.Avatar(ctx, p.JID, false)
	if errors.Is(err, api.ErrNotFound) || errors.Is(err, api.ErrNotLoggedIn) || errors.Is(err, api.ErrNotConnected) {
		return &api.PathResult{}, nil // no picture, or none we can fetch right now
	}
	if err != nil {
		return nil, err
	}
	if err := c.store.SetAvatar(ctx, p.JID, path); err != nil {
		c.logErr("set avatar", err, "jid", p.JID)
	}
	return &api.PathResult{Path: path, Mime: "image/jpeg"}, nil
}

func (c *Core) PresenceSubscribe(ctx context.Context, p api.JIDParams) error {
	if p.JID == "" {
		return api.InvalidParams("jid is required")
	}
	if !c.wa.IsLoggedIn() {
		return nil
	}
	if err := c.wa.SubscribePresence(ctx, p.JID); err != nil && !errors.Is(err, api.ErrNotConnected) {
		return err
	}
	return nil
}

// GroupsMembers lists whom the @ picker offers: named members by name, then
// those known only by number.
func (c *Core) GroupsMembers(ctx context.Context, p api.ChatParams) (*api.GroupMembersResult, error) {
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	ms, err := c.wa.GroupMembers(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	for i := range ms {
		ms[i].Name = c.displayName(ctx, ms[i].JID)
	}
	unnamed := func(name string) int {
		if name == "" || name[0] == '+' || (name[0] >= '0' && name[0] <= '9') {
			return 1
		}
		return 0
	}
	slices.SortFunc(ms, func(a, b api.GroupMember) int {
		return cmp.Or(cmp.Compare(unnamed(a.Name), unnamed(b.Name)),
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.JID, b.JID))
	})
	if ms == nil {
		ms = []api.GroupMember{}
	}
	return &api.GroupMembersResult{Members: ms}, nil
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
