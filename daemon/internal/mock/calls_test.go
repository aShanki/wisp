package mock

import (
	"context"
	"testing"

	"wisp/daemon/internal/api"
)

func ringingID(b *Backend) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for k := range b.calls {
		return k
	}
	return ""
}

func TestCallRingsAndDeclines(t *testing.T) {
	b, rec := newBackend(t)
	b.mu.Lock()
	b.ring()
	b.mu.Unlock()
	id := ringingID(b)
	if id == "" || rec.count(api.ECallState) != 1 {
		t.Fatal("no call rang")
	}
	ctx := context.Background()
	if err := b.CallsReject(ctx, api.CallParams{CallID: id}); err != nil {
		t.Fatal(err)
	}
	if rec.count(api.ECallState) != 2 || rec.count(api.EMessageNew) != 1 {
		t.Errorf("events = %v", rec.events)
	}
	if err := b.CallsReject(ctx, api.CallParams{CallID: id}); err == nil {
		t.Error("declined twice")
	}
}

func TestCallAcceptMuteEnd(t *testing.T) {
	b, rec := newBackend(t)
	b.mu.Lock()
	b.ring()
	b.mu.Unlock()
	id := ringingID(b)
	ctx := context.Background()
	if err := b.CallsMute(ctx, api.CallMuteParams{CallID: id, Muted: true}); err == nil {
		t.Error("muted a ringing call")
	}
	if err := b.CallsAccept(ctx, api.CallParams{CallID: id}); err != nil {
		t.Fatal(err)
	}
	if err := b.CallsMute(ctx, api.CallMuteParams{CallID: id, Muted: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.CallsEnd(ctx, api.CallParams{CallID: id}); err != nil {
		t.Fatal(err)
	}
	if rec.count(api.ECallState) != 4 || rec.count(api.EMessageNew) != 0 {
		t.Errorf("events = %v", rec.events)
	}
}

func TestOutgoingCall(t *testing.T) {
	b, rec := newBackend(t)
	ctx := context.Background()
	if _, err := b.CallsStart(ctx, api.ChatParams{Chat: "nobody@s.whatsapp.net"}); err == nil {
		t.Error("called an unknown chat")
	}
	var jid string
	b.mu.Lock()
	for _, c := range b.sorted() {
		if c.row.Kind == api.ChatUser {
			jid = c.spec.jid
			break
		}
	}
	b.mu.Unlock()
	res, err := b.CallsStart(ctx, api.ChatParams{Chat: jid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.CallsStart(ctx, api.ChatParams{Chat: jid}); err == nil {
		t.Error("two calls at once")
	}
	b.mu.Lock()
	b.connect(res.CallID)
	b.mu.Unlock()
	if err := b.CallsEnd(ctx, api.CallParams{CallID: res.CallID}); err != nil {
		t.Fatal(err)
	}
	if rec.count(api.ECallState) != 3 {
		t.Errorf("events = %v", rec.events)
	}
}
