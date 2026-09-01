package whatsapp

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/piwi3910/whatsapp-go/internal/jid"
)

// SyncHistory requests `count` past messages immediately before the oldest
// locally-stored message of chatJID, from the user's primary device
// (issue #21). The request is a whatsmeow peer data operation: the primary
// device replies with a history sync, and the returned messages flow through
// the regular receive path, where they are stored and dispatched like any
// other inbound message — so `wa message list` and event consumers see the
// backfill with no separate plumbing.
//
// The sync walks backwards from a known message; a chat with no locally
// stored messages has nothing to anchor the request to, and that is
// reported as an error rather than silently doing nothing.
//
// The returned count is how many new messages landed in the store during
// the wait window. It can be 0 with a nil error: the request went out, but
// nothing arrived in time (the primary device is usually the reason).
func (c *Client) SyncHistory(ctx context.Context, chatJID string, count int) (int, error) {
	if count <= 0 {
		count = 50 // whatsmeow's recommended on-demand batch size
	}

	chat, err := jid.NormalizeJID(chatJID)
	if err != nil {
		return 0, fmt.Errorf("normalizing chat JID: %w", err)
	}

	anchor, err := c.store.GetOldest(chat)
	if err != nil {
		return 0, fmt.Errorf("looking up oldest stored message: %w", err)
	}
	if anchor == nil {
		return 0, fmt.Errorf("no locally stored messages for %s: history requests walk backwards from a known message, so there is nothing to anchor from (exchange a few messages in the chat first)", chat)
	}

	before, err := c.store.CountMessages(chat)
	if err != nil {
		return 0, fmt.Errorf("counting stored messages: %w", err)
	}

	waChat, err := types.ParseJID(chat)
	if err != nil {
		return 0, fmt.Errorf("parsing chat JID: %w", err)
	}
	req := c.wac.BuildHistorySyncRequest(&types.MessageInfo{
		MessageSource: types.MessageSource{Chat: waChat, IsFromMe: anchor.IsFromMe},
		ID:            types.MessageID(anchor.WaID),
		Timestamp:     time.Unix(anchor.Timestamp, 0),
	}, count)
	if _, err := c.wac.SendPeerMessage(ctx, req); err != nil {
		return 0, fmt.Errorf("sending history sync request: %w", err)
	}

	// Delivery is asynchronous: poll the store until messages land or the
	// bounded wait window closes.
	const waitWindow = 60 * time.Second
	deadline := time.Now().Add(waitWindow)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		after, err := c.store.CountMessages(chat)
		if err != nil {
			return 0, fmt.Errorf("counting stored messages: %w", err)
		}
		if imported := after - before; imported > 0 {
			return imported, nil
		}
		time.Sleep(2 * time.Second)
	}
	return 0, nil
}
