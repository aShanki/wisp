//go:build !darwin

// Package notify shows message notifications through
// org.freedesktop.Notifications on the session bus, with KDE Plasma's inline
// reply, and plays WhatsApp's notification sound when it is installed. Each
// chat has one notification, replaced in place, that counts the messages it
// covers. Nothing polls: one goroutine waits on D-Bus signals and a lost bus
// is redialled on the next Notify. Without a bus every method is a no-op.
// Deciding what to notify is the caller's job.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"wisp/daemon/internal/api"
)

const (
	appName = "Wisp"
	appIcon = "wisp" // freedesktop icon name, also the image when a chat has no avatar

	busName = "org.freedesktop.Notifications"
	objPath = dbus.ObjectPath("/org/freedesktop/Notifications")
	iface   = "org.freedesktop.Notifications"

	// Action keys.
	actDefault  = "default"
	actReply    = "inline-reply"
	actMarkRead = "mark-read"
	actDecline  = "decline"

	// NotificationClosed reasons.
	closedExpired = 1

	callTimeout   = 3 * time.Second
	redialBackoff = 15 * time.Second
	maxBodyRunes  = 400
	maxTitleRunes = 120
	soundGap      = 2 * time.Second
)

// Config configures a Notifier.
type Config struct {
	Log *slog.Logger // nil: slog.Default()
	// BusAddress overrides the D-Bus address (tests). Empty means the
	// session bus from $DBUS_SESSION_BUS_ADDRESS or $XDG_RUNTIME_DIR/bus.
	BusAddress string
	// Sound is played for each new message. Empty means the installed
	// notification.* (see findSound); no file means silence.
	Sound string
	// Exec starts a command without waiting for it (tests). nil: os/exec.
	Exec func(name string, arg ...string) error
}

// Notifier implements api.Notifier over D-Bus. Safe for concurrent use.
type Notifier struct {
	log   *slog.Logger
	addr  string // "" = no session bus: permanent no-op
	sound string // "" = silent
	exec  func(name string, arg ...string) error

	mu       sync.Mutex
	closed   bool
	conn     *dbus.Conn
	lastDial time.Time
	caps     map[string]bool // nil = not fetched yet
	owner    string          // unique name of the notification server, "" = unknown
	actions  api.NotifyActions
	byChat   map[string]*entry
	byID     map[uint32]*entry
	calls    map[string]*entry // ringing calls by call id
	token    string            // last xdg-activation token from the server
	played   time.Time
}

// entry is the notification state of one chat.
type entry struct {
	chat  string
	id    uint32 // 0 = not currently shown
	msgID string // newest message notified
	count int    // messages since the user last dealt with this chat
	call  string // the call ringing, for a call notification
}

var _ api.Notifier = (*Notifier)(nil)

// New connects to the session bus. Without one it returns a working no-op
// Notifier.
func New(cfg Config) *Notifier {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Sound == "" {
		cfg.Sound = findSound()
	}
	if cfg.Exec == nil {
		cfg.Exec = start
	}
	n := &Notifier{
		log:    cfg.Log.With("component", "notify"),
		addr:   sessionBusAddress(cfg.BusAddress),
		sound:  cfg.Sound,
		exec:   cfg.Exec,
		byChat: make(map[string]*entry),
		byID:   make(map[uint32]*entry),
		calls:  make(map[string]*entry),
	}
	n.log.Debug("notification sound", "file", n.sound)
	if n.addr == "" {
		n.log.Info("no D-Bus session bus; desktop notifications disabled")
	} else {
		n.mu.Lock()
		if n.dialLocked() == nil {
			n.log.Warn("D-Bus session bus unavailable; will retry on next notification", "addr", n.addr)
		}
		n.mu.Unlock()
	}
	return n
}

// sessionBusAddress resolves the bus address without autolaunching a bus.
func sessionBusAddress(override string) string {
	if override != "" {
		return override
	}
	if a := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" && a != "autolaunch:" {
		return a
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		p := filepath.Join(dir, "bus")
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return "unix:path=" + dbus.EscapeBusAddressValue(p)
		}
	}
	return ""
}

// SetActions sets the callbacks for notification clicks and buttons.
// Callbacks run on their own goroutine and may call back into the Notifier.
func (n *Notifier) SetActions(a api.NotifyActions) {
	n.mu.Lock()
	n.actions = a
	n.mu.Unlock()
}

// TakeActivationToken returns (and forgets) the xdg-activation token the
// notification server sent with the most recent click, or "". Pass it to a
// spawned UI as XDG_ACTIVATION_TOKEN so Wayland lets it take focus.
func (n *Notifier) TakeActivationToken() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	t := n.token
	n.token = ""
	return t
}

// Notify shows the notification for n.Chat, replacing the one already shown
// for that chat. Without a bus it does nothing and returns nil.
func (n *Notifier) Notify(ctx context.Context, msg api.Notification) error {
	if msg.Chat == "" {
		return api.InvalidParams("notification without chat")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	conn := n.connLocked()
	if conn == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if n.caps == nil {
		n.loadCapsLocked(cctx, conn)
	}

	e := n.byChat[msg.Chat]
	if e == nil {
		e = &entry{chat: msg.Chat}
		n.byChat[msg.Chat] = e
	}
	fresh := msg.MsgID == "" || msg.MsgID != e.msgID || e.count == 0
	if fresh {
		e.count++
	}
	e.msgID = msg.MsgID

	summary, body := render(msg, e.count, n.markup())
	actions := []string{actDefault, "Open", actMarkRead, "Mark as read"}
	hints := map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant("wisp"),
		"category":      dbus.MakeVariant("im.received"),
		"urgency":       dbus.MakeVariant(byte(1)),
		"image-path":    dbus.MakeVariant(imagePath(msg.Avatar)),
	}
	if n.sound != "" {
		hints["suppress-sound"] = dbus.MakeVariant(true) // ours, not the server's
	}
	if n.caps["inline-reply"] {
		actions = append(actions, actReply, "Reply")
		hints["x-kde-reply-placeholder-text"] = dbus.MakeVariant("Reply…")
		hints["x-kde-reply-submit-button-icon-name"] = dbus.MakeVariant("document-send")
	}

	var id uint32
	err := conn.Object(busName, objPath).CallWithContext(cctx, iface+".Notify", 0,
		appName, e.id, appIcon, summary, body, actions, hints, int32(-1)).Store(&id)
	if err != nil {
		if !conn.Connected() {
			n.dropConnLocked(conn)
		}
		return fmt.Errorf("notify: %w", err)
	}
	if e.id != 0 && e.id != id {
		delete(n.byID, e.id)
	}
	e.id = id
	n.byID[id] = e
	if fresh && n.sound != "" && time.Since(n.played) >= soundGap && !n.inhibitedLocked(cctx, conn) {
		n.played = time.Now()
		n.playLocked()
	}
	return nil
}

// inhibitedLocked reports Do Not Disturb: the server's Inhibited property
// (KDE). Servers without it are never inhibited.
func (n *Notifier) inhibitedLocked(ctx context.Context, conn *dbus.Conn) bool {
	var v dbus.Variant
	err := conn.Object(busName, objPath).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, "Inhibited").Store(&v)
	inhibited, _ := v.Value().(bool)
	return err == nil && inhibited
}

// playLocked plays the sound with PipeWire, else PulseAudio, without waiting.
func (n *Notifier) playLocked() {
	err := n.exec("pw-play", "--media-role=Notification", n.sound)
	if err != nil {
		err = n.exec("paplay", "--property=media.role=event", n.sound)
	}
	if err != nil {
		n.log.Debug("cannot play notification sound", "err", err)
	}
}

func start(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// findSound returns the notification.* that `make install` puts in
// $XDG_DATA_HOME/wisp, ~/.local/share/wisp or <prefix>/share/wisp, or "".
func findSound() string {
	var dirs []string
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		dirs = append(dirs, filepath.Join(d, "wisp"))
	}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".local", "share", "wisp"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "..", "share", "wisp"))
	}
	for _, d := range dirs {
		if m, _ := filepath.Glob(filepath.Join(d, "notification.*")); len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// NotifyCall shows a ringing call until ClearCall: resident, critical, with
// Decline. Clicking it opens the chat.
func (n *Notifier) NotifyCall(ctx context.Context, c api.CallNotification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	conn := n.connLocked()
	if conn == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	kind := "Voice call"
	if c.Video {
		kind = "Video call"
	}
	if c.Group {
		kind = "Group " + strings.ToLower(kind)
	}
	hints := map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant("wisp"),
		"category":      dbus.MakeVariant("call.incoming"),
		"urgency":       dbus.MakeVariant(byte(2)),
		"resident":      dbus.MakeVariant(true),
		"image-path":    dbus.MakeVariant(imagePath(c.Avatar)),
	}
	actions := []string{actDefault, "Open", actDecline, "Decline"}
	var id uint32
	err := conn.Object(busName, objPath).CallWithContext(cctx, iface+".Notify", 0,
		appName, uint32(0), appIcon, truncate(c.Name, maxTitleRunes), kind, actions, hints, int32(0)).Store(&id)
	if err != nil {
		if !conn.Connected() {
			n.dropConnLocked(conn)
		}
		return fmt.Errorf("notify call: %w", err)
	}
	e := &entry{chat: c.Chat, id: id, call: c.ID}
	n.calls[c.ID] = e
	n.byID[id] = e
	return nil
}

// ClearCall closes the notification of a call that stopped ringing.
func (n *Notifier) ClearCall(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if e := n.calls[id]; e != nil {
		n.forgetLocked(e, true)
	}
}

// Clear closes the notification shown for chat (if any) and resets its
// message count.
func (n *Notifier) Clear(chat string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e := n.byChat[chat]
	if e == nil {
		return
	}
	delete(n.byChat, chat)
	if e.id != 0 {
		delete(n.byID, e.id)
		n.closeNotificationLocked(e.id)
	}
}

// ClearAll closes every notification shown.
func (n *Notifier) ClearAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.clearAllLocked()
}

func (n *Notifier) clearAllLocked() {
	for id := range n.byID {
		n.closeNotificationLocked(id)
	}
	clear(n.byID)
	clear(n.byChat)
	clear(n.calls)
}

// Close closes all shown notifications (their actions would be dead) and
// releases the bus. Idempotent.
func (n *Notifier) Close() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	n.closed = true
	n.clearAllLocked()
	if n.conn != nil {
		n.dropConnLocked(n.conn)
	}
	return nil
}

// closeNotificationLocked asks the server to close id without waiting.
func (n *Notifier) closeNotificationLocked(id uint32) {
	if n.conn == nil {
		return
	}
	call := n.conn.Object(busName, objPath).Go(iface+".CloseNotification", dbus.FlagNoReplyExpected, nil, id)
	if call.Err != nil {
		n.log.Debug("CloseNotification failed", "id", id, "err", call.Err)
	}
}

// connLocked returns the live connection, re-dialling (rate limited) if the
// bus was lost. nil means notifications are unavailable right now.
func (n *Notifier) connLocked() *dbus.Conn {
	if n.conn != nil && n.conn.Connected() {
		return n.conn
	}
	if n.conn != nil {
		n.dropConnLocked(n.conn)
	}
	if n.addr == "" || time.Since(n.lastDial) < redialBackoff {
		return nil
	}
	return n.dialLocked()
}

func (n *Notifier) dialLocked() *dbus.Conn {
	n.lastDial = time.Now()
	conn, err := dbus.Connect(n.addr)
	if err != nil {
		n.log.Debug("dbus connect failed", "err", err)
		return nil
	}
	rules := [][]dbus.MatchOption{
		{dbus.WithMatchSender(busName), dbus.WithMatchObjectPath(objPath), dbus.WithMatchInterface(iface)},
		{dbus.WithMatchSender("org.freedesktop.DBus"), dbus.WithMatchInterface("org.freedesktop.DBus"),
			dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, busName)},
	}
	for _, r := range rules {
		if err := conn.AddMatchSignal(r...); err != nil {
			n.log.Warn("dbus AddMatch failed", "err", err)
			_ = conn.Close()
			return nil
		}
	}
	ch := make(chan *dbus.Signal, 16)
	conn.Signal(ch)
	n.conn = conn
	n.caps = nil
	n.owner = ""
	go n.signalLoop(conn, ch)
	n.log.Debug("connected to session bus")
	return conn
}

// dropConnLocked forgets conn and every notification id tied to it (ids are
// meaningless on a new bus or after the server restarted). Message counts
// survive so a reconnect keeps summarising.
func (n *Notifier) dropConnLocked(conn *dbus.Conn) {
	if n.conn == conn {
		n.conn = nil
		n.caps = nil
		n.owner = ""
		n.forgetIDsLocked()
	}
	_ = conn.Close()
}

func (n *Notifier) forgetIDsLocked() {
	for id, e := range n.byID {
		e.id = 0
		delete(n.byID, id)
	}
}

// loadCapsLocked fetches the server capabilities (this also D-Bus-activates
// the server if needed) and its unique name for sender checks.
func (n *Notifier) loadCapsLocked(ctx context.Context, conn *dbus.Conn) {
	var caps []string
	err := conn.Object(busName, objPath).CallWithContext(ctx, iface+".GetCapabilities", 0).Store(&caps)
	if err != nil {
		n.log.Debug("GetCapabilities failed", "err", err)
		return // try again next time
	}
	n.caps = make(map[string]bool, len(caps))
	for _, c := range caps {
		n.caps[c] = true
	}
	var owner string
	if err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, busName).Store(&owner); err == nil {
		n.owner = owner
	}
	n.log.Debug("notification server", "owner", n.owner, "caps", caps)
}

// markup reports whether the body is interpreted as markup (so must be
// escaped). Unknown capabilities: assume markup, the common case.
func (n *Notifier) markup() bool {
	return n.caps == nil || n.caps["body-markup"]
}

func (n *Notifier) signalLoop(conn *dbus.Conn, ch <-chan *dbus.Signal) {
	for sig := range ch {
		n.handleSignal(conn, sig)
	}
	// The channel closes when the connection does (bus lost or Close).
	n.mu.Lock()
	if n.conn == conn {
		n.log.Info("lost D-Bus session bus; will reconnect on next notification")
		n.dropConnLocked(conn)
		n.lastDial = time.Time{} // allow an immediate redial
	}
	n.mu.Unlock()
}

func (n *Notifier) handleSignal(conn *dbus.Conn, sig *dbus.Signal) {
	if sig.Name == "org.freedesktop.DBus.NameOwnerChanged" {
		if sig.Sender != "org.freedesktop.DBus" || len(sig.Body) != 3 {
			return
		}
		name, _ := sig.Body[0].(string)
		oldOwner, _ := sig.Body[1].(string)
		newOwner, _ := sig.Body[2].(string)
		if name != busName {
			return
		}
		n.mu.Lock()
		if n.conn == conn {
			n.owner = newOwner
			if oldOwner != "" {
				// The server that issued our ids went away or was
				// replaced: they are void. (A server merely appearing,
				// e.g. activated by our own Notify, keeps them.)
				n.caps = nil
				n.forgetIDsLocked()
			}
		}
		n.mu.Unlock()
		return
	}
	if sig.Path != objPath || !strings.HasPrefix(sig.Name, iface+".") {
		return
	}
	member := sig.Name[len(iface)+1:]

	n.mu.Lock()
	if n.conn != conn || n.closed {
		n.mu.Unlock()
		return
	}
	// Only the notification server may drive actions: a unicast signal from
	// any other peer would otherwise be able to send replies as the user.
	if n.owner == "" {
		n.mu.Unlock()
		var owner string
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, busName).Store(&owner)
		cancel()
		if err != nil {
			return
		}
		n.mu.Lock()
		if n.conn != conn || n.closed {
			n.mu.Unlock()
			return
		}
		n.owner = owner
	}
	if sig.Sender != n.owner {
		n.mu.Unlock()
		n.log.Warn("ignoring notification signal from non-owner", "sender", sig.Sender, "member", member)
		return
	}
	defer n.mu.Unlock()

	switch member {
	case "ActionInvoked":
		id, key, ok := uintString(sig.Body)
		if !ok {
			return
		}
		e := n.byID[id]
		if e == nil {
			return
		}
		switch key {
		case actDecline:
			n.forgetLocked(e, true)
			n.callLocked(func(a api.NotifyActions) {
				if a.Decline != nil && e.call != "" {
					a.Decline(e.call)
				}
			})
		case actMarkRead:
			n.forgetLocked(e, true)
			n.callLocked(func(a api.NotifyActions) {
				if a.MarkRead != nil {
					a.MarkRead(e.chat)
				}
			})
		default: // "default", or "inline-reply" pressed as a plain button on non-KDE servers
			// The user is going to the chat: close it and count afresh.
			n.forgetLocked(e, true)
			n.callLocked(func(a api.NotifyActions) {
				if a.Open != nil {
					a.Open(e.chat)
				}
			})
		}
	case "NotificationReplied":
		id, text, ok := uintString(sig.Body)
		if !ok {
			return
		}
		e := n.byID[id]
		if e == nil || strings.TrimSpace(text) == "" {
			return
		}
		n.forgetLocked(e, true)
		n.callLocked(func(a api.NotifyActions) {
			if a.Reply != nil {
				a.Reply(e.chat, e.msgID, text)
			}
		})
	case "NotificationClosed":
		if len(sig.Body) != 2 {
			return
		}
		id, ok1 := sig.Body[0].(uint32)
		reason, ok2 := sig.Body[1].(uint32)
		if !ok1 || !ok2 {
			return
		}
		e := n.byID[id]
		if e == nil {
			return
		}
		delete(n.byID, id)
		e.id = 0
		if n.calls[e.call] == e {
			delete(n.calls, e.call)
		}
		// Expired popups may not have been seen: keep counting so the next
		// one says "N new messages". Dismissed/closed: start over.
		if reason != closedExpired && n.byChat[e.chat] == e {
			delete(n.byChat, e.chat)
		}
	case "ActivationToken":
		if _, tok, ok := uintString(sig.Body); ok {
			n.token = tok
		}
	}
}

// forgetLocked drops e, optionally closing its notification.
func (n *Notifier) forgetLocked(e *entry, closeIt bool) {
	if n.byChat[e.chat] == e {
		delete(n.byChat, e.chat)
	}
	if n.calls[e.call] == e {
		delete(n.calls, e.call)
	}
	if e.id != 0 {
		delete(n.byID, e.id)
		if closeIt {
			n.closeNotificationLocked(e.id)
		}
	}
}

// callLocked runs f with the current actions on a new goroutine so slow
// callbacks (sending a reply) never stall signal handling, and callbacks may
// re-enter the Notifier.
func (n *Notifier) callLocked(f func(api.NotifyActions)) {
	a := n.actions
	go f(a)
}

func uintString(body []any) (uint32, string, bool) {
	if len(body) != 2 {
		return 0, "", false
	}
	id, ok1 := body[0].(uint32)
	s, ok2 := body[1].(string)
	return id, s, ok1 && ok2
}

// render builds the summary (plain text) and body for a chat notification
// that now covers count messages.
func render(msg api.Notification, count int, markup bool) (summary, body string) {
	chatName := strings.TrimSpace(msg.ChatName)
	if chatName == "" {
		chatName = displayJID(msg.Chat)
	}
	sender := strings.TrimSpace(msg.Sender)
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = "New message"
	}

	line := text
	summary = chatName
	if msg.IsGroup && sender != "" {
		if count > 1 {
			line = sender + ": " + text
		} else {
			summary = sender + " @ " + chatName
		}
	}
	if count > 1 {
		line = strconv.Itoa(count) + " new messages\n" + line
	}
	summary = truncate(summary, maxTitleRunes)
	body = truncate(line, maxBodyRunes)
	if markup {
		body = escapeMarkup(body)
	}
	return summary, body
}

// escapeMarkup escapes text for the notification body-markup subset.
func escapeMarkup(s string) string {
	if !strings.ContainsAny(s, "&<>") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max { // fast path: bytes >= runes
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// displayJID turns "61400000000@s.whatsapp.net" into "+61400000000".
func displayJID(jid string) string {
	user, server, ok := strings.Cut(jid, "@")
	if !ok {
		return jid
	}
	if server == "s.whatsapp.net" {
		return "+" + user
	}
	return user
}

// imagePath returns the image-path hint: a file:// URI for a local avatar,
// else the app icon.
func imagePath(avatar string) string {
	if filepath.IsAbs(avatar) {
		if _, err := os.Stat(avatar); err == nil {
			return (&url.URL{Scheme: "file", Path: avatar}).String()
		}
	}
	return appIcon
}
