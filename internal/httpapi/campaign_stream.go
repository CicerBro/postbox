package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (a *API) streamCampaignStats(w http.ResponseWriter, r *http.Request, brand, id int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, errors.New("streaming unavailable"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		// Read credentials again before each event so logout, expiry, key
		// revocation, and brand permission changes stop an existing connection.
		i, _, err := a.requestIdentity(r)
		if err != nil {
			return
		}
		current := r.WithContext(context.WithValue(r.Context(), identityKey{}, i))
		currentBrand, err := a.brandID(current)
		if err != nil || currentBrand != brand {
			return
		}
		v, err := queryRows(r.Context(), a.db.Reader, "SELECT s.* FROM campaign_stats s JOIN campaigns c ON c.id=s.campaign_id WHERE c.id=? AND c.brand_id=?", id, brand)
		if err != nil {
			return
		}
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		if _, err = fmt.Fprintf(w, "event: stats\ndata: %s\n\n", b); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
