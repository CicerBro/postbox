package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

const publicSubscriptionsKey = "public_subscriptions_enabled"

type subscriptionSettings struct {
	PublicSubscriptionsEnabled bool `json:"public_subscriptions_enabled"`
	ApplicationEnabled         bool `json:"application_enabled"`
	Enabled                    bool `json:"enabled"`
}

func (a *API) registerSubscriptionSettings(m *http.ServeMux) {
	for _, method := range []string{"GET", "PATCH"} {
		a.route(m, method+" /api/v1/subscription-settings", a.subscriptionSettings)
		a.route(m, method+" /api/v1/application-settings", a.applicationSettings)
	}
}

// Missing switches default to enabled, preserving existing installations. Read
// both switches in one database snapshot so the effective state is consistent.
func (a *API) publicSubscriptionSettings(ctx context.Context, brandID int64) (subscriptionSettings, error) {
	var app, brand string
	err := a.db.Reader.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT value FROM settings WHERE key=?),'true'),
		COALESCE((SELECT value FROM settings WHERE key=?),'true')`,
		publicSubscriptionsKey, fmt.Sprintf("brand.%d.%s", brandID, publicSubscriptionsKey)).Scan(&app, &brand)
	if err != nil {
		return subscriptionSettings{}, err
	}
	appEnabled, err := strconv.ParseBool(app)
	if err != nil {
		return subscriptionSettings{}, err
	}
	brandEnabled, err := strconv.ParseBool(brand)
	if err != nil {
		return subscriptionSettings{}, err
	}
	return subscriptionSettings{brandEnabled, appEnabled, appEnabled && brandEnabled}, nil
}

func (a *API) requirePublicSubscriptions(w http.ResponseWriter, r *http.Request, brandID int64) bool {
	settings, err := a.publicSubscriptionSettings(r.Context(), brandID)
	if err != nil {
		result(w, nil, err)
		return false
	}
	if !settings.Enabled {
		fail(w, http.StatusForbidden, errors.New("public subscriptions are disabled"))
		return false
	}
	return true
}

func (a *API) subscriptionSettings(w http.ResponseWriter, r *http.Request) {
	brandID, ok := a.scoped(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPatch && !a.savePublicSubscriptionSwitch(w, r, fmt.Sprintf("brand.%d.%s", brandID, publicSubscriptionsKey)) {
		return
	}
	settings, err := a.publicSubscriptionSettings(r.Context(), brandID)
	result(w, settings, err)
}

func (a *API) applicationSettings(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	if r.Method == http.MethodPatch && !a.savePublicSubscriptionSwitch(w, r, publicSubscriptionsKey) {
		return
	}
	settings, err := a.publicSubscriptionSettings(r.Context(), 0)
	result(w, map[string]bool{publicSubscriptionsKey: settings.ApplicationEnabled}, err)
}

func (a *API) savePublicSubscriptionSwitch(w http.ResponseWriter, r *http.Request, key string) bool {
	var in map[string]json.RawMessage
	if !decode(w, r, &in) {
		return false
	}
	raw, ok := in[publicSubscriptionsKey]
	var enabled bool
	if !ok || len(in) != 1 || (string(raw) != "true" && string(raw) != "false") || json.Unmarshal(raw, &enabled) != nil {
		fail(w, http.StatusBadRequest, errors.New("public_subscriptions_enabled must be a boolean and the only setting"))
		return false
	}
	if err := a.db.SetSetting(r.Context(), key, strconv.FormatBool(enabled)); err != nil {
		result(w, nil, err)
		return false
	}
	return true
}
