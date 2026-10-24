package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"wisp/daemon/internal/api"
)

const sendTimeout = 2 * time.Minute

func (c *Core) MessagesSend(ctx context.Context, p api.SendParams) (*api.Message, error) {
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	m := c.newOutgoing(p.Chat)
	m.Type, m.Text = api.MsgText, p.Text
	var jids []string
	for _, j := range p.Mentions {
		if j != "" && !slices.Contains(jids, j) {
			jids = append(jids, j)
			m.Mentions = append(m.Mentions, api.Mention{JID: j, Me: j == m.Sender})
		}
	}
	reply := c.attachReply(ctx, m, p.ReplyTo)
	return c.sendAsync(ctx, m, func(ctx context.Context) (*api.MediaRef, error) {
		return nil, c.wa.SendText(ctx, m.Chat, m.ID, m.Text, reply, jids)
	})
}

// MessagesSendMedia sends a local file as a photo or a document.
func (c *Core) MessagesSendMedia(ctx context.Context, p api.SendMediaParams) (*api.Message, error) {
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	om, err := c.media.Prepare(ctx, p.Path)
	if err != nil {
		return nil, err
	}
	return c.sendPrepared(ctx, p.Chat, om, p.Caption, p.ReplyTo)
}

// MessagesSendSticker sends a sticker from stickers.list. It is uploaded
// afresh: the original upload may have left WhatsApp's servers.
func (c *Core) MessagesSendSticker(ctx context.Context, p api.SendStickerParams) (*api.Message, error) {
	if err := c.requireLogin(); err != nil {
		return nil, err
	}
	path, st, err := c.stickerFile(ctx, p.Sticker)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("core: read sticker: %w", err)
	}
	om := &api.OutgoingMedia{Type: api.MsgSticker, Mime: "image/webp", FileName: "sticker.webp", Data: data,
		Width: cmp.Or(st.Width, 512), Height: cmp.Or(st.Height, 512), Animated: animatedWebP(data)}
	return c.sendPrepared(ctx, p.Chat, om, "", p.ReplyTo)
}

// animatedWebP reports the animation flag of an extended (VP8X) WebP.
func animatedWebP(b []byte) bool {
	return len(b) > 20 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP" &&
		string(b[12:16]) == "VP8X" && b[20]&0x02 != 0
}

// sendPrepared caches a copy of the file so the pending message already
// shows it, then uploads and sends in the background.
func (c *Core) sendPrepared(ctx context.Context, chat string, om *api.OutgoingMedia, caption, replyTo string) (*api.Message, error) {
	m := c.newOutgoing(chat)
	m.Type, m.Text, m.Thumb = om.Type, caption, om.Thumb
	m.MediaRef = &api.MediaRef{Chat: m.Chat, ID: m.ID, Type: om.Type, Mime: om.Mime,
		FileName: om.FileName, FileLength: uint64(len(om.Data))}
	path, err := c.media.Put(ctx, m.MediaRef, om.Data)
	if err != nil {
		return nil, err
	}
	m.Media = &api.MediaInfo{Mime: om.Mime, Size: int64(len(om.Data)), Width: om.Width, Height: om.Height,
		FileName: om.FileName, HasThumb: len(om.Thumb) > 0, Path: path}
	reply := c.attachReply(ctx, m, replyTo)
	return c.sendAsync(ctx, m, func(ctx context.Context) (*api.MediaRef, error) {
		return c.wa.SendMedia(ctx, m.Chat, m.ID, om, m.Text, reply)
	})
}

func (c *Core) newOutgoing(chat string) *api.Message {
	return &api.Message{
		ID:        c.wa.NewMessageID(),
		Chat:      chat,
		Sender:    c.wa.SelfJID(),
		FromMe:    true,
		Timestamp: time.Now().Unix(),
		Status:    api.StatusPending,
		Reactions: []api.Reaction{},
		Mentions:  []api.Mention{},
	}
}

// attachReply fills m.ReplyTo from the stored original and returns what the
// WA layer needs to quote it (nil if replyTo is empty or unknown).
func (c *Core) attachReply(ctx context.Context, m *api.Message, replyTo string) *api.ReplyRef {
	if replyTo == "" {
		return nil
	}
	q, err := c.store.GetMessage(ctx, m.Chat, replyTo)
	if err != nil {
		c.logErr("reply target", err, "chat", m.Chat, "id", replyTo)
		return nil
	}
	sender := q.Sender
	if sender == "" && q.FromMe {
		sender = c.wa.SelfJID()
	}
	m.ReplyTo = &api.Quote{ID: q.ID, Sender: sender, SenderName: q.SenderName, FromMe: q.FromMe,
		Type: q.Type, Preview: api.PreviewText(q)}
	return &api.ReplyRef{ID: q.ID, Sender: sender, Text: q.Text}
}

// sendAsync stores and emits m as pending, then runs send in the background;
// the status moves to sent or error via message.update.
func (c *Core) sendAsync(ctx context.Context, m *api.Message, send func(context.Context) (*api.MediaRef, error)) (*api.Message, error) {
	c.writeMu.Lock()
	err := c.store.PutMessage(ctx, m, false)
	out := *m
	if err == nil {
		if stored, err := c.store.GetMessage(ctx, m.Chat, m.ID); err == nil {
			out = *stored
		}
		c.emitEv(api.EMessageNew, api.MessageEvent{Message: out})
		c.emitChat(ctx, m.Chat)
	}
	c.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		sctx, cancel := context.WithTimeout(c.life, sendTimeout)
		defer cancel()
		ref, err := send(sctx)
		c.finishSend(m, ref, err)
	}()
	return &out, nil
}

func (c *Core) finishSend(m *api.Message, ref *api.MediaRef, sendErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if !c.wa.IsLoggedIn() {
		return // logged out meanwhile: the store was wiped
	}
	if sendErr != nil {
		c.log.Warn("core: send failed", "chat", m.Chat, "id", m.ID, "err", sendErr)
		c.setStatus(ctx, m.Chat, m.ID, api.StatusError)
		return
	}
	if ref != nil {
		// Keep the upload keys so our own media can be downloaded again.
		u := *m
		u.MediaRef, u.Status = ref, api.StatusSent
		if err := c.store.PutMessage(ctx, &u, false); err != nil {
			c.logErr("store sent media", err, "chat", m.Chat, "id", m.ID)
		}
	}
	c.setStatus(ctx, m.Chat, m.ID, api.StatusSent)
}

// setStatus moves a message's status forward and emits it. If the store
// already had it further along, the current row is emitted anyway so a
// pending clock never sticks.
func (c *Core) setStatus(ctx context.Context, chat, id, status string) {
	msgs, err := c.store.UpdateStatus(ctx, chat, []string{id}, status)
	if err != nil {
		c.logErr("update status", err, "chat", chat, "id", id)
		return
	}
	if len(msgs) == 0 {
		m, err := c.store.GetMessage(ctx, chat, id)
		if err != nil {
			if !errors.Is(err, api.ErrNotFound) {
				c.logErr("get message", err, "chat", chat, "id", id)
			}
			return
		}
		msgs = append(msgs, m)
	}
	c.emitUpdates(ctx, chat, msgs...)
}
