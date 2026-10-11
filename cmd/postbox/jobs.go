package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"postbox/internal/config"
	"postbox/internal/events"
	"postbox/internal/jobs"
	"postbox/internal/render"
	"postbox/internal/send"
	"postbox/internal/store"
	"postbox/internal/subscribers"
)

type transactionalEmail struct {
	Purpose           string `json:"purpose"`
	SubscriberID      int64  `json:"subscriber_id"`
	ListID            int64  `json:"list_id"`
	ConfirmationToken string `json:"confirmation_token"`
	BrandID           int64  `json:"brand_id"`
	To                string `json:"to"`
	Subject           string `json:"subject"`
	HTML              string `json:"html"`
	Text              string `json:"text"`
}

func registerJobs(db *store.DB, runner *jobs.Runner, engine *send.Engine, c config.Config) error {
	deliver := func(ctx context.Context, p transactionalEmail) error {
		if err := validateTransactional(ctx, db, p); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return jobs.RetrySafe(err)
		}
		var name, from, reply, configurationSet string
		if err := db.Reader.QueryRowContext(ctx, "SELECT from_name,from_email,coalesce(reply_to,''),coalesce(configuration_set,'') FROM brands WHERE id=?", p.BrandID).Scan(&name, &from, &reply, &configurationSet); err != nil {
			return jobs.RetrySafe(err)
		}
		raw, err := render.BuildTransactionalMIME(render.Message{FromName: name, FromEmail: from, ReplyTo: reply, ToEmail: p.To, Subject: p.Subject, HTML: p.HTML, Text: p.Text, Date: time.Now()})
		if err != nil {
			return err
		}
		_, err = engine.SendNotificationChecked(ctx, send.Request{From: from, To: p.To, Raw: raw, ConfigurationSet: configurationSet, BrandID: p.BrandID}, func(ctx context.Context) error { return validateTransactional(ctx, db, p) })
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return mailJobError(err)
	}
	if err := runner.Register("transactional_email", func(ctx context.Context, j jobs.Job) error {
		var p transactionalEmail
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return err
		}
		return deliver(ctx, p)
	}); err != nil {
		return err
	}
	if os.Getenv("POSTBOX_NOTIFICATION_TO") != "" {
		if os.Getenv("POSTBOX_NOTIFICATION_FROM") == "" {
			return errors.New("POSTBOX_NOTIFICATION_FROM is required when notifications are enabled")
		}
		if err := runner.RegisterNotifications(func(ctx context.Context, j jobs.Job) error {
			var event jobs.Event
			if err := json.Unmarshal(j.Payload, &event); err != nil {
				return err
			}
			data, err := json.MarshalIndent(event.Data, "", "  ")
			if err != nil {
				return err
			}
			sentAny := false
			for _, to := range strings.Split(os.Getenv("POSTBOX_NOTIFICATION_TO"), ",") {
				to, err = render.NormalizeEmail(to)
				if err != nil {
					return err
				}
				raw, err := render.BuildTransactionalMIME(render.Message{FromEmail: os.Getenv("POSTBOX_NOTIFICATION_FROM"), ToEmail: to, Subject: "Postbox: " + event.Type, HTML: "<p>" + event.Type + "</p>", Text: event.Type + "\n" + string(data), Date: time.Now()})
				if err != nil {
					return err
				}
				if _, err = engine.SendNotification(ctx, send.Request{From: os.Getenv("POSTBOX_NOTIFICATION_FROM"), To: to, Raw: raw, BrandID: event.BrandID}); err != nil {
					if !sentAny {
						return mailJobError(err)
					}
					return err
				}
				sentAny = true
			}
			return nil
		}); err != nil {
			return err
		}
	}
	root := filepath.Join(filepath.Dir(c.DB), "jobs")
	return runner.RegisterCSV(func(ctx context.Context, j jobs.Job) error {
		var p struct {
			InputFile string                    `json:"input_file"`
			Options   subscribers.ImportOptions `json:"options"`
		}
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return err
		}
		path, err := jobPath(root, p.InputFile)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = subscribers.New(db).ImportCSV(ctx, f, p.Options)
		return err
	}, func(ctx context.Context, j jobs.Job) error {
		var p struct {
			OutputFile string `json:"output_file"`
			BrandID    int64  `json:"brand_id"`
			ListID     int64  `json:"list_id"`
		}
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return err
		}
		path, err := jobPath(root, p.OutputFile)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(root, 0700); err != nil {
			return err
		}
		stage := filepath.Join(root, fmt.Sprintf(".%s.job-%d", p.OutputFile, j.ID))
		if finalInfo, e := os.Lstat(path); e == nil {
			stageInfo, e := os.Lstat(stage)
			if e == nil && stageInfo.Mode().IsRegular() && os.SameFile(finalInfo, stageInfo) {
				return nil
			}
			return errors.New("CSV export destination already exists")
		} else if !os.IsNotExist(e) {
			return e
		}
		if info, e := os.Lstat(stage); e == nil {
			if !info.Mode().IsRegular() {
				return errors.New("CSV export stage is not a regular file")
			}
			if e = os.Remove(stage); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		f, err := os.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		complete := false
		defer func() {
			_ = f.Close()
			if !complete {
				_ = os.Remove(stage)
			}
		}()
		if err = subscribers.New(db).ExportCSV(ctx, f, p.BrandID, p.ListID); err != nil {
			return err
		}
		if err = f.Sync(); err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		// Linking is atomic and refuses to overwrite an existing export. Keep
		// the private stage hardlink as this job's completion marker for retries.
		if err = os.Link(stage, path); err != nil {
			return err
		}
		complete = true
		directory, err := os.Open(root)
		if err != nil {
			return err
		}
		defer directory.Close()
		return directory.Sync()

	})
}

// Job file payloads are basenames within the private spool, never arbitrary paths.
func jobPath(root, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return "", errors.New("CSV job file must be a basename in the database jobs directory")
	}
	p := filepath.Join(root, name)
	info, err := os.Lstat(p)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("CSV job file may not be a symlink")
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("CSV job file: %w", err)
	}
	return p, nil
}

// Suppression synchronization always imports into the brand whose credentials
// read the SES account. Account suppression lists must never become global.
func registerBrandSuppressionSync(ctx context.Context, db *store.DB, r *jobs.Runner, resolve func(context.Context, int64) (events.SESAdmin, error)) error {
	return registerBrandSuppressionSyncChecked(ctx, db, r, func(ctx context.Context, id int64) (events.SESAdmin, func(*sql.Conn) error, error) {
		client, err := resolve(ctx, id)
		return client, nil, err
	})
}

func registerBrandSuppressionSyncChecked(ctx context.Context, db *store.DB, r *jobs.Runner, resolve func(context.Context, int64) (events.SESAdmin, func(*sql.Conn) error, error)) error {
	return registerSuppressionJob(ctx, db, r, func(ctx context.Context) error {
		rows, err := db.Reader.QueryContext(ctx, "SELECT id FROM brands ORDER BY id")
		if err != nil {
			return err
		}
		var brands []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			brands = append(brands, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		var failures []error
		for _, id := range brands {
			client, check, err := resolve(ctx, id)
			if err == nil {
				err = events.SyncBrandSuppressionsChecked(ctx, db, client, id, check)
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("brand %d suppression sync: %w", id, err))
			}
		}
		return errors.Join(failures...)
	})
}

func registerSuppressionJob(ctx context.Context, db *store.DB, r *jobs.Runner, sync func(context.Context) error) error {
	schedule := func(ctx context.Context, at time.Time) error {
		_, err := db.Writer.ExecContext(ctx, `INSERT INTO jobs(queue,payload,status,run_at,created_at) SELECT 'ses_suppressions','{}','pending',?,? WHERE NOT EXISTS(SELECT 1 FROM jobs WHERE queue='ses_suppressions' AND status='pending')`, at.UnixMilli(), time.Now().UnixMilli())
		return err
	}
	if err := r.Register("ses_suppressions", func(ctx context.Context, j jobs.Job) error {
		// Keep the daily job alive even if one account is unavailable.
		err := sync(ctx)
		return errors.Join(err, schedule(ctx, time.Now().Add(24*time.Hour)))
	}); err != nil {
		return err
	}
	return schedule(ctx, time.Now())
}

func validateTransactional(ctx context.Context, db *store.DB, p transactionalEmail) error {
	if p.SubscriberID == 0 {
		return errors.New("transactional email requires subscriber_id")
	}
	var email string
	if err := db.Reader.QueryRowContext(ctx, "SELECT email FROM subscribers WHERE id=? AND brand_id=? AND status='active'", p.SubscriberID, p.BrandID).Scan(&email); err != nil {
		return err
	}
	if email != p.To {
		return sql.ErrNoRows
	}
	if p.Purpose == "thankyou" {
		if p.ListID == 0 {
			return errors.New("thank-you email requires list_id")
		}
		var one int
		return db.Reader.QueryRowContext(ctx, `SELECT 1 FROM list_subscribers ls JOIN lists l ON l.id=ls.list_id JOIN subscribers s ON s.id=ls.subscriber_id WHERE ls.subscriber_id=? AND ls.list_id=? AND l.brand_id=? AND ls.status='subscribed' AND `+subscribers.MarketingEligibilitySQL+``, p.SubscriberID, p.ListID, p.BrandID).Scan(&one)
	}
	if p.ConfirmationToken != "" {
		hash := sha256.Sum256([]byte(p.ConfirmationToken))
		var one int
		return db.Reader.QueryRowContext(ctx, `SELECT 1 FROM subscription_confirmations c JOIN list_subscribers ls ON ls.list_id=c.list_id AND ls.subscriber_id=c.subscriber_id WHERE c.token_hash=? AND c.subscriber_id=? AND c.list_id=? AND c.expires_at>? AND ls.status='unconfirmed'`, hash[:], p.SubscriberID, p.ListID, time.Now().UnixMilli()).Scan(&one)
	}
	return nil
}

// Only errors with evidence of non-delivery may re-enter a mail queue.
func mailJobError(err error) error {
	if err == nil {
		return nil
	}
	var notSent *send.NotificationNotSentError
	if errors.As(err, &notSent) {
		return jobs.RetrySafe(err)
	}
	switch send.Classify(err) {
	case send.Throttled, send.Retryable, send.PauseAccount:
		return jobs.RetrySafe(err)
	default:
		return err
	}
}
