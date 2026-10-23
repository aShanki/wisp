package core

import (
	"testing"

	"wisp/daemon/internal/api"
)

func TestOpenUIRaisesConnectedWindow(t *testing.T) {
	h := newHarness(t)
	h.c.Hello(clientCtx(3), api.HelloParams{Client: "wisp-ui"})
	h.ev.reset()
	h.c.OpenUI("", "tok")
	h.ev.waitFor(t, "ui.openChat with the token", func(e event) bool {
		p, ok := e.params.(api.OpenChatEvent)
		return e.name == api.EUIOpenChat && ok && p == api.OpenChatEvent{Token: "tok"}
	})
}
