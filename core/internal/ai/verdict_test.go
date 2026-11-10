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
		{
			name:        "plain json",
			content:     `{"verdict":"allow","reason":"matches the goal"}`,
			wantVerdict: Allow, wantReason: "matches the goal",
		},
		{
			name:        "code fence",
			content:     "```json\n" + `{"verdict":"deny","reason":"wrong product"}` + "\n```",
			wantVerdict: Deny, wantReason: "wrong product",
		},
		{
			name:        "prose before and after",
			content:     `Here is my answer: {"verdict":"escalate","reason":"goal is vague"} Hope that helps.`,
			wantVerdict: Escalate, wantReason: "goal is vague",
		},