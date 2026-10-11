package previewtoken

import (
	"encoding/base64"
	"postbox/internal/tracking/token"
	"strings"
	"testing"
	"time"
)

func TestEncryptedPreviewCapabilities(t *testing.T) {
	secret := []byte("long-testing-token-secret-value")
	c := Claims{Brand: 12345, Campaign: 23456, Subscriber: 34567, Host: "example.org", Expires: time.Now().Add(time.Hour).Unix()}
	tok, err := Encode(secret, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(secret, tok)
	if err != nil || got != c {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	if strings.Contains(string(raw), `"s":34567`) {
		t.Fatal("subscriber exposed")
	}
	if _, _, err := token.Decode(secret, tok); err == nil {
		t.Fatal("preview accepted as live token")
	}
	for _, bad := range []string{"preview", tok[:len(tok)-5] + "aaaaa", token.Encode(secret, c.Campaign, c.Subscriber)} {
		if _, err := Decode(secret, bad); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	c.Expires = time.Now().Add(-time.Second).Unix()
	tok, _ = Encode(secret, c)
	if _, err := Decode(secret, tok); err == nil {
		t.Fatal("expired token accepted")
	}
}
