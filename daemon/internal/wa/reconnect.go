package wa

import (
	"errors"
	"time"

	"go.mau.fi/whatsmeow"

	"wisp/daemon/internal/api"
)

// Wisp reconnects itself instead of using whatsmeow's auto-reconnect, which
// waits 3 minutes of failed keepalives before dropping a dead socket and then
// backs off linearly without bound (2s, 4s, 6s ... 50s seen after a
// suspend). After a suspend the socket is always dead and the network comes
// back within seconds, so: reconnect the moment the system resumes, retry
// every second for a while, and give up on a silent socket after
// keepAliveMaxFail.

// keepAliveMaxFail is how long keepalives may fail before the socket is
// dropped and redialled. whatsmeow pings every 20-30s with a 10s deadline,
// so this reconnects after the second failed ping.
const keepAliveMaxFail = 45 * time.Second

// redialDelay is the wait before reconnect attempt n (0-based): immediate,
// then every second for 10s (Wi-Fi rejoining after a resume), then backing
// off to every 30s.
func redialDelay(n int) time.Duration {
	slow := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}
	switch {
	case n == 0:
		return 0
	case n <= 10:
		return time.Second
	case n-11 < len(slow):
		return slow[n-11]
	}
	return 30 * time.Second
}

// redialTarget returns the client to reconnect, or nil if reconnecting
// isn't wanted: no session, or offline on purpose (Disconnect, replaced
// stream, ban...).
func (c *Client) redialTarget() *whatsmeow.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	cli := c.cli
	if cli.Store.ID == nil || cli.Store.Deleted || c.auth != api.AuthPaired || c.conn == api.ConnOffline {
		return nil
	}
	return cli
}

// startRedial reconnects in the background until connected. If a redial is
// already running it retries now, with the backoff reset.
func (c *Client) startRedial() {
	c.mu.Lock()
	if c.redialKick != nil {
		select {
		case c.redialKick <- struct{}{}:
		default:
		}
		c.mu.Unlock()
		return
	}
	kick := make(chan struct{}, 1)
	c.redialKick = kick
	c.mu.Unlock()
	go c.redial(kick)
}

func (c *Client) redial(kick chan struct{}) {
	defer func() {
		c.mu.Lock()
		if c.redialKick == kick {
			c.redialKick = nil
		}
		c.mu.Unlock()
	}()
	for n := 0; ; n++ {
		if d := redialDelay(n); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-t.C:
			case <-kick:
				t.Stop()
				n = 0
			case <-c.ctx.Done():
				t.Stop()
				return
			}
		}
		cli := c.redialTarget()
		if cli == nil || cli.IsConnected() {
			return
		}
		err := cli.Connect()
		if err == nil || errors.Is(err, whatsmeow.ErrAlreadyConnected) {
			if c.redialTarget() != cli { // Disconnect raced the dial
				cli.Disconnect()
			}
			return
		}
		if n == 0 || n%10 == 0 {
			c.log.Warn("reconnect failed, retrying", "attempt", n+1, "err", err)
		}
	}
}

// resumed runs when the system wakes from sleep. The old socket died while
// asleep, but whatsmeow wouldn't notice for minutes: drop it and redial now.
func (c *Client) resumed() {
	cli := c.redialTarget()
	if cli == nil {
		return
	}
	c.log.Info("resumed from sleep, reconnecting")
	if cli.IsConnected() {
		// Emits events.Disconnected, which starts a redial.
		cli.ResetConnection()
		return
	}
	c.startRedial()
}
