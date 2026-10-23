package meowcaller

import (
	"testing"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func rejectFrom(device uint16, reason string) *events.CallReject {
	from := peerJID()
	from.Device = device
	ev := &events.CallReject{BasicCallMeta: types.BasicCallMeta{CallID: "CID", From: from}}
	if reason != "" {
		ev.Data = &waBinary.Node{Tag: "reject", Attrs: waBinary.Attrs{"reason": reason}}
	}
	return ev
}

func TestOutgoingRejectRules(t *testing.T) {
	cases := []struct {
		name           string
		companionsOnly bool
		preaccepted    []uint16
		answered       bool
		rejects        []*events.CallReject
		ends           bool
	}{
		{"primary device", false, nil, false, []*events.CallReject{rejectFrom(0, "")}, true},
		{"a device that rang", false, []uint16{3}, false, []*events.CallReject{rejectFrom(3, "busy")}, true},
		{"a companion that never rang", false, []uint16{0}, false, []*events.CallReject{rejectFrom(3, "unavailable")}, false},
		{"unsupported, even from the primary", false, nil, false, []*events.CallReject{rejectFrom(0, "unsupported")}, false},
		{"after another device answered", false, []uint16{3}, true, []*events.CallReject{rejectFrom(3, "")}, false},
		{"every offered device", true, nil, false, []*events.CallReject{rejectFrom(3, ""), rejectFrom(5, "")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng, call := testEngineWithOutgoingCall()
			m := eng.calls["CID"]
			m.offered = map[uint16]bool{3: true, 5: true}
			if !tc.companionsOnly {
				m.offered[0] = true
			}
			m.preaccepted = map[uint16]bool{}
			for _, d := range tc.preaccepted {
				m.preaccepted[d] = true
			}
			if tc.answered {
				call.phase = CallPhaseConnecting
			}
			ended := ""
			call.OnEnd(func(r string) { ended = r })
			for _, ev := range tc.rejects {
				eng.onReject(ev)
			}
			if got := ended != ""; got != tc.ends {
				t.Fatalf("ended = %v (%q), want %v", got, ended, tc.ends)
			}
		})
	}
}

func TestPreacceptMarksTheDeviceRinging(t *testing.T) {
	eng, _ := testEngineWithOutgoingCall()
	from := peerJID()
	from.Device = 7
	eng.onPreAccept(&events.CallPreAccept{BasicCallMeta: types.BasicCallMeta{CallID: "CID", From: from}})
	if !eng.calls["CID"].preaccepted[7] {
		t.Fatal("device 7 not recorded as ringing")
	}
}
