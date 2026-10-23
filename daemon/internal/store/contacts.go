package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"wisp/daemon/internal/api"
)

// jidUser returns the user part of a JID without device/agent suffixes.
func jidUser(jid string) (user, server string) {
	user, server, ok := strings.Cut(jid, "@")
	if !ok {
		return jid, ""
	}
	if i := strings.IndexByte(user, ':'); i >= 0 {
		user = user[:i]
	}
	if i := strings.IndexByte(user, '.'); i >= 0 {
		user = user[:i]
	}
	return user, server
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// phoneOf returns the E.164 digits (no '+') for a phone-number JID, "" otherwise.
func phoneOf(jid string) string {
	user, server := jidUser(jid)
	if server == "s.whatsapp.net" && allDigits(user) {
		return user
	}
	return ""
}

// formatPhone renders a phone-number JID for display ("+61400000001"), or
// the bare user part for other JIDs.
func formatPhone(jid string) string {
	if p := phoneOf(jid); p != "" {
		return "+" + p
	}
	user, _ := jidUser(jid)
	return user
}

// kindOf derives the chat kind from the JID server.
func kindOf(jid string) string {
	_, server := jidUser(jid)
	switch server {
	case "g.us":
		return api.ChatGroup
	case "broadcast":
		return api.ChatBroadcast
	case "newsletter":
		return api.ChatNewsletter
	}
	return api.ChatUser
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// names is the contact name fields joined onto a row.
type names struct {
	full, push, business string
}

// display resolves a person's display name: address book > push name >
// business name > fallback push name (e.g. from the message) > phone.
func (n names) display(jid, pushFallback string) string {
	if s := firstNonEmpty(n.full, n.push, n.business, pushFallback); s != "" {
		return s
	}
	if jid == "" {
		return ""
	}
	return formatPhone(jid)
}

// chatName resolves a chat's display name.
func chatName(jid, kind, explicit string, n names) string {
	if explicit != "" {
		return explicit
	}
	if kind == api.ChatGroup {
		return firstNonEmpty(n.full, "Unnamed group")
	}
	return n.display(jid, "")
}

const contactCols = `jid, full_name, push_name, business_name, avatar`

func scanContact(sc interface{ Scan(...any) error }) (api.Contact, error) {
	var c api.Contact
	if err := sc.Scan(&c.JID, &c.FullName, &c.PushName, &c.BusinessName, &c.Avatar); err != nil {
		return c, err
	}
	c.Phone = phoneOf(c.JID)
	c.Name = names{c.FullName, c.PushName, c.BusinessName}.display(c.JID, "")
	return c, nil
}

const upsertContactSQL = `
INSERT INTO contacts(jid, full_name, push_name, business_name, avatar)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(jid) DO UPDATE SET
	full_name     = CASE WHEN excluded.full_name     <> '' THEN excluded.full_name     ELSE full_name END,
	push_name     = CASE WHEN excluded.push_name     <> '' THEN excluded.push_name     ELSE push_name END,
	business_name = CASE WHEN excluded.business_name <> '' THEN excluded.business_name ELSE business_name END,
	avatar        = CASE WHEN excluded.avatar        <> '' THEN excluded.avatar        ELSE avatar END`

// contactArgs ignores c.Name and c.Phone: both are derived, and storing a
// fallback like the phone number as a name would hide push names learned later.
func contactArgs(c *api.Contact) []any {
	return []any{c.JID, c.FullName, c.PushName, c.BusinessName, c.Avatar}
}

// PutContacts merges contacts: non-empty fields overwrite, empty ones keep
// what we had.
func (s *Store) PutContacts(ctx context.Context, cs []api.Contact) error {
	if len(cs) == 0 {
		return nil
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		return putContacts(ctx, tx, cs)
	})
}

func putContacts(ctx context.Context, tx *sql.Tx, cs []api.Contact) error {
	if len(cs) == 0 {
		return nil
	}
	st, err := tx.PrepareContext(ctx, upsertContactSQL)
	if err != nil {
		return fmt.Errorf("store: prepare contact: %w", err)
	}
	defer st.Close()
	for i := range cs {
		if cs[i].JID == "" {
			continue
		}
		if _, err := st.ExecContext(ctx, contactArgs(&cs[i])...); err != nil {
			return fmt.Errorf("store: put contact %s: %w", cs[i].JID, err)
		}
	}
	return nil
}

// GetContact returns the stored contact for jid (ErrNotFound if unknown).
func (s *Store) GetContact(ctx context.Context, jid string) (*api.Contact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, err := scanContact(s.r.QueryRowContext(ctx, `SELECT `+contactCols+` FROM contacts WHERE jid = ?`, jid))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("contact %s", jid)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get contact: %w", err)
	}
	return &c, nil
}

// SetAvatar records the cached avatar path for a person or group ("" clears).
func (s *Store) SetAvatar(ctx context.Context, jid, path string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET avatar = ? WHERE jid = ?`, path, jid); err != nil {
			return fmt.Errorf("store: set chat avatar: %w", err)
		}
		if kindOf(jid) != api.ChatUser {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO contacts(jid, avatar) VALUES (?, ?)
ON CONFLICT(jid) DO UPDATE SET avatar = excluded.avatar`, jid, path); err != nil {
			return fmt.Errorf("store: set contact avatar: %w", err)
		}
		return nil
	})
}
