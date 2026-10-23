// Package wa implements api.WA on top of go.mau.fi/whatsmeow: pairing,
// connection management, event translation and sending.
package wa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/purpshell/meowcaller"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	_ "modernc.org/sqlite"

	"wisp/daemon/internal/api"
	"wisp/daemon/internal/audio"
)

// Config configures the WhatsApp client.
type Config struct {
	DBPath string       // whatsmeow device store (wa.db)
	Log    *slog.Logger // nil = slog.Default()
}

// Client implements api.WA.
type Client struct {
	log       *slog.Logger
	container *sqlstore.Container
	ctx       context.Context // lives as long as the Client
	cancel    context.CancelFunc

	handler atomic.Pointer[func(api.WAEvent)]

	mu            sync.Mutex
	cli           *whatsmeow.Client
	mcaller       *meowcaller.Client // calls on cli
	auth          string
	conn          string
	qrCancel      context.CancelFunc
	qrReady       chan struct{} // closed when the first QR code of a flow arrives (or the flow ends)
	lastQR        api.EvQR
	groupsFetched bool

	offlineSync atomic.Bool // true while whatsmeow replays messages received while we were offline

	retryMu sync.Mutex
	retries map[types.MessageID]chan *events.MediaRetry // media retries waiting for the phone

	groupMu  sync.Mutex
	groupGen uint64                         // bumped by every change seen, so a fetch racing one isn't cached
	groups   map[types.JID]*types.GroupInfo // groupInfo cache

	callMu sync.Mutex
	calls  map[string]types.JID // ringing incoming calls: the creator as addressed
	live   map[string]*live     // calls meowcaller carries

	toneMu sync.Mutex
	tone   *audio.Ringer // the ringing or ringback tone, while one plays
	toneID string
}

var _ api.WA = (*Client)(nil)

var setOSOnce sync.Once

func setDeviceProps() {
	setOSOnce.Do(func() {
		store.SetOSInfo("Wisp", [3]uint32{0, 1, 0})
		store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
		store.DeviceProps.RequireFullSync = new(false)
	})
}

// New opens (creating if needed) the device store at cfg.DBPath. It does
// not connect; call SetEventHandler and then Connect.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.DBPath == "" {
		return nil, errors.New("wa: DBPath is required")
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("component", "wa")
	setDeviceProps()

	dsn := "file:" + cfg.DBPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	container, err := sqlstore.New(ctx, "sqlite", dsn, newWALog(log, "store"))
	if err != nil {
		return nil, fmt.Errorf("wa: open device store: %w", err)
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("wa: load device: %w", err)
	}
	c := &Client{
		log:       log,
		container: container,
		conn:      api.ConnOffline,
		retries:   map[types.MessageID]chan *events.MediaRetry{},
		groups:    map[types.JID]*types.GroupInfo{},
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.cli = c.newWAClient(dev)
	if dev.ID != nil {
		c.auth = api.AuthPaired
	} else {
		c.auth = api.AuthUnpaired
	}
	return c, nil
}

// newWAClient makes a whatsmeow client and its caller. c.mu must be held once
// c is shared.
func (c *Client) newWAClient(dev *store.Device) *whatsmeow.Client {
	cli := whatsmeow.NewClient(dev, newWALog(c.log, "client"))
	c.mcaller = c.newCaller(cli)
	cli.EnableAutoReconnect = true
	cli.InitialAutoReconnect = true
	cli.EmitAppStateEventsOnFullSync = true
	cli.AutomaticMessageRerequestFromPhone = true
	cli.AddEventHandler(func(evt any) {
		if c.client() == cli { // a replaced client's stragglers are dropped
			c.onEvent(cli, evt)
		}
	})
	return cli
}

// client returns the current whatsmeow client.
func (c *Client) client() *whatsmeow.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cli
}

// freshClientLocked replaces a client whose device was deleted (logout) by
// one with a brand new device identity. c.mu must be held. The old client's
// handlers stay: removing them waits for a running one, which may need c.mu.
func (c *Client) freshClientLocked() *whatsmeow.Client {
	if c.cli.Store.Deleted {
		c.cli = c.newWAClient(c.container.NewDevice())
		c.groupsFetched = false
		c.forgetGroup(types.JID{})
	}
	return c.cli
}

// Close disconnects and closes the device store.
func (c *Client) Close() error {
	c.Disconnect()
	c.cancel()
	return c.container.Close()
}

func (c *Client) emit(ev api.WAEvent) {
	if h := c.handler.Load(); h != nil {
		(*h)(ev)
	}
}

func (c *Client) setConn(state string) {
	c.mu.Lock()
	c.conn = state
	c.mu.Unlock()
	c.emit(api.EvConnState{State: state})
}

func (c *Client) setAuth(state string) {
	c.mu.Lock()
	c.auth = state
	c.mu.Unlock()
}

func (c *Client) SetEventHandler(h func(api.WAEvent)) {
	if h == nil {
		c.handler.Store(nil)
		return
	}
	c.handler.Store(&h)
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	cli := c.freshClientLocked()
	if cli.Store.ID == nil {
		if c.auth != api.AuthLoggedOut {
			c.auth = api.AuthUnpaired
		}
		c.conn = api.ConnOffline
		c.mu.Unlock()
		return nil
	}
	c.auth = api.AuthPaired
	c.mu.Unlock()
	if cli.IsConnected() {
		return nil
	}
	c.setConn(api.ConnConnecting)
	// Connect (not ConnectContext): the connection must outlive ctx.
	err := cli.Connect()
	if errors.Is(err, whatsmeow.ErrAlreadyConnected) {
		return nil
	}
	if err != nil {
		c.setConn(api.ConnDisconnected)
		return fmt.Errorf("wa: connect: %w", err)
	}
	return nil
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	cli := c.cli
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	wasOffline := c.conn == api.ConnOffline
	c.conn = api.ConnOffline
	c.mu.Unlock()
	cli.Disconnect()
	if !wasOffline {
		c.emit(api.EvConnState{State: api.ConnOffline})
	}
}

func (c *Client) IsLoggedIn() bool {
	cli := c.client()
	return cli.Store.ID != nil && !cli.Store.Deleted
}

func (c *Client) SelfJID() string {
	cli := c.client()
	if id := cli.Store.ID; id != nil {
		return id.ToNonAD().String()
	}
	return ""
}

func (c *Client) SelfName() string {
	return c.client().Store.PushName
}

func (c *Client) AuthState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.auth
}

func (c *Client) ConnState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn
}

// StartQR starts the QR linking flow: it connects the pre-login websocket
// and emits EvQR for every code. If a flow is already running it re-emits
// the current code. When the codes run out without a scan, AuthState goes
// back to unpaired and EvConnState{offline} is emitted.
func (c *Client) StartQR(ctx context.Context) error {
	return c.startQR(false)
}

// startQR starts a flow; fresh restarts a running one, so a phone code gets
// the pre-login socket's full lifetime.
func (c *Client) startQR(fresh bool) error {
	c.mu.Lock()
	cli := c.freshClientLocked()
	if cli.Store.ID != nil {
		c.mu.Unlock()
		return nil // already paired
	}
	if !fresh && c.qrCancel != nil && cli.IsConnected() {
		last := c.lastQR
		c.mu.Unlock()
		if last.Code != "" {
			c.emit(last)
		}
		return nil
	}
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	if cli.IsConnected() {
		cli.Disconnect()
	}
	qctx, cancel := context.WithCancel(c.ctx)
	ch, err := cli.GetQRChannel(qctx)
	if err != nil {
		c.mu.Unlock()
		cancel()
		return fmt.Errorf("wa: qr channel: %w", err)
	}
	ready := make(chan struct{})
	c.qrCancel, c.qrReady, c.lastQR = cancel, ready, api.EvQR{}
	c.auth = api.AuthPairing
	c.conn = api.ConnConnecting
	c.mu.Unlock()

	go c.qrLoop(qctx, cli, ch, ready)
	if err := cli.Connect(); err != nil {
		cancel()
		return fmt.Errorf("wa: connect for pairing: %w", err)
	}
	return nil
}

func (c *Client) qrLoop(ctx context.Context, cli *whatsmeow.Client, ch <-chan whatsmeow.QRChannelItem, ready chan struct{}) {
	readyClosed := false
	markReady := func() {
		if !readyClosed {
			close(ready)
			readyClosed = true
		}
	}
	defer markReady()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case item, ok := <-ch:
			if !ok {
				break loop
			}
			switch item.Event {
			case whatsmeow.QRChannelEventCode:
				ev := api.EvQR{Code: item.Code, Timeout: int(item.Timeout / time.Second)}
				c.mu.Lock()
				c.lastQR = ev
				c.mu.Unlock()
				markReady()
				c.emit(ev)
			case whatsmeow.QRChannelSuccess.Event:
				c.log.Info("pairing succeeded")
			case whatsmeow.QRChannelTimeout.Event:
				c.log.Info("pairing timed out")
			case whatsmeow.QRChannelEventError:
				c.log.Warn("pairing error", "err", item.Error)
			default:
				c.log.Warn("pairing event", "event", item.Event)
			}
		}
	}
	markReady()
	c.mu.Lock()
	current := c.qrReady == ready // not replaced by a newer flow
	if current {
		c.qrCancel, c.qrReady, c.lastQR = nil, nil, api.EvQR{}
	}
	paired := cli.Store.ID != nil
	changed := false
	if current && !paired && c.auth == api.AuthPairing && c.cli == cli {
		c.auth = api.AuthUnpaired
		c.conn = api.ConnOffline
		changed = true
	}
	c.mu.Unlock()
	if changed {
		c.emit(api.EvConnState{State: api.ConnOffline})
	}
}

// PairPhone starts phone-number linking and returns the 8-character code
// ("ABCD-EFGH") the user types into WhatsApp on the phone.
func (c *Client) PairPhone(ctx context.Context, phone string) (string, error) {
	if err := c.startQR(true); err != nil {
		return "", err
	}
	c.mu.Lock()
	ready, cli := c.qrReady, c.cli
	c.mu.Unlock()
	if ready == nil {
		if cli.Store.ID != nil {
			return "", fmt.Errorf("%w: already paired", api.ErrInvalidParams)
		}
		return "", api.ErrNotConnected
	}
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	select {
	case <-ready:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-t.C:
		return "", fmt.Errorf("wa: timed out waiting for pairing connection: %w", api.ErrNotConnected)
	}
	if !cli.IsConnected() {
		return "", api.ErrNotConnected
	}
	code, err := cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if errors.Is(err, whatsmeow.ErrPhoneNumberTooShort) || errors.Is(err, whatsmeow.ErrPhoneNumberIsNotInternational) {
		return "", api.InvalidParams("%v", err)
	}
	if err != nil {
		return "", fmt.Errorf("wa: pair phone: %w", err)
	}
	return code, nil
}

// Logout unlinks this device and wipes the local keys. The local wipe
// happens even if the server request fails (e.g. offline).
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	cli := c.cli
	if c.qrCancel != nil {
		c.qrCancel()
		c.qrCancel = nil
	}
	c.mu.Unlock()
	if cli.Store.ID != nil && !cli.Store.Deleted {
		if err := cli.Logout(ctx); err != nil {
			c.log.Warn("logout request failed; wiping local session anyway", "err", err)
			cli.Disconnect()
			if derr := cli.Store.Delete(ctx); derr != nil && !errors.Is(derr, store.ErrDeviceDeleted) {
				return fmt.Errorf("wa: delete session: %w", derr)
			}
		}
	} else {
		cli.Disconnect()
	}
	c.mu.Lock()
	if cli.Store.Deleted {
		c.freshClientLocked()
	}
	c.auth = api.AuthUnpaired
	c.conn = api.ConnOffline
	c.mu.Unlock()
	c.emit(api.EvConnState{State: api.ConnOffline})
	return nil
}

func (c *Client) NewMessageID() string {
	return string(c.client().GenerateMessageID())
}

// requireLoggedIn returns the client if a session exists.
func (c *Client) requireLoggedIn() (*whatsmeow.Client, error) {
	cli := c.client()
	if cli.Store.ID == nil || cli.Store.Deleted {
		return nil, api.ErrNotLoggedIn
	}
	return cli, nil
}

// requireConnected returns the client if logged in and the websocket is up.
func (c *Client) requireConnected() (*whatsmeow.Client, error) {
	cli, err := c.requireLoggedIn()
	if err != nil {
		return nil, err
	}
	if !cli.IsConnected() {
		return nil, api.ErrNotConnected
	}
	return cli, nil
}

func parseJID(s string) (types.JID, error) {
	if s == "" {
		return types.JID{}, api.InvalidParams("empty jid")
	}
	j, err := types.ParseJID(s)
	if err != nil {
		return types.JID{}, api.InvalidParams("bad jid %q: %v", s, err)
	}
	return j, nil
}
