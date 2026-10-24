package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"wisp/daemon/internal/api"
)

// favoriteChat is the media cache's chat key for favourites, which belong to
// no message.
const favoriteChat = "stickers"

// ListStickers returns the favourites, newest first, then up to recent other
// stickers we sent, most recently sent first, one per file. Only WebP
// stickers: animated Lottie ones can't be shown.
func (s *Store) ListStickers(ctx context.Context, recent int) ([]api.Sticker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []api.Sticker{}
	rows, err := s.r.QueryContext(ctx, `SELECT id, width, height FROM sticker_favorites ORDER BY ts DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("store: list favourite stickers: %w", err)
	}
	for rows.Next() {
		st := api.Sticker{Favorite: true}
		if err := rows.Scan(&st.ID, &st.Width, &st.Height); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan favourite sticker: %w", err)
		}
		out = append(out, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list favourite stickers: %w", err)
	}

	// SQLite takes the bare columns from the row holding MAX(m.ts).
	rows, err = s.r.QueryContext(ctx, `SELECT lower(hex(md.file_enc_sha256)), md.width, md.height, MAX(m.ts)
FROM messages m JOIN media md ON md.msg = m.seq
WHERE m.type = 'sticker' AND m.from_me = 1 AND m.deleted = 0 AND md.mime = 'image/webp'
	AND length(md.file_enc_sha256) = 32 AND length(md.file_sha256) = 32
	AND lower(hex(md.file_enc_sha256)) NOT IN (SELECT id FROM sticker_favorites)
GROUP BY md.file_sha256 ORDER BY MAX(m.ts) DESC LIMIT ?`, recent)
	if err != nil {
		return nil, fmt.Errorf("store: list recent stickers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st api.Sticker
		var ts int64
		if err := rows.Scan(&st.ID, &st.Width, &st.Height, &ts); err != nil {
			return nil, fmt.Errorf("store: scan recent sticker: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list recent stickers: %w", err)
	}
	return out, nil
}

// StickerRef returns what's needed to download and resend sticker id: the
// favourite's, else the newest message's with that file.
func (s *Store) StickerRef(ctx context.Context, id string) (*api.MediaRef, api.Sticker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := api.Sticker{ID: id}
	ref := &api.MediaRef{Type: api.MsgSticker}
	var length int64
	err := s.r.QueryRowContext(ctx, `SELECT mime, width, height, direct_path, url, media_key, file_enc_sha256, file_length
FROM sticker_favorites WHERE id = ?`, id).Scan(&ref.Mime, &st.Width, &st.Height, &ref.DirectPath, &ref.URL,
		&ref.MediaKey, &ref.FileEncSHA256, &length)
	if err == nil {
		st.Favorite = true
		ref.Chat, ref.ID, ref.FileLength = favoriteChat, id, uint64(length)
		return ref, st, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, st, fmt.Errorf("store: get favourite sticker: %w", err)
	}
	hash, err := hex.DecodeString(id)
	if err != nil || len(hash) != 32 {
		return nil, st, notFound("sticker %s", id)
	}
	err = s.r.QueryRowContext(ctx, `SELECT m.chat, m.id, m.sender, m.from_me, md.mime, md.width, md.height,
	md.direct_path, md.url, md.media_key, md.file_sha256, md.file_enc_sha256, md.file_length
FROM messages m JOIN media md ON md.msg = m.seq
WHERE m.type = 'sticker' AND m.deleted = 0 AND md.file_enc_sha256 = ?
ORDER BY m.ts DESC LIMIT 1`, hash).Scan(&ref.Chat, &ref.ID, &ref.Sender, &ref.FromMe, &ref.Mime, &st.Width, &st.Height,
		&ref.DirectPath, &ref.URL, &ref.MediaKey, &ref.FileSHA256, &ref.FileEncSHA256, &length)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, st, notFound("sticker %s", id)
	}
	if err != nil {
		return nil, st, fmt.Errorf("store: get sticker: %w", err)
	}
	ref.FileLength = uint64(length)
	return ref, st, nil
}

// PutFavoriteSticker records a favourite from the phone, or forgets it.
func (s *Store) PutFavoriteSticker(ctx context.Context, f api.EvFavoriteSticker) error {
	if len(f.Ref.FileEncSHA256) == 0 {
		return api.InvalidParams("favourite sticker without a file hash")
	}
	id := hex.EncodeToString(f.Ref.FileEncSHA256)
	return s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if !f.Favorite {
			_, err = tx.ExecContext(ctx, `DELETE FROM sticker_favorites WHERE id = ?`, id)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO sticker_favorites
	(id, ts, mime, width, height, direct_path, url, media_key, file_enc_sha256, file_length)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET ts = excluded.ts, mime = excluded.mime, width = excluded.width,
	height = excluded.height, direct_path = excluded.direct_path, url = excluded.url,
	media_key = excluded.media_key, file_length = excluded.file_length`,
				id, f.TS, f.Ref.Mime, f.Width, f.Height, f.Ref.DirectPath, f.Ref.URL, f.Ref.MediaKey,
				f.Ref.FileEncSHA256, int64(f.Ref.FileLength))
		}
		if err != nil {
			return fmt.Errorf("store: put favourite sticker: %w", err)
		}
		return nil
	})
}
