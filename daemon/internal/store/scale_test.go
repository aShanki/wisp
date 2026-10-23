package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wisp/daemon/internal/api"
)

// scaleAccount is a synthetic account the size of a heavy real one: chat
// sizes fall off like 1/rank, a quarter are groups, a fifth of messages
// carry media with a thumbnail.
type scaleAccount struct {
	chats, msgs int
	sizes       []int
	now         int64
}

func newScaleAccount(chats, msgs int) *scaleAccount {
	a := &scaleAccount{chats: chats, msgs: msgs, now: 1_760_000_000}
	var h float64
	for i := range chats {
		h += 1 / float64(i+1)
	}
	for i := range chats {
		a.sizes = append(a.sizes, max(1, int(float64(msgs)/(h*float64(i+1)))))
	}
	return a
}

func (a *scaleAccount) jid(i int) string {
	if i%4 == 3 {
		return fmt.Sprintf("1203630%011d@g.us", i)
	}
	return fmt.Sprintf("614%08d@s.whatsapp.net", i)
}

func (a *scaleAccount) unread(i int) int {
	if i%7 == 0 {
		return i%5 + 1
	}
	return 0
}

var scaleWords = strings.Fields(`the and you see ok yes no maybe tomorrow tonight dinner climbing work meeting
	coffee photo train late early weekend beach birthday cake call later thanks love great idea plan
	running gym movie music party travel airport hotel flight booked ticket price cheap rain sunny`)

// message k (0 = oldest) of chat i.
func (a *scaleAccount) message(r *rand.Rand, i, k int) *api.Message {
	chat := a.jid(i)
	ts := a.now - int64(i)*600 - int64(a.sizes[i]-k)*90
	m := &api.Message{ID: fmt.Sprintf("3EB0%04X%06X", i, k), Chat: chat, Sender: chat, Timestamp: ts, Type: api.MsgText}
	if kindOf(chat) == api.ChatGroup {
		m.Sender = fmt.Sprintf("6149%07d@s.whatsapp.net", (i*7+r.IntN(20))%1500)
	}
	var words []string
	for range 3 + r.IntN(15) {
		words = append(words, scaleWords[r.IntN(len(scaleWords))])
	}
	m.Text = strings.Join(words, " ")
	if k%997 == 0 {
		m.Text += " zanzibar" // rare search term
	}
	switch n := r.IntN(100); {
	case n < 30:
		m.Sender, m.FromMe, m.Status = me, true, api.StatusRead
	case n < 32:
		m.Type, m.Text = api.MsgSystem, "Missed voice call"
	}
	if r.IntN(5) == 0 && m.Type == api.MsgText {
		m.Type = api.MsgImage
		if r.IntN(2) == 0 {
			m.Text = ""
		}
		m.Media = &api.MediaInfo{Mime: "image/jpeg", Width: 1280, Height: 960, HasThumb: true}
		m.MediaRef = &api.MediaRef{Chat: chat, ID: m.ID, Type: api.MsgImage, Mime: "image/jpeg",
			DirectPath: "/v/t62.7118-24/" + strconv.Itoa(r.Int()) + "_n.enc?ccb=11-4&oh=01_Q5Aa&oe=68F",
			MediaKey:   randBytes(r, 32), FileSHA256: randBytes(r, 32), FileEncSHA256: randBytes(r, 32), FileLength: 180_000}
		m.Thumb = randBytes(r, 1500+r.IntN(3000))
	}
	if k > 0 && r.IntN(12) == 0 {
		m.ReplyTo = &api.Quote{ID: fmt.Sprintf("3EB0%04X%06X", i, r.IntN(k)), Sender: chat, Type: api.MsgText, Preview: "earlier"}
	}
	return m
}

func randBytes(r *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

// batches streams the history like WhatsApp sends it: first the newest 20
// messages of every chat, then older ones, about 2,000 messages per batch.
// Every batch carries the metadata of its chats and the push names of its
// senders. Only one batch is in memory at a time.
func (a *scaleAccount) batches(yield func(*api.EvHistory) bool) {
	r := rand.New(rand.NewPCG(1, 2))
	const perBatch, recent = 2000, 20
	h := &api.EvHistory{}
	inBatch := map[int]bool{}
	people := map[string]bool{}
	sent, total := 0, 0
	for _, n := range a.sizes {
		total += n
	}
	flush := func(final bool) bool {
		sent += len(h.Messages)
		h.Percent, h.Final = min(100, sent*100/max(total, 1)), final
		ok := yield(h)
		h, inBatch, people = &api.EvHistory{}, map[int]bool{}, map[string]bool{}
		return ok
	}
	add := func(i, from, to int) bool {
		if !inBatch[i] {
			inBatch[i] = true
			kind := kindOf(a.jid(i))
			p := api.ChatPatch{JID: a.jid(i), Kind: &kind, Unread: ptr(a.unread(i)), MarkedUnread: ptr(false),
				MutedUntil: ptr(int64(0)), Pinned: ptr(int64(b2i(i < 3))), Archived: ptr(i%50 == 49), ReadOnly: ptr(false),
				LastActivity: ptr(a.now - int64(i)*600)}
			if kind == api.ChatGroup {
				p.Name = ptr(fmt.Sprintf("Group %d", i))
			}
			h.Chats = append(h.Chats, p)
		}
		for k := from; k < to; k++ {
			m := a.message(r, i, k)
			h.Messages = append(h.Messages, m)
			if !m.FromMe && !people[m.Sender] {
				people[m.Sender] = true
				h.Contacts = append(h.Contacts, api.Contact{JID: m.Sender, PushName: "Person " + m.Sender[4:9]})
			}
			if r.IntN(20) == 0 {
				h.Reactions = append(h.Reactions, api.EvReaction{Chat: m.Chat, ID: m.ID, Sender: a.jid((i + 1) % a.chats), Emoji: "👍", Ts: m.Timestamp + 60})
			}
		}
		if len(h.Messages) >= perBatch {
			return flush(false)
		}
		return true
	}
	for i := range a.chats {
		if !add(i, max(0, a.sizes[i]-recent), a.sizes[i]) {
			return
		}
	}
	for i := range a.chats {
		for to := a.sizes[i] - recent; to > 0; to -= perBatch {
			if !add(i, max(0, to-perBatch), to) {
				return
			}
		}
	}
	flush(true)
}

// peakMem samples the Go heap and the process RSS until stopped.
type peakMem struct {
	heap, rss atomic.Uint64
	stop      chan struct{}
	wg        sync.WaitGroup
}

func startPeakMem() *peakMem {
	p := &peakMem{stop: make(chan struct{})}
	p.wg.Go(func() {
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			p.heap.Store(max(p.heap.Load(), ms.HeapInuse))
			p.rss.Store(max(p.rss.Load(), rssBytes()))
			select {
			case <-t.C:
			case <-p.stop:
				return
			}
		}
	})
	return p
}

func (p *peakMem) done() { close(p.stop); p.wg.Wait() }

func rssBytes() uint64 {
	data, _ := os.ReadFile("/proc/self/statm")
	f := strings.Fields(string(data))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.ParseUint(f[1], 10, 64)
	return pages * uint64(os.Getpagesize())
}

func fileSize(paths ...string) int64 {
	var n int64
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil {
			n += st.Size()
		}
	}
	return n
}

func timeIt(b *testing.B, name string, n int, f func()) {
	start := time.Now()
	for range n {
		f()
	}
	d := time.Since(start) / time.Duration(n)
	b.ReportMetric(float64(d.Microseconds())/1000, name+"-ms")
}

// BenchmarkHistoryScale ingests a heavy account (2,000 chats, 200,000
// messages) batch by batch and measures what the UI then asks for:
//
//	go test -run '^$' -bench HistoryScale -benchtime 1x ./internal/store
func BenchmarkHistoryScale(b *testing.B) {
	ctx := context.Background()
	acct := newScaleAccount(2000, 200_000)
	for b.Loop() {
		path := filepath.Join(b.TempDir(), "wisp.db")
		s, err := Open(ctx, path)
		if err != nil {
			b.Fatal(err)
		}
		runtime.GC()
		base := rssBytes()
		mem := startPeakMem()
		start := time.Now()
		var nBatches, nMsgs int
		for h := range acct.batches {
			if err := s.PutHistory(ctx, h); err != nil {
				b.Fatal(err)
			}
			nBatches++
			nMsgs += len(h.Messages)
		}
		ingest := time.Since(start)
		mem.done()
		b.ReportMetric(ingest.Seconds(), "ingest-s")
		b.ReportMetric(float64(nBatches), "batches")
		b.ReportMetric(float64(nMsgs), "messages")
		b.ReportMetric(float64(mem.heap.Load())/(1<<20), "peak-heap-MB")
		b.ReportMetric(float64(mem.rss.Load()-min(base, mem.rss.Load()))/(1<<20), "peak-rss-growth-MB")

		// Unread counts and read markers come from the chat metadata.
		for _, i := range []int{0, 7, 14, 35, 1995, 1, 2} {
			c := mustChat(b, s, acct.jid(i))
			keys, err := s.UnreadIncoming(ctx, acct.jid(i), 500)
			if err != nil || c.Unread != acct.unread(i) || len(keys) != acct.unread(i) {
				b.Errorf("chat %d: unread %d, unread incoming %d, want %d (%v)", i, c.Unread, len(keys), acct.unread(i), err)
			}
		}

		timeIt(b, "chats-first-page", 20, func() {
			res, err := s.ListChats(ctx, api.ChatsListParams{Limit: 100})
			if err != nil || len(res.Chats) != 100 {
				b.Fatal(len(res.Chats), err)
			}
		})
		timeIt(b, "chats-all-pages", 5, func() {
			n, cursor := 0, ""
			for {
				res, err := s.ListChats(ctx, api.ChatsListParams{Limit: 100, Cursor: cursor})
				if err != nil {
					b.Fatal(err)
				}
				n += len(res.Chats)
				if cursor = res.NextCursor; cursor == "" {
					break
				}
			}
			if n != acct.chats-acct.chats/50 {
				b.Fatalf("listed %d chats", n)
			}
		})
		big, mid := acct.jid(0), fmt.Sprintf("3EB0%04X%06X", 0, acct.sizes[0]/2)
		timeIt(b, "big-chat-newest", 20, func() {
			if page, more, err := s.ListMessages(ctx, big, "", 50); err != nil || len(page) != 50 || !more {
				b.Fatal(len(page), more, err)
			}
		})
		timeIt(b, "big-chat-middle", 20, func() {
			if page, _, err := s.ListMessages(ctx, big, mid, 50); err != nil || len(page) != 50 {
				b.Fatal(len(page), err)
			}
		})
		for _, q := range []struct{ name, query, chat string }{
			{"search-common", "dinner", ""},
			{"search-rare", "zanzibar", ""},
			{"search-prefix", "clim", ""},
			{"search-big-chat", "coffee", big},
		} {
			timeIt(b, q.name, 5, func() {
				if res, err := s.Search(ctx, api.SearchParams{Query: q.query, Chat: q.chat}); err != nil || len(res) == 0 {
					b.Fatal(len(res), err)
				}
			})
		}
		if err := s.Close(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(fileSize(path, path+"-wal"))/(1<<20), "db-MB")
	}
}
