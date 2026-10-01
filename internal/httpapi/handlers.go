package httpapi

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"pocketful/internal/apierr"
	"pocketful/internal/password"
	"pocketful/internal/store"
)

const minPasswordRunes = 8

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func validEmail(e string) bool {
	local, domain, ok := strings.Cut(e, "@")
	return ok && local != "" && domain != "" && !strings.ContainsAny(e, " \t\r\n") && !strings.Contains(domain, "@")
}

// credentialFields reads the fields signup and login share.
func credentialFields(obj map[string]any) (email, pw string, err error) {
	if email, err = stringField(obj, "email"); err != nil {
		return
	}
	pw, err = stringField(obj, "password")
	return
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	obj, err := readObject(w, r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	email, pw, err := credentialFields(obj)
	if err == nil {
		var display string
		if display, err = stringField(obj, "display_name"); err == nil {
			switch {
			case !validEmail(email):
				err = apierr.Invalid("email must be of the form local@domain")
			case utf8.RuneCountInString(pw) < minPasswordRunes:
				err = apierr.Invalid("password must be at least %d characters", minPasswordRunes)
			case display == "":
				err = apierr.Invalid("display_name must not be empty")
			}
			if err == nil {
				s.createAccount(w, email, pw, display)
				return
			}
		}
	}
	writeError(w, err)
}

func (s *Server) createAccount(w http.ResponseWriter, email, pw, display string) {
	hash, err := password.Hash(pw)
	if err != nil {
		writeError(w, err)
		return
	}
	var sess store.Session
	err = s.store.Update(func(tx *store.Tx) error {
		var serr error
		sess, serr = tx.Signup(email, display, hash)
		return serr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	obj, err := readObject(w, r, false)
	if err != nil {
		writeError(w, err)
		return
	}
	email, pw, err := credentialFields(obj)
	if err != nil {
		writeError(w, err)
		return
	}
	var creds store.Credentials
	var found bool
	_ = s.store.View(func(tx *store.Tx) error {
		creds, found = tx.CredentialsFor(email)
		return nil
	})
	if !found || !password.Verify(pw, creds.Hash) {
		writeError(w, apierr.Unauthenticated("wrong email or password"))
		return
	}
	var token string
	err = s.store.Update(func(tx *store.Tx) error {
		var terr error
		token, terr = tx.IssueToken(creds.UserID, creds.Hash)
		return terr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, store.Session{UserID: creds.UserID, DisplayName: creds.DisplayName, Token: token})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, sess session) {
	q := r.URL.Query()
	var tq store.TemporalQuery
	var err error
	if tq.AsOf, err = instantParam(q, "as_of"); err == nil {
		tq.KnownAt, err = instantParam(q, "known_at")
	}
	if err != nil {
		writeError(w, err)
		return
	}
	var view store.MeView
	err = s.view(sess, func(tx *store.Tx) error {
		var merr error
		view, merr = tx.MeAt(sess.userID, tq)
		return merr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// idempotentWrite describes one of the five idempotent write endpoints.
type idempotentWrite struct {
	emptyBodyOK bool
	// gate runs right after authentication, before the key is looked at.
	gate func(tx *store.Tx, caller string) error
	// run validates the body and performs the write.
	run func(tx *store.Tx, caller string, body map[string]any, r *http.Request) (any, error)
}

func (s *Server) idempotent(op idempotentWrite) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.authenticate(r)
		caller := sess.userID
		if err == nil && op.gate != nil {
			err = s.view(sess, func(tx *store.Tx) error { return op.gate(tx, caller) })
		}
		if err != nil {
			writeError(w, err)
			return
		}
		key, err := idempotencyKey(r)
		if err != nil {
			writeError(w, err)
			return
		}
		body, err := readObject(w, r, op.emptyBodyOK)
		if err != nil {
			writeError(w, err)
			return
		}
		var resp []byte
		var replayed bool
		err = s.update(sess, func(tx *store.Tx) error {
			var ierr error
			resp, replayed, ierr = tx.Idempotent(caller, r.URL.Path, key, fingerprint(body), func() (any, error) {
				return op.run(tx, caller, body, r)
			})
			return ierr
		})
		if err != nil {
			writeError(w, err)
			return
		}
		status := http.StatusCreated
		if replayed {
			status = http.StatusOK
		}
		writeRaw(w, status, resp)
	}
}

func (s *Server) sendPayment(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{run: func(tx *store.Tx, caller string, body map[string]any, _ *http.Request) (any, error) {
		in, err := parsePayment(body)
		if err != nil {
			return nil, err
		}
		return tx.SendPayment(caller, in)
	}})(w, r)
}

func (s *Server) createRequest(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{run: func(tx *store.Tx, caller string, body map[string]any, _ *http.Request) (any, error) {
		in, err := parseRequest(body)
		if err != nil {
			return nil, err
		}
		return tx.CreateRequest(caller, in)
	}})(w, r)
}

func (s *Server) payRequest(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{emptyBodyOK: true, run: func(tx *store.Tx, caller string, body map[string]any, r *http.Request) (any, error) {
		visibility, err := visibilityField(body)
		if err != nil {
			return nil, err
		}
		return tx.PayRequest(caller, r.PathValue("id"), visibility)
	}})(w, r)
}

func (s *Server) createSplit(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{run: func(tx *store.Tx, caller string, body map[string]any, _ *http.Request) (any, error) {
		in, err := parseSplit(body)
		if err != nil {
			return nil, err
		}
		return tx.CreateSplit(caller, in)
	}})(w, r)
}

func (s *Server) settle(w http.ResponseWriter, r *http.Request) {
	s.idempotent(idempotentWrite{
		gate: requireOperator,
		run: func(tx *store.Tx, caller string, body map[string]any, _ *http.Request) (any, error) {
			transfers, err := parseTransfers(body)
			if err != nil {
				return nil, err
			}
			return tx.Settle(transfers)
		},
	})(w, r)
}

func (s *Server) declineRequest(w http.ResponseWriter, r *http.Request, sess session) {
	s.closeRequest(w, r, sess, (*store.Tx).Decline)
}

func (s *Server) cancelRequest(w http.ResponseWriter, r *http.Request, sess session) {
	s.closeRequest(w, r, sess, (*store.Tx).Cancel)
}

func (s *Server) closeRequest(w http.ResponseWriter, r *http.Request, sess session, op func(*store.Tx, string, string) (store.RequestView, error)) {
	var view store.RequestView
	err := s.update(sess, func(tx *store.Tx) error {
		var oerr error
		view, oerr = op(tx, sess.userID, r.PathValue("id"))
		return oerr
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) listRequests(w http.ResponseWriter, r *http.Request, sess session) {
	q := r.URL.Query()
	var f store.RequestFilter
	f.Direction = q.Get("direction")
	if _, ok := q["direction"]; ok && f.Direction != "incoming" && f.Direction != "outgoing" {
		writeError(w, apierr.Invalid("direction must be incoming or outgoing"))
		return
	}
	f.Status = q.Get("status")
	if _, ok := q["status"]; ok {
		switch f.Status {
		case store.StatusPending, store.StatusPaid, store.StatusDeclined, store.StatusCancelled:
		default:
			writeError(w, apierr.Invalid("status must be pending, paid, declined or cancelled"))
			return
		}
	}
	pg, err := parsePage(q)
	if err != nil {
		writeError(w, err)
		return
	}
	var items []store.RequestView
	var more bool
	err = s.view(sess, func(tx *store.Tx) error {
		items, more = tx.ListRequests(sess.userID, f, pg)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": items, "has_more": more})
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request, sess session) {
	pg, err := parsePage(r.URL.Query())
	if err != nil {
		writeError(w, err)
		return
	}
	var items []store.PaymentView
	var more bool
	err = s.view(sess, func(tx *store.Tx) error {
		items, more = tx.Activity(sess.userID, pg)
		return nil
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": items, "has_more": more})
}
