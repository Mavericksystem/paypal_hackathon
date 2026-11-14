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
		{
			name:    "whitespace reason",
			content: `{"verdict":"allow","reason":"   "}`,
			wantErr: ErrInvalid,
		},
		{
			name:    "reason is not a string",
			content: `{"verdict":"allow","reason":42}`,
			wantErr: ErrInvalid,
		},
		{
			name:    "unknown verdict value",
			content: `{"verdict":"approve","reason":"ok"}`,
			wantErr: ErrInvalid,
		},
		{
			name:    "verdict is not a string",
			content: `{"verdict":1,"reason":"ok"}`,
			wantErr: ErrInvalid,
		},
		{
			name:    "no json at all",
			content: `I think this payment is fine.`,
			wantErr: ErrNoVerdict,
		},
		{
			name:    "json without a verdict key",
			content: `{"decision":"allow","reason":"ok"}`,
			wantErr: ErrNoVerdict,
		},
		{
			name:    "truncated json",
			content: `{"verdict":"allow","reason":"cut off`,
			wantErr: ErrNoVerdict,
		},
		{
			name:    "empty content",
			content: ``,
			wantErr: ErrNoVerdict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDecision(tc.content)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Verdict != tc.wantVerdict {
				t.Errorf("verdict = %q, want %q", got.Verdict, tc.wantVerdict)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
		})
	}
}