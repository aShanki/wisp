package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"wisp/daemon/internal/api"
)

// ftsQuery turns free user input into a safe FTS5 query: every whitespace
// separated term becomes a quoted phrase (so operators and punctuation are
// literal), terms are ANDed, and the last one is a prefix match.
func ftsQuery(input string) string {
	var terms []string
	for _, f := range strings.Fields(input) {
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) < 0 {
			continue // nothing the tokenizer would index
		}
		terms = append(terms, `"`+strings.ReplaceAll(f, `"`, `""`)+`"`)
	}
	if len(terms) == 0 {
		return ""
	}
	terms[len(terms)-1] += "*"
	return strings.Join(terms, " ")
}

// snippetTokens is the FTS5 snippet length; ~16 tokens is about 100 chars.
const snippetTokens = 16

// Search runs a full-text query over message text and captions, newest
// first, optionally within one chat.
func (s *Store) Search(ctx context.Context, p api.SearchParams) ([]api.SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []api.SearchResult{}
	match := ftsQuery(p.Query)
	if match == "" {
		return out, nil
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := fmt.Sprintf(`SELECT m.chat, m.id, m.sender, m.push_name, m.from_me, m.ts,
	snippet(messages_fts, 0, '', '', '…', %d),
	COALESCE(ch.name, ''), COALESCE(ch.kind, ''),
	COALESCE(cc.full_name, ''), COALESCE(cc.push_name, ''), COALESCE(cc.business_name, ''),
	COALESCE(sc.full_name, ''), COALESCE(sc.push_name, ''), COALESCE(sc.business_name, ''),
	`+mentionsCol+`
FROM messages_fts
	JOIN messages m ON m.seq = messages_fts.rowid
	LEFT JOIN chats ch ON ch.jid = m.chat
	LEFT JOIN contacts cc ON cc.jid = m.chat
	LEFT JOIN contacts sc ON sc.jid = m.sender
WHERE messages_fts MATCH ?`, snippetTokens)
	args := []any{match}
	if p.Chat != "" {
		q += ` AND m.chat = ?`
		args = append(args, p.Chat)
	}
	q += ` ORDER BY m.ts DESC, m.seq DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: search: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r api.SearchResult
		var push, explicit, kind, mentions string
		var cn, sn names
		if err := rows.Scan(&r.Chat, &r.ID, &r.Sender, &push, &r.FromMe, &r.Timestamp, &r.Snippet,
			&explicit, &kind, &cn.full, &cn.push, &cn.business, &sn.full, &sn.push, &sn.business, &mentions); err != nil {
			return nil, fmt.Errorf("store: scan search: %w", err)
		}
		ms, err := decodeMentions(mentions)
		if err != nil {
			return nil, fmt.Errorf("store: scan search: %w", err)
		}
		if kind == "" {
			kind = kindOf(r.Chat)
		}
		r.ChatName = chatName(r.Chat, kind, explicit, cn)
		r.SenderName = senderName(r.Sender, r.FromMe, sn, push)
		r.Snippet = cleanSnippet(api.MentionText(r.Snippet, ms))
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search: %w", err)
	}
	return out, nil
}

// cleanSnippet makes a snippet one line of plain text.
func cleanSnippet(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
