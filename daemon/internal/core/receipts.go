package core

import (
	"context"
	"wisp/daemon/internal/api"
)

func (c *Core) MessagesInfo(ctx context.Context, p api.MessageRefParams) (*api.MessageInfoResult, error) {
	rs, err := c.store.ListReceipts(ctx, p.Chat, p.ID)
	if err != nil {
		return nil, err
	}
	for i := range rs {
		r := &rs[i]
		if r.Name == "" && c.wa.IsLoggedIn() {
			if ct, e := c.wa.ResolveContact(ctx, r.JID); e == nil && ct != nil {
				r.Name = ct.Name
			}
		}
		if r.Name == "" {
			r.Name = jidUser(r.JID)
		}
	}
	return &api.MessageInfoResult{Receipts: rs}, nil
}
