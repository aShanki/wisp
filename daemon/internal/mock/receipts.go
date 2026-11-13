package mock

import (
	"context"
	"fmt"
	"wisp/daemon/internal/api"
)

func (b *Backend) MessagesInfo(ctx context.Context, p api.MessageRefParams) (*api.MessageInfoResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, err := b.chat(p.Chat)
	if err != nil {
		return nil, err
	}
	at, ok := c.pos(p.ID)
	if !ok || c.gone[p.ID] || !c.at(at).FromMe {
		return nil, fmt.Errorf("message %s: %w", p.ID, api.ErrNotFound)
	}
	out := []api.MessageReceipt{}
	m := c.at(at)
	if c.spec.isGroup() && api.StatusRank(m.Status) >= api.StatusRank(api.StatusDelivered) {
		for i, member := range c.spec.members {
			if i >= 3 {
				break
			}
			jid := personJID(member)
			r := api.MessageReceipt{JID: jid, Name: nameOf(jid), Delivered: true, DeliveredAt: m.Timestamp + 60}
			if i == 0 && m.Status == api.StatusRead {
				r.Read = true
				r.ReadAt = m.Timestamp + 120
			}
			out = append(out, r)
		}
	}
	return &api.MessageInfoResult{Receipts: out}, nil
}
