package ai

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mavericksystem/warden/core/internal/policy"
)

func TestParseDecision(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantVerdict Verdict
		wantReason  string
		wantErr     error
	}{