//go:build !darwin

package notify

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The bus tests never touch the user's session bus: each test starts its
// own dbus-daemon with a config that has no service directories, so nothing
// (e.g. plasmashell's notification service) can be D-Bus-activated on it and
// nothing pops up on the desktop. Works the same under dbus-run-session.

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

// privateBus starts a throwaway dbus-daemon and returns its address.
func privateBus(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not installed (apt-get install dbus)")
	}
	dir, err := os.MkdirTemp("", "wisp-notify-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "bus")
	conf := filepath.Join(dir, "bus.conf")
	if err := os.WriteFile(conf, []byte(strings.ReplaceAll(busConfig, "%SOCK%", sock)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config-file="+conf, "--nofork", "--nopidfile", "--print-address=1")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")} // no DISPLAY/WAYLAND: nothing graphical
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start dbus-daemon: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addrCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		addrCh <- strings.TrimSpace(line)
	}()
	select {
	case addr := <-addrCh:
		if addr == "" {
			t.Fatal("dbus-daemon printed no address")
		}
		return addr
	case <-time.After(5 * time.Second):
		t.Fatal("dbus-daemon did not start")
	}
	return ""
}

type notifyCall struct {
	AppName    string
	ReplacesID uint32
	Icon       string
	Summary    string
	Body       string
	Actions    []string
	Hints      map[string]dbus.Variant
	Timeout    int32
	ID         uint32
}

func (c notifyCall) hint(k string) any {
	v, ok := c.Hints[k]
	if !ok {
		return nil
	}
	return v.Value()
}

func (c notifyCall) hasAction(key string) bool {
	for i := 0; i+1 < len(c.Actions); i += 2 {
		if c.Actions[i] == key {
			return true
		}
	}
	return false
}

// fakeServer is a minimal org.freedesktop.Notifications implementation.
type fakeServer struct {
	t    *testing.T
	conn *dbus.Conn
	caps []string

	mu        sync.Mutex
	nextID    uint32
	live      map[uint32]bool
	inhibited bool
	calls     chan notifyCall
	closes    chan uint32
}

func newFakeServer(t *testing.T, addr string, caps []string, firstID ...uint32) *fakeServer {
	t.Helper()
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f := &fakeServer{t: t, conn: conn, caps: caps, nextID: 100, live: map[uint32]bool{},
		calls: make(chan notifyCall, 64), closes: make(chan uint32, 64)}
	if len(firstID) > 0 {
		f.nextID = firstID[0] - 1
	}
	if err := conn.Export(f, objPath, iface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(fakeProps{f}, objPath, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("RequestName: %v %v", reply, err)
	}
	return f
}

func (f *fakeServer) Notify(appName string, replacesID uint32, icon, summary, body string,
	actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	f.mu.Lock()
	id := replacesID
	if id == 0 || !f.live[id] {
		f.nextID++
		id = f.nextID
	}
	f.live[id] = true
	f.mu.Unlock()
	f.calls <- notifyCall{appName, replacesID, icon, summary, body, actions, hints, timeout, id}
	return id, nil
}

func (f *fakeServer) CloseNotification(id uint32) *dbus.Error {
	f.mu.Lock()
	wasLive := f.live[id]
	delete(f.live, id)
	f.mu.Unlock()
	f.closes <- id
	if wasLive {
		_ = f.conn.Emit(objPath, iface+".NotificationClosed", id, uint32(3))
	}
	return nil
}

func (f *fakeServer) setInhibited(v bool) {
	f.mu.Lock()
	f.inhibited = v
	f.mu.Unlock()
}

// fakeProps serves KDE's Inhibited (Do Not Disturb) property.
type fakeProps struct{ f *fakeServer }

func (p fakeProps) Get(ifc, name string) (dbus.Variant, *dbus.Error) {
	if ifc != iface || name != "Inhibited" {
		return dbus.Variant{}, dbus.MakeFailedError(os.ErrNotExist)
	}
	p.f.mu.Lock()
	defer p.f.mu.Unlock()
	return dbus.MakeVariant(p.f.inhibited), nil
}

func (f *fakeServer) GetCapabilities() ([]string, *dbus.Error) { return f.caps, nil }

func (f *fakeServer) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return "fake", "wisp", "1", "1.2", nil
}

// emit sends a server signal (as Plasma would).
func (f *fakeServer) emit(member string, args ...any) {
	f.t.Helper()
	if member == "NotificationClosed" {
		f.mu.Lock()
		delete(f.live, args[0].(uint32))
		f.mu.Unlock()
	}
	if err := f.conn.Emit(objPath, iface+"."+member, args...); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeServer) nextCall() notifyCall {
	f.t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(3 * time.Second):
		f.t.Fatal("timed out waiting for Notify call")
	}
	return notifyCall{}
}

func (f *fakeServer) nextClose() uint32 {
	f.t.Helper()
	select {
	case id := <-f.closes:
		return id
	case <-time.After(3 * time.Second):
		f.t.Fatal("timed out waiting for CloseNotification")
	}
	return 0
}

func (f *fakeServer) noClose() {
	f.t.Helper()
	select {
	case id := <-f.closes:
		f.t.Fatalf("unexpected CloseNotification(%d)", id)
	case <-time.After(100 * time.Millisecond):
	}
}
