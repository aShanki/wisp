package wa

import (
	"errors"
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
)

func TestReplyContext(t *testing.T) {
	if replyContext(nil, alicePN) != nil {
		t.Fatal("context for no reply")
	}
	ci := replyContext(&api.ReplyRef{ID: "Q1", Text: "where?"}, alicePN)
	if ci.GetStanzaID() != "Q1" || ci.GetParticipant() != alicePN.String() || ci.GetQuotedMessage().GetConversation() != "where?" {
		t.Fatalf("got %v", ci)
	}
	// The quote round-trips through the parser.
	q := testConverter().quote(ci, alicePN)
	if q.ID != "Q1" || q.Sender != alicePN.String() || q.SenderName != "Alice" || q.Preview != "where?" {
		t.Fatalf("quote %+v", q)
	}
}

// The phone no longer having the file is a final answer: the path is cleared.
func TestRetryPathNotOnPhone(t *testing.T) {
	path, err := retryPath(&events.MediaRetry{MessageID: "M", Error: &events.MediaRetryError{Code: 2}}, []byte{1})
	if err != nil || path != "" {
		t.Fatalf("path %q err %v", path, err)
	}
	if _, err := retryPath(&events.MediaRetry{MessageID: "M", Error: &events.MediaRetryError{Code: 1}}, []byte{1}); !errors.Is(err, api.ErrMediaExpired) {
		t.Fatalf("unknown error: %v", err)
	}
}

func TestTextMessage(t *testing.T) {
	if m := textMessage("hi", nil, nil); m.GetConversation() != "hi" || m.ExtendedTextMessage != nil {
		t.Fatalf("plain: %v", m)
	}
	// A reply that mentions someone carries both in one context.
	m := textMessage("@61400000001 here", replyContext(&api.ReplyRef{ID: "Q1", Sender: alicePN.String(), Text: "where?"}, group),
		[]types.JID{alicePN})
	ci := m.GetExtendedTextMessage().GetContextInfo()
	if m.GetExtendedTextMessage().GetText() != "@61400000001 here" || ci.GetStanzaID() != "Q1" ||
		!reflect.DeepEqual(ci.GetMentionedJID(), []string{alicePN.String()}) {
		t.Fatalf("reply with mention: %v", m)
	}
	// Our own copy, as another device sees it, parses back to both.
	info := &types.MessageInfo{MessageSource: types.MessageSource{Chat: group, Sender: selfPN, IsFromMe: true, IsGroup: true}, ID: "S1", Timestamp: ts}
	got := mustMsg(t, testConverter().convert(info, m))
	if got.ReplyTo == nil || got.ReplyTo.ID != "Q1" || got.ReplyTo.Preview != "where?" ||
		!reflect.DeepEqual(got.Mentions, []api.Mention{{JID: alicePN.String(), Name: "Alice"}}) {
		t.Fatalf("parsed: %+v reply %+v", got, got.ReplyTo)
	}
}

// Groups addressed by LID are sent LIDs, where known.
func TestMentionLIDs(t *testing.T) {
	bobPN := types.NewJID("61400000002", types.DefaultUserServer)
	jids := []types.JID{alicePN, bobPN, aliceLID}
	text := mentionLIDs("@61400000001 @61400000002 @614000000010", jids, func(pn types.JID) types.JID {
		if pn == alicePN {
			return aliceLID
		}
		return types.JID{}
	})
	if text != "@901 @61400000002 @614000000010" || !reflect.DeepEqual(jids, []types.JID{aliceLID, bobPN, aliceLID}) {
		t.Fatalf("text %q jids %v", text, jids)
	}
}
