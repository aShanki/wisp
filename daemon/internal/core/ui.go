package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"wisp/daemon/internal/api"
)

func (c *Core) UISetActive(ctx context.Context, p api.UISetActiveParams) error {
	c.clientsMu.Lock()
	cl := c.clientLocked(ctx)
	cl.chat, cl.focused = p.Chat, p.Focused
	c.clientsMu.Unlock()
	c.applyOnline(false)
	if !p.Focused || p.Chat == "" {
		return nil
	}
	// The open chat in a focused window is read.
	if ch, err := c.store.GetChat(ctx, p.Chat); err == nil && (ch.Unread > 0 || ch.MarkedUnread) {
		return c.markRead(ctx, p.Chat, true)
	}
	c.notify.Clear(p.Chat)
	return nil
}

// clientLocked returns the state of the UI connection ctx came from.
func (c *Core) clientLocked(ctx context.Context) *client {
	id := api.ClientIDFrom(ctx)
	cl := c.clients[id]
	if cl == nil {
		cl = &client{}
		c.clients[id] = cl
	}
	return cl
}

// ClientGone forgets a disconnected UI client.
func (c *Core) ClientGone(clientID int64) {
	c.clientsMu.Lock()
	delete(c.clients, clientID)
	c.clientsMu.Unlock()
	c.applyOnline(false)
}

// activeFocused reports whether chat is open in some focused UI window.
func (c *Core) activeFocused(chat string) bool {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	for _, cl := range c.clients {
		if cl.focused && cl.chat == chat {
			return true
		}
	}
	return false
}

func (c *Core) anyHello() bool {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()
	for _, cl := range c.clients {
		if cl.hello {
			return true
		}
	}
	return false
}

// applyOnline records whether any UI window is focused and wakes
// presenceLoop. force re-sends the state (after reconnecting).
func (c *Core) applyOnline(force bool) {
	c.clientsMu.Lock()
	want := false
	for _, cl := range c.clients {
		want = want || cl.focused
	}
	c.online = want
	c.onlineForce = c.onlineForce || force
	c.clientsMu.Unlock()
	select {
	case c.onlineWake <- struct{}{}:
	default:
	}
}

// presenceLoop tells WhatsApp we're "available" only while some UI window is
// focused. One goroutine, so updates reach WhatsApp in order.
func (c *Core) presenceLoop() {
	defer c.wg.Done()
	sent := false
	for {
		select {
		case <-c.onlineWake:
		case <-c.life.Done():
			return
		}
		c.clientsMu.Lock()
		want, force := c.online, c.onlineForce
		c.onlineForce = false
		c.clientsMu.Unlock()
		if (want == sent && !force) || !c.wa.IsLoggedIn() {
			continue
		}
		ctx, cancel := context.WithTimeout(c.life, 30*time.Second)
		err := c.wa.SetOnline(ctx, want)
		cancel()
		if err != nil {
			c.log.Debug("core: set online failed", "online", want, "err", err)
			continue
		}
		sent = want
	}
}

func (c *Core) onNotifyOpen(chat string) {
	token := ""
	// The click's xdg-activation token lets the window take focus on Wayland.
	if t, ok := c.notify.(interface{ TakeActivationToken() string }); ok {
		token = t.TakeActivationToken()
	}
	c.OpenUI(chat, token)
}

// OpenUI shows chat ("" = just the window) in the connected UI, or launches
// one. token is an xdg-activation token for the window, or "".
func (c *Core) OpenUI(chat, token string) {
	if c.anyHello() {
		c.emitEv(api.EUIOpenChat, api.OpenChatEvent{Chat: chat, Token: token})
		return
	}
	if err := c.spawnUI(chat, token); err != nil {
		c.log.Warn("core: launching UI failed", "err", err)
	}
}

func (c *Core) onNotifyReply(chat, msgID, text string) {
	if text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(c.life, 30*time.Second)
	defer cancel()
	if _, err := c.MessagesSend(ctx, api.SendParams{Chat: chat, Text: text, ReplyTo: msgID}); err != nil {
		c.log.Warn("core: reply from notification failed", "chat", chat, "err", err)
		return
	}
	if err := c.markRead(ctx, chat, true); err != nil {
		c.logErr("mark read after reply", err, "chat", chat)
	}
}

func (c *Core) onNotifyMarkRead(chat string) {
	ctx, cancel := context.WithTimeout(c.life, 30*time.Second)
	defer cancel()
	if err := c.markRead(ctx, chat, true); err != nil {
		c.logErr("mark read from notification", err, "chat", chat)
	}
}

// spawnUI launches the UI detached (own session, no inherited stdio).
func (c *Core) spawnUI(chat, token string) error {
	bin := uiBinary()
	args := []string{bin}
	if c.socket != "" {
		args = append(args, "--socket", c.socket)
	}
	if chat != "" {
		args = append(args, "--chat", chat)
	}
	// Under systemd (wispd.service) the window gets its own scope, so it
	// survives a restart of the service.
	if os.Getenv("INVOCATION_ID") != "" {
		if run, err := exec.LookPath("systemd-run"); err == nil {
			args = append([]string{run, "--user", "--scope", "--quiet", "--collect", "--"}, args...)
		}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if token != "" {
		cmd.Env = append(os.Environ(), "XDG_ACTIVATION_TOKEN="+token)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.log.Info("core: launched UI", "bin", bin, "chat", chat, "pid", cmd.Process.Pid)
	go func() { _ = cmd.Wait() }() // reap
	return nil
}

// uiBinary finds wisp next to wispd, else on PATH.
func uiBinary() string {
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "wisp")
		if fileExists(cand) {
			return cand
		}
	}
	if p, err := exec.LookPath("wisp"); err == nil {
		return p
	}
	return "wisp"
}
