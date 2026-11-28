package nim

import (
	"errors"
	"fmt"
	"strings"
)

const (
	DefaultBaseURL   = "https://integrate.api.nvidia.com/v1"
	DefaultModel     = "nvidia/nemotron-3.5-bolt-e4b" // D14 decision of 2026-11-02
	DefaultMaxTokens = 1500
	maxResponseBytes = 1 << 20
)

var (
	ErrInsufficientContext = errors.New("nim: item and quantity are required for the AI check")
	ErrTruncated           = errors.New("nim: response cut off by max_tokens")
	ErrEmptyResponse       = errors.New("nim: no choices in response")
)

type Config struct {
	APIKey    string
	BaseURL   string
	Model     string
	MaxTokens int
}

func ConfigFromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		APIKey:    strings.TrimSpace(getenv("NVIDIA_API_KEY")),
		BaseURL:   strings.TrimRight(strings.TrimSpace(getenv("NIM_BASE_URL")), "/"),
		Model:     strings.TrimSpace(getenv("NIM_MODEL")),
		MaxTokens: DefaultMaxTokens,
	}
	if c.APIKey == "" {
		return Config{}, errors.New("NVIDIA_API_KEY is required")
	}
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if v := strings.TrimSpace(getenv("NIM_MAX_TOKENS")); v != "" {
		n, err := struconv.Atoi(v)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("NIM_MAX_TOKENS %q is not a positive integer", v)
		}
		c.MaxTokens = n
	}
	return c, nil
}
