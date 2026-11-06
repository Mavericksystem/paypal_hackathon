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