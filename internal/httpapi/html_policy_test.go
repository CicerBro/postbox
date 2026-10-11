package httpapi

import (
	"net/http"
	"postbox/internal/htmlpolicy"
	"testing"
)

func assertHTMLPolicy(t *testing.T, h http.Header, policy htmlpolicy.Policy) {
	t.Helper()
	if h.Get("Cache-Control") != "no-store, no-transform" || h.Get("Content-Security-Policy") != string(policy) {
		t.Fatal("incorrect HTML policy or proxy protection", h)
	}
}
