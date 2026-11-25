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
			t.Fatalf("DATABASE_URL %q: WANT an error", v)
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

func TestLoopback(t *testing.T) {
	cases := map[string]bool{
		"localhost:8080":   true,
		"127.0.0.1:8080":   true,
		"[::1]:8080":       true,
		"0.0.0.0:8080":     false,
		"192.168.1.1:8080": false,
	}
	for addr, want := range cases {
		c, err := config.Load(env(map[string]string{"DATABASE_URL": "postgres://x", "LISTEN_ADDR": addr}))
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if got := c.Loopback(); got != want {
			t.Errorf("%s: Loopback() = %v, want %v", addr, got, want)
		}
	}
	if (config.Config{ListenAddr: "8080"}).Loopback() {
		t.Error("Loopback() = true for an invalid listen address")
	}
}
