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

func canonicalPayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return canonical(raw)
}

func Append(ctx context.Context, tx pgx.Tx, intentID, actor, event string, payload any, now time.Time) (Entry, error) {
	canon, err := canonicalPayload(payload)
	if err != nil {
		return Entry{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return Entry{}, fmt.Errorf("lock audit chain: %w", err)
	}

	prevSeq := int64(0)
	prevHash := genesis
	err = tx.QueryRow(ctx, `SELECT seq, hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&prevSeq, &prevHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, fmt.Errorf("read chain head: %w", err)
	}
	e := Entry{
		Seq:      prevSeq + 1,
		TS:       now.UTC().Truncate(time.Microsecond),
		IntentID: intentID,
		Actor:    actor,
		Event:    event,
		Payload:  canon,
		PrevHash: prevHash,
	}
	e.Hash = computeHash(e)

	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (seq, ts, intent_id, actor, event, payload, prev_hash, hash)
		 VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6::jsonb, $7, $8)`,
		e.Seq, e.TS, e.IntentID, e.Actor, e.Event, string(e.Payload), e.PrevHash, e.Hash)
	if err != nil {
		return Entry{}, fmt.Errorf("insert audit entry: %w", err)
	}
	return e, nil
}

type Queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type Result struct {
	OK          bool
	Entries     int64
	BrokenAtSeq int64
	Reason      string
}