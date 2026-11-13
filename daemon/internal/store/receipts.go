package store

import (
	"context"
	"database/sql"
	"fmt"
	"wisp/daemon/internal/api"
)

// ApplyReceipt records only observed receipts for existing outgoing messages.
func (s *Store) ApplyReceipt(ctx context.Context, e api.EvReceipt) ([]*api.Message, error) {
	out := []*api.Message{}
	if e.Sender == "" || (e.ReceiptStatus != api.StatusRead && e.ReceiptStatus != api.StatusDelivered) {
		return out, nil
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for _, id := range e.IDs {
			var seq int64
			err := tx.QueryRowContext(ctx, `SELECT seq FROM messages WHERE chat=? AND id=? AND from_me=1 AND deleted=0`, e.Chat, id).Scan(&seq)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			col := "delivered"
			if e.ReceiptStatus == api.StatusRead {
				col = "read"
			}
			// Preserve the first known timestamp; a later duplicate must not rewrite it.
			q := fmt.Sprintf(`INSERT INTO message_receipts(msg,sender,%s,%s_seen) VALUES(?,?,?,1)
    ON CONFLICT(msg,sender) DO UPDATE SET %s=CASE WHEN %s=0 THEN excluded.%s ELSE %s END, %s_seen=1
    WHERE %s_seen=0 OR (%s=0 AND excluded.%s<>0)`, col, col, col, col, col, col, col, col, col, col)
			result, err := tx.ExecContext(ctx, q, seq, e.Sender, e.Ts)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			m, err := getMessage(ctx, tx, e.Chat, id)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	return out, err
}
func (s *Store) ListReceipts(ctx context.Context, chat, id string) ([]api.MessageReceipt, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var exists int
	if err := s.r.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE chat=? AND id=? AND from_me=1 AND deleted=0`, chat, id).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, notFound("message %s/%s", chat, id)
		}
		return nil, err
	}
	rows, err := s.r.QueryContext(ctx, `SELECT r.sender,r.delivered_seen,r.delivered,r.read_seen,r.read,COALESCE(c.full_name,''),COALESCE(c.push_name,''),COALESCE(c.business_name,'') FROM message_receipts r JOIN messages m ON m.seq=r.msg LEFT JOIN contacts c ON c.jid=r.sender WHERE m.chat=? AND m.id=? ORDER BY r.read_seen DESC, r.read DESC, r.delivered DESC, r.sender`, chat, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.MessageReceipt{}
	for rows.Next() {
		var r api.MessageReceipt
		var n names
		if err := rows.Scan(&r.JID, &r.Delivered, &r.DeliveredAt, &r.Read, &r.ReadAt, &n.full, &n.push, &n.business); err != nil {
			return nil, err
		}
		r.Name = n.display("", "")
		out = append(out, r)
	}
	return out, rows.Err()
}
