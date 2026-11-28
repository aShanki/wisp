package wa

import (
	"context"
	"os"
	"strconv"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types/events"
)

// offlineStats measures how long the server takes to replay what arrived
// while we were away (the "catching up" after a long sleep).
type offlineStats struct {
	mu        sync.Mutex
	connected time.Time // last events.Connected
	start     time.Time // offline_preview arrived
	msgs      int       // messages handled since
	cancel    context.CancelFunc
}

func (c *Client) offlineConnected() {
	c.off.mu.Lock()
	c.off.connected = time.Now()
	c.off.mu.Unlock()
}

func (c *Client) offlinePreview(cli *whatsmeow.Client, v *events.OfflineSyncPreview) {
	c.off.mu.Lock()
	defer c.off.mu.Unlock()
	c.off.start, c.off.msgs = time.Now(), 0
	c.log.Info("offline sync started", "total", v.Total, "messages", v.Messages,
		"receipts", v.Receipts, "notifications", v.Notifications, "appdata", v.AppDataChanges,
		"sinceConnect", time.Since(c.off.connected).Round(time.Millisecond))
	if c.off.cancel != nil {
		c.off.cancel()
		c.off.cancel = nil
	}
	if n := offlineBatchSize(); n > 0 {
		ctx, cancel := context.WithCancel(c.ctx)
		c.off.cancel = cancel
		go c.requestOfflineBatches(ctx, cli, n)
	}
}

func (c *Client) offlineMessage() {
	if !c.offlineSync.Load() {
		return
	}
	c.off.mu.Lock()
	c.off.msgs++
	c.off.mu.Unlock()
}

func (c *Client) offlineDone(v *events.OfflineSyncCompleted) {
	c.off.mu.Lock()
	defer c.off.mu.Unlock()
	if c.off.cancel != nil {
		c.off.cancel()
		c.off.cancel = nil
	}
	var took time.Duration
	if !c.off.start.IsZero() {
		took = time.Since(c.off.start)
	}
	c.log.Info("offline sync done", "count", v.Count, "messages", c.off.msgs,
		"took", took.Round(time.Millisecond), "sinceConnect", time.Since(c.off.connected).Round(time.Millisecond))
	c.off.start = time.Time{}
}

// offlineBatchSize is WISP_OFFLINE_BATCH (experimental; 0 = off).
func offlineBatchSize() int {
	n, _ := strconv.Atoi(os.Getenv("WISP_OFFLINE_BATCH"))
	return n
}

// requestOfflineBatches asks the server for the next n queued stanzas, the
// way WhatsApp Web does (<ib><offline_batch count=n/></ib>), every second
// until the offline sync completes.
func (c *Client) requestOfflineBatches(ctx context.Context, cli *whatsmeow.Client, n int) {
	node := waBinary.Node{Tag: "ib", Content: []waBinary.Node{{
		Tag: "offline_batch", Attrs: waBinary.Attrs{"count": strconv.Itoa(n)},
	}}}
	for i := 0; i < 600; i++ {
		if err := cli.DangerousInternals().SendNode(ctx, node); err != nil {
			c.log.Warn("offline_batch request failed", "err", err)
		} else if i < 3 || i%10 == 0 {
			c.log.Info("offline_batch requested", "count", n, "request", i+1)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
