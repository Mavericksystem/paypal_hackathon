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