package httpapi

import (
	"errors"
	"net/http"
	"time"
	_ "time/tzdata" // Keep IANA validation available in minimal deployment images.
)

type PreferencesResponse struct {
	Timezone string `json:"timezone"`
}

func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
	i := r.Context().Value(identityKey{}).(*identity)
	if i.UserID == 0 {
		fail(w, http.StatusForbidden, errors.New("session authentication required"))
		return
	}
	var in struct {
		Timezone *string `json:"timezone"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Timezone == nil {
		fail(w, http.StatusBadRequest, errors.New("timezone is required; use an empty string for the browser timezone"))
		return
	}
	zone := *in.Timezone
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
	res, err := a.db.Writer.ExecContext(r.Context(), "UPDATE users SET timezone=? WHERE id=?", zone, i.UserID)
	if err = requireChanged(res, err); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, http.StatusOK, PreferencesResponse{Timezone: zone})
}
