// Package previewtoken provides encrypted capabilities isolated from live tracking tokens.
package previewtoken

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

type Claims struct {
	Brand      int64  `json:"b"`
	Campaign   int64  `json:"c"`
	Subscriber int64  `json:"s"`
	List       int64  `json:"l"`
	Host       string `json:"h"`
	Expires    int64  `json:"e"`
}

func aead(secret []byte) (cipher.AEAD, error) {
	if len(secret) < 24 {
		return nil, errors.New("preview secret unavailable")
	}
	key := sha256.Sum256(append([]byte("postbox-editor-preview-v1:"), secret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func Encode(secret []byte, claims Claims) (string, error) {
	a, err := aead(secret)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(a.Seal(nonce, nonce, body, []byte("editor-preview"))), nil
}
func Decode(secret []byte, token string) (Claims, error) {
	var c Claims
	a, err := aead(secret)
	if err != nil {
		return c, err
	}
	if len(token) > 2048 {
		return c, errors.New("invalid preview")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < a.NonceSize() {
		return c, errors.New("invalid preview")
	}
	body, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte("editor-preview"))
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(body, &c); err != nil {
		return c, err
	}
	if c.Brand <= 0 || c.Campaign <= 0 || c.Subscriber < 0 || c.List < 0 || c.Expires <= time.Now().Unix() {
		return Claims{}, errors.New("expired or invalid preview")
	}
	return c, nil
}
