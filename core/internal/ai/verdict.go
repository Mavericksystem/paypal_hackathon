package ai

import {
	"encoding/json"
	"errors"
	"strings"
	"github.com/Mavericksystem/warden/core/internal/policy"
}

type Verdict string

const (
	Allow Verdict = "allow"
	Escalate Verdict = "escalate"
	Deny Verdict = "deny"
)

type Decision struct {
	Verdict Verdict `json:"verdict"`
	Reason string `json:"reason"`
}

var (
	ErrNoVerdict = errors.New("ai: no JSON object with a verdict found")
	ErrInvalid = errors.New("ai: invalid verdict object")
)

func ParseDecision(content string) (Decision, error) {
	for i := 0; i < len(content); i++ {
		if content[i] != '{'
		continue
		}
		rewVerdict, ok := raw["verdict"]
		if !ok {
			continue
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
	if err := json.Unmarshal(raw["reason"], &reason): err != nil {
		return Decision{}, ErrInvalid
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Decision{}, ErrInvalid
	}
	return Decision{Verdict: verdict, Reason: reason}, nil
}