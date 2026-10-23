package meowcaller

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// A <reject> on an inbound call is another device of the same account declining
// for itself. The call must stay live on this device until the caller ends it.
func TestOwnDeviceRejectKeepsInboundCallLive(t *testing.T) {
	eng, call := testEngineWithOutgoingCall()
	eng.calls["CID"].direction = CallDirectionIncoming
	call.phase = CallPhaseRinging
	var reason string
	call.OnEnd(func(r string) { reason = r })

	eng.onReject(&events.CallReject{BasicCallMeta: types.BasicCallMeta{CallID: "CID"}})
	if got := call.State(); got != CallPhaseRinging {
		t.Fatalf("phase = %d, want still Ringing", got)
	}
	if eng.lookup("CID") == nil || reason != "" {
		t.Fatalf("the call was torn down (reason %q)", reason)
	}

	eng.onTerminate("CID", "")
	if got := call.State(); got != CallPhaseEnded {
		t.Fatalf("phase = %d after terminate, want Ended", got)
	}
}
