package api

import (
	"fmt"
	"strings"
)

// PreviewText renders the one-line chat-list / quote / notification preview
// for a message. Shared so daemon, mock and notifications agree.
func PreviewText(m *Message) string {
	if m == nil {
		return ""
	}
	if m.Deleted || m.Type == MsgRevoked {
		return "🚫 This message was deleted"
	}
	text := firstLine(MentionText(m.Text, m.Mentions), 120)
	withCaption := func(label string) string {
		if text != "" {
			return label + " " + text
		}
		return label
	}
	switch m.Type {
	case MsgText, MsgSystem:
		return text
	case MsgImage:
		return withCaption("📷 Photo")
	case MsgVideo:
		return withCaption("🎥 Video")
	case MsgGIF:
		return withCaption("GIF")
	case MsgVoice:
		if m.Media != nil && m.Media.Duration > 0 {
			return fmt.Sprintf("🎤 Voice message (%d:%02d)", m.Media.Duration/60, m.Media.Duration%60)
		}
		return "🎤 Voice message"
	case MsgAudio:
		return withCaption("🎵 Audio")
	case MsgDocument:
		name := ""
		if m.Media != nil {
			name = m.Media.FileName
		}
		if name == "" {
			name = "Document"
		}
		return withCaption("📄 " + name)
	case MsgSticker:
		return "Sticker"
	case MsgLocation:
		if m.Location != nil && m.Location.Name != "" {
			return "📍 " + m.Location.Name
		}
		return "📍 Location"
	case MsgContact:
		return withCaption("👤 Contact")
	case MsgPoll:
		return withCaption("📊 Poll:")
	case MsgUnsupported:
		return "Unsupported message"
	}
	return text
}

// Stub builds the chat-list stub for a message.
func Stub(m *Message) *MessageStub {
	if m == nil {
		return nil
	}
	return &MessageStub{
		ID:         m.ID,
		Sender:     m.Sender,
		SenderName: m.SenderName,
		FromMe:     m.FromMe,
		Type:       m.Type,
		Preview:    PreviewText(m),
		Timestamp:  m.Timestamp,
		Status:     m.Status,
		Mentions:   m.Mentions,
	}
}

// MentionText shows each mention's "@<user>" token in text as "@Name".
func MentionText(text string, ms []Mention) string {
	for _, mn := range ms {
		user, _, _ := strings.Cut(mn.JID, "@")
		if mn.Name != "" {
			text = ReplaceMention(text, user, mn.Name)
		}
	}
	return text
}

// ReplaceMention replaces the "@from" mention tokens in text by "@to". A
// token ends with its digits: "@614" is not in "@6145".
func ReplaceMention(text, from, to string) string {
	tok := "@" + from
	if from == "" || !strings.Contains(text, tok) {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, tok)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		end := i + len(tok)
		b.WriteString(text[:i])
		if end < len(text) && text[end] >= '0' && text[end] <= '9' {
			b.WriteString(tok)
		} else {
			b.WriteString("@" + to)
		}
		text = text[end:]
	}
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}
