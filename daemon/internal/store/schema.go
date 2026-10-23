package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations[i] upgrades the schema from user_version i to i+1. Append only;
// never edit a migration that has shipped.
var migrations = []string{
	// 1: initial schema.
	`
CREATE TABLE chats (
	jid           TEXT PRIMARY KEY,
	name          TEXT NOT NULL DEFAULT '',  -- explicit name (group subject, history); '' = derive from contact
	kind          TEXT NOT NULL DEFAULT 'user',
	avatar        TEXT NOT NULL DEFAULT '',
	last_activity INTEGER NOT NULL DEFAULT 0, -- only moves forward
	last_seq      INTEGER NOT NULL DEFAULT 0, -- messages.seq of the newest message, 0 = none
	unread        INTEGER NOT NULL DEFAULT 0,
	marked_unread INTEGER NOT NULL DEFAULT 0,
	muted_until   INTEGER NOT NULL DEFAULT 0, -- 0 = not muted, -1 = forever
	pinned        INTEGER NOT NULL DEFAULT 0,
	archived      INTEGER NOT NULL DEFAULT 0,
	read_only     INTEGER NOT NULL DEFAULT 0,
	read_ts       INTEGER NOT NULL DEFAULT 0, -- read marker: sort key of the newest message marked read
	read_seq      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX chats_order ON chats(archived, pinned DESC, last_activity DESC, jid);

CREATE TABLE messages (
	seq           INTEGER PRIMARY KEY,        -- insertion order; FTS rowid
	chat          TEXT NOT NULL,
	id            TEXT NOT NULL,
	sender        TEXT NOT NULL DEFAULT '',
	push_name     TEXT NOT NULL DEFAULT '',
	from_me       INTEGER NOT NULL DEFAULT 0,
	ts            INTEGER NOT NULL DEFAULT 0,
	type          TEXT NOT NULL DEFAULT 'text',
	text          TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL DEFAULT '',
	edited        INTEGER NOT NULL DEFAULT 0,
	edit_ts       INTEGER NOT NULL DEFAULT 0,
	deleted       INTEGER NOT NULL DEFAULT 0,
	forwarded     INTEGER NOT NULL DEFAULT 0,
	reply_id      TEXT NOT NULL DEFAULT '',
	reply_sender  TEXT NOT NULL DEFAULT '',
	reply_from_me INTEGER NOT NULL DEFAULT 0,
	reply_type    TEXT NOT NULL DEFAULT '',
	reply_preview TEXT NOT NULL DEFAULT '',
	lat           REAL,                       -- NULL = no location
	lng           REAL,
	loc_name      TEXT NOT NULL DEFAULT '',
	mentions      TEXT NOT NULL DEFAULT '',   -- space separated JIDs
	UNIQUE (chat, id)
);
CREATE INDEX messages_chat_ts ON messages(chat, ts, seq);

CREATE TABLE media (
	msg             INTEGER PRIMARY KEY REFERENCES messages(seq) ON DELETE CASCADE,
	mime            TEXT NOT NULL DEFAULT '',
	size            INTEGER NOT NULL DEFAULT 0,
	width           INTEGER NOT NULL DEFAULT 0,
	height          INTEGER NOT NULL DEFAULT 0,
	duration        INTEGER NOT NULL DEFAULT 0,
	file_name       TEXT NOT NULL DEFAULT '',
	has_thumb       INTEGER NOT NULL DEFAULT 0,
	path            TEXT NOT NULL DEFAULT '',  -- cached decrypted file
	direct_path     TEXT NOT NULL DEFAULT '',
	url             TEXT NOT NULL DEFAULT '',
	media_key       BLOB,
	file_sha256     BLOB,
	file_enc_sha256 BLOB,
	file_length     INTEGER NOT NULL DEFAULT 0,
	thumb           BLOB                       -- embedded JPEG thumbnail; last so listing skips its pages
);

CREATE TABLE reactions (
	chat    TEXT NOT NULL,
	msg_id  TEXT NOT NULL,
	sender  TEXT NOT NULL,                     -- '' for our own reactions
	from_me INTEGER NOT NULL DEFAULT 0,
	emoji   TEXT NOT NULL,
	ts      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (chat, msg_id, sender)
) WITHOUT ROWID;

CREATE TABLE contacts (
	jid           TEXT PRIMARY KEY,
	full_name     TEXT NOT NULL DEFAULT '',
	push_name     TEXT NOT NULL DEFAULT '',
	business_name TEXT NOT NULL DEFAULT '',
	phone         TEXT NOT NULL DEFAULT '',
	avatar        TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;

CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL                        -- raw JSON
) WITHOUT ROWID;

CREATE VIRTUAL TABLE messages_fts USING fts5(
	text,
	content='messages',
	content_rowid='seq',
	tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER messages_fts_ai AFTER INSERT ON messages BEGIN
	INSERT INTO messages_fts(rowid, text) VALUES (new.seq, new.text);
END;
CREATE TRIGGER messages_fts_ad AFTER DELETE ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, text) VALUES ('delete', old.seq, old.text);
END;
CREATE TRIGGER messages_fts_au AFTER UPDATE OF text ON messages WHEN old.text IS NOT new.text BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, text) VALUES ('delete', old.seq, old.text);
	INSERT INTO messages_fts(rowid, text) VALUES (new.seq, new.text);
END;
`,
	// 2: drop columns nothing reads.
	`
ALTER TABLE messages DROP COLUMN mentions;
ALTER TABLE contacts DROP COLUMN phone;
`,
	// 3: @mentions.
	`
ALTER TABLE messages ADD COLUMN mentions TEXT NOT NULL DEFAULT ''; -- JSON [{"jid", "me"}], '' = none
`,
	// 4: voice note waveforms.
	`
ALTER TABLE media ADD COLUMN waveform BLOB; -- levels 0-100
`,
}

func migrate(ctx context.Context, db *sql.DB) error {
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("store: read user_version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("store: database schema v%d is newer than this wispd (v%d)", v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: migrate begin: %w", err)
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: set user_version %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("store: migration %d commit: %w", v+1, err)
		}
	}
	return nil
}
