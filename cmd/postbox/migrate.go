package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"postbox/internal/migrate"
	"postbox/internal/store"
	"strconv"
)

func migrateSendy(ctx context.Context, db *store.DB) error {
	source := os.Getenv("POSTBOX_SENDY_SOURCE_ID")
	if source == "" {
		return errors.New("set a stable POSTBOX_SENDY_SOURCE_ID for idempotent Sendy migration")
	}
	dry := false
	if value := os.Getenv("POSTBOX_SENDY_DRY_RUN"); value != "" {
		var err error
		dry, err = strconv.ParseBool(value)
		if err != nil {
			return err
		}
	}
	report, err := migrate.Run(ctx, db, migrate.Options{DSN: os.Getenv("POSTBOX_SENDY_DSN"), DumpPath: os.Getenv("POSTBOX_SENDY_DUMP"), SourceID: source, DryRun: dry})
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}
