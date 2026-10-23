// Package tray shows Wisp in the system tray as a StatusNotifierItem, so the
// daemon stays visible while the window is closed. A click opens the UI; the
// item asks for attention while an unmuted chat has unread messages; a small
// com.canonical.dbusmenu offers Open and Quit. Nothing polls: the state
// changes only through Chat and Reset. Without a session bus every method is
// a no-op, and without a StatusNotifierWatcher the item waits for one.
package tray

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"wisp/daemon/internal/api"
)

const (
	watcherName = "org.kde.StatusNotifierWatcher"
	watcherPath = dbus.ObjectPath("/StatusNotifierWatcher")
	itemIface   = "org.kde.StatusNotifierItem"
	itemPath    = dbus.ObjectPath("/StatusNotifierItem")
	menuIface   = "com.canonical.dbusmenu"
	menuPath    = dbus.ObjectPath("/MenuBar")
	propsIface  = "org.freedesktop.DBus.Properties"

	menuOpen = 1
	menuQuit = 2

	callTimeout  = 3 * time.Second
	openDebounce = 1500 * time.Millisecond // a double click, or a click while the UI starts
)

// Config configures a Tray.
type Config struct {
	Log *slog.Logger // nil: slog.Default()
	// BusAddress overrides the D-Bus address (tests). Empty means the
	// session bus, never autolaunched.
	BusAddress string
	// Open shows the UI. token is the xdg-activation token the tray host
	// gave for the click, or "".
	Open func(token string)
	Quit func()
}

// Tray is a StatusNotifierItem. Safe for concurrent use.
type Tray struct {
	cfg  Config
	log  *slog.Logger
	conn *dbus.Conn // nil: no bus
	name string
	icon string

	mu     sync.Mutex
	unread map[string]struct{} // unmuted, unarchived chats with unread messages
	token  string
	opened time.Time
}

// New exports the item on the session bus and registers it with the
// watcher, now and whenever a watcher (re)starts.
func New(cfg Config) *Tray {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	t := &Tray{cfg: cfg, log: cfg.Log.With("component", "tray"), icon: iconName(),
		unread: map[string]struct{}{}}
	conn, err := connect(cfg.BusAddress)
	if err != nil {
		t.log.Info("tray: no D-Bus session bus; no tray icon", "err", err)
		return t
	}
	t.name = fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	if err := t.export(conn); err != nil {
		conn.Close()
		t.log.Warn("tray: export failed", "err", err)
		return t
	}
	t.conn = conn
	sigs := make(chan *dbus.Signal, 8)
	conn.Signal(sigs)
	go func() { // closed when conn closes
		for s := range sigs {
			if s.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(s.Body) != 3 {
				continue
			}
			if owner, _ := s.Body[2].(string); owner != "" { // a watcher (re)started
				t.register()
			}
		}
	}()
	t.register()
	return t
}

func connect(addr string) (*dbus.Conn, error) {
	if addr != "" {
		return dbus.Connect(addr)
	}
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return nil, err
	}
	if err = conn.Auth(nil); err == nil {
		err = conn.Hello()
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (t *Tray) export(conn *dbus.Conn) error {
	exports := []struct {
		v     any
		path  dbus.ObjectPath
		iface string
	}{
		{item{t}, itemPath, itemIface},
		{props{itemIface, t.itemProps}, itemPath, propsIface},
		{introspect.Introspectable(itemXML), itemPath, "org.freedesktop.DBus.Introspectable"},
		{menu{t}, menuPath, menuIface},
		{props{menuIface, menuProperties}, menuPath, propsIface},
		{introspect.Introspectable(menuXML), menuPath, "org.freedesktop.DBus.Introspectable"},
	}
	for _, e := range exports {
		if err := conn.Export(e.v, e.path, e.iface); err != nil {
			return err
		}
	}
	if err := conn.AddMatchSignal(dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, watcherName)); err != nil {
		return err
	}
	reply, err := conn.RequestName(t.name, dbus.NameFlagDoNotQueue)
	if err == nil && reply != dbus.RequestNameReplyPrimaryOwner {
		err = fmt.Errorf("name %s is taken", t.name)
	}
	return err
}

func (t *Tray) register() {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	err := t.conn.Object(watcherName, watcherPath).CallWithContext(ctx,
		watcherName+".RegisterStatusNotifierItem", dbus.FlagNoAutoStart, t.name).Err
	if err != nil {
		t.log.Info("tray: no StatusNotifierWatcher yet", "err", err)
		return
	}
	t.log.Info("tray: registered", "name", t.name)
}

// Close removes the item.
func (t *Tray) Close() error {
	if t.conn == nil {
		return nil
	}
	return t.conn.Close()
}

// Chat records a chat's unread state (from chats.list or chat.update) and
// updates the item if the count of unread chats changed.
func (t *Tray) Chat(c api.Chat) {
	if t.conn == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.unread)
	if (c.Unread > 0 || c.MarkedUnread) && !c.Muted && !c.Archived {
		t.unread[c.JID] = struct{}{}
	} else {
		delete(t.unread, c.JID)
	}
	if len(t.unread) != n {
		t.changedLocked()
	}
}

// Reset forgets all chats (after logout).
func (t *Tray) Reset() {
	if t.conn == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.unread) > 0 {
		t.unread = map[string]struct{}{}
		t.changedLocked()
	}
}

// changedLocked tells the host to re-read the state. Under mu, so the
// signals go out in order.
func (t *Tray) changedLocked() {
	status, _ := t.stateLocked()
	for _, s := range []struct {
		name string
		args []any
	}{{"NewStatus", []any{status}}, {"NewOverlayIcon", nil}, {"NewToolTip", nil}} {
		if err := t.conn.Emit(itemPath, itemIface+"."+s.name, s.args...); err != nil {
			t.log.Debug("tray: signal failed", "signal", s.name, "err", err)
		}
	}
}

func (t *Tray) stateLocked() (status, text string) {
	switch n := len(t.unread); n {
	case 0:
		return "Active", ""
	case 1:
		return "NeedsAttention", "1 unread chat"
	default:
		return "NeedsAttention", fmt.Sprintf("%d unread chats", n)
	}
}

func (t *Tray) open() {
	t.mu.Lock()
	token := t.token
	t.token = ""
	again := time.Since(t.opened) < openDebounce
	if !again {
		t.opened = time.Now()
	}
	t.mu.Unlock()
	if t.cfg.Open != nil && !again {
		t.cfg.Open(token)
	}
}

// pixmap is an ARGB32 image in network byte order, the a(iiay) of the spec.
type pixmap struct {
	W, H int32
	ARGB []byte
}

type toolTip struct {
	Icon    string
	Pixmaps []pixmap
	Title   string
	Text    string
}

func (t *Tray) itemProps() map[string]dbus.Variant {
	t.mu.Lock()
	status, text := t.stateLocked()
	t.mu.Unlock()
	overlay := []pixmap{}
	if status == "NeedsAttention" {
		overlay = unreadDot
	}
	v := dbus.MakeVariant
	return map[string]dbus.Variant{
		"Category":            v("Communications"),
		"Id":                  v("wisp"),
		"Title":               v("Wisp"),
		"Status":              v(status),
		"WindowId":            v(int32(0)),
		"IconName":            v(t.icon),
		"IconPixmap":          v([]pixmap{}),
		"OverlayIconName":     v(""),
		"OverlayIconPixmap":   v(overlay),
		"AttentionIconName":   v(""),
		"AttentionIconPixmap": v([]pixmap{}),
		"AttentionMovieName":  v(""),
		"ToolTip":             v(toolTip{t.icon, []pixmap{}, "Wisp", text}),
		"ItemIsMenu":          v(false),
		"Menu":                v(menuPath),
	}
}

// iconName is the installed app icon, or a themed stand-in when wisp runs
// uninstalled.
func iconName() string {
	home := os.Getenv("XDG_DATA_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".local", "share")
	}
	dirs := os.Getenv("XDG_DATA_DIRS")
	if dirs == "" {
		dirs = "/usr/local/share:/usr/share"
	}
	for _, d := range append([]string{home}, filepath.SplitList(dirs)...) {
		if m, _ := filepath.Glob(filepath.Join(d, "icons", "hicolor", "*", "apps", "wisp.*")); len(m) > 0 {
			return "wisp"
		}
	}
	return "dialog-messages"
}

// unreadDot is the overlay shown while chats are unread: a red dot that the
// host draws over the icon's bottom-right quarter.
var unreadDot = func() []pixmap {
	const size = 32
	const cx, cy, r = 17.0, 17.0, 12.0
	px := make([]byte, 0, size*size*4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			a := math.Max(0, math.Min(1, r-d+0.5))
			px = append(px, byte(a*255), 0xe5, 0x39, 0x35)
		}
	}
	return []pixmap{{size, size, px}}
}()

// item is the org.kde.StatusNotifierItem interface.
type item struct{ t *Tray }

func (i item) Activate(x, y int32) *dbus.Error            { go i.t.open(); return nil }
func (i item) SecondaryActivate(x, y int32) *dbus.Error   { return nil }
func (i item) ContextMenu(x, y int32) *dbus.Error         { return nil }
func (i item) Scroll(delta int32, dir string) *dbus.Error { return nil }

// ProvideXdgActivationToken is KDE's extension: the host sends a token just
// before Activate so the window may take focus on Wayland.
func (i item) ProvideXdgActivationToken(token string) *dbus.Error {
	i.t.mu.Lock()
	i.t.token = token
	i.t.mu.Unlock()
	return nil
}

// props is a read-only org.freedesktop.DBus.Properties for one interface.
type props struct {
	iface string
	all   func() map[string]dbus.Variant
}

func (p props) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	if v, ok := p.all()[name]; ok && iface == p.iface {
		return v, nil
	}
	return dbus.Variant{}, dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", []any{name})
}

func (p props) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != p.iface {
		return map[string]dbus.Variant{}, nil
	}
	return p.all(), nil
}

func (p props) Set(iface, name string, v dbus.Variant) *dbus.Error {
	return dbus.NewError("org.freedesktop.DBus.Error.PropertyReadOnly", []any{name})
}
