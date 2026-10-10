package config

import "testing"

func TestTrustedProxyConfiguration(t *testing.T) {
	t.Setenv("POSTBOX_TRUSTED_PROXIES", "")
	c, err := Load(nil)
	if err != nil || len(c.TrustedProxies) != 0 {
		t.Fatal("proxies trusted by default", err)
	}
	t.Setenv("POSTBOX_TRUSTED_PROXIES", "127.0.0.1, ::1, 10.0.0.0/24")
	c, err = Load(nil)
	if err != nil || len(c.TrustedProxies) != 3 {
		t.Fatal("proxy environment", err)
	}
	c, err = Load([]string{"--trusted-proxies=192.0.2.1/24"})
	if err != nil || c.TrustedProxies[0].String() != "192.0.2.0/24" {
		t.Fatal("proxy flag", err)
	}
	for _, raw := range []string{"bad", "127.0.0.1,", "fe80::1%en0", "10.0.0.1:80", "::ffff:10.0.0.0/120"} {
		if _, err := Load([]string{"--trusted-proxies=" + raw}); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
