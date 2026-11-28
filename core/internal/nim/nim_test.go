package nim

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Paste meta.prompt_sha256 from eval/results/*_test_v4_final.json here.
const wantPromptSHA = "PASTE_FROM_RESULTS_JSON"

func TestSystemPromptMatchesEvaluatedPrompt(t *testing.T) {
	if wantPromptSHA == "PASTE_FROM_RESULTS_JSON" {
		t.Skip("set wantPromptSHA from the v4 results file")
	}
	sum := sha256.Sum256([]byte(systemPrompt))
	if got := hex.EncodeToString(sum[:])[:16]; got != wantPromptSHA {
		t.Fatalf("system prompt differs from the evaluated v4 prompt: %s", got)
	}
}
