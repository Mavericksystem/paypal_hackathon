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

	