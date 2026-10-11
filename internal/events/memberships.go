package events

import (
	"context"
	"database/sql"
	"postbox/internal/consent"
)

func UnsubscribeMembershipsTx(ctx context.Context, c *sql.Conn, subscriber, list, at int64, reason string) error {
	_, err := consent.UnsubscribeMembershipsTx(ctx, c, subscriber, list, at, reason)
	return err
}
