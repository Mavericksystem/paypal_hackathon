package audit
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const lockKey int64 = 7243001

var genesis = make([]byte, 32)

type Entry struct {
	Seq      int64
	TS       time.Time
	IntentID string // empty when the event is not tied to an intent
	Actor    string
	Event    string
	Payload  json.RawMessage // canonical JSON
	PrevHash []byte
	Hash     []byte
}

type hashInput struct {
	Seq      int64           `json:"seq"`
	TS       int64           `json:"ts"`
	IntentID string          `json:"intent_id"`
	Actor    string          `json:"actor"`
	Event    string          `json:"event"`
	Payload  json.RawMessage `json:"payload"`
	PrevHash string          `json:"prev_hash"`
}

func computeHash(e Entry) []byte {
	b, err := json.Marshal(hashInput{
		Seq:      e.Seq,
		TS:       e.TS.UnixMicro(),
		IntentID: e.IntentID,
		Actor:    e.Actor,
		Event:    e.Event,
		Payload:  e.Payload,
		PrevHash: hex.EncodeToString(e.PrevHash),
	})
	if err != nil {
		// hashInput only contains strings, ints and validated JSON.
		panic(fmt.Sprintf("audit: marshal hash input: %v", err))
	}
	sum := sha256.Sum256(b)
	return sum[:]
}

func canonical(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	return json.Marshal(v)
}