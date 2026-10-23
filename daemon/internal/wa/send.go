package wa

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
)

func (c *Client) SendText(ctx context.Context, chat, id, text string, reply *api.ReplyRef, mentions []string) error {
	cli, to, err := c.sendTarget(ctx, chat)
	if err != nil {
		return err
	}
	jids := make([]types.JID, 0, len(mentions))
	for _, s := range mentions {
		j, err := parseJID(s)
		if err != nil {
			return err
		}
		jids = append(jids, j.ToNonAD())
	}
	if len(jids) > 0 && c.lidAddressed(ctx, cli, to) {
		text = mentionLIDs(text, jids, func(pn types.JID) types.JID {
			lid, _ := cli.Store.LIDs.GetLIDForPN(ctx, pn)
			return lid
		})
	}
	m := textMessage(text, replyContext(reply, to), jids)
	if _, err := cli.SendMessage(ctx, to, m, whatsmeow.SendRequestExtra{ID: id}); err != nil {
		return fmt.Errorf("wa: send: %w", err)
	}
	return nil
}

// textMessage is a plain text message, or an extended one when it quotes a
// message or mentions people.
func textMessage(text string, ci *waE2E.ContextInfo, mentions []types.JID) *waE2E.Message {
	if len(mentions) > 0 {
		if ci == nil {
			ci = &waE2E.ContextInfo{}
		}
		for _, j := range mentions {
			ci.MentionedJID = append(ci.MentionedJID, j.String())
		}
	}
	if ci == nil {
		return &waE2E.Message{Conversation: new(text)}
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: new(text), ContextInfo: ci}}
}

// lidAddressed reports whether a group addresses its members by LID.
func (c *Client) lidAddressed(ctx context.Context, cli *whatsmeow.Client, chat types.JID) bool {
	if chat.Server != types.GroupServer {
		return false
	}
	g, err := c.groupInfo(ctx, cli, chat)
	if err != nil {
		c.log.Debug("group info", "jid", chat, "err", err)
		return false
	}
	return g.AddressingMode == types.AddressingModeLID
}

// mentionLIDs swaps the mentioned phone JIDs whose LID is known for it, in
// jids and in the text's tokens: groups addressed by LID mention by LID.
func mentionLIDs(text string, jids []types.JID, lidFor func(types.JID) types.JID) string {
	for i, j := range jids {
		if j.Server != types.DefaultUserServer {
			continue
		}
		if lid := lidFor(j); !lid.IsEmpty() {
			text = api.ReplaceMention(text, j.User, lid.User)
			jids[i] = lid.ToNonAD()
		}
	}
	return text
}

// SendMedia uploads m and sends it as an image or, for anything else, a document.
func (c *Client) SendMedia(ctx context.Context, chat, id string, m *api.OutgoingMedia, caption string, reply *api.ReplyRef) (*api.MediaRef, error) {
	cli, to, err := c.sendTarget(ctx, chat)
	if err != nil {
		return nil, err
	}
	typ, mt := api.MsgDocument, whatsmeow.MediaDocument
	if m.Type == api.MsgImage || m.Type == api.MsgSticker {
		typ, mt = m.Type, whatsmeow.MediaImage
	}
	up, err := cli.Upload(ctx, m.Data, mt)
	if err != nil {
		return nil, fmt.Errorf("wa: upload: %w", err)
	}
	ci := replyContext(reply, to)
	var msg *waE2E.Message
	if typ == api.MsgSticker {
		msg = &waE2E.Message{StickerMessage: &waE2E.StickerMessage{
			Mimetype:      new(m.Mime),
			URL:           new(up.URL),
			DirectPath:    new(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    new(up.FileLength),
			Width:         new(uint32(m.Width)),
			Height:        new(uint32(m.Height)),
			IsAnimated:    new(m.Animated),
			ContextInfo:   ci,
		}}
	} else if typ == api.MsgImage {
		msg = &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption:       optional(caption),
			Mimetype:      new(m.Mime),
			URL:           new(up.URL),
			DirectPath:    new(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    new(up.FileLength),
			Width:         new(uint32(m.Width)),
			Height:        new(uint32(m.Height)),
			JPEGThumbnail: m.Thumb,
			ContextInfo:   ci,
		}}
	} else {
		doc := &waE2E.DocumentMessage{
			Caption:       optional(caption),
			Mimetype:      new(m.Mime),
			Title:         new(m.FileName),
			FileName:      new(m.FileName),
			URL:           new(up.URL),
			DirectPath:    new(up.DirectPath),
			MediaKey:      up.MediaKey,
			FileEncSHA256: up.FileEncSHA256,
			FileSHA256:    up.FileSHA256,
			FileLength:    new(up.FileLength),
			JPEGThumbnail: m.Thumb,
			ContextInfo:   ci,
		}
		msg = &waE2E.Message{DocumentMessage: doc}
		if caption != "" {
			msg = &waE2E.Message{DocumentWithCaptionMessage: &waE2E.FutureProofMessage{Message: msg}}
		}
	}
	if _, err := cli.SendMessage(ctx, to, msg, whatsmeow.SendRequestExtra{ID: id}); err != nil {
		return nil, fmt.Errorf("wa: send media: %w", err)
	}
	return &api.MediaRef{
		Chat: chat, ID: id, Type: typ, Mime: m.Mime,
		DirectPath: up.DirectPath, URL: up.URL, MediaKey: up.MediaKey,
		FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256,
		FileLength: up.FileLength, FileName: m.FileName,
	}, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// target checks the connection and parses the chat JID.
func (c *Client) target(chat string) (*whatsmeow.Client, types.JID, error) {
	cli, err := c.requireConnected()
	if err != nil {
		return nil, types.JID{}, err
	}
	to, err := parseJID(chat)
	return cli, to.ToNonAD(), err
}

// sendTarget is target for sends: instead of failing at once while the
// socket is down (a blip, resume, cold start) it waits, bounded by ctx, for
// whatsmeow's auto-reconnect.
func (c *Client) sendTarget(ctx context.Context, chat string) (*whatsmeow.Client, types.JID, error) {
	cli, err := c.requireLoggedIn()
	if err != nil {
		return nil, types.JID{}, err
	}
	to, err := parseJID(chat)
	if err != nil {
		return nil, types.JID{}, err
	}
	if !cli.IsConnected() || !cli.IsLoggedIn() {
		wait := 30 * time.Second
		if d, ok := ctx.Deadline(); ok {
			wait = min(wait, time.Until(d))
		}
		if wait <= 0 || !cli.WaitForConnection(wait) {
			return nil, types.JID{}, api.ErrNotConnected
		}
	}
	return cli, to.ToNonAD(), nil
}

// replyContext quotes the replied-to message. The phone renders the quote
// from its own copy, so a text stub of the original is enough.
func replyContext(r *api.ReplyRef, chat types.JID) *waE2E.ContextInfo {
	if r == nil || r.ID == "" {
		return nil
	}
	sender := r.Sender
	if sender == "" {
		sender = chat.String()
	}
	return &waE2E.ContextInfo{
		StanzaID:      new(r.ID),
		Participant:   new(sender),
		QuotedMessage: &waE2E.Message{Conversation: new(r.Text)},
	}
}

// MarkRead sends read receipts, one per (chat, sender) as WhatsApp requires.
// Private ones (read-self) only sync the read state to our other devices.
func (c *Client) MarkRead(ctx context.Context, keys []api.MsgKey, private bool) error {
	cli, err := c.requireConnected()
	if err != nil {
		return err
	}
	type group struct{ chat, sender string }
	ids := map[group][]types.MessageID{}
	for _, k := range keys {
		if k.FromMe || k.ID == "" {
			continue
		}
		g := group{k.Chat, k.Sender}
		ids[g] = append(ids[g], k.ID)
	}
	typ := types.ReceiptTypeRead
	if private {
		typ = types.ReceiptTypeReadSelf
	}
	now := time.Now()
	for g, list := range ids {
		chat, err := parseJID(g.chat)
		if err != nil {
			return err
		}
		var sender types.JID
		if g.sender != "" {
			if sender, err = parseJID(g.sender); err != nil {
				return err
			}
		}
		if err := cli.MarkRead(ctx, list, now, chat, sender, typ); err != nil {
			return fmt.Errorf("wa: mark read: %w", err)
		}
	}
	return nil
}

func (c *Client) SetTyping(ctx context.Context, chat string, typing bool) error {
	cli, to, err := c.target(chat)
	if err != nil {
		return err
	}
	state := types.ChatPresencePaused
	if typing {
		state = types.ChatPresenceComposing
	}
	return cli.SendChatPresence(ctx, to, state, types.ChatPresenceMediaText)
}

func (c *Client) SubscribePresence(ctx context.Context, jid string) error {
	cli, to, err := c.target(jid)
	if err != nil {
		return err
	}
	return cli.SubscribePresence(ctx, to)
}

// SetOnline sets our own presence. WhatsApp only delivers notifications to
// the phone while every companion is unavailable.
func (c *Client) SetOnline(ctx context.Context, online bool) error {
	cli, err := c.requireConnected()
	if err != nil {
		return err
	}
	p := types.PresenceUnavailable
	if online {
		p = types.PresenceAvailable
	}
	return cli.SendPresence(ctx, p)
}

// settingsTarget is the JID a chat's settings are keyed by in app state:
// the phone keys 1:1 chats by LID once it knows one.
func (c *Client) settingsTarget(ctx context.Context, chat string) (*whatsmeow.Client, types.JID, error) {
	cli, to, err := c.target(chat)
	if err != nil || to.Server != types.DefaultUserServer {
		return cli, to, err
	}
	if lid, err := cli.Store.LIDs.GetLIDForPN(ctx, to); err == nil && !lid.IsEmpty() {
		to = lid.ToNonAD()
	}
	return cli, to, nil
}

// SetMuted mutes a chat until a unix time (-1 forever, 0 unmutes).
func (c *Client) SetMuted(ctx context.Context, chat string, until int64) error {
	cli, to, err := c.settingsTarget(ctx, chat)
	if err != nil {
		return err
	}
	var patch appstate.PatchInfo
	switch {
	case until == 0:
		patch = appstate.BuildMuteAbs(to, false, nil)
	case until < 0:
		patch = appstate.BuildMuteAbs(to, true, nil)
	default:
		patch = appstate.BuildMuteAbs(to, true, new(until*1000))
	}
	if err := cli.SendAppState(ctx, patch); err != nil {
		return fmt.Errorf("wa: mute: %w", err)
	}
	return nil
}

func (c *Client) SetPinned(ctx context.Context, chat string, pinned bool) error {
	cli, to, err := c.settingsTarget(ctx, chat)
	if err != nil {
		return err
	}
	if err := cli.SendAppState(ctx, appstate.BuildPin(to, pinned)); err != nil {
		return fmt.Errorf("wa: pin: %w", err)
	}
	return nil
}

// ResyncAppState re-fetches the collections holding pin and archive
// (regular_low), mute (regular_high) and favourite stickers (one of the
// regular ones) from a snapshot. whatsmeow emits every setting again, as on
// the first sync.
func (c *Client) ResyncAppState(ctx context.Context) error {
	cli, err := c.requireConnected()
	if err != nil {
		return err
	}
	for _, name := range []appstate.WAPatchName{appstate.WAPatchRegular, appstate.WAPatchRegularLow, appstate.WAPatchRegularHigh} {
		if v, _, err := cli.Store.AppState.GetAppStateVersion(ctx, string(name)); err != nil || v == 0 {
			continue // never synced: whatsmeow's own first sync will do it
		}
		if err := cli.FetchAppState(ctx, name, true, false); err != nil {
			return fmt.Errorf("wa: resync %s: %w", name, err)
		}
	}
	return nil
}

func (c *Client) Download(ctx context.Context, ref *api.MediaRef) ([]byte, error) {
	cli, err := c.requireConnected()
	if err != nil {
		return nil, err
	}
	path := ref.DirectPath
	if path == "" && ref.URL != "" {
		if u, err := url.Parse(ref.URL); err == nil {
			path = u.RequestURI()
		}
	}
	if path == "" {
		return nil, api.ErrMediaExpired
	}
	data, err := cli.DownloadMediaWithPath(ctx, path, ref.FileEncSHA256, ref.FileSHA256, ref.MediaKey, mediaType(ref.Type), "", false)
	if errors.Is(err, whatsmeow.ErrInvalidMediaSHA256) && len(ref.FileSHA256) == 0 && len(data) > 0 {
		err = nil // a favourite sticker comes without the plain hash; the MAC still matched
	}
	if expired(err) {
		return c.retryDownload(ctx, cli, ref)
	}
	if err != nil {
		return nil, fmt.Errorf("wa: download: %w", err)
	}
	return data, nil
}

func expired(err error) bool {
	return errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) || errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410)
}

// mediaRetryWait bounds the wait for the phone's answer to a media retry.
const mediaRetryWait = 30 * time.Second

// retryDownload asks the phone to re-upload media the CDN no longer has, as
// WhatsApp's own companions do, and downloads it from the new path. The
// outcome is emitted as EvMediaPath so the store remembers it; if the phone
// doesn't answer, nothing is stored and a later view asks again.
func (c *Client) retryDownload(ctx context.Context, cli *whatsmeow.Client, ref *api.MediaRef) ([]byte, error) {
	chat, err := parseJID(ref.Chat)
	if err != nil || len(ref.MediaKey) == 0 {
		return nil, api.ErrMediaExpired
	}
	info := &types.MessageInfo{ID: ref.ID, MessageSource: types.MessageSource{
		Chat: chat, IsFromMe: ref.FromMe, IsGroup: chat.Server == types.GroupServer,
	}}
	if info.IsGroup {
		if info.Sender, err = parseJID(ref.Sender); err != nil {
			return nil, api.ErrMediaExpired
		}
	}
	ch := make(chan *events.MediaRetry, 1)
	c.retryMu.Lock()
	c.retries[ref.ID] = ch
	c.retryMu.Unlock()
	defer func() {
		c.retryMu.Lock()
		if c.retries[ref.ID] == ch {
			delete(c.retries, ref.ID)
		}
		c.retryMu.Unlock()
	}()
	if err := cli.SendMediaRetryReceipt(ctx, info, ref.MediaKey); err != nil {
		return nil, fmt.Errorf("%w: retry: %v", api.ErrMediaExpired, err)
	}
	t := time.NewTimer(mediaRetryWait)
	defer t.Stop()
	var evt *events.MediaRetry
	select {
	case evt = <-ch:
	case <-t.C:
		return nil, fmt.Errorf("%w: no answer from the phone", api.ErrMediaExpired)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	path, err := retryPath(evt, ref.MediaKey)
	if err != nil {
		return nil, err
	}
	c.emit(api.EvMediaPath{Chat: ref.Chat, ID: ref.ID, DirectPath: path})
	if path == "" {
		return nil, fmt.Errorf("%w: not on the phone", api.ErrMediaExpired)
	}
	data, err := cli.DownloadMediaWithPath(ctx, path, ref.FileEncSHA256, ref.FileSHA256, ref.MediaKey, mediaType(ref.Type), "", false)
	if expired(err) {
		return nil, fmt.Errorf("%w: %v", api.ErrMediaExpired, err)
	}
	if err != nil {
		return nil, fmt.Errorf("wa: download: %w", err)
	}
	return data, nil
}

// retryPath reads the phone's answer to a media retry: the new direct path,
// or "" if the phone no longer has the file.
func retryPath(evt *events.MediaRetry, mediaKey []byte) (string, error) {
	n, err := whatsmeow.DecryptMediaRetryNotification(evt, mediaKey)
	if errors.Is(err, whatsmeow.ErrMediaNotAvailableOnPhone) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: retry: %v", api.ErrMediaExpired, err)
	}
	if n.GetResult() != waMmsRetry.MediaRetryNotification_SUCCESS {
		return "", nil
	}
	return n.GetDirectPath(), nil
}

func mediaType(t string) whatsmeow.MediaType {
	switch t {
	case api.MsgImage, api.MsgSticker:
		return whatsmeow.MediaImage
	case api.MsgVideo, api.MsgGIF:
		return whatsmeow.MediaVideo
	case api.MsgAudio, api.MsgVoice:
		return whatsmeow.MediaAudio
	}
	return whatsmeow.MediaDocument
}

// AvatarURL returns the small preview picture, which is all the UI shows.
func (c *Client) AvatarURL(ctx context.Context, jid string) (string, string, error) {
	cli, to, err := c.target(jid)
	if err != nil {
		return "", "", err
	}
	info, err := cli.GetProfilePictureInfo(ctx, to, &whatsmeow.GetProfilePictureParams{Preview: true})
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) || errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return "", "", api.ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("wa: avatar: %w", err)
	}
	if info == nil || info.URL == "" {
		return "", "", api.ErrNotFound
	}
	return info.URL, info.ID, nil
}

func (c *Client) ResolveContact(ctx context.Context, jid string) (*api.Contact, error) {
	cli, err := c.requireLoggedIn()
	if err != nil {
		return nil, err
	}
	j, err := parseJID(jid)
	if err != nil {
		return nil, err
	}
	cv := c.converterFor(cli)
	alts := []types.JID{j}
	if j.Server == types.DefaultUserServer {
		if lid, err := cli.Store.LIDs.GetLIDForPN(ctx, j); err == nil {
			alts = append(alts, lid)
		}
	}
	ct := c.contactFor(cli, cv.canon(j), alts...)
	return &ct, nil
}
