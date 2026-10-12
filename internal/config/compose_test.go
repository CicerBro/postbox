package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeDoesNotOverrideTurnstileSecret(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "..", "compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(compose), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != "POSTBOX_TURNSTILE_SECRET" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		remainder, inherited := strings.CutPrefix(value, "${POSTBOX_TURNSTILE_SECRET")
		remainder, closed := strings.CutSuffix(remainder, "}")
		if !inherited || !closed || !(remainder == "" || strings.HasPrefix(remainder, ":?") || strings.HasPrefix(remainder, "?")) {
			t.Fatal("Compose must inherit the Turnstile secret from .env without a literal or default override")
		}
	}
	t.Setenv("POSTBOX_TURNSTILE_SECRET", "deployment-turnstile-secret")
	config, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.TurnstileSecret != "deployment-turnstile-secret" {
		t.Fatal("Turnstile secret was not loaded from the deployment environment")
	}
}
