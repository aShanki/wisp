// Package core implements api.Backend for real WhatsApp by composing the
// store (wisp.db), media cache, WA client (whatsmeow) and desktop notifier.
//
// Data flow:
//   - WA events are queued and handled one at a time on a single goroutine
//     (handle), which writes to the store and emits protocol events. History
//     batches are handled on the WA layer's goroutine, serialized by writeMu.
//   - RPC methods read from the store; mutations call WA first, then apply to
//     the store and emit. Sends store a "pending" message and return
//     immediately; a goroutine completes the send.
//
// Nothing polls: all work is driven by WA events or RPC calls.
package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/version"
)

// Config configures New.
type Config struct {
	DataDir string
	Socket  string // passed to a UI launched from a notification
	Log     *slog.Logger
}

// Deps are the components Core composes (NewWith, for tests with fakes).
type Deps struct {
	Store    api.Store
	Media    api.Media
	WA       api.WA
	Notifier api.Notifier
	Log      *slog.Logger
}

// Core is the real backend.
type Core struct {
	store  api.Store
	media  api.Media
	wa     api.WA
	notify api.Notifier
	log    *slog.Logger
	socket string

	// Lifetime context for background work (sends, QR loop, avatar refresh).
	life   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	emitMu sync.RWMutex
	emit   api.Emitter

	// WA events are handled serially by loop(). writeMu also serializes
	// read-modify-write message mutations done outside the loop (send
	// completion) against event handling.
	events  chan api.WAEvent
	writeMu sync.Mutex

	// UI clients (ui.setActive / daemon.hello).
	clientsMu   sync.Mutex
	clients     map[int64]*client
	online      bool // some UI window is focused
	onlineForce bool
	onlineWake  chan struct{}

	// Settings cache (raw values decoded from JSON), loaded lazily.
	setMu    sync.Mutex
	settings map[string]any

	resyncing atomic.Bool // resyncAppState running

	// Read receipts that couldn't be sent (e.g. reconnecting).
	unsentMu sync.Mutex
	unsent   []api.MsgKey

	// History-sync progress, guarded by writeMu.
	histChats map[string]struct{}
	histMsgs  int

	calls map[string]*call // calls in progress, guarded by writeMu

	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
}

type client struct {
	hello   bool
	chat    string
	focused bool
}

var _ api.Backend = (*Core)(nil)

// NewWith builds a Core from already-constructed components.
func NewWith(d Deps) *Core {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	life, cancel := context.WithCancel(context.Background())
	return &Core{
		store:      d.Store,
		media:      d.Media,
		wa:         d.WA,
		notify:     d.Notifier,
		log:        log,
		life:       life,
		cancel:     cancel,
		events:     make(chan api.WAEvent, 128),
		onlineWake: make(chan struct{}, 1),
		clients:    map[int64]*client{},
		histChats:  map[string]struct{}{},
		done:       make(chan struct{}),
	}
}

// Start wires callbacks, applies settings and connects to WhatsApp in the
// background (if a session exists; otherwise it stays offline until
// auth.startQR).
func (c *Core) Start(ctx context.Context) {
	c.startOnce.Do(func() {
		c.notify.SetActions(api.NotifyActions{
			Open:     c.onNotifyOpen,
			Reply:    c.onNotifyReply,
			MarkRead: c.onNotifyMarkRead,
			Decline:  c.onNotifyDecline,
		})
		c.media.SetCap(c.cacheBytes(ctx))
		c.wa.SetEventHandler(c.enqueue)
		c.wg.Add(3)
		go c.loop()
		go c.presenceLoop()
		go c.connect()
	})
}

// connect runs off the RPC path so the UI is served during the handshake.
// whatsmeow keeps retrying after a network failure.
func (c *Core) connect() {
	defer c.wg.Done()
	if err := c.wa.Connect(c.life); err != nil {
		c.log.Warn("core: connect failed", "err", err)
	}
}

// Close stops event handling and releases all components. Idempotent.
func (c *Core) Close() error {
	var errs []error
	c.closeOnce.Do(func() {
		c.cancel()
		c.wa.Disconnect()
		// Wait (bounded) for the event loop and in-flight sends.
		waited := make(chan struct{})
		go func() { c.wg.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
			c.log.Warn("core: background work still running at close")
		}
		errs = append(errs, c.notify.Close())
		if wc, ok := c.wa.(io.Closer); ok {
			errs = append(errs, wc.Close())
		}
		errs = append(errs, c.store.Close())
	})
	return errors.Join(errs...)
}

// Done is closed after daemon.shutdown.
func (c *Core) Done() <-chan struct{} { return c.done }

// SetEmitter is called once by the RPC server before serving.
func (c *Core) SetEmitter(e api.Emitter) {
	c.emitMu.Lock()
	c.emit = e
	c.emitMu.Unlock()
}

func (c *Core) emitEv(event string, params any) {
	c.emitMu.RLock()
	e := c.emit
	c.emitMu.RUnlock()
	if e != nil {
		e.Emit(event, params)
	}
}

func (c *Core) Hello(ctx context.Context, p api.HelloParams) (*api.HelloResult, error) {
	c.clientsMu.Lock()
	c.clientLocked(ctx).hello = true
	c.clientsMu.Unlock()
	return &api.HelloResult{
		Protocol: api.ProtocolVersion,
		Version:  version.Version,
		PID:      os.Getpid(),
		Auth:     c.wa.AuthState(),
		Conn:     c.wa.ConnState(),
		SelfJID:  c.wa.SelfJID(),
		SelfName: c.wa.SelfName(),
		Calls:    c.wa.CallsAvailable(),
	}, nil
}

func (c *Core) Shutdown(ctx context.Context) error {
	c.doneOnce.Do(func() { close(c.done) })
	return nil
}

func (c *Core) AuthStatus(ctx context.Context) (*api.AuthStatusResult, error) {
	return &api.AuthStatusResult{
		State:    c.wa.AuthState(),
		SelfJID:  c.wa.SelfJID(),
		SelfName: c.wa.SelfName(),
		Conn:     c.wa.ConnState(),
	}, nil
}

func (c *Core) AuthStartQR(ctx context.Context) error {
	// The QR loop outlives the request: use the core lifetime context.
	if err := c.wa.StartQR(c.life); err != nil {
		return err
	}
	c.emitAuthState()
	return nil
}

func (c *Core) AuthPairPhone(ctx context.Context, p api.PairPhoneParams) (*api.PairPhoneResult, error) {
	if p.Phone == "" {
		return nil, api.InvalidParams("phone is required")
	}
	code, err := c.wa.PairPhone(c.life, p.Phone)
	if err != nil {
		return nil, err
	}
	c.emitAuthState()
	return &api.PairPhoneResult{Code: code}, nil
}

func (c *Core) AuthLogout(ctx context.Context) error {
	if err := c.wa.Logout(ctx); err != nil {
		return err
	}
	c.writeMu.Lock()
	c.wipe(context.WithoutCancel(ctx))
	c.writeMu.Unlock()
	c.emitEv(api.EAuthState, api.AuthStateEvent{State: api.AuthUnpaired})
	return nil
}

// wipe deletes the logged-out account's chats, messages and media so an
// account linked next starts empty. Caller holds writeMu.
func (c *Core) wipe(ctx context.Context) {
	c.notify.ClearAll()
	if err := c.store.Wipe(ctx); err != nil {
		c.log.Error("core: wiping the store failed", "err", err)
	}
	if err := c.media.Wipe(); err != nil {
		c.log.Error("core: wiping the media cache failed", "err", err)
	}
	c.histChats, c.histMsgs = map[string]struct{}{}, 0
	clear(c.calls)
	c.unsentMu.Lock()
	c.unsent = nil
	c.unsentMu.Unlock()
}

func (c *Core) emitAuthState() {
	c.emitEv(api.EAuthState, api.AuthStateEvent{State: c.wa.AuthState(), SelfJID: c.wa.SelfJID(), SelfName: c.wa.SelfName()})
}

func (c *Core) requireLogin() error {
	if !c.wa.IsLoggedIn() {
		return api.ErrNotLoggedIn
	}
	return nil
}
