package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// verifyCampaignTrackingIdentity validates the complete known expanded 011
// before recognizing 012 as already applied. A checksum alone is not enough to
// skip a forward migration if its column or safeguards are missing.
func verifyCampaignTrackingIdentity(ctx context.Context, conn *sql.Conn) error {
	var columnType, defaultValue string
	var notNull int
	if err := conn.QueryRowContext(ctx, `SELECT type,"notnull",dflt_value FROM pragma_table_info('campaigns') WHERE name='tracking_identity'`).Scan(&columnType, &notNull, &defaultValue); err != nil {
		return err
	}
	if columnType != "TEXT" || notNull != 1 || defaultValue != "''" {
		return errors.New("campaign tracking identity column differs")
	}
	var tableSQL string
	if err := conn.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name='campaigns'`).Scan(&tableSQL); err != nil {
		return err
	}
	if !strings.Contains(normalizedSchemaSQL(tableSQL), "CHECK(length(tracking_identity) IN (0,32))") {
		return errors.New("campaign tracking identity constraint missing")
	}
	source, err := migrations.ReadFile("migrations/012_campaign_tracking_identity.sql")
	if err != nil {
		return err
	}
	for _, object := range []struct{ name, prefix, terminator string }{
		{"campaigns_tracking_identity", "CREATE UNIQUE INDEX campaigns_tracking_identity", ";"},
		{"campaigns_tracking_identity_insert", "CREATE TRIGGER campaigns_tracking_identity_insert", "END;"},
		{"campaigns_tracking_identity_immutable", "CREATE TRIGGER campaigns_tracking_identity_immutable", "END;"},
	} {
		start := strings.Index(string(source), object.prefix)
		if start < 0 {
			return fmt.Errorf("canonical schema missing %s", object.name)
		}
		tail := string(source[start:])
		end := strings.Index(tail, object.terminator)
		if end < 0 {
			return fmt.Errorf("canonical schema incomplete for %s", object.name)
		}
		expected := tail[:end+len(object.terminator)]
		var actual string
		if err := conn.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name=? AND type IN ('index','trigger')`, object.name).Scan(&actual); err != nil {
			return fmt.Errorf("%s: %w", object.name, err)
		}
		if normalizedSchemaSQL(actual) != normalizedSchemaSQL(expected) {
			return fmt.Errorf("%s differs from known expanded migration", object.name)
		}
	}
	var incomplete int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM campaigns WHERE length(tracking_identity)!=32`).Scan(&incomplete); err != nil {
		return err
	}
	if incomplete != 0 {
		return errors.New("campaign tracking identities are incomplete")
	}
	return nil
}

func normalizedSchemaSQL(value string) string {
	return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(value), ";")), " ")
}
