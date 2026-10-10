package tracking

import (
	"context"
	"database/sql"
	"postbox/internal/subscribers"
)

func skipAfterUnsubscribe(ctx context.Context, c *sql.Conn, subscriber, list int64) error {
	return subscribers.SkipAfterUnsubscribeTx(ctx, c, subscriber, list)
}
func stillInAudience(ctx context.Context, c *sql.Conn, campaign, subscriber int64) (bool, error) {
	return subscribers.StillInAudienceTx(ctx, c, campaign, subscriber)
}
