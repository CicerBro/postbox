package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"postbox/internal/awsbrand"
	"time"
)

func (a *API) registerAWSSettings(m *http.ServeMux) {
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		a.route(m, method+" /api/v1/aws-settings", a.awsSettings)
	}
	a.route(m, "POST /api/v1/aws-settings/check", a.awsCheck)
	a.route(m, "POST /api/v1/aws-settings/setup-events", a.awsSetupEvents)
}

func (a *API) awsScope(w http.ResponseWriter, r *http.Request, admin bool) (int64, bool) {
	i := r.Context().Value(identityKey{}).(*identity)
	if admin && (!i.Admin || i.UserID == 0) {
		fail(w, 403, errors.New("administrator session required"))
		return 0, false
	}
	id, ok := a.scoped(w, r)
	if !ok {
		return 0, false
	}
	var found int64
	if err := a.db.Reader.QueryRowContext(r.Context(), "SELECT id FROM brands WHERE id=?", id).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, errors.New("brand not found"))
		} else {
			fail(w, 500, errors.New("brand could not be loaded"))
		}
		return 0, false
	}
	if a.cfg.AWSProvider == nil {
		fail(w, 503, errors.New("brand AWS configuration is unavailable"))
		return 0, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return id, true
}

func (a *API) awsSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := a.awsScope(w, r, r.Method != "GET")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	p := a.cfg.AWSProvider
	if r.Method == "PATCH" {
		// Do not echo JSON decoding errors: an unknown field or malformed value can contain a credential.
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var in awsbrand.Update
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		err := d.Decode(&in)
		var trailing any
		if err != nil || d.Decode(&trailing) != io.EOF {
			fail(w, 400, errors.New("invalid AWS settings JSON"))
			return
		}
		if len(in.Mode) > 32 || len(in.Region) > 64 || len(in.AccessKeyID) > 128 || len(in.SecretAccessKey) > 256 || len(in.SessionToken) > 8192 {
			fail(w, 400, errors.New("AWS settings exceed the maximum field length"))
			return
		}
		if in.TopicARN != nil && len(*in.TopicARN) > 512 || in.QueueURL != nil && len(*in.QueueURL) > 1024 {
			fail(w, 400, errors.New("AWS settings exceed the maximum field length"))
			return
		}
		saved, err := p.Settings(ctx, id)
		if err != nil {
			fail(w, 500, errors.New("AWS settings could not be loaded"))
			return
		}
		if in.Mode == "" {
			in.Mode = saved.Mode
		}
		if in.Region == "" {
			in.Region = saved.Region
		}
		if err = p.Save(ctx, id, in); err != nil {
			fail(w, 400, safeAWSSettingsError(err))
			return
		}
	}
	if r.Method == "DELETE" {
		if err := p.Delete(ctx, id); err != nil {
			fail(w, 500, errors.New("AWS settings could not be cleared"))
			return
		}
	}
	out, err := p.Settings(ctx, id)
	if err != nil {
		fail(w, 500, errors.New("AWS settings could not be loaded"))
		return
	}
	respond(w, 200, out)
}

func safeAWSSettingsError(err error) error {
	switch err.Error() {
	case "AWS mode must be static", "an explicit AWS region is required", "a new access key requires its secret access key", "access key ID and secret access key are required", "an SNS topic ARN is required for SQS ingestion", "SNS topic ARN must match the brand AWS region", "SQS queue URL must match the brand AWS region":
		return err
	default:
		return errors.New("AWS settings could not be saved")
	}
}

func (a *API) awsCheck(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, 403, errors.New("user session required"))
		return
	}
	// The POST requires CSRF, but the AWS operation only reads account metadata.
	read := r.Clone(r.Context())
	read.Method = http.MethodGet
	id, ok := a.awsScope(w, read, false)
	if !ok {
		return
	}
	if !a.allow(fmt.Sprintf("aws-check:%d:%d", i.UserID, id), 6) {
		fail(w, 429, errors.New("AWS check rate limit exceeded"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := a.cfg.AWSProvider.Status(ctx, id)
	if err != nil {
		fail(w, 502, errors.New("AWS connection check failed. Check credentials, region, and SES permissions."))
		return
	}
	respond(w, 200, struct {
		awsbrand.Status
		CheckedAt int64 `json:"checked_at"`
	}{out, time.Now().UnixMilli()})
}

func (a *API) awsSetupEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := a.awsScope(w, r, true)
	if !ok {
		return
	}
	i := r.Context().Value(identityKey{}).(*identity)
	if !a.allow(fmt.Sprintf("aws-events-setup:%d:%d", i.UserID, id), 3) {
		fail(w, 429, errors.New("event setup rate limit exceeded"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.cfg.AWSProvider.SetupEvents(ctx, id); err != nil {
		if err.Error() == "brand keys, SNS topic and SES configuration set are required" {
			fail(w, 400, err)
		} else {
			fail(w, 502, errors.New("SES event destination could not be configured. Check the brand keys, configuration set, topic policy and SES permissions."))
		}
		return
	}
	respond(w, 200, map[string]bool{"configured": true})
}
