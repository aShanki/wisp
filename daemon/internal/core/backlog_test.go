package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

// countingEmitter marshals like the RPC server and counts, never blocking.
type countingEmitter struct{ n atomic.Int64 }

func (e *countingEmitter) Emit(name string, params any) {
	if _, err := json.Marshal(params); err != nil {
		panic(err)
	}
	e.n.Add(1)
}

// TestBacklogThroughput times how long the daemon takes to absorb what a
// long sleep leaves queued: an existing 100k-message store, then ~1000 new
// messages across 20 chats plus ~1300 receipts, fed through the WA event
// handler as fast as whatsmeow could deliver them. WISP_BACKLOG=1 to run.
func TestBacklogThroughput(t *testing.T) {
	if os.Getenv("WISP_BACKLOG") == "" {
		t.Skip("WISP_BACKLOG=1 to run")
	}
	h := newHarness(t)
	em := &countingEmitter{}
	h.c.SetEmitter(em)
	ctx := context.Background()

	const chats, existing, backlog, receipts = 20, 100_000, 1000, 1300
	jid := func(i int) string { return fmt.Sprintf("1555000%04d@s.whatsapp.net", i%chats) }
	start := time.Now()
	base := time.Now().Add(-30 * 24 * time.Hour).Unix()
	for b := 0; b < existing/1000; b++ {
		hist := &api.EvHistory{}
		for i := range 1000 {
			n := b*1000 + i
			m := incoming(jid(n), fmt.Sprintf("OLD%07d", n), jid(n), "old message text that is about this long")
			m.Timestamp = base + int64(n)
			hist.Messages = append(hist.Messages, m)
		}
		if err := h.st.PutHistory(ctx, hist); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("seeded %d messages in %v", existing, time.Since(start).Round(time.Millisecond))

	before := em.n.Load()
	now := time.Now().Unix()
	start = time.Now()
	for i := range backlog {
		m := incoming(jid(i), fmt.Sprintf("NEW%07d", i), jid(i), "a message that came in while the laptop slept")
		m.Timestamp = now - int64(backlog-i)
		h.wa.handler(api.EvMessage{Msg: m, Live: false})
		if i < receipts {
			h.wa.handler(api.EvReceipt{Chat: jid(i), IDs: []string{fmt.Sprintf("OLD%07d", existing-1-i)}, Status: api.StatusRead})
		}
	}
	for i := backlog; i < receipts; i++ {
		h.wa.handler(api.EvReceipt{Chat: jid(i), IDs: []string{fmt.Sprintf("OLD%07d", existing-1-i)}, Status: api.StatusRead})
	}
	fed := time.Since(start)
	// Drain: the last message is stored once the loop has handled it.
	last := fmt.Sprintf("NEW%07d", backlog-1)
	for {
		if _, err := h.st.GetMessage(ctx, jid(backlog-1), last); err == nil && len(h.c.events) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	h.c.writeMu.Lock() // wait for the event in hand
	h.c.writeMu.Unlock()
	took := time.Since(start)
	t.Logf("backlog: %d messages + %d receipts fed in %v, absorbed in %v (%.2f ms/event), %d UI events",
		backlog, receipts, fed.Round(time.Millisecond), took.Round(time.Millisecond),
		float64(took.Microseconds())/1000/float64(backlog+receipts), em.n.Load()-before)
}
