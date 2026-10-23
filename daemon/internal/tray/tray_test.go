package tray

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"wisp/daemon/internal/api"
)

// Every test runs its own dbus-daemon, so nothing ever reaches the user's
// session bus or panel. The config has no service directories: nothing can be
// D-Bus-activated on it.
const busConfig = `<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=%SOCK%</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`

func privateBus(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed")
	}
	dir := t.TempDir()
	sock, conf := filepath.Join(dir, "bus"), filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(conf, []byte(strings.ReplaceAll(busConfig, "%SOCK%", sock)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config-file="+conf, "--nofork", "--nopidfile", "--print-address=1")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start dbus-daemon: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		addr <- strings.TrimSpace(line)
	}()
	select {
	case a := <-addr:
		if a == "" {
			t.Fatal("dbus-daemon printed no address")
		}
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("dbus-daemon did not start")
	}
	return ""
}

func dial(t *testing.T, addr string) *dbus.Conn {
	t.Helper()
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// fakeWatcher is a minimal org.kde.StatusNotifierWatcher.
type fakeWatcher struct {
	conn  *dbus.Conn
	items chan string
}

func newWatcher(t *testing.T, addr string) *fakeWatcher {
	t.Helper()
	w := &fakeWatcher{conn: dial(t, addr), items: make(chan string, 8)}
	if err := w.conn.Export(w, watcherPath, watcherName); err != nil {
		t.Fatal(err)
	}
	if reply, err := w.conn.RequestName(watcherName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName: %v %v", reply, err)
	}
	return w
}

func (w *fakeWatcher) RegisterStatusNotifierItem(service string) *dbus.Error {
	w.items <- service
	return nil
}

func (w *fakeWatcher) next(t *testing.T) string {
	t.Helper()
	select {
	case s := <-w.items:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("no RegisterStatusNotifierItem")
	}
	return ""
}

var itemName = fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())

func newTray(t *testing.T, addr string, cfg Config) *Tray {
	t.Helper()
	cfg.BusAddress = addr
	tr := New(cfg)
	if tr.conn == nil {
		t.Fatal("tray did not connect")
	}
	t.Cleanup(func() { tr.Close() })
	return tr
}

func getAll(t *testing.T, c *dbus.Conn, path dbus.ObjectPath, iface string) map[string]dbus.Variant {
	t.Helper()
	var m map[string]dbus.Variant
	if err := c.Object(itemName, path).Call(propsIface+".GetAll", 0, iface).Store(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func tooltipText(t *testing.T, m map[string]dbus.Variant) string {
	t.Helper()
	tt, ok := m["ToolTip"].Value().([]any)
	if !ok || len(tt) != 4 || tt[2] != "Wisp" {
		t.Fatalf("ToolTip = %#v", m["ToolTip"].Value())
	}
	return tt[3].(string)
}

func TestRegistersWithProperties(t *testing.T) {
	addr := privateBus(t)
	w := newWatcher(t, addr)
	newTray(t, addr, Config{})
	if got := w.next(t); got != itemName {
		t.Fatalf("registered %q, want %q", got, itemName)
	}
	m := getAll(t, dial(t, addr), itemPath, itemIface)
	for k, want := range map[string]any{"Category": "Communications", "Id": "wisp", "Title": "Wisp",
		"Status": "Active", "ItemIsMenu": false, "Menu": menuPath} {
		if got := m[k].Value(); got != want {
			t.Errorf("%s = %#v, want %#v", k, got, want)
		}
	}
	if name, _ := m["IconName"].Value().(string); name != "wisp" && name != "dialog-messages" {
		t.Errorf("IconName = %q", name)
	}
	if text := tooltipText(t, m); text != "" {
		t.Errorf("tooltip text %q with nothing unread", text)
	}
}

func TestActivateOpensWithToken(t *testing.T) {
	addr := privateBus(t)
	opened := make(chan string, 4)
	tr := newTray(t, addr, Config{Open: func(token string) { opened <- token }})
	obj := dial(t, addr).Object(itemName, itemPath)
	call := func(method string, args ...any) {
		t.Helper()
		if err := obj.Call(itemIface+"."+method, 0, args...).Err; err != nil {
			t.Fatal(err)
		}
	}
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-opened:
			if got != want {
				t.Fatalf("Open(%q), want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Activate did not open")
		}
	}

	call("ProvideXdgActivationToken", "tok-1")
	call("Activate", int32(10), int32(20))
	expect("tok-1")
	call("Activate", int32(10), int32(20)) // a double click opens once
	select {
	case got := <-opened:
		t.Fatalf("second Open(%q) within the debounce", got)
	case <-time.After(150 * time.Millisecond):
	}
	tr.mu.Lock()
	tr.opened = time.Time{}
	tr.mu.Unlock()
	call("Activate", int32(10), int32(20))
	expect("") // the token is used once
}

func TestUnreadNeedsAttention(t *testing.T) {
	addr := privateBus(t)
	tr := newTray(t, addr, Config{})
	client := dial(t, addr)
	if err := client.AddMatchSignal(dbus.WithMatchInterface(itemIface), dbus.WithMatchMember("NewStatus")); err != nil {
		t.Fatal(err)
	}
	sigs := make(chan *dbus.Signal, 16)
	client.Signal(sigs)
	nextStatus := func() string {
		t.Helper()
		select {
		case s := <-sigs:
			return s.Body[0].(string)
		case <-time.After(3 * time.Second):
			t.Fatal("no NewStatus")
		}
		return ""
	}
	noSignal := func() {
		t.Helper()
		select {
		case s := <-sigs:
			t.Fatalf("unexpected %s %v", s.Name, s.Body)
		case <-time.After(150 * time.Millisecond):
		}
	}
	state := func() (status, text string, overlay bool) {
		m := getAll(t, client, itemPath, itemIface)
		px := reflect.ValueOf(m["OverlayIconPixmap"].Value())
		return m["Status"].Value().(string), tooltipText(t, m), px.Len() > 0
	}

	tr.Chat(api.Chat{JID: "a", Muted: true, Unread: 3}) // muted: no badge
	tr.Chat(api.Chat{JID: "b", Archived: true, Unread: 1})
	noSignal()

	tr.Chat(api.Chat{JID: "c", Unread: 2})
	if s := nextStatus(); s != "NeedsAttention" {
		t.Fatalf("NewStatus %q", s)
	}
	if st, text, overlay := state(); st != "NeedsAttention" || text != "1 unread chat" || !overlay {
		t.Fatalf("state %q %q overlay=%v", st, text, overlay)
	}
	tr.Chat(api.Chat{JID: "c", Unread: 3}) // same count: no signal
	noSignal()
	tr.Chat(api.Chat{JID: "d", MarkedUnread: true})
	nextStatus()
	if _, text, _ := state(); text != "2 unread chats" {
		t.Fatalf("tooltip %q", text)
	}

	tr.Chat(api.Chat{JID: "c"})
	nextStatus()
	tr.Chat(api.Chat{JID: "d", Muted: true, MarkedUnread: true})
	if s := nextStatus(); s != "Active" {
		t.Fatalf("NewStatus %q after reading", s)
	}
	if st, text, overlay := state(); st != "Active" || text != "" || overlay {
		t.Fatalf("state %q %q overlay=%v", st, text, overlay)
	}

	tr.Chat(api.Chat{JID: "e", Unread: 1})
	nextStatus()
	tr.Reset()
	if s := nextStatus(); s != "Active" {
		t.Fatalf("NewStatus %q after Reset", s)
	}
}

func TestReregistersWhenWatcherRestarts(t *testing.T) {
	addr := privateBus(t)
	newTray(t, addr, Config{}) // no watcher yet: waits
	w := newWatcher(t, addr)
	if got := w.next(t); got != itemName {
		t.Fatalf("registered %q", got)
	}
	w.conn.Close()
	w2 := newWatcher(t, addr)
	if got := w2.next(t); got != itemName {
		t.Fatalf("re-registered %q", got)
	}
}

func TestMenu(t *testing.T) {
	addr := privateBus(t)
	opened, quit := make(chan string, 1), make(chan struct{}, 1)
	newTray(t, addr, Config{Open: func(tok string) { opened <- tok }, Quit: func() { quit <- struct{}{} }})
	client := dial(t, addr)
	obj := client.Object(itemName, menuPath)

	var rev uint32
	var layout []any
	if err := obj.Call(menuIface+".GetLayout", 0, int32(0), int32(-1), []string{}).Store(&rev, &layout); err != nil {
		t.Fatal(err)
	}
	children := layout[2].([]dbus.Variant)
	var labels []string
	for _, c := range children {
		props := c.Value().([]any)[1].(map[string]dbus.Variant)
		labels = append(labels, props["label"].Value().(string))
	}
	if strings.Join(labels, ",") != "Open Wisp,Quit Wisp" {
		t.Fatalf("menu %v", labels)
	}
	if v := getAll(t, client, menuPath, menuIface)["Version"].Value(); v != uint32(3) {
		t.Fatalf("Version %v", v)
	}

	click := func(id int32) {
		t.Helper()
		if err := obj.Call(menuIface+".Event", 0, id, "clicked", dbus.MakeVariant(""), uint32(0)).Err; err != nil {
			t.Fatal(err)
		}
	}
	click(menuOpen)
	select {
	case <-opened:
	case <-time.After(3 * time.Second):
		t.Fatal("Open Wisp did not open")
	}
	click(menuQuit)
	select {
	case <-quit:
	case <-time.After(3 * time.Second):
		t.Fatal("Quit Wisp did not quit")
	}
}

func TestNoBusIsNoOp(t *testing.T) {
	tr := New(Config{BusAddress: "unix:path=" + filepath.Join(t.TempDir(), "none")})
	tr.Chat(api.Chat{JID: "a", Unread: 1})
	tr.Reset()
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}
