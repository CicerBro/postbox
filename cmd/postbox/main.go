package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"postbox/internal/awsbrand"
	"postbox/internal/config"
	"postbox/internal/events"
	"postbox/internal/httpapi"
	"postbox/internal/httplog"
	"postbox/internal/jobs"
	"postbox/internal/send"
	"postbox/internal/store"
	"postbox/internal/tracking"
	"postbox/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(os.Args[1:]); err != nil {
		slog.Error("postbox failed", "error", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: postbox <serve|init|admin|migrate-sendy|backup> [flags]")
	}
	command := args[0]
	args = args[1:]
	if command == "help" || command == "--help" {
		fmt.Println("postbox <serve|init|admin|migrate-sendy|backup> [--db path] [--base-url https://mail.example.com]\nAdmin: POSTBOX_ADMIN_EMAIL, POSTBOX_ADMIN_PASSWORD, POSTBOX_ADMIN_TOTP\nMigration: POSTBOX_SENDY_DUMP or POSTBOX_SENDY_DSN\nBackup: POSTBOX_BACKUP_FILE")
		return nil
	}
	switch command {
	case "serve", "init", "admin", "migrate-sendy", "backup":
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	c, err := config.Load(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if command == "serve" {
		if err = c.ValidateServe(); err != nil {
			return err
		}
	}
	db, err := store.Open(c.DB)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	if err = bootstrapSending(ctx, db); err != nil {
		return err
	}
	switch command {
	case "init":
		_, err = config.TokenSecret(c.SecretFile)
		if err == nil {
			slog.Info("database initialized", "path", c.DB)
		}
		return err
	case "admin":
		email, password, totp := os.Getenv("POSTBOX_ADMIN_EMAIL"), os.Getenv("POSTBOX_ADMIN_PASSWORD"), os.Getenv("POSTBOX_ADMIN_TOTP")
		if email == "" || password == "" || totp == "" {
			return errors.New("set POSTBOX_ADMIN_EMAIL, POSTBOX_ADMIN_PASSWORD and POSTBOX_ADMIN_TOTP to create an admin with TOTP")
		}
		return httpapi.CreateAdmin(ctx, db, email, password, totp)
	case "backup":
		path := os.Getenv("POSTBOX_BACKUP_FILE")
		if path == "" {
			return errors.New("set POSTBOX_BACKUP_FILE to a new output file")
		}
		return jobs.New(db, jobs.Config{}).Backup(ctx, path)
	case "migrate-sendy":
		return migrateSendy(ctx, db)
	case "serve":
		return serve(db, c)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

// The CLI establishes its fail-closed default once, atomically, even if a core
// tool created this database first. Later init calls preserve operator settings.
func bootstrapSending(ctx context.Context, db *store.DB) error {
	return db.Immediate(ctx, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, "INSERT OR IGNORE INTO settings(key,value) VALUES('cli_bootstrapped','true')")
		if err != nil {
			return err
		}
		created, err := result.RowsAffected()
		if err != nil || created == 0 {
			return err
		}
		_, err = conn.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('sending_enabled','false') ON CONFLICT(key) DO UPDATE SET value='false'")
		return err
	})
}
func serve(db *store.DB, c config.Config) error {
	secret, err := config.TokenSecret(c.SecretFile)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	awsProvider, err := awsbrand.New(db, secret, c.Region, "", c.MaxWorkers)
	if err != nil {
		return err
	}
	runner := jobs.New(db, jobs.Config{Logger: slog.Default()})
	engine := send.New(db, nil, send.Config{BrandSenderProvider: awsProvider, EventHook: runner.EmitTx, MaxWorkers: c.MaxWorkers, RecoveryPolicy: c.RecoveryPolicy, BaseURL: strings.TrimRight(c.BaseURL, "/"), TokenSecret: secret, SoftBounceLimit: c.SoftBounceLimit})
	if err = engine.Recover(ctx); err != nil {
		return err
	}
	var enabled string
	if err = db.Reader.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='sending_enabled'").Scan(&enabled); err != nil {
		return err
	}
	if err = engine.SetEnabled(ctx, enabled == "true" || enabled == "1"); err != nil {
		return err
	}
	if err = registerJobs(db, runner, engine, c); err != nil {
		return err
	}
	if err = runner.RegisterMaintenance(jobs.MaintenanceConfig{RawRetention: time.Duration(c.RetentionDays) * 24 * time.Hour, UnconfirmedRetention: 30 * 24 * time.Hour, BackupDirectory: c.BackupDir}); err != nil {
		return err
	}
	if err = registerBrandSuppressionSyncChecked(ctx, db, runner, func(ctx context.Context, brandID int64) (events.SESAdmin, func(*sql.Conn) error, error) {
		client, fingerprint, err := awsProvider.SESClientSnapshot(ctx, brandID)
		return client, func(conn *sql.Conn) error { return awsProvider.CheckSnapshot(ctx, conn, brandID, fingerprint) }, err
	}); err != nil {
		return err
	}
	if err = runner.ScheduleMaintenance(ctx); err != nil {
		return err
	}
	tracker := tracking.New(db, &tracking.Config{TokenSecret: secret, BaseURL: c.BaseURL, GeoIPPath: c.GeoIP})
	eventService := events.New(db, &events.Config{ResolveTopic: awsProvider.EventBrand, SoftBounceLimit: c.SoftBounceLimit})
	mux := http.NewServeMux()
	httpapi.New(db, engine, httpapi.Config{TrustedProxies: c.TrustedProxies, AWSProvider: awsProvider, SecureCookies: c.SecureCookies, SessionTTL: c.SessionTTL, BaseURL: c.BaseURL, TurnstileSecret: c.TurnstileSecret, TurnstileSiteKey: c.TurnstileSiteKey}).Register(mux)
	tracker.Register(mux)
	eventService.Register(mux)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		check, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if err := db.Reader.PingContext(check); err != nil {
			httplog.RecordError(w, err)
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "unhealthy"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		writeMetrics(w, "postbox_send", engine.Metrics())
		writeMetrics(w, "postbox_jobs", runner.Metrics())
		writeMetrics(w, "postbox_events", eventService.Metrics())
		fmt.Fprintf(w, "postbox_tracking_dropped_opens %d\n", tracker.DroppedOpens.Load())
	})
	mux.Handle("/", web.Handler())
	server := &http.Server{Addr: c.Listen, Handler: requestLog(mux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	errorsCh := make(chan error, 5)
	done := make(chan struct{}, 4)
	workers := 3
	start := func(name string, fn func() error) {
		go func() {
			defer func() { done <- struct{}{} }()
			err := fn()
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
				errorsCh <- fmt.Errorf("%s: %w", name, err)
				cancel()
			}
		}()
	}
	trackingCtx, stopTracking := context.WithCancel(context.Background())
	defer stopTracking()
	if err = tracker.Start(trackingCtx); err != nil {
		return err
	}
	start("sending engine", func() error { return engine.Start(ctx) })
	start("jobs", func() error { return runner.Run(ctx) })
	start("HTTP server", server.ListenAndServe)
	slog.Info("postbox listening", "address", c.Listen, "base_url", c.BaseURL)
	<-ctx.Done()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdown)
	if shutdownErr != nil {
		_ = server.Close()
	}
	stopTracking()
	if err := tracker.Close(shutdown); err != nil {
		return err
	}
	for i := 0; i < workers; i++ {
		select {
		case <-done:
		case <-shutdown.Done():
			return fmt.Errorf("graceful shutdown timed out: %w", shutdown.Err())
		}
	}
	if _, err := db.Writer.ExecContext(shutdown, "PRAGMA optimize"); err != nil {
		return err
	}
	select {
	case err := <-errorsCh:
		return err
	default:
		return shutdownErr
	}
}
func requestLog(next http.Handler) http.Handler {
	return httplog.Middleware(next, slog.Default())
}
func writeMetrics(w http.ResponseWriter, prefix string, v any) {
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return
	}
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		name := typ.Field(i).Tag.Get("json")
		if name == "" {
			name = snake(typ.Field(i).Name)
		}
		name = strings.Split(name, ",")[0]
		switch field.Kind() {
		case reflect.Int, reflect.Int32, reflect.Int64:
			fmt.Fprintf(w, "%s_%s %d\n", prefix, name, field.Int())
		case reflect.Uint, reflect.Uint64:
			fmt.Fprintf(w, "%s_%s %d\n", prefix, name, field.Uint())
		case reflect.Float32, reflect.Float64:
			fmt.Fprintf(w, "%s_%s %g\n", prefix, name, field.Float())
		case reflect.Map:
			for _, key := range field.MapKeys() {
				if key.Kind() == reflect.String {
					label, _ := json.Marshal(key.String())
					fmt.Fprintf(w, "%s_%s{type=%s} %v\n", prefix, name, label, field.MapIndex(key).Interface())
				}
			}
		}
	}
}
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
