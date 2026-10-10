package config

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentSecret(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret")
	a, e := TokenSecret(p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := TokenSecret(p)
	if e != nil || !bytes.Equal(a, b) {
		t.Fatalf("secret changed: %v", e)
	}
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = TokenSecret(p); e == nil {
		t.Fatal("insecure permissions accepted")
	}
}
func TestProductionOrigin(t *testing.T) {
	for _, c := range []Config{{BaseURL: "http://example.com", SecureCookies: true}, {BaseURL: "https://example.com/path", SecureCookies: true}, {BaseURL: "https://example.com"}} {
		if c.ValidateServe() == nil {
			t.Fatalf("accepted %#v", c)
		}
	}
	if (Config{BaseURL: "https://example.com", SecureCookies: true}).ValidateServe() != nil {
		t.Fatal("HTTPS rejected")
	}
}

func TestSecretRejectsCorruptionAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := TokenSecret(path); err == nil {
		t.Fatal("corrupt secret silently replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "corrupt\n" {
		t.Fatal("corrupt secret changed")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := TokenSecret(link); err == nil {
		t.Fatal("symlink secret accepted")
	}
}

func TestUnsafeRetryConfigurationRejected(t *testing.T) {
	t.Setenv("POSTBOX_AMBIGUOUS_POLICY", "")
	t.Setenv("POSTBOX_RECOVERY_POLICY", "unknown")
	c, err := Load(nil)
	if err != nil || c.RecoveryPolicy != "unknown" {
		t.Fatalf("defaults %+v: %v", c, err)
	}
	for _, args := range [][]string{{"--ambiguous-policy=retry"}, {"--recovery-policy=retry"}} {
		if _, err = Load(args); err == nil {
			t.Fatalf("unsafe configuration accepted: %v", args)
		}
	}
	t.Setenv("POSTBOX_AMBIGUOUS_POLICY", "retry")
	if _, err = Load(nil); err == nil {
		t.Fatal("unsafe ambiguous environment accepted")
	}
	t.Setenv("POSTBOX_AMBIGUOUS_POLICY", "")
	t.Setenv("POSTBOX_RECOVERY_POLICY", "retry")
	if _, err = Load(nil); err == nil {
		t.Fatal("unsafe recovery environment accepted")
	}
}

func TestHelpDoesNotRevealTurnstileSecret(t *testing.T) {
	t.Setenv("POSTBOX_TURNSTILE_SECRET", "protected-turnstile-secret")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = writer
	_, loadErr := Load([]string{"--help"})
	os.Stderr = old
	writer.Close()
	output, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(loadErr, flag.ErrHelp) {
		t.Fatalf("help: %v", loadErr)
	}
	if bytes.Contains(output, []byte("protected-turnstile-secret")) {
		t.Fatal("help revealed Turnstile secret")
	}
	c, err := Load(nil)
	if err != nil || c.TurnstileSecret != "protected-turnstile-secret" {
		t.Fatalf("secret not loaded: %v", err)
	}
}
