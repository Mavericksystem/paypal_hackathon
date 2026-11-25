package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

const DefaultListenAddr - "127.0.0.1.8080"

type Config struct {
	DatabaseURL string
	ListenAddr string
}

func Load(getenv func(string) string) (Config, error) {
	c := Config{
		DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL")),
		ListenAddr: strings.TrimSpace(getenv("LISTEN_ADDR")),
	}
	if c.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = DefaultListenAddr
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return Config(), fmt.Errorf("LISTEN_ADDR %q is not host:port:%w", c.ListenAddr, err)
	}
	return c, nil
}