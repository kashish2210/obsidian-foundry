package httpapi

import (
	"net/http"

	"pocketful/internal/apierr"
	"pocketful/internal/store"
)

// requireOperator is the gate shared by the operator-only write paths.
func requireOperator(tx *store.Tx, caller string) error {
	if !tx.IsOperator(caller) {
		return apierr.Forbidden("this action requires a settlement operator")
	}
	return nil
}

func (s *Server) refund(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{run: func(tx *store.Tx, caller string, body map[string]any, r *http.Request) (any, error) {
		amount, err := amountField(body, "amount")
		if err != nil {
			return nil, err
		}
		return tx.Refund(caller, r.PathValue("id"), amount)
	}})(w, r)
}

// parseBatch checks the shape of a correction batch; each entry's own
// errors travel with it so CorrectBatch can report the first in input order.
func parseBatch(obj map[string]any) ([]store.BatchItem, error) {
	list, ok := obj["corrections"].([]any)
	if !ok || len(list) < 1 || len(list) > maxTransfers {
		return nil, apierr.Invalid("corrections must be an array of 1 to %d objects", maxTransfers)
	}
	entries := make([]map[string]any, len(list))
	seen := map[string]bool{}
	for i, e := range list {
		entry, ok := e.(map[string]any)
		if !ok {
			return nil, apierr.Invalid("corrections[%d] must be an object", i)
		}
		if id, isString := entry["payment_id"].(string); isString {
			if seen[id] {
				return nil, apierr.Invalid("corrections contains payment %q twice", id)
			}
			seen[id] = true
		}
		entries[i] = entry
	}
	items := make([]store.BatchItem, len(entries))
	for i, entry := range entries {
		id, isString := entry["payment_id"].(string)
		if !isString {
			items[i].Invalid = apierr.Invalid("corrections[%d].payment_id must be a string", i)
			continue
		}
		items[i].PaymentID = id
		items[i].Input, items[i].Invalid = parseCorrection(entry)
	}
	return items, nil
}

func (s *Server) correctBatch(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{
		gate: requireOperator,
		run: func(tx *store.Tx, caller string, body map[string]any, _ *http.Request) (any, error) {
			items, err := parseBatch(body)
			if err != nil {
				return nil, err
			}
			return tx.CorrectBatch(items)
		},
	})(w, r)
}
