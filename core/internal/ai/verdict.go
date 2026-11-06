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