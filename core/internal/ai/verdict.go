package ai

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Mavericksystem/warden/core/internal/policy"
)

type Verdict string

const (
	Allow    Verdict = "allow"
	Escalate Verdict = "escalate"
	Deny     Verdict = "deny"
)

type Decision struct {
	Verdict Verdict `json:"verdict"`
	Reason  string  `json:"reason"`
}

var (
	ErrNoVerdict = errors.New("ai: no JSON object with a verdict found")
	ErrInvalid   = errors.New("ai: invalid verdict object")
)

func ParseDecision(content string) (Decision, error) {
	for i := 0; i < len(content); i++ {
		if content[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(content[i:]))
		var raw map[string]json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue // not a complete object here, try the next '{'
		}
		rawVerdict, ok := raw["verdict"]
		if !ok {
			continue // a different object, keep scanning
		}
		return validate(raw, rawVerdict)
	}
	return Decision{}, ErrNoVerdict
}
func validate(raw map[string]json.RawMessage, rawVerdict json.RawMessage) (Decision, error) {
	var v string
	if err := json.Unmarshal(rawVerdict, &v); err != nil {
		return Decision{}, ErrInvalid
	}
	verdict := Verdict(strings.ToLower(strings.TrimSpace(v)))
	switch verdict {
	case Allow, Escalate, Deny:
	default:
		return Decision{}, ErrInvalid
	}

	var reason string
	if err := json.Unmarshal(raw["reason"], &reason); err != nil {
		return Decision{}, ErrInvalid
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Decision{}, ErrInvalid
	}
	return Decision{Verdict: verdict, Reason: reason}, nil
}

type Source string

const (
	SourceRules     Source = "rules"
	SourceAI        Source = "ai"
	SourceAIFailure Source = "ai_failure"
	SourceThreshold Source = "amount_threshold"
	SourcePassed    Source = "rules_and_ai"
)

type Final struct {
	Verdict Verdict
	Reason  string
	Source  Source
}

func Combine(rules policy.Result, d Decision, aiErr error) Final {
	if rules.Verdict == policy.Deny {
		return Final{Deny, rules.Reason, SourceRules}
	}
	if aiErr != nil {
		return Final{Escalate, "AI check failed, failing closed: " + aiErr.Error(), SourceAIFailure}
	}
	switch d.Verdict {
	case Deny:
		return Final{Deny, d.Reason, SourceAI}
	case Escalate:
		return Final{Escalate, d.Reason, SourceAI}
	case Allow:
		if rules.NeedsHuman {
			return Final{Escalate, rules.Reason, SourceThreshold}

		}
		return Final{Allow, d.Reason, SourcePassed}
	}
	return Final{Escalate, "unrecognised AI verdict, failing closed", SourceAIFailure}
}
