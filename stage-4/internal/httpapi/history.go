package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"

	"pocketful/internal/apierr"
	"pocketful/internal/money"
	"pocketful/internal/store"
)

const maxReasonRunes = 200

// instantParam reads an optional RFC 3339 instant with an offset from the
// query. A parameter that is present but empty or malformed is invalid.
func instantParam(q url.Values, name string) (*store.Instant, error) {
	vals, ok := q[name]
	if !ok {
		return nil, nil
	}
	at, err := store.ParseInstant(vals[0])
	if err != nil {
		return nil, apierr.Invalid("%s must be an RFC 3339 instant with an offset", name)
	}
	return &at, nil
}

// parseCorrection reads the correction body; every field is required and
// a value of the wrong type is a validation failure.
func parseCorrection(obj map[string]any) (store.CorrectionInput, error) {
	var in store.CorrectionInput
	rev, err := integerField(obj, "expected_revision", 1, 1<<53)
	if err != nil {
		return in, err
	}
	in.ExpectedRevision = rev
	if in.Amount, err = integerField(obj, "amount", 0, money.MaxAmount); err != nil {
		return in, err
	}
	text, ok := obj["effective_at"].(string)
	if !ok {
		return in, apierr.Invalid("effective_at must be an RFC 3339 instant with an offset")
	}
	if in.EffectiveAt, err = store.ParseInstant(text); err != nil {
		return in, apierr.Invalid("effective_at must be an RFC 3339 instant with an offset")
	}
	if in.EffectiveAt.T.After(time.Now()) {
		return in, apierr.Invalid("effective_at must not be in the future")
	}
	reason, ok := obj["reason"].(string)
	if !ok || utf8.RuneCountInString(reason) < 1 || utf8.RuneCountInString(reason) > maxReasonRunes {
		return in, apierr.Invalid("reason must be a string of 1 to %d characters", maxReasonRunes)
	}
	in.Reason = reason
	return in, nil
}

// integerField reads a required integral number within [min, max].
func integerField(obj map[string]any, key string, min, max int64) (int64, error) {
	n, ok := obj[key].(json.Number)
	if !ok {
		return 0, apierr.Invalid("%s must be an integer", key)
	}
	v, ok := money.ParseIntegral(n.String())
	if !ok || v < min || v > max {
		return 0, apierr.Invalid("%s must be an integer from %d to %d", key, min, max)
	}
	return v, nil
}

func (s *Server) correct(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{run: func(tx *store.Tx, caller string, body map[string]any, r *http.Request) (any, error) {
		in, err := parseCorrection(body)
		if err != nil {
			return nil, err
		}
		return tx.Correct(caller, r.PathValue("id"), in)
	}})(w, r)
}

func (s *Server) revisions(w http.ResponseWriter, r *http.Request, sess session) {
	var revs []store.RevisionView
	err := s.view(sess, func(tx *store.Tx) error {
		var rerr error
		revs, rerr = tx.Revisions(sess.userID, r.PathValue("id"))
		return rerr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": revs})
}

func (s *Server) statement(w http.ResponseWriter, r *http.Request, sess session) {
	q := r.URL.Query()
	pg, pageErr := parsePage(q)
	var view store.StatementView
	if token, paged := q["snapshot"]; paged {
		for _, name := range []string{"from", "to", "known_at"} {
			if _, has := q[name]; has {
				writeError(w, apierr.Invalid("%s cannot be combined with snapshot", name))
				return
			}
		}
		if pageErr != nil {
			writeError(w, pageErr)
			return
		}
		err := s.view(sess, func(tx *store.Tx) error {
			var serr error
			view, serr = tx.StatementSnapshot(sess.userID, token[0], pg)
			return serr
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	var sq store.StatementQuery
	var err error
	if sq.From, err = instantParam(q, "from"); err == nil {
		if sq.To, err = instantParam(q, "to"); err == nil {
			sq.KnownAt, err = instantParam(q, "known_at")
		}
	}
	if err == nil {
		err = pageErr
	}
	if err != nil {
		writeError(w, err)
		return
	}
	err = s.update(sess, func(tx *store.Tx) error {
		var serr error
		view, serr = tx.Statement(sess.userID, sq, pg)
		return serr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
