package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"wisp/daemon/internal/api"
)

func TestReceiptStatus(t *testing.T) {
	for _, tc := range []struct {
		typ   types.ReceiptType
		group bool
		want  string
	}{
		{types.ReceiptTypeDelivered, false, api.StatusDelivered},
		{types.ReceiptTypeRead, false, api.StatusRead},
		{types.ReceiptTypePlayed, false, api.StatusPlayed},
		{types.ReceiptTypeDelivered, true, api.StatusDelivered},
		{types.ReceiptTypeRead, true, api.StatusDelivered},
		{types.ReceiptTypePlayed, true, api.StatusDelivered},
		{types.ReceiptTypeRetry, false, ""},
		{types.ReceiptTypeSender, false, ""},
	} {
		r := &events.Receipt{MessageSource: types.MessageSource{IsGroup: tc.group}, Type: tc.typ}
		if got := receiptStatus(r); got != tc.want {
			t.Errorf("receiptStatus(%q, group=%v) = %q, want %q", tc.typ, tc.group, got, tc.want)
		}
	}
}

func TestReceiptEvent(t *testing.T) {
	cv := testConverter()
	mine := types.MessageSource{Chat: aliceLID, Sender: selfLID, IsFromMe: true}
	// Read on the phone (receipts on): the chat is no longer unread.
	ev := receiptEvent(cv, &events.Receipt{MessageSource: mine, MessageIDs: []string{"A"}, Type: types.ReceiptTypeRead})
	meta, ok := ev.(api.EvChatMeta)
	if !ok || meta.Patch.JID != alicePN.String() || meta.Patch.Unread == nil || *meta.Patch.Unread != 0 {
		t.Fatalf("own read: %#v", ev)
	}
	// Our other device acknowledging delivery: nothing.
	if ev := receiptEvent(cv, &events.Receipt{MessageSource: mine, MessageIDs: []string{"A"}, Type: types.ReceiptTypeDelivered}); ev != nil {
		t.Fatalf("own delivered: %#v", ev)
	}
	// Alice reading our message.
	theirs := types.MessageSource{Chat: alicePN, Sender: alicePN}
	ev = receiptEvent(cv, &events.Receipt{MessageSource: theirs, MessageIDs: []string{"B"}, Type: types.ReceiptTypeRead})
	if r, ok := ev.(api.EvReceipt); !ok || r.Status != api.StatusRead || r.IDs[0] != "B" {
		t.Fatalf("their read: %#v", ev)
	}
}

func TestMarkChatRead(t *testing.T) {
	var live, replay api.ChatPatch
	markChatRead(&live, true, false)
	markChatRead(&replay, true, true)
	if live.Unread == nil || *live.Unread != 0 || *live.MarkedUnread {
		t.Fatalf("live read: %+v", live)
	}
	if replay.Unread != nil || *replay.MarkedUnread {
		t.Fatalf("full-sync read must keep the unread count: %+v", replay)
	}
	var unread api.ChatPatch
	markChatRead(&unread, false, true)
	if unread.Unread != nil || !*unread.MarkedUnread {
		t.Fatalf("marked unread: %+v", unread)
	}
}

func TestLIDChatMapping(t *testing.T) {
	ev := func(index ...string) *events.AppState {
		return &events.AppState{Index: index, SyncActionValue: &waSyncAction.SyncActionValue{
			PnForLidChatAction: &waSyncAction.PnForLidChatAction{PnJID: new(alicePN.String())},
		}}
	}
	if m, ok := lidChatMapping(ev(appstate.IndexPNForLIDChat, aliceLID.String())); !ok || m != (store.LIDMapping{LID: aliceLID, PN: alicePN}) {
		t.Errorf("mapping = %v, %v", m, ok)
	}
	for _, e := range []*events.AppState{
		ev(appstate.IndexPin, aliceLID.String()),
		ev(appstate.IndexPNForLIDChat, alicePN.String()),
		ev(appstate.IndexPNForLIDChat),
		{Index: []string{appstate.IndexPNForLIDChat, aliceLID.String()}},
	} {
		if m, ok := lidChatMapping(e); ok {
			t.Errorf("%v: unexpected mapping %v", e.Index, m)
		}
	}
}

func TestPinTime(t *testing.T) {
	at := time.Unix(1761000000, 0)
	if pinTime(true, at) != 1761000000 || pinTime(false, at) != 0 || pinTime(true, time.Time{}) != 1 {
		t.Error("pinTime")
	}
}
