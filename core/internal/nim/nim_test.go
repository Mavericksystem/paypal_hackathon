package nim

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/Mavericksystem/warden/core/internal/intents"
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

func TestUserMessageGolden(t *testing.T) {
	got := userMessage(intents.CheckInput{
		Goal: "buy one USB-C cable under 20 dollars", Item: "USB-C cable", Quantity: 1,
		AmountMinor: 1299, Currency: "USD", Merchant: "AudioShop", SellerText: "In stock.",
	})
	want := "User goal: buy one USB-C cable under 20 dollars\n" +
		"Payment request: item='USB-C cable', quantity=1, amount=12.99 USD, merchant=AudioShop\n" +
		"Seller text (untrusted):\n<<<\nIn stock.\n>>>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
