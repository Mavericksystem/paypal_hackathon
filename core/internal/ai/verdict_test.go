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

var (
	rulesPass  = policy.Result{Verdict: policy.Pass, Code: "ok", Reason: "all rules passed"}
	rulesHuman = policy.Result{Verdict: policy.Pass, Code: "ok", Reason: "amount above auto-approve limit", NeedsHuman: true}
	rulesDeny  = policy.Result{Verdict: policy.Deny, Code: "merchant_not_allowed", Reason: "merchant is not on the allowlist"}
)

func TestCombine(t *testing.T) {
	errTimeout := errors.New("timeout")

	tests := []struct {
		name       string
		rules      policy.Result
		dec        Decision
		aiErr      error
		wantVerd   Verdict
		wantSource Source
		wantReason string // exact match, empty means skip
	}{
		{
			name:  "rules deny, AI allow cannot loosen it",
			rules: rulesDeny, dec: Decision{Allow, "looks fine"},
			wantVerd: Deny, wantSource: SourceRules, wantReason: rulesDeny.Reason,
		},
		{
			name:  "rules deny, AI error does not matter",
			rules: rulesDeny, aiErr: errTimeout,
			wantVerd: Deny, wantSource: SourceRules, wantReason: rulesDeny.Reason,
		},
		{
			name:  "rules pass, AI allow",
			rules: rulesPass, dec: Decision{Allow, "matches goal"},
			wantVerd: Allow, wantSource: SourcePassed, wantReason: "matches goal",
		},
		{
			name:  "rules pass, AI escalate",
			rules: rulesPass, dec: Decision{Escalate, "goal is vague"},
			wantVerd: Escalate, wantSource: SourceAI, wantReason: "goal is vague",
		},
		{
			name:  "rules pass, AI deny",
			rules: rulesPass, dec: Decision{Deny, "wrong product"},
			wantVerd: Deny, wantSource: SourceAI, wantReason: "wrong product",
		},
		{
			name:  "rules pass, AI error fails closed to escalate",
			rules: rulesPass, aiErr: errTimeout,
			wantVerd: Escalate, wantSource: SourceAIFailure,
		},
		{
			name:  "AI error wins over a leftover allow decision",
			rules: rulesPass, dec: Decision{Allow, "stale"}, aiErr: errTimeout,
			wantVerd: Escalate, wantSource: SourceAIFailure,
		},
		{
			name:  "above auto-approve limit, AI allow still escalates",
			rules: rulesHuman, dec: Decision{Allow, "matches goal"},
			wantVerd: Escalate, wantSource: SourceThreshold, wantReason: rulesHuman.Reason,
		},
		{
			name:  "above auto-approve limit, AI deny stays deny",
			rules: rulesHuman, dec: Decision{Deny, "wrong product"},
			wantVerd: Deny, wantSource: SourceAI, wantReason: "wrong product",
		},
		{
			name:  "above auto-approve limit, AI escalate stays escalate",
			rules: rulesHuman, dec: Decision{Escalate, "unclear"},
			wantVerd: Escalate, wantSource: SourceAI, wantReason: "unclear",
		},
		{
			name:  "unknown AI verdict fails closed",
			rules: rulesPass, dec: Decision{Verdict("approve"), "x"},
			wantVerd: Escalate, wantSource: SourceAIFailure,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Combine(tc.rules, tc.dec, tc.aiErr)
			if got.Verdict != tc.wantVerd {
				t.Errorf("verdict = %q, want %q", got.Verdict, tc.wantVerd)
			}
			if got.Source != tc.wantSource {
				t.Errorf("source = %q, want %q", got.Source, tc.wantSource)
			}
			if tc.wantReason != "" && got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Reason == "" {
				t.Error("reason must never be empty")
			}
		})
	}

	t.Run("AI failure reason names the error", func(t *testing.T) {
		got := Combine(rulesPass, Decision{}, errTimeout)
		if !strings.Contains(got.Reason, "timeout") {
			t.Errorf("reason %q should mention the error", got.Reason)
		}
	})
}

func rank(v Verdict) int {
	switch v {
	case Allow:
		return 0
	case Escalate:
		return 1
	default:
		return 2
	}
}

func TestCombineNeverLoosens(t *testing.T) {
	rulesSet := []policy.Result{rulesPass, rulesHuman, rulesDeny}
	verdicts := []Verdict{Allow, Escalate, Deny, Verdict("garbage")}
	errs := []error{nil, errors.New("boom")}

	for _, rules := range rulesSet {
		for _, v := range verdicts {
			for _, e := range errs {
				got := Combine(rules, Decision{v, "r"}, e)

				base := 0 // what the rules alone would decide
				switch {
				case rules.Verdict == policy.Deny:
					base = 2
				case rules.NeedsHuman:
					base = 1
				}
				if rank(got.Verdict) < base {
					t.Errorf("rules=%s human=%v ai=%q err=%v: final %q is looser than rules alone",
						rules.Verdict, rules.NeedsHuman, v, e, got.Verdict)
				}

				if got.Verdict == Allow {
					if rules.Verdict != policy.Pass || rules.NeedsHuman || e != nil || v != Allow {
						t.Errorf("rules=%s human=%v ai=%q err=%v: Allow without full agreement",
							rules.Verdict, rules.NeedsHuman, v, e)
					}
				}
			}
		}
	}
}
