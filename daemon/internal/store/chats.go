package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"wisp/daemon/internal/api"
)

// ensureChat creates the chat row if missing (kind from the JID).
func ensureChat(ctx context.Context, tx querier, jid string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO chats(jid, kind) VALUES (?, ?) ON CONFLICT(jid) DO NOTHING`, jid, kindOf(jid))
	if err != nil {
		return fmt.Errorf("store: ensure chat %s: %w", jid, err)
	}
	return nil
}

// applyPatch applies a ChatPatch inside a transaction.
func applyPatch(ctx context.Context, tx querier, p *api.ChatPatch) error {
	if p.JID == "" {
		return api.InvalidParams("chat patch without jid")
	}
	if err := ensureChat(ctx, tx, p.JID); err != nil {
		return err
	}
	var sets []string
	var args []any
	set := func(col string, v any) {
		sets = append(sets, col)
		args = append(args, v)
	}
	if p.Name != nil {
		set("name = ?", *p.Name)
	}
	if p.Kind != nil && *p.Kind != "" {
		set("kind = ?", *p.Kind)
	}
	if p.LastActivity != nil {
		set("last_activity = MAX(last_activity, ?)", *p.LastActivity)
	}
	if p.Unread != nil {
		u := *p.Unread
		if u < 0 {
			u = 0
		}
		set("unread = ?", u)
	}
	if p.MarkedUnread != nil {
		set("marked_unread = ?", b2i(*p.MarkedUnread))
	}
	if p.MutedUntil != nil {
		set("muted_until = ?", *p.MutedUntil)
	}
	if p.Pinned != nil {
		set("pinned = ?", max(*p.Pinned, 0))
	}
	if p.Archived != nil {
		set("archived = ?", b2i(*p.Archived))
	}
	if p.ReadOnly != nil {
		set("read_only = ?", b2i(*p.ReadOnly))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, p.JID)
	if _, err := tx.ExecContext(ctx, `UPDATE chats SET `+strings.Join(sets, ", ")+` WHERE jid = ?`, args...); err != nil {
		return fmt.Errorf("store: patch chat %s: %w", p.JID, err)
	}
	// Reading a chat elsewhere (unread -> 0) also moves our read marker so
	// we don't send stale receipts later.
	if p.Unread != nil && *p.Unread == 0 {
		if err := advanceReadMarker(ctx, tx, p.JID, 0); err != nil {
			return err
		}
	}
	return nil
}

// UpsertChat creates or patches a chat row. Nil fields are left alone;
// LastActivity only moves forward.
func (s *Store) UpsertChat(ctx context.Context, p api.ChatPatch) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return applyPatch(ctx, tx, &p) })
}

const chatCols = `c.jid, c.name, c.kind, c.avatar, c.last_activity, c.last_seq, c.unread,
	c.marked_unread, c.muted_until, c.pinned, c.archived, c.read_only,
	COALESCE(ct.full_name, ''), COALESCE(ct.push_name, ''), COALESCE(ct.business_name, ''), COALESCE(ct.avatar, '')`

const chatFrom = ` FROM chats c LEFT JOIN contacts ct ON ct.jid = c.jid`

type chatRow struct {
	chat    api.Chat
	lastSeq int64
}

func scanChat(sc interface{ Scan(...any) error }, now int64) (chatRow, error) {
	var r chatRow
	c := &r.chat
	var explicit, ctAvatar string
	var n names
	if err := sc.Scan(&c.JID, &explicit, &c.Kind, &c.Avatar, &c.LastActivity, &r.lastSeq, &c.Unread,
		&c.MarkedUnread, &c.MutedUntil, &c.Pinned, &c.Archived, &c.ReadOnly,
		&n.full, &n.push, &n.business, &ctAvatar); err != nil {
		return r, err
	}
	c.Name = chatName(c.JID, c.Kind, explicit, n)
	if c.Avatar == "" {
		c.Avatar = ctAvatar
	}
	switch {
	case c.MutedUntil == -1:
		c.Muted = true
	case c.MutedUntil > now:
		c.Muted = true
	default:
		c.MutedUntil = 0
	}
	return r, nil
}

// fillLastMessages attaches the lastMessage stub to each chat.
func (s *Store) fillLastMessages(ctx context.Context, q querier, rows []chatRow) error {
	seqs := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.lastSeq != 0 {
			seqs = append(seqs, r.lastSeq)
		}
	}
	if len(seqs) == 0 {
		return nil
	}
	msgs, err := loadBySeq(ctx, q, seqs, false)
	if err != nil {
		return err
	}
	for i := range rows {
		if m, ok := msgs[rows[i].lastSeq]; ok {
			rows[i].chat.LastMessage = api.Stub(m)
		}
	}
	return nil
}

func getChat(ctx context.Context, s *Store, q querier, jid string) (*api.Chat, error) {
	r, err := scanChat(q.QueryRowContext(ctx, `SELECT `+chatCols+chatFrom+` WHERE c.jid = ?`, jid), time.Now().Unix())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("chat %s", jid)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get chat: %w", err)
	}
	rows := []chatRow{r}
	if err := s.fillLastMessages(ctx, q, rows); err != nil {
		return nil, err
	}
	return &rows[0].chat, nil
}

// GetChat returns one chat (ErrNotFound if missing).
func (s *Store) GetChat(ctx context.Context, jid string) (*api.Chat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return getChat(ctx, s, s.r, jid)
}

func encodeCursor(c *api.Chat) string {
	raw := fmt.Sprintf("%d|%d|%s", c.Pinned, c.LastActivity, c.JID)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (pinned, act int64, jid string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, 0, "", api.InvalidParams("bad cursor")
	}
	parts := strings.SplitN(string(raw), "|", 3)
	if len(parts) != 3 {
		return 0, 0, "", api.InvalidParams("bad cursor")
	}
	pinned, err1 := strconv.ParseInt(parts[0], 10, 64)
	act, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, "", api.InvalidParams("bad cursor")
	}
	return pinned, act, parts[2], nil
}

// ListChats returns one page of the chat list: pinned first (newest pin
// first, as WhatsApp does), then lastActivity descending, then jid.
// Keyset-paged via an opaque cursor.
func (s *Store) ListChats(ctx context.Context, p api.ChatsListParams) (*api.ChatsListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	q := `SELECT ` + chatCols + chatFrom + ` WHERE c.archived = ? AND c.jid <> 'status@broadcast'`
	args := []any{b2i(p.Archived)}
	if p.Cursor != "" {
		pinned, act, jid, err := decodeCursor(p.Cursor)
		if err != nil {
			return nil, err
		}
		q += ` AND (c.pinned < ? OR (c.pinned = ? AND (c.last_activity < ? OR (c.last_activity = ? AND c.jid > ?))))`
		args = append(args, pinned, pinned, act, act, jid)
	}
	q += ` ORDER BY c.pinned DESC, c.last_activity DESC, c.jid LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list chats: %w", err)
	}
	now := time.Now().Unix()
	var list []chatRow
	for rows.Next() {
		r, err := scanChat(rows, now)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan chat: %w", err)
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list chats: %w", err)
	}
	res := &api.ChatsListResult{Chats: []api.Chat{}}
	if len(list) > limit {
		list = list[:limit]
		res.NextCursor = encodeCursor(&list[limit-1].chat)
	}
	if err := s.fillLastMessages(ctx, s.r, list); err != nil {
		return nil, err
	}
	for i := range list {
		res.Chats = append(res.Chats, list[i].chat)
	}
	return res, nil
}

// refreshLast recomputes a chat's newest message pointer and moves
// lastActivity forward to it.
func refreshLast(ctx context.Context, tx querier, chat string) error {
	_, err := tx.ExecContext(ctx, `UPDATE chats SET
	last_seq = COALESCE((SELECT seq FROM messages WHERE chat = ?1 ORDER BY ts DESC, seq DESC LIMIT 1), 0),
	last_activity = MAX(last_activity, COALESCE((SELECT MAX(ts) FROM messages WHERE chat = ?1), 0))
WHERE jid = ?1`, chat)
	if err != nil {
		return fmt.Errorf("store: refresh chat %s: %w", chat, err)
	}
	return nil
}

// advanceReadMarker moves the chat's read marker forward. skipIncoming = 0
// puts it on the newest message; n > 0 leaves the newest n incoming messages
// unread (used by history sync, which knows unread counts but not which).
func advanceReadMarker(ctx context.Context, tx querier, chat string, skipIncoming int) error {
	var ts, seq int64
	var err error
	if skipIncoming <= 0 {
		err = tx.QueryRowContext(ctx, `SELECT ts, seq FROM messages WHERE chat = ? ORDER BY ts DESC, seq DESC LIMIT 1`, chat).Scan(&ts, &seq)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT ts, seq FROM messages WHERE chat = ? AND from_me = 0 AND type <> 'system'
ORDER BY ts DESC, seq DESC LIMIT 1 OFFSET ?`, chat, skipIncoming).Scan(&ts, &seq)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: read marker: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE chats SET read_ts = ?2, read_seq = ?3
WHERE jid = ?1 AND (read_ts < ?2 OR (read_ts = ?2 AND read_seq < ?3))`, chat, ts, seq)
	if err != nil {
		return fmt.Errorf("store: read marker: %w", err)
	}
	return nil
}

// MarkRead zeroes the unread count, clears markedUnread and moves the read
// marker to the newest message.
func (s *Store) MarkRead(ctx context.Context, chat string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE chats SET unread = 0, marked_unread = 0 WHERE jid = ?`, chat)
		if err != nil {
			return fmt.Errorf("store: mark read: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("chat %s", chat)
		}
		return advanceReadMarker(ctx, tx, chat, 0)
	})
}

// UnreadIncoming returns incoming messages newer than the read marker (the
// newest `limit` of them), oldest first, for sending read receipts.
func (s *Store) UnreadIncoming(ctx context.Context, chat string, limit int) ([]api.MsgKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	var rts, rseq int64
	err := s.r.QueryRowContext(ctx, `SELECT read_ts, read_seq FROM chats WHERE jid = ?`, chat).Scan(&rts, &rseq)
	if errors.Is(err, sql.ErrNoRows) {
		return []api.MsgKey{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: unread incoming: %w", err)
	}
	rows, err := s.r.QueryContext(ctx, `SELECT id, sender FROM messages
WHERE chat = ? AND (ts, seq) > (?, ?) AND from_me = 0 AND type <> 'system'
ORDER BY ts DESC, seq DESC LIMIT ?`, chat, rts, rseq, limit)
	if err != nil {
		return nil, fmt.Errorf("store: unread incoming: %w", err)
	}
	defer rows.Close()
	out := []api.MsgKey{}
	for rows.Next() {
		k := api.MsgKey{Chat: chat}
		if err := rows.Scan(&k.ID, &k.Sender); err != nil {
			return nil, err
		}
		if k.Sender == "" {
			k.Sender = chat
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Reverse(out)
	return out, nil
}
