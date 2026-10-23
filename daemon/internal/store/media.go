package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"wisp/daemon/internal/api"
)

// GetMediaRef returns the download reference and embedded thumbnail for a
// message's attachment (ErrNotFound if the message has no media).
func (s *Store) GetMediaRef(ctx context.Context, chat, id string) (*api.MediaRef, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref := &api.MediaRef{Chat: chat, ID: id}
	var thumb []byte
	var fileLength int64
	err := s.r.QueryRowContext(ctx, `SELECT m.sender, m.from_me, m.type, md.mime, md.direct_path, md.url, md.media_key,
	md.file_sha256, md.file_enc_sha256, md.file_length, md.file_name, md.thumb
FROM messages m JOIN media md ON md.msg = m.seq
WHERE m.chat = ? AND m.id = ? AND m.deleted = 0`, chat, id).Scan(&ref.Sender, &ref.FromMe, &ref.Type, &ref.Mime,
		&ref.DirectPath, &ref.URL, &ref.MediaKey, &ref.FileSHA256, &ref.FileEncSHA256, &fileLength, &ref.FileName, &thumb)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, notFound("media %s/%s", chat, id)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("store: get media ref: %w", err)
	}
	ref.FileLength = uint64(fileLength)
	return ref, thumb, nil
}

// SetMediaPath records the cached decrypted file for a message ("" clears,
// e.g. after LRU eviction).
func (s *Store) SetMediaPath(ctx context.Context, chat, id, path string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE media SET path = ?
WHERE msg = (SELECT seq FROM messages WHERE chat = ? AND id = ?)`, path, chat, id)
		if err != nil {
			return fmt.Errorf("store: set media path: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("media %s/%s", chat, id)
		}
		return nil
	})
}

// SetMediaDirectPath records the path the phone re-uploaded media to after
// a media retry ("" = gone for good) and drops the stale URL.
func (s *Store) SetMediaDirectPath(ctx context.Context, chat, id, path string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE media SET direct_path = ?, url = ''
WHERE msg = (SELECT seq FROM messages WHERE chat = ? AND id = ?)`, path, chat, id)
		if err != nil {
			return fmt.Errorf("store: set media direct path: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("media %s/%s", chat, id)
		}
		return nil
	})
}
