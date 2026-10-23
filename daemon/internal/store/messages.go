package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"wisp/daemon/internal/api"
)

const msgCols = `m.seq, m.chat, m.id, m.sender, m.push_name, m.from_me, m.ts, m.type, m.text, m.status,
	m.edited, m.deleted, m.forwarded,
	m.reply_id, m.reply_sender, m.reply_from_me, m.reply_type, m.reply_preview,
	m.lat IS NOT NULL, COALESCE(m.lat, 0), COALESCE(m.lng, 0), m.loc_name,
	md.msg IS NOT NULL, COALESCE(md.mime, ''), COALESCE(md.size, 0), COALESCE(md.width, 0), COALESCE(md.height, 0),
	COALESCE(md.duration, 0), COALESCE(md.file_name, ''), COALESCE(md.has_thumb, 0), COALESCE(md.path, ''),
	COALESCE(sc.full_name, ''), COALESCE(sc.push_name, ''), COALESCE(sc.business_name, ''),
	COALESCE(rc.full_name, ''), COALESCE(rc.push_name, ''), COALESCE(rc.business_name, ''),
	` + mentionsCol

// mentionsCol is m.mentions with each person's contact names joined (see
// decodeMentions).
const mentionsCol = `CASE WHEN m.mentions = '' THEN '' ELSE (SELECT json_group_array(json_object('jid', mn.value ->> 'jid',
		'me', COALESCE(mn.value ->> 'me', 0), 'full', COALESCE(mc.full_name, ''), 'push', COALESCE(mc.push_name, ''),
		'business', COALESCE(mc.business_name, '')))
	FROM json_each(m.mentions) mn LEFT JOIN contacts mc ON mc.jid = mn.value ->> 'jid') END`

// decodeMentions reads mentionsCol, naming people like senders.
func decodeMentions(col string) ([]api.Mention, error) {
	out := []api.Mention{}
	if col == "" {
		return out, nil
	}
	var rows []struct {
		JID      string `json:"jid"`
		Me       int    `json:"me"`
		Full     string `json:"full"`
		Push     string `json:"push"`
		Business string `json:"business"`
	}
	if err := json.Unmarshal([]byte(col), &rows); err != nil {
		return nil, fmt.Errorf("mentions: %w", err)
	}
	for _, r := range rows {
		name := "You"
		if r.Me == 0 {
			name = names{r.Full, r.Push, r.Business}.display(r.JID, "")
		}
		out = append(out, api.Mention{JID: r.JID, Name: name, Me: r.Me != 0})
	}
	return out, nil
}

const msgFrom = ` FROM messages m
	LEFT JOIN media md ON md.msg = m.seq
	LEFT JOIN contacts sc ON sc.jid = m.sender
	LEFT JOIN contacts rc ON rc.jid = m.reply_sender`

type rowScanner interface{ Scan(...any) error }

func scanMessage(sc rowScanner) (*api.Message, int64, error) {
	m := &api.Message{}
	var seq int64
	var replyID, replySender, replyType, replyPreview string
	var replyFromMe, hasLoc, hasMedia bool
	var lat, lng float64
	var locName string
	var mi api.MediaInfo
	var sn, rn names
	var mentions string
	err := sc.Scan(&seq, &m.Chat, &m.ID, &m.Sender, &m.PushName, &m.FromMe, &m.Timestamp, &m.Type, &m.Text, &m.Status,
		&m.Edited, &m.Deleted, &m.Forwarded,
		&replyID, &replySender, &replyFromMe, &replyType, &replyPreview,
		&hasLoc, &lat, &lng, &locName,
		&hasMedia, &mi.Mime, &mi.Size, &mi.Width, &mi.Height, &mi.Duration, &mi.FileName, &mi.HasThumb, &mi.Path,
		&sn.full, &sn.push, &sn.business, &rn.full, &rn.push, &rn.business, &mentions)
	if err != nil {
		return nil, 0, err
	}
	m.SenderName = senderName(m.Sender, m.FromMe, sn, m.PushName)
	m.Reactions = []api.Reaction{}
	m.Mentions = []api.Mention{}
	if m.Deleted {
		m.Type = api.MsgRevoked
		m.Text = ""
		return m, seq, nil
	}
	if m.Mentions, err = decodeMentions(mentions); err != nil {
		return nil, 0, err
	}
	if replyID != "" {
		m.ReplyTo = &api.Quote{
			ID: replyID, Sender: replySender, FromMe: replyFromMe, Type: replyType, Preview: replyPreview,
			SenderName: senderName(replySender, replyFromMe, rn, ""),
		}
	}
	if hasLoc {
		m.Location = &api.Location{Lat: lat, Lng: lng, Name: locName}
	}
	if hasMedia {
		m.Media = &mi
	}
	return m, seq, nil
}

func senderName(jid string, fromMe bool, n names, push string) string {
	if fromMe {
		if s := firstNonEmpty(n.full, n.push); s != "" {
			return s
		}
		return "You"
	}
	return n.display(jid, push)
}

// queryMessages runs a message query (where/tail appended to the standard
// select) and returns rows in query order with their seqs.
func queryMessages(ctx context.Context, q querier, tail string, args ...any) ([]*api.Message, []int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+msgCols+msgFrom+` `+tail, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("store: query messages: %w", err)
	}
	defer rows.Close()
	var out []*api.Message
	var seqs []int64
	for rows.Next() {
		m, seq, err := scanMessage(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("store: scan message: %w", err)
		}
		out = append(out, m)
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: query messages: %w", err)
	}
	return out, seqs, nil
}

// attachReactions fills Reactions for messages that all belong to chat.
func attachReactions(ctx context.Context, q querier, chat string, msgs []*api.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	byID := make(map[string]*api.Message, len(msgs))
	args := make([]any, 0, len(msgs)+1)
	args = append(args, chat)
	for _, m := range msgs {
		if m.Deleted {
			continue
		}
		byID[m.ID] = m
		args = append(args, m.ID)
	}
	if len(byID) == 0 {
		return nil
	}
	ph := strings.Repeat(",?", len(args)-1)[1:]
	rows, err := q.QueryContext(ctx, `SELECT msg_id, emoji, COUNT(*), MAX(from_me), MIN(ts) AS first
FROM reactions WHERE chat = ? AND msg_id IN (`+ph+`)
GROUP BY msg_id, emoji ORDER BY msg_id, COUNT(*) DESC, first, emoji`, args...)
	if err != nil {
		return fmt.Errorf("store: reactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var r api.Reaction
		var first int64
		if err := rows.Scan(&id, &r.Emoji, &r.Count, &r.Mine, &first); err != nil {
			return fmt.Errorf("store: scan reaction: %w", err)
		}
		if m := byID[id]; m != nil {
			m.Reactions = append(m.Reactions, r)
		}
	}
	return rows.Err()
}

// loadBySeq loads messages by seq (any chats).
func loadBySeq(ctx context.Context, q querier, seqs []int64, withReactions bool) (map[int64]*api.Message, error) {
	out := make(map[int64]*api.Message, len(seqs))
	if len(seqs) == 0 {
		return out, nil
	}
	args := make([]any, len(seqs))
	for i, s := range seqs {
		args[i] = s
	}
	ph := strings.Repeat(",?", len(seqs))[1:]
	msgs, got, err := queryMessages(ctx, q, `WHERE m.seq IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	for i, m := range msgs {
		out[got[i]] = m
	}
	if withReactions {
		byChat := map[string][]*api.Message{}
		for _, m := range msgs {
			byChat[m.Chat] = append(byChat[m.Chat], m)
		}
		for chat, ms := range byChat {
			if err := attachReactions(ctx, q, chat, ms); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func getMessage(ctx context.Context, q querier, chat, id string) (*api.Message, error) {
	msgs, _, err := queryMessages(ctx, q, `WHERE m.chat = ? AND m.id = ?`, chat, id)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, notFound("message %s/%s", chat, id)
	}
	if err := attachReactions(ctx, q, chat, msgs); err != nil {
		return nil, err
	}
	return msgs[0], nil
}

// GetMessage returns one message (ErrNotFound if unknown).
func (s *Store) GetMessage(ctx context.Context, chat, id string) (*api.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getMessage(ctx, s.r, chat, id)
}

// ListMessages returns up to limit messages strictly older than beforeID
// (newest page if beforeID == ""), newest first, and whether older ones
// exist. Keyset pagination on messages(chat, ts, seq).
func (s *Store) ListMessages(ctx context.Context, chat, beforeID string, limit int) ([]api.Message, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	var msgs []*api.Message
	var err error
	if beforeID == "" {
		msgs, _, err = queryMessages(ctx, s.r, `WHERE m.chat = ? ORDER BY m.ts DESC, m.seq DESC LIMIT ?`, chat, limit+1)
	} else {
		var ts, seq int64
		e := s.r.QueryRowContext(ctx, `SELECT ts, seq FROM messages WHERE chat = ? AND id = ?`, chat, beforeID).Scan(&ts, &seq)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, false, notFound("message %s/%s", chat, beforeID)
		}
		if e != nil {
			return nil, false, fmt.Errorf("store: list messages: %w", e)
		}
		msgs, _, err = queryMessages(ctx, s.r, `WHERE m.chat = ? AND (m.ts, m.seq) < (?, ?)
ORDER BY m.ts DESC, m.seq DESC LIMIT ?`, chat, ts, seq, limit+1)
	}
	if err != nil {
		return nil, false, err
	}
	more := len(msgs) > limit
	if more {
		msgs = msgs[:limit]
	}
	if err := attachReactions(ctx, s.r, chat, msgs); err != nil {
		return nil, false, err
	}
	out := make([]api.Message, len(msgs))
	for i, m := range msgs {
		out[i] = *m
	}
	return out, more, nil
}

func rankSQL(col string) string {
	return `(CASE ` + col + ` WHEN 'error' THEN -1 WHEN 'pending' THEN 0 WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 ELSE 0 END)`
}

var upsertMsgSQL = `
INSERT INTO messages(chat, id, sender, push_name, from_me, ts, type, text, status, edited, deleted, forwarded,
	reply_id, reply_sender, reply_from_me, reply_type, reply_preview, lat, lng, loc_name, mentions)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(chat, id) DO UPDATE SET
	sender    = CASE WHEN excluded.sender <> '' THEN excluded.sender ELSE sender END,
	push_name = CASE WHEN excluded.push_name <> '' THEN excluded.push_name ELSE push_name END,
	from_me   = excluded.from_me,
	ts        = CASE WHEN excluded.ts <> 0 THEN excluded.ts ELSE ts END,
	type      = CASE WHEN deleted THEN 'revoked' ELSE excluded.type END,
	text      = CASE WHEN deleted OR excluded.deleted THEN ''
	                 WHEN edited AND NOT excluded.edited THEN text
	                 ELSE excluded.text END,
	mentions  = CASE WHEN deleted OR excluded.deleted THEN ''
	                 WHEN edited AND NOT excluded.edited THEN mentions
	                 ELSE excluded.mentions END,
	status    = CASE
		WHEN excluded.status = '' THEN status
		WHEN status = '' THEN excluded.status
		WHEN excluded.status = 'error' THEN CASE WHEN status = 'pending' THEN 'error' ELSE status END
		WHEN ` + rankSQL("excluded.status") + ` > ` + rankSQL("status") + ` THEN excluded.status
		ELSE status END,
	edited    = MAX(edited, excluded.edited),
	deleted   = MAX(deleted, excluded.deleted),
	forwarded = MAX(forwarded, excluded.forwarded),
	reply_id      = CASE WHEN excluded.reply_id <> '' THEN excluded.reply_id ELSE reply_id END,
	reply_sender  = CASE WHEN excluded.reply_id <> '' THEN excluded.reply_sender ELSE reply_sender END,
	reply_from_me = CASE WHEN excluded.reply_id <> '' THEN excluded.reply_from_me ELSE reply_from_me END,
	reply_type    = CASE WHEN excluded.reply_id <> '' THEN excluded.reply_type ELSE reply_type END,
	reply_preview = CASE WHEN excluded.reply_id <> '' THEN excluded.reply_preview ELSE reply_preview END,
	lat       = CASE WHEN excluded.lat IS NOT NULL THEN excluded.lat ELSE lat END,
	lng       = CASE WHEN excluded.lat IS NOT NULL THEN excluded.lng ELSE lng END,
	loc_name  = CASE WHEN excluded.lat IS NOT NULL THEN excluded.loc_name ELSE loc_name END
RETURNING seq`

func msgArgs(m *api.Message) []any {
	typ, text := m.Type, m.Text
	if typ == "" {
		typ = api.MsgText
	}
	deleted := m.Deleted || typ == api.MsgRevoked
	if deleted {
		typ, text = api.MsgRevoked, ""
	}
	var rID, rSender, rType, rPreview string
	var rFromMe bool
	if q := m.ReplyTo; q != nil && q.ID != "" {
		rID, rSender, rFromMe, rType, rPreview = q.ID, q.Sender, q.FromMe, q.Type, q.Preview
	}
	var lat, lng any
	var locName string
	if l := m.Location; l != nil && !deleted {
		lat, lng, locName = l.Lat, l.Lng, l.Name
	}
	mentions := ""
	if !deleted {
		mentions = mentionsJSON(m.Mentions)
	}
	return []any{m.Chat, m.ID, m.Sender, m.PushName, b2i(m.FromMe), m.Timestamp, typ, text, m.Status,
		b2i(m.Edited), b2i(deleted), b2i(m.Forwarded),
		rID, rSender, b2i(rFromMe), rType, rPreview, lat, lng, locName, mentions}
}

// mentionsJSON is the stored form of mentions: their JIDs, and whether one
// is us; names are resolved when read, like sender names.
func mentionsJSON(ms []api.Mention) string {
	type stored struct {
		JID string `json:"jid"`
		Me  bool   `json:"me,omitempty"`
	}
	out := make([]stored, 0, len(ms))
	for _, mn := range ms {
		if mn.JID != "" {
			out = append(out, stored{mn.JID, mn.Me})
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}

const upsertMediaSQL = `
INSERT INTO media(msg, mime, size, width, height, duration, file_name, has_thumb, path,
	direct_path, url, media_key, file_sha256, file_enc_sha256, file_length, thumb)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(msg) DO UPDATE SET
	mime        = CASE WHEN excluded.mime <> '' THEN excluded.mime ELSE mime END,
	size        = CASE WHEN excluded.size <> 0 THEN excluded.size ELSE size END,
	width       = CASE WHEN excluded.width <> 0 THEN excluded.width ELSE width END,
	height      = CASE WHEN excluded.height <> 0 THEN excluded.height ELSE height END,
	duration    = CASE WHEN excluded.duration <> 0 THEN excluded.duration ELSE duration END,
	file_name   = CASE WHEN excluded.file_name <> '' THEN excluded.file_name ELSE file_name END,
	has_thumb   = MAX(has_thumb, excluded.has_thumb),
	path        = CASE WHEN excluded.path <> '' THEN excluded.path ELSE path END,
	direct_path = CASE WHEN excluded.direct_path <> '' THEN excluded.direct_path ELSE direct_path END,
	url         = CASE WHEN excluded.url <> '' THEN excluded.url ELSE url END,
	media_key       = COALESCE(excluded.media_key, media_key),
	file_sha256     = COALESCE(excluded.file_sha256, file_sha256),
	file_enc_sha256 = COALESCE(excluded.file_enc_sha256, file_enc_sha256),
	file_length = CASE WHEN excluded.file_length <> 0 THEN excluded.file_length ELSE file_length END,
	thumb       = COALESCE(excluded.thumb, thumb)`

func blob(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// mediaArgs returns the media row args for m, or nil if m has no media.
func mediaArgs(seq int64, m *api.Message) []any {
	if m.Deleted || m.Type == api.MsgRevoked || (m.Media == nil && m.MediaRef == nil && len(m.Thumb) == 0) {
		return nil
	}
	var mi api.MediaInfo
	if m.Media != nil {
		mi = *m.Media
	}
	var ref api.MediaRef
	if m.MediaRef != nil {
		ref = *m.MediaRef
	}
	mime := firstNonEmpty(mi.Mime, ref.Mime)
	fileName := firstNonEmpty(mi.FileName, ref.FileName)
	size := mi.Size
	if size == 0 {
		size = int64(ref.FileLength)
	}
	hasThumb := mi.HasThumb || len(m.Thumb) > 0
	return []any{seq, mime, size, mi.Width, mi.Height, mi.Duration, fileName, b2i(hasThumb), mi.Path,
		ref.DirectPath, ref.URL, blob(ref.MediaKey), blob(ref.FileSHA256), blob(ref.FileEncSHA256), int64(ref.FileLength),
		blob(m.Thumb)}
}

// stmts are the prepared statements used for bulk message ingestion.
type msgStmts struct {
	exists, upsert, media, dropMedia *sql.Stmt
}

func prepareMsgStmts(ctx context.Context, tx *sql.Tx) (*msgStmts, error) {
	var st msgStmts
	var err error
	prep := func(dst **sql.Stmt, q string) {
		if err != nil {
			return
		}
		*dst, err = tx.PrepareContext(ctx, q)
	}
	prep(&st.exists, `SELECT 1 FROM messages WHERE chat = ? AND id = ?`)
	prep(&st.upsert, upsertMsgSQL)
	prep(&st.media, upsertMediaSQL)
	prep(&st.dropMedia, `DELETE FROM media WHERE msg = ?`)
	if err != nil {
		st.close()
		return nil, fmt.Errorf("store: prepare: %w", err)
	}
	return &st, nil
}

func (st *msgStmts) close() {
	for _, s := range []*sql.Stmt{st.exists, st.upsert, st.media, st.dropMedia} {
		if s != nil {
			s.Close()
		}
	}
}

// put upserts one message (and its media row); reports whether it was new.
func (st *msgStmts) put(ctx context.Context, m *api.Message) (isNew bool, err error) {
	var one int
	switch err := st.exists.QueryRowContext(ctx, m.Chat, m.ID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		isNew = true
	case err != nil:
		return false, fmt.Errorf("store: lookup message: %w", err)
	}
	var seq int64
	if err := st.upsert.QueryRowContext(ctx, msgArgs(m)...).Scan(&seq); err != nil {
		return false, fmt.Errorf("store: put message %s/%s: %w", m.Chat, m.ID, err)
	}
	if args := mediaArgs(seq, m); args != nil {
		if _, err := st.media.ExecContext(ctx, args...); err != nil {
			return false, fmt.Errorf("store: put media %s/%s: %w", m.Chat, m.ID, err)
		}
	} else if (m.Deleted || m.Type == api.MsgRevoked) && !isNew {
		if _, err := st.dropMedia.ExecContext(ctx, seq); err != nil {
			return false, fmt.Errorf("store: drop media: %w", err)
		}
	}
	return isNew, nil
}

func validMsg(m *api.Message) error {
	if m == nil || m.Chat == "" || m.ID == "" {
		return api.InvalidParams("message without chat/id")
	}
	return nil
}

// PutMessage inserts or merges one message (matched by chat+id), creates the
// chat if missing, moves its lastActivity/lastMessage forward and, if
// incUnread and the message is new to us, bumps unread.
func (s *Store) PutMessage(ctx context.Context, m *api.Message, incUnread bool) error {
	if err := validMsg(m); err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := ensureChat(ctx, tx, m.Chat); err != nil {
			return err
		}
		st, err := prepareMsgStmts(ctx, tx)
		if err != nil {
			return err
		}
		defer st.close()
		isNew, err := st.put(ctx, m)
		if err != nil {
			return err
		}
		if err := refreshLast(ctx, tx, m.Chat); err != nil {
			return err
		}
		if incUnread && isNew {
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET unread = unread + 1 WHERE jid = ?`, m.Chat); err != nil {
				return fmt.Errorf("store: bump unread: %w", err)
			}
		}
		return nil
	})
}

// PutHistory ingests one history-sync batch in a single transaction.
func (s *Store) PutHistory(ctx context.Context, h *api.EvHistory) error {
	if h == nil {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		touched := map[string]bool{}
		for i := range h.Chats {
			if h.Chats[i].JID == "" {
				continue
			}
			if err := applyPatch(ctx, tx, &h.Chats[i]); err != nil {
				return err
			}
			touched[h.Chats[i].JID] = true
		}
		if err := putContacts(ctx, tx, h.Contacts); err != nil {
			return err
		}

		// Insert oldest first so seq follows time within equal timestamps.
		msgs := make([]*api.Message, 0, len(h.Messages))
		for _, m := range h.Messages {
			if validMsg(m) == nil {
				msgs = append(msgs, m)
			}
		}
		slices.SortStableFunc(msgs, func(a, b *api.Message) int { return cmp.Compare(a.Timestamp, b.Timestamp) })
		if len(msgs) > 0 {
			st, err := prepareMsgStmts(ctx, tx)
			if err != nil {
				return err
			}
			defer st.close()
			ensure, err := tx.PrepareContext(ctx, `INSERT INTO chats(jid, kind) VALUES (?, ?) ON CONFLICT(jid) DO NOTHING`)
			if err != nil {
				return fmt.Errorf("store: prepare: %w", err)
			}
			defer ensure.Close()
			for _, m := range msgs {
				if !touched[m.Chat] {
					if _, err := ensure.ExecContext(ctx, m.Chat, kindOf(m.Chat)); err != nil {
						return fmt.Errorf("store: ensure chat: %w", err)
					}
					touched[m.Chat] = true
				}
				if _, err := st.put(ctx, m); err != nil {
					return err
				}
			}
		}

		if len(h.Reactions) > 0 {
			rs, err := prepareReactionStmts(ctx, tx)
			if err != nil {
				return err
			}
			defer rs.close()
			for i := range h.Reactions {
				if err := rs.apply(ctx, &h.Reactions[i]); err != nil {
					return err
				}
			}
		}

		for chat := range touched {
			if err := refreshLast(ctx, tx, chat); err != nil {
				return err
			}
			var unread int
			if err := tx.QueryRowContext(ctx, `SELECT unread FROM chats WHERE jid = ?`, chat).Scan(&unread); err != nil {
				return fmt.Errorf("store: history unread: %w", err)
			}
			if err := advanceReadMarker(ctx, tx, chat, unread); err != nil {
				return err
			}
		}
		return nil
	})
}

// UpdateStatus applies a receipt to our own messages. A status never moves
// backwards; "error" only replaces "pending". Returns the messages that
// actually changed (unknown ids and no-op receipts are skipped).
func (s *Store) UpdateStatus(ctx context.Context, chat string, ids []string, status string) ([]*api.Message, error) {
	var cond string
	switch status {
	case api.StatusError:
		cond = `status IN ('pending', '')`
	case api.StatusPending, api.StatusSent, api.StatusDelivered, api.StatusRead:
		cond = fmt.Sprintf(`(status = '' OR %s < %d)`, rankSQL("status"), api.StatusRank(status))
	default:
		return nil, api.InvalidParams("unknown status %q", status)
	}
	out := []*api.Message{}
	if len(ids) == 0 {
		return out, nil
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `UPDATE messages SET status = ? WHERE chat = ? AND id = ? AND from_me = 1 AND `+cond+` RETURNING seq`)
		if err != nil {
			return fmt.Errorf("store: prepare status: %w", err)
		}
		defer st.Close()
		var seqs []int64
		for _, id := range ids {
			var seq int64
			switch err := st.QueryRowContext(ctx, status, chat, id).Scan(&seq); {
			case errors.Is(err, sql.ErrNoRows):
				continue
			case err != nil:
				return fmt.Errorf("store: update status: %w", err)
			}
			seqs = append(seqs, seq)
		}
		got, err := loadBySeq(ctx, tx, seqs, true)
		if err != nil {
			return err
		}
		for _, seq := range seqs {
			if m := got[seq]; m != nil {
				out = append(out, m)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ApplyEdit replaces a message's text and mentions (edits older than the
// last applied one, and edits of revoked messages, are ignored).
func (s *Store) ApplyEdit(ctx context.Context, chat, id, text string, mentions []api.Mention, ts int64) (*api.Message, error) {
	var out *api.Message
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var deleted bool
		var editTS int64
		err := tx.QueryRowContext(ctx, `SELECT deleted, edit_ts FROM messages WHERE chat = ? AND id = ?`, chat, id).Scan(&deleted, &editTS)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("message %s/%s", chat, id)
		}
		if err != nil {
			return fmt.Errorf("store: edit: %w", err)
		}
		if !deleted && (ts == 0 || ts >= editTS) {
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET text = ?, mentions = ?, edited = 1, edit_ts = ? WHERE chat = ? AND id = ?`,
				text, mentionsJSON(mentions), max(ts, editTS), chat, id); err != nil {
				return fmt.Errorf("store: edit: %w", err)
			}
		}
		out, err = getMessage(ctx, tx, chat, id)
		return err
	})
	return out, err
}

// ApplyRevoke marks a message deleted for everyone: type "revoked", no
// text, mentions, media, location or reactions.
func (s *Store) ApplyRevoke(ctx context.Context, chat, id string) (*api.Message, error) {
	var out *api.Message
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var seq int64
		err := tx.QueryRowContext(ctx, `UPDATE messages SET deleted = 1, type = 'revoked', text = '', mentions = '',
	lat = NULL, lng = NULL, loc_name = '', reply_id = '', reply_sender = '', reply_preview = ''
WHERE chat = ? AND id = ? RETURNING seq`, chat, id).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("message %s/%s", chat, id)
		}
		if err != nil {
			return fmt.Errorf("store: revoke: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM media WHERE msg = ?`, seq); err != nil {
			return fmt.Errorf("store: revoke media: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM reactions WHERE chat = ? AND msg_id = ?`, chat, id); err != nil {
			return fmt.Errorf("store: revoke reactions: %w", err)
		}
		out, err = getMessage(ctx, tx, chat, id)
		return err
	})
	return out, err
}

type reactionStmts struct {
	upsert, del *sql.Stmt
}

func prepareReactionStmts(ctx context.Context, tx *sql.Tx) (*reactionStmts, error) {
	up, err := tx.PrepareContext(ctx, `INSERT INTO reactions(chat, msg_id, sender, from_me, emoji, ts) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(chat, msg_id, sender) DO UPDATE SET emoji = excluded.emoji, from_me = excluded.from_me, ts = excluded.ts
WHERE excluded.ts = 0 OR excluded.ts >= ts`)
	if err != nil {
		return nil, fmt.Errorf("store: prepare reaction: %w", err)
	}
	del, err := tx.PrepareContext(ctx, `DELETE FROM reactions WHERE chat = ?1 AND msg_id = ?2 AND sender = ?3 AND (?4 = 0 OR ts <= ?4)`)
	if err != nil {
		up.Close()
		return nil, fmt.Errorf("store: prepare reaction: %w", err)
	}
	return &reactionStmts{upsert: up, del: del}, nil
}

func (rs *reactionStmts) close() {
	rs.upsert.Close()
	rs.del.Close()
}

// reactionSender is the per-message reaction key: "" for our own reactions
// (from any of our devices / identities), else the reactor's JID.
func reactionSender(r *api.EvReaction) string {
	if r.FromMe {
		return ""
	}
	if r.Sender == "" {
		return r.Chat
	}
	return r.Sender
}

func (rs *reactionStmts) apply(ctx context.Context, r *api.EvReaction) error {
	if r.Chat == "" || r.ID == "" {
		return nil
	}
	sender := reactionSender(r)
	var err error
	if r.Emoji == "" {
		_, err = rs.del.ExecContext(ctx, r.Chat, r.ID, sender, r.Ts)
	} else {
		_, err = rs.upsert.ExecContext(ctx, r.Chat, r.ID, sender, b2i(r.FromMe), r.Emoji, r.Ts)
	}
	if err != nil {
		return fmt.Errorf("store: reaction: %w", err)
	}
	return nil
}

// ApplyReaction sets or (emoji "") removes one sender's reaction. Reactions
// to unknown messages return ErrNotFound and are dropped.
func (s *Store) ApplyReaction(ctx context.Context, r api.EvReaction) (*api.Message, error) {
	var out *api.Message
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE chat = ? AND id = ?`, r.Chat, r.ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound("message %s/%s", r.Chat, r.ID)
		}
		if err != nil {
			return fmt.Errorf("store: reaction: %w", err)
		}
		rs, err := prepareReactionStmts(ctx, tx)
		if err != nil {
			return err
		}
		defer rs.close()
		if err := rs.apply(ctx, &r); err != nil {
			return err
		}
		out, err = getMessage(ctx, tx, r.Chat, r.ID)
		return err
	})
	return out, err
}
