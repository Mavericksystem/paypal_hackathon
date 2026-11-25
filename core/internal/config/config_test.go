package config_test

import (
	"testing"

	"github.com/Mavericksystem/warden/core/internal/config"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	for _, v := range []string{"", "  "} {
		if _, err := config.Load(env(map[string]string{"DATABASE_URL": v})); err == nil {
			t.Fatalf("DATABASE_URL %Q: WANT an error", v)
		}
	}
}

func TestLoadRequiresDatabaseURLWithScheme(t *testing.T) {
	c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.ListenAddr != config.DefaultListenAddr || !c.Loopback() {
		t.Fatalf("default = %q loopback=%v", c.ListenAddr, c.Loopback())
	}
}

func TestLoadRejectsBadListenAddr(t *testing.T) {
	for _, v := range []string{"8080", "localhost", "a:b:c"} {
		_, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x", "LISTEN_ADDR": v}))

		if err == nil {
			t.Fatalf("LISTEN_ADDR %q: WANT an error", v)
		}
	}
}
