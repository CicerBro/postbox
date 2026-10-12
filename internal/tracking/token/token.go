// Package token implements authenticated, compact recipient identifiers.
package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

// Encode returns an authenticated token. IDs must be positive and the key nonempty.
func Encode(secret []byte, campaignID, subscriberID int64) string {
	return encode(secret, campaignID, subscriberID, nil)
}

// EncodeBound binds a capability to immutable campaign and subscriber identities.
// Numeric database IDs alone can be reused after a deletion.
func EncodeBound(secret []byte, campaignID, subscriberID int64, campaignIdentity, subscriberIdentity string) string {
	if campaignIdentity == "" || subscriberIdentity == "" {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("tracking identity\x00" + campaignIdentity + "\x00" + subscriberIdentity))
	return encode(secret, campaignID, subscriberID, mac.Sum(nil)[:16])
}

func encode(secret []byte, campaignID, subscriberID int64, binding []byte) string {
	if len(secret) == 0 || campaignID <= 0 || subscriberID <= 0 {
		return ""
	}
	var b [53]byte
	b[0] = 1
	if binding != nil {
		b[0] = 2
	}
	n := 1 + binary.PutUvarint(b[1:], uint64(campaignID))
	n += binary.PutUvarint(b[n:], uint64(subscriberID))
	n += copy(b[n:], binding)
	mac := hmac.New(sha256.New, secret)
	mac.Write(b[:n])
	sum := mac.Sum(nil)
	copy(b[n:], sum[:16])
	return base64.RawURLEncoding.EncodeToString(b[:n+16])
}

func Decode(secret []byte, value string) (campaignID, subscriberID int64, err error) {
	invalid := errors.New("invalid tracking token")
	if len(secret) == 0 || len(value) > 71 {
		return 0, 0, invalid
	}
	b, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil || len(b) < 19 || (b[0] != 1 && b[0] != 2) {
		return 0, 0, invalid
	}
	payload := b[:len(b)-16]
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil)[:16], b[len(b)-16:]) {
		return 0, 0, invalid
	}
	c, n := binary.Uvarint(payload[1:])
	if n <= 0 {
		return 0, 0, invalid
	}
	s, m := binary.Uvarint(payload[1+n:])
	end := 1 + n + m
	var binding []byte
	if b[0] == 2 && end+16 == len(payload) {
		binding = payload[end:]
		end += 16
	}
	if m <= 0 || end != len(payload) || c == 0 || s == 0 || c > 1<<63-1 || s > 1<<63-1 {
		return 0, 0, invalid
	}
	// Canonical encoding prevents multiple token strings for the same recipient.
	if encode(secret, int64(c), int64(s), binding) != value {
		return 0, 0, invalid
	}
	return int64(c), int64(s), nil
}

// MatchesIdentity accepts only a v2 capability for these immutable identities.
func MatchesIdentity(secret []byte, value string, campaignID, subscriberID int64, campaignIdentity, subscriberIdentity string) bool {
	expected := EncodeBound(secret, campaignID, subscriberID, campaignIdentity, subscriberIdentity)
	return expected != "" && hmac.Equal([]byte(value), []byte(expected))
}

// Legacy reports whether a token uses the original numeric-ID-only format.
// Callers must authenticate it with Decode before using this information.
func Legacy(value string) bool {
	b, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(b) > 0 && b[0] == 1
}
