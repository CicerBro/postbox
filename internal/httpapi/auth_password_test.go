package httpapi

import "testing"

func TestHashPasswordMinimumLength(t *testing.T) {
	for _, password := range []string{"1234567", "ééééééé"} {
		if hash, err := HashPassword(password); err == nil || err.Error() != "password must have at least 8 characters" || hash != "" {
			t.Fatalf("seven-character password: hash=%q, err=%v", hash, err)
		}
	}
	hash, err := HashPassword("12345678")
	if err != nil {
		t.Fatal("eight-character password rejected:", err)
	}
	if !verifyPassword(hash, "12345678") || verifyPassword(hash, "87654321") {
		t.Fatal("eight-character password hash did not verify correctly")
	}
	if _, err := HashPassword("éééééééé"); err != nil {
		t.Fatal("eight Unicode characters rejected:", err)
	}
}
