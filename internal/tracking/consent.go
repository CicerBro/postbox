package tracking

import (
	"context"
	"database/sql"
	"postbox/internal/contacts"
)

func skipAfterUnsubscribe(ctx context.Context, c *sql.Conn, subscriber, list int64) error {
	return contacts.SkipAfterUnsubscribeTx(ctx, c, subscriber, list)
}
func stillInAudience(ctx context.Context, c *sql.Conn, campaign, subscriber int64) (bool, error) {
	return contacts.StillInAudienceTx(ctx, c, campaign, subscriber)
}
