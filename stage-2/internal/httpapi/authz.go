package httpapi

import (
	"net/http"

	"pocketful/internal/apierr"
	"pocketful/internal/store"
)

// parseCapture reads {amount?, final?}. A missing amount means the whole
// remainder and a missing final means true; a non-boolean final is a wrong
// JSON type.
func parseCapture(obj map[string]any) (store.CaptureInput, error) {
	in := store.CaptureInput{Final: true}
	if _, ok := obj["amount"]; ok {
		amount, err := amountField(obj, "amount")
		if err != nil {
			return in, err
		}
		in.Amount = &amount
	}
	if v, ok := obj["final"]; ok {
		final, isBool := v.(bool)
		if !isBool {
			return in, apierr.Malformed("final must be a boolean")
		}
		in.Final = final
	}
	return in, nil
}

func (s *Server) createAuthorization(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{run: func(tx *store.Tx, caller string, body map[string]any, _ *http.Request) (any, error) {
		in, err := parsePayment(body)
		if err != nil {
			return nil, err
		}
		return tx.CreateAuthorization(caller, in)
	}})(w, r)
}

func (s *Server) capture(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{emptyBodyOK: true, run: func(tx *store.Tx, caller string, body map[string]any, r *http.Request) (any, error) {
		in, err := parseCapture(body)
		if err != nil {
			return nil, err
		}
		return tx.Capture(caller, r.PathValue("id"), in)
	}})(w, r)
}

func (s *Server) voidAuthorization(w http.ResponseWriter, r *http.Request, sess session) {
	var view store.AuthorizationView
	err := s.update(sess, func(tx *store.Tx) error {
		var verr error
		view, verr = tx.Void(sess.userID, r.PathValue("id"))
		return verr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listAuthorizations(w http.ResponseWriter, r *http.Request, sess session) {
	q := r.URL.Query()
	var f store.AuthorizationFilter
	f.Direction = q.Get("direction")
	if _, ok := q["direction"]; ok && f.Direction != "incoming" && f.Direction != "outgoing" {
		writeError(w, apierr.Invalid("direction must be incoming or outgoing"))
		return
	}
	f.Status = q.Get("status")
	if _, ok := q["status"]; ok {
		switch f.Status {
		case store.AuthOpen, store.AuthCaptured, store.AuthVoided, store.AuthExpired:
		default:
			writeError(w, apierr.Invalid("status must be open, captured, voided or expired"))
			return
		}
	}
	pg, err := parsePage(q)
	if err != nil {
		writeError(w, err)
		return
	}
	var items []store.AuthorizationView
	var more bool
	err = s.view(sess, func(tx *store.Tx) error {
		items, more = tx.ListAuthorizations(sess.userID, f, pg)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authorizations": items, "has_more": more})
}
