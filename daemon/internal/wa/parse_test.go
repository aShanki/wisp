package wa

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"wisp/daemon/internal/api"
)

var (
	selfPN   = types.NewJID("61400000000", types.DefaultUserServer)
	selfLID  = types.NewJID("900", types.HiddenUserServer)
	alicePN  = types.NewJID("61400000001", types.DefaultUserServer)
	aliceLID = types.NewJID("901", types.HiddenUserServer)
	group    = types.NewJID("120363000000000001", types.GroupServer)
	ts       = time.Unix(1761190000, 0)
)

func testConverter() *converter {
	return &converter{
		pnForLID: func(lid types.JID) types.JID {
			if lid.User == aliceLID.User {
				return alicePN
			}
			return types.JID{}
		},
		name: func(j types.JID) string {
			if j == alicePN {
				return "Alice"
			}
			return ""
		},
		selfPN:  selfPN,
		selfLID: selfLID,
	}
}

func fromAlice(id string) *types.MessageInfo {
	return &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: alicePN, Sender: alicePN},
		ID:            id, Timestamp: ts, PushName: "ally",
	}
}

func mustMsg(t *testing.T, p parsed) *api.Message {
	t.Helper()
	if p.msg == nil {
		t.Fatalf("no message: %+v", p)
	}
	return p.msg
}

func TestText(t *testing.T) {
	m := mustMsg(t, testConverter().convert(fromAlice("A1"), &waE2E.Message{Conversation: new("hi *there*")}))
	if m.Type != api.MsgText || m.Text != "hi *there*" || m.ID != "A1" || m.Chat != alicePN.String() ||
		m.Sender != alicePN.String() || m.SenderName != "Alice" || m.PushName != "ally" ||
		m.Timestamp != ts.Unix() || m.FromMe || m.Status != "" || m.Reactions == nil {
		t.Fatalf("got %+v", m)
	}
}

func TestLIDCanonical(t *testing.T) {
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: aliceLID, Sender: aliceLID, SenderAlt: alicePN},
		ID:            "L1", Timestamp: ts,
	}
	m := mustMsg(t, testConverter().convert(info, &waE2E.Message{Conversation: new("x")}))
	if m.Chat != alicePN.String() || m.Sender != alicePN.String() {
		t.Fatalf("not canonical: chat %s sender %s", m.Chat, m.Sender)
	}
	// Mapping known only from the LID store.
	info.SenderAlt = types.JID{}
	m = mustMsg(t, testConverter().convert(info, &waE2E.Message{Conversation: new("x")}))
	if m.Chat != alicePN.String() || m.Sender != alicePN.String() {
		t.Fatalf("not canonical via store: chat %s sender %s", m.Chat, m.Sender)
	}
}

func TestFromMe(t *testing.T) {
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: alicePN, Sender: selfLID, IsFromMe: true},
		ID:            "M1", Timestamp: ts,
	}
	m := mustMsg(t, testConverter().convert(info, &waE2E.Message{Conversation: new("yo")}))
	if !m.FromMe || m.Sender != selfPN.String() || m.Status != api.StatusSent || m.SenderName != "" {
		t.Fatalf("got %+v", m)
	}
}

func TestReply(t *testing.T) {
	raw := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: new("sure"),
		ContextInfo: &waE2E.ContextInfo{
			StanzaID:      new("Q1"),
			Participant:   new(selfLID.String()),
			QuotedMessage: &waE2E.Message{Conversation: new("dinner at 6?")},
			IsForwarded:   new(true),
		},
	}}
	m := mustMsg(t, testConverter().convert(fromAlice("A2"), raw))
	q := m.ReplyTo
	if q == nil || q.ID != "Q1" || !q.FromMe || q.Sender != selfPN.String() || q.Type != api.MsgText || q.Preview != "dinner at 6?" {
		t.Fatalf("quote %+v", q)
	}
	if !m.Forwarded || m.Text != "sure" {
		t.Fatalf("got %+v", m)
	}
}

func TestImageWithThumb(t *testing.T) {
	thumb := []byte{0xff, 0xd8, 0xff}
	raw := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption:       new("look"),
		Mimetype:      new("image/jpeg"),
		DirectPath:    new("/v/t62/abc"),
		MediaKey:      []byte{1, 2, 3},
		FileSHA256:    []byte{4},
		FileEncSHA256: []byte{5},
		FileLength:    new(uint64(183422)),
		Width:         new(uint32(1280)),
		Height:        new(uint32(960)),
		JPEGThumbnail: thumb,
	}}
	m := mustMsg(t, testConverter().convert(fromAlice("A3"), raw))
	if m.Type != api.MsgImage || m.Text != "look" || !bytes.Equal(m.Thumb, thumb) {
		t.Fatalf("got %+v", m)
	}
	if mi := m.Media; mi == nil || mi.Mime != "image/jpeg" || mi.Size != 183422 || mi.Width != 1280 || mi.Height != 960 || !mi.HasThumb {
		t.Fatalf("media %+v", m.Media)
	}
	r := m.MediaRef
	if r == nil || r.Chat != alicePN.String() || r.ID != "A3" || r.Type != api.MsgImage || r.DirectPath != "/v/t62/abc" ||
		!bytes.Equal(r.MediaKey, []byte{1, 2, 3}) || r.FileLength != 183422 {
		t.Fatalf("ref %+v", r)
	}
}

func TestVoice(t *testing.T) {
	raw := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
		Mimetype: new("audio/ogg; codecs=opus"), PTT: new(true), Seconds: new(uint32(7)), DirectPath: new("/v/a"),
	}}
	m := mustMsg(t, testConverter().convert(fromAlice("A4"), raw))
	if m.Type != api.MsgVoice || m.Media.Duration != 7 || m.MediaRef.Type != api.MsgVoice {
		t.Fatalf("got %+v", m)
	}
}

func TestSticker(t *testing.T) {
	raw := &waE2E.Message{StickerMessage: &waE2E.StickerMessage{DirectPath: new("/v/s"), Width: new(uint32(512)), Height: new(uint32(512))}}
	m := mustMsg(t, testConverter().convert(fromAlice("A5"), raw))
	if m.Type != api.MsgSticker || m.Media.Mime != "image/webp" || m.Media.Width != 512 {
		t.Fatalf("got %+v", m)
	}
}

func TestReaction(t *testing.T) {
	raw := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{
		Key:               &waCommon.MessageKey{ID: new("M1")},
		Text:              new("👍"),
		SenderTimestampMS: new(int64(1761190005000)),
	}}
	p := testConverter().convert(fromAlice("R1"), raw)
	want := api.EvReaction{Chat: alicePN.String(), ID: "M1", Sender: alicePN.String(), Emoji: "👍", Ts: 1761190005}
	if p.reaction == nil || *p.reaction != want {
		t.Fatalf("got %+v", p.reaction)
	}
}

func TestRevoke(t *testing.T) {
	raw := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type: waE2E.ProtocolMessage_REVOKE.Enum(),
		Key:  &waCommon.MessageKey{ID: new("A1")},
	}}
	p := testConverter().convert(fromAlice("X1"), raw)
	if p.revoke == nil || *p.revoke != (api.EvRevoke{Chat: alicePN.String(), ID: "A1"}) {
		t.Fatalf("got %+v", p)
	}
}

func TestEdit(t *testing.T) {
	// Live edits arrive wrapped in EditedMessage.
	raw := &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		ProtocolMessage: &waE2E.ProtocolMessage{
			Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
			Key:           &waCommon.MessageKey{ID: new("A1")},
			EditedMessage: &waE2E.Message{Conversation: new("fixed")},
			TimestampMS:   new(int64(1761190100000)),
		},
	}}}
	p := testConverter().convert(fromAlice("E1"), raw)
	want := api.EvEdit{Chat: alicePN.String(), ID: "A1", Text: "fixed", Ts: 1761190100}
	if p.edit == nil || !reflect.DeepEqual(*p.edit, want) {
		t.Fatalf("got %+v", p)
	}
}

func TestMentions(t *testing.T) {
	cv := testConverter()
	bobPN := types.NewJID("61400000002", types.DefaultUserServer)
	stranger := types.NewJID("999", types.HiddenUserServer)
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: group, Sender: aliceLID, IsGroup: true},
		ID:            "G2", Timestamp: ts,
	}
	mentions := func(jids ...types.JID) *waE2E.ContextInfo {
		ci := &waE2E.ContextInfo{}
		for _, j := range jids {
			ci.MentionedJID = append(ci.MentionedJID, j.String())
		}
		return ci
	}

	// Known LIDs become phone JIDs, in the text too; a repeat counts once.
	raw := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        new("@901 @900 @61400000002 @999, not @9010"),
		ContextInfo: mentions(aliceLID, selfLID, bobPN, stranger, aliceLID),
	}}
	m := mustMsg(t, cv.convert(info, raw))
	want := []api.Mention{
		{JID: alicePN.String(), Name: "Alice"},
		{JID: selfPN.String(), Name: "You", Me: true},
		{JID: bobPN.String(), Name: "+61400000002"},
		{JID: stranger.String(), Name: "999"},
	}
	if m.Text != "@61400000001 @61400000000 @61400000002 @999, not @9010" || !reflect.DeepEqual(m.Mentions, want) {
		t.Fatalf("text %q mentions %+v", m.Text, m.Mentions)
	}

	// Captions carry them in the media's context.
	img := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{DirectPath: new("/v/i"), Caption: new("@901 look"),
		ContextInfo: mentions(aliceLID)}}
	m = mustMsg(t, cv.convert(info, img))
	if m.Text != "@61400000001 look" || !reflect.DeepEqual(m.Mentions, want[:1]) {
		t.Fatalf("caption %q mentions %+v", m.Text, m.Mentions)
	}

	// A quoted message's preview shows names.
	quoted := &waE2E.ExtendedTextMessage{Text: new("@900 ready?"), ContextInfo: mentions(selfLID)}
	reply := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: new("yes"), ContextInfo: &waE2E.ContextInfo{
		StanzaID: new("Q1"), Participant: new(alicePN.String()), QuotedMessage: &waE2E.Message{ExtendedTextMessage: quoted},
	}}}
	m = mustMsg(t, cv.convert(info, reply))
	if m.ReplyTo == nil || m.ReplyTo.Preview != "@You ready?" || len(m.Mentions) != 0 {
		t.Fatalf("reply %+v mentions %+v", m.ReplyTo, m.Mentions)
	}

	// Edits bring the new text's.
	edit := &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{
		Type:          waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(),
		Key:           &waCommon.MessageKey{ID: new("G2")},
		EditedMessage: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: new("@901 fixed"), ContextInfo: mentions(aliceLID)}},
	}}
	p := cv.convert(info, edit)
	if p.edit == nil || p.edit.Text != "@61400000001 fixed" || !reflect.DeepEqual(p.edit.Mentions, want[:1]) {
		t.Fatalf("edit %+v", p.edit)
	}
}

func TestEphemeralAndViewOnce(t *testing.T) {
	raw := &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{DirectPath: new("/v/i")},
		}},
	}}}
	if m := mustMsg(t, testConverter().convert(fromAlice("A6"), raw)); m.Type != api.MsgImage {
		t.Fatalf("got %+v", m)
	}
}

func TestGroupSender(t *testing.T) {
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: group, Sender: aliceLID, IsGroup: true},
		ID:            "G1", Timestamp: ts,
	}
	m := mustMsg(t, testConverter().convert(info, &waE2E.Message{Conversation: new("hey all")}))
	if m.Chat != group.String() || m.Sender != alicePN.String() || m.SenderName != "Alice" {
		t.Fatalf("got %+v", m)
	}
}

func TestSkipped(t *testing.T) {
	cv := testConverter()
	status := &types.MessageInfo{MessageSource: types.MessageSource{Chat: types.StatusBroadcastJID, Sender: alicePN}, ID: "S1"}
	if p := cv.convert(status, &waE2E.Message{Conversation: new("story")}); p != (parsed{}) {
		t.Fatalf("status not skipped: %+v", p)
	}
	skd := &waE2E.Message{SenderKeyDistributionMessage: &waE2E.SenderKeyDistributionMessage{}}
	if p := cv.convert(fromAlice("K1"), skd); p != (parsed{}) {
		t.Fatalf("key distribution not ignored: %+v", p)
	}
	unknown := &waE2E.Message{ButtonsMessage: &waE2E.ButtonsMessage{}}
	if m := mustMsg(t, cv.convert(fromAlice("U1"), unknown)); m.Type != api.MsgUnsupported {
		t.Fatalf("got %+v", m)
	}
}

func TestMuteUntil(t *testing.T) {
	for in, want := range map[int64]int64{-1: -1, 0: -1, 1761190000000: 1761190000, 1 << 62: -1} {
		if got := muteUntil(in); got != want {
			t.Errorf("muteUntil(%d) = %d, want %d", in, got, want)
		}
	}
}
