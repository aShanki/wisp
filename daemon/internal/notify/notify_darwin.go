package notify

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
void wispNotify(const char *payload);
*/
import "C"

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"unsafe"

	"wisp/daemon/internal/api"
)

type Config struct{ Log *slog.Logger }
type Notifier struct {
	mu     sync.Mutex
	closed bool
}

func New(Config) *Notifier                       { return &Notifier{} }
func (n *Notifier) SetActions(api.NotifyActions) {}
func post(value map[string]any) {
	b, err := json.Marshal(value)
	if err != nil {
		return
	}
	s := C.CString(string(b))
	defer C.free(unsafe.Pointer(s))
	C.wispNotify(s)
}
func (n *Notifier) Notify(_ context.Context, m api.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed {
		return nil
	}
	title, body := m.ChatName, m.Text
	if title == "" {
		title = "New message"
	}
	if m.IsGroup && m.Sender != "" {
		body = m.Sender + ": " + body
	}
	post(map[string]any{"action": "show", "id": m.Chat, "chat": m.Chat, "message": m.MsgID, "title": title, "body": body})
	return nil
}
func (n *Notifier) NotifyCall(_ context.Context, c api.CallNotification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.closed {
		post(map[string]any{"action": "show", "id": "call-" + c.ID, "chat": c.Chat, "call": c.ID, "title": c.Name, "body": "Incoming call"})
	}
	return nil
}
func (n *Notifier) Clear(chat string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	post(map[string]any{"action": "clear", "id": chat})
}
func (n *Notifier) ClearCall(id string) { post(map[string]any{"action": "clear", "id": "call-" + id}) }
func (n *Notifier) ClearAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	post(map[string]any{"action": "clearAll"})
}
func (n *Notifier) Close() error { n.mu.Lock(); n.closed = true; n.mu.Unlock(); return nil }
