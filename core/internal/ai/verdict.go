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