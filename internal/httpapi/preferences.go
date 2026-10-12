package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA validation available in minimal deployment images.
	"unicode/utf8"
)

type PreferencesResponse struct {
	Timezone    string `json:"timezone"`
	DisplayName string `json:"display_name"`
}

func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, http.StatusForbidden, errors.New("session authentication required"))
		return
	}
	var in struct {
		Timezone    *string `json:"timezone"`
		DisplayName *string `json:"display_name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Timezone == nil && in.DisplayName == nil {
		fail(w, http.StatusBadRequest, errors.New("timezone or display_name is required"))
		return
	}
	zone := ""
	if in.Timezone != nil {
		zone = *in.Timezone
	}
	if zone != "" {
		if zone == "Local" || len(zone) > 255 {
			fail(w, http.StatusBadRequest, errors.New("timezone must be a valid IANA timezone"))
			return
		}
		if _, err := time.LoadLocation(zone); err != nil {
			fail(w, http.StatusBadRequest, errors.New("timezone must be a valid IANA timezone"))
			return
		}
	}
	if in.DisplayName != nil {
		name, err := normalizeDisplayName(*in.DisplayName)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		in.DisplayName = &name
	}
	res, err := a.db.Writer.ExecContext(r.Context(), "UPDATE users SET timezone=COALESCE(?,timezone),display_name=COALESCE(?,display_name) WHERE id=?", in.Timezone, in.DisplayName, i.UserID)
	if err = requireChanged(res, err); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var saved PreferencesResponse
	if err := a.db.Reader.QueryRowContext(r.Context(), "SELECT timezone,display_name FROM users WHERE id=?", i.UserID).Scan(&saved.Timezone, &saved.DisplayName); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	respond(w, http.StatusOK, saved)
}

func normalizeDisplayName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) > 100 {
		return "", errors.New("display_name must be at most 100 characters")
	}
	return value, nil
}
