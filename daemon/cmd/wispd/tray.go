package main

import (
	"context"
	"log/slog"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/rpc"
	"wisp/daemon/internal/tray"
)

// startTray shows the tray icon. Its unread state comes from chats.list now
// and from the backend's chat.update events after, teed off on their way to
// the UI, so call it before the backend starts. openUI is core's (nil for the
// mock, which only asks a connected UI to raise its window).
func startTray(ctx context.Context, b api.Backend, srv *rpc.Server, openUI func(chat, token string), log *slog.Logger) *tray.Tray {
	if openUI == nil {
		openUI = func(chat, token string) { srv.Emit(api.EUIOpenChat, api.OpenChatEvent{Chat: chat, Token: token}) }
	}
	t := tray.New(tray.Config{
		Log:  log,
		Open: func(token string) { openUI("", token) },
		Quit: func() {
			log.Info("wispd: quit from the tray")
			srv.Emit(api.EUIQuit, struct{}{})
			_ = b.Shutdown(ctx)
		},
	})
	b.SetEmitter(trayEmitter{srv, t})
	for cursor := ""; ; {
		r, err := b.ChatsList(ctx, api.ChatsListParams{Limit: 1000, Cursor: cursor})
		if err != nil {
			log.Warn("wispd: tray unread count", "err", err)
			break
		}
		for _, c := range r.Chats {
			t.Chat(c)
		}
		if cursor = r.NextCursor; cursor == "" {
			break
		}
	}
	return t
}

type trayEmitter struct {
	api.Emitter
	tray *tray.Tray
}

func (e trayEmitter) Emit(event string, params any) {
	e.Emitter.Emit(event, params)
	switch p := params.(type) {
	case api.ChatEvent:
		if event == api.EChatUpdate {
			e.tray.Chat(p.Chat)
		}
	case api.AuthStateEvent:
		if p.State == api.AuthUnpaired || p.State == api.AuthLoggedOut {
			e.tray.Reset() // logged out: the chats are wiped
		}
	}
}
