package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

const taglineSettingsKey = "tagline"
const defaultTagline = "Self-hosted. Your audience, under your control."

type TaglineSettings struct {
	Enabled bool   `json:"enabled"`
	Text    string `json:"text"`
}

func (a *API) loadTaglineSettings(ctx context.Context) (TaglineSettings, error) {
	settings := TaglineSettings{Enabled: true, Text: defaultTagline}
	value, err := a.db.Setting(ctx, taglineSettingsKey)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal([]byte(value), &settings)
	return settings, err
}

func (a *API) taglineSettings(w http.ResponseWriter, r *http.Request) {
	if !a.userAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPatch {
		var in struct {
			Enabled *bool   `json:"enabled"`
			Text    *string `json:"text"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Enabled == nil || in.Text == nil {
			fail(w, http.StatusBadRequest, errors.New("enabled and text are required"))
			return
		}
		if utf8.RuneCountInString(*in.Text) > 500 || (*in.Enabled && strings.TrimSpace(*in.Text) == "") {
			fail(w, http.StatusBadRequest, errors.New("tagline text must be at most 500 characters and cannot be blank when enabled"))
			return
		}
		settings := TaglineSettings{Enabled: *in.Enabled, Text: *in.Text}
		value, err := json.Marshal(settings)
		if err == nil {
			err = a.db.SetSetting(r.Context(), taglineSettingsKey, string(value))
		}
		result(w, settings, err)
		return
	}
	settings, err := a.loadTaglineSettings(r.Context())
	result(w, settings, err)
}
