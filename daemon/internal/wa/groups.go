package wa

import (
	"context"
	"errors"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"wisp/daemon/internal/api"
)

// groupInfo returns a group's metadata, cached until the group changes.
func (c *Client) groupInfo(ctx context.Context, cli *whatsmeow.Client, jid types.JID) (*types.GroupInfo, error) {
	c.groupMu.Lock()
	g, gen := c.groups[jid], c.groupGen
	c.groupMu.Unlock()
	if g != nil {
		return g, nil
	}
	if !cli.IsConnected() {
		return nil, api.ErrNotConnected
	}
	g, err := cli.GetGroupInfo(ctx, jid)
	if errors.Is(err, whatsmeow.ErrGroupNotFound) || errors.Is(err, whatsmeow.ErrNotInGroup) {
		return nil, fmt.Errorf("group %s: %w", jid, api.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("wa: group info: %w", err)
	}
	c.groupMu.Lock()
	if c.groupGen == gen { // no change seen while fetching
		c.groups[jid] = g
	}
	c.groupMu.Unlock()
	return g, nil
}

// forgetGroup drops a group's cached metadata, or every group's if jid is empty.
func (c *Client) forgetGroup(jid types.JID) {
	c.groupMu.Lock()
	defer c.groupMu.Unlock()
	c.groupGen++
	if jid.IsEmpty() {
		clear(c.groups)
	} else {
		delete(c.groups, jid.ToNonAD())
	}
}

func (c *Client) GroupMembers(ctx context.Context, chat string) ([]api.GroupMember, error) {
	cli, err := c.requireLoggedIn()
	if err != nil {
		return nil, err
	}
	jid, err := parseJID(chat)
	if err != nil {
		return nil, err
	}
	if jid.Server != types.GroupServer {
		return nil, api.InvalidParams("%s is not a group", chat)
	}
	g, err := c.groupInfo(ctx, cli, jid.ToNonAD())
	if err != nil {
		return nil, err
	}
	return c.converterFor(cli).members(g), nil
}

// members lists a group's participants other than us, by phone JID where known.
func (cv *converter) members(g *types.GroupInfo) []api.GroupMember {
	out := make([]api.GroupMember, 0, len(g.Participants))
	for _, pt := range g.Participants {
		if cv.isSelfParticipant(pt) {
			continue
		}
		j := pt.JID
		if j.Server == types.HiddenUserServer && pt.PhoneNumber.Server == types.DefaultUserServer {
			j = pt.PhoneNumber
		}
		out = append(out, api.GroupMember{JID: cv.canon(j).String(), Admin: pt.IsAdmin || pt.IsSuperAdmin})
	}
	return out
}

func (cv *converter) isSelfParticipant(pt types.GroupParticipant) bool {
	return cv.isSelf(pt.JID) || cv.isSelf(pt.PhoneNumber) || cv.isSelf(pt.LID)
}
