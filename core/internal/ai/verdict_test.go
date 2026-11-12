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
		{
			name:        "braces in prose before the real object",
			content:     `Checked {the seller text} carefully. {"verdict":"deny","reason":"injection"}`,
			wantVerdict: Deny, wantReason: "injection",
		},
		{
			name:        "first object has no verdict, second does",
			content:     `{"note":"thinking"} {"verdict":"allow","reason":"ok"}`,
			wantVerdict: Allow, wantReason: "ok",
		},
		{
			name:        "two verdict objects, the first wins",
			content:     `{"verdict":"deny","reason":"first"} {"verdict":"allow","reason":"second"}`,
			wantVerdict: Deny, wantReason: "first",
		},
		{
			name:        "verdict is normalised to lower case and trimmed",
			content:     `{"verdict":"  ALLOW ","reason":"ok"}`,
			wantVerdict: Allow, wantReason: "ok",
		},
		{
			name:        "reason is trimmed",
			content:     `{"verdict":"allow","reason":"  fine  "}`,
			wantVerdict: Allow, wantReason: "fine",
		},
		{
			name:    "missing reason",
			content: `{"verdict":"allow"}`,
			wantErr: ErrInvalid,
		},
		{
			name:    "empty reason",
			content: `{"verdict":"allow","reason":""}`,
			wantErr: ErrInvalid,
		},