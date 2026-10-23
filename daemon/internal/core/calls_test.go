package core

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

func TestMissedCall(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvCallOffer{ID: "C1", Chat: alice, From: alice, Ts: time.Now().Unix()})
	if got := h.ev.names(); !reflect.DeepEqual(got, []string{api.ECallState}) {
		t.Fatalf("events = %v", got)
	}
	if len(h.nt.calls) != 1 || h.nt.calls[0].ID != "C1" {
		t.Fatalf("call notifications = %+v", h.nt.calls)
	}
	h.c.handle(api.EvCallState{ID: "C1", State: api.CallEnded, Reason: api.CallMissed})
	want := []string{api.ECallState, api.ECallState, api.EMessageNew, api.EChatUpdate}
	if got := h.ev.names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	m, err := h.st.GetMessage(context.Background(), alice, "call-C1")
	if err != nil || m.Type != api.MsgSystem || m.Text != "Missed voice call" {
		t.Fatalf("call message = %+v, %v", m, err)
	}
	if !slices.Contains(h.nt.cleared, "call:C1") {
		t.Error("ringing notification not cleared")
	}
	if len(h.nt.shown) != 1 || h.nt.shown[0].Text != "Missed voice call" {
		t.Errorf("missed call notification = %+v", h.nt.shown)
	}
	h.c.handle(api.EvCallState{ID: "C1", State: api.CallEnded, Reason: api.CallMissed})
	if n := len(h.ev.names()); n != len(want) {
		t.Errorf("a second end emitted again: %d events", n)
	}
}

func TestDeclinedAndAnsweredCalls(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := time.Now().Unix()
	if err := h.c.CallsReject(ctx, api.CallParams{CallID: "C2"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.wa.rejected, []string{"C2"}) {
		t.Errorf("rejected = %v", h.wa.rejected)
	}
	h.c.handle(api.EvCallOffer{ID: "C2", Chat: alice, From: alice, Video: true, Ts: now})
	h.c.handle(api.EvCallState{ID: "C2", State: api.CallEnded, Reason: api.CallDeclined})
	if m, err := h.st.GetMessage(ctx, alice, "call-C2"); err != nil || m.Text != "Declined video call" {
		t.Errorf("declined = %+v, %v", m, err)
	}
	h.c.handle(api.EvCallOffer{ID: "C3", Chat: alice, From: alice, Ts: now})
	h.c.handle(api.EvCallState{ID: "C3", State: api.CallEnded, Reason: api.CallAnswered})
	if _, err := h.st.GetMessage(ctx, alice, "call-C3"); err == nil {
		t.Error("a call answered elsewhere left a message")
	}
	if len(h.nt.shown) != 0 {
		t.Errorf("notifications = %+v", h.nt.shown)
	}
}

func TestStaleOfferIsMissed(t *testing.T) {
	h := newHarness(t)
	h.c.handle(api.EvCallOffer{ID: "C4", Chat: group, From: alice, Group: true, Ts: time.Now().Unix() - 600})
	if slices.Contains(h.ev.names(), api.ECallState) || len(h.nt.calls) != 0 {
		t.Fatal("an old offer rang")
	}
	if m, err := h.st.GetMessage(context.Background(), group, "call-C4"); err != nil || m.Text != "Missed group voice call" {
		t.Errorf("stale = %+v, %v", m, err)
	}
}

func TestOutgoingCall(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	res, err := h.c.CallsStart(ctx, api.ChatParams{Chat: alice})
	if err != nil || res.CallID != "OUT1" {
		t.Fatalf("start = %+v, %v", res, err)
	}
	h.c.handle(api.EvCallState{ID: "OUT1", State: api.CallConnecting})
	h.c.handle(api.EvCallState{ID: "OUT1", State: api.CallActive})
	if err := h.c.CallsMute(ctx, api.CallMuteParams{CallID: "OUT1", Muted: true}); err != nil {
		t.Fatal(err)
	}
	h.c.handle(api.EvCallState{ID: "OUT1", State: api.CallActive, PeerMuted: true})
	h.c.handle(api.EvCallState{ID: "OUT1", State: api.CallEnded, Reason: api.CallHungUp, PeerMuted: true})
	var got []string
	var last api.CallStateEvent
	for _, e := range h.ev.all() {
		if s, ok := e.params.(api.CallStateEvent); ok {
			got = append(got, s.State)
			last = s
		}
	}
	want := []string{api.CallRinging, api.CallConnecting, api.CallActive, api.CallActive, api.CallActive, api.CallEnded}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("states = %v, want %v", got, want)
	}
	if last.Incoming || !last.Muted || !last.PeerMuted || last.Reason != api.CallHungUp || last.Chat != alice {
		t.Errorf("last = %+v", last)
	}
	if _, err := h.st.GetMessage(ctx, alice, "call-OUT1"); err == nil {
		t.Error("an outgoing call left a message")
	}
}
