package store

import (
	"encoding/json"
	"fmt"

	"pocketful/internal/apierr"
)

// Idempotent runs a write at most once per (user, path, key).
//
// A claimed key is resolved before run executes: the same fingerprint
// replays the stored response (replayed=true), a different one is a key
// reuse error. Only a successful run claims the key, so a key whose
// original attempt failed can be used again. run executes under the same
// lock as the claim, which makes concurrent identical requests collapse to
// one effect.
func (tx *Tx) Idempotent(userID, path, key, fingerprint string, run func() (any, error)) (body []byte, replayed bool, err error) {
	k := idemKey(userID, path, key)
	if rec := tx.st.idem[k]; rec != nil {
		if rec.Fingerprint != fingerprint {
			return nil, false, apierr.KeyReuse()
		}
		return rec.Response, true, nil
	}
	result, err := run()
	if err != nil {
		return nil, false, err
	}
	body, err = json.Marshal(result)
	if err != nil {
		return nil, false, fmt.Errorf("encode response: %w", err)
	}
	rec := &IdempotencyRecord{UserID: userID, Path: path, Key: key, Fingerprint: fingerprint, Response: body}
	tx.st.Idempotency = append(tx.st.Idempotency, rec)
	tx.st.idem[k] = rec
	return body, false, nil
}
