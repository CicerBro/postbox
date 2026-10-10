package token

import (
	"encoding/base64"
	"testing"
)

func TestRoundTripForgeryAndBinding(t *testing.T) {
	key := []byte("a strong secret stored across restarts")
	for _, ids := range [][2]int64{{1, 1}, {350000, 999999999}, {1<<63 - 1, 1<<63 - 1}} {
		v := Encode(key, ids[0], ids[1])
		c, s, e := Decode(key, v)
		if e != nil || c != ids[0] || s != ids[1] {
			t.Fatal(c, s, e)
		}
		b, _ := base64.RawURLEncoding.DecodeString(v)
		b[2] ^= 1
		if _, _, e = Decode(key, base64.RawURLEncoding.EncodeToString(b)); e == nil {
			t.Fatal("accepted forged token")
		}
		if _, _, e = Decode([]byte("another key"), v); e == nil {
			t.Fatal("accepted wrong key")
		}
	}
	for _, v := range []string{"", "!!!", "AA", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, _, e := Decode(key, v); e == nil {
			t.Fatal("accepted malformed token")
		}
	}
	if Encode(nil, 1, 1) != "" || Encode(key, -1, 1) != "" {
		t.Fatal("accepted invalid token input")
	}
}
