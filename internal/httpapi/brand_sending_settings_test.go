package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBrandSendRateCanBeSetPreservedAndCleared(t *testing.T) {
	_, mux, _, admin, csrf := setup(t)
	const path = "/api/v1/brands/1"
	const identity = `{"name":"Brand","from_email":"sender@example.org"`
	if w := request(mux, "POST", "/api/v1/brands", identity+`}`, &admin, csrf); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	check := func(want int) {
		t.Helper()
		w := request(mux, "GET", path, "", &admin, csrf)
		var brands []struct {
			SendsPerMinute *int `json:"sends_per_minute"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &brands) != nil ||
			len(brands) != 1 || brands[0].SendsPerMinute == nil || *brands[0].SendsPerMinute != want {
			t.Fatalf("want rate %d, got %d %s", want, w.Code, w.Body.String())
		}
	}
	check(0)
	for _, invalid := range []string{"-1", "60001", "1.5"} {
		w := request(mux, "PATCH", path, identity+`,"sends_per_minute":`+invalid+`}`, &admin, csrf)
		if w.Code != http.StatusBadRequest {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, change := range []struct {
		fields string
		want   int
	}{
		{`,"sends_per_minute":120`, 120},
		{"", 120},
		{`,"sends_per_minute":0`, 0},
	} {
		w := request(mux, "PATCH", path, identity+change.fields+`}`, &admin, csrf)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		check(change.want)
	}
}
