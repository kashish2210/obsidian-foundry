// Package httpapi exposes the store over HTTP: routing, request parsing,
// authentication and error rendering.
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"pocketful/internal/apierr"
	"pocketful/internal/store"
)

const contentType = "application/json; charset=utf-8"

// Server routes HTTP requests to the store.
type Server struct {
	store *store.Store
}

// New returns the HTTP handler for the service.
func New(s *store.Store) http.Handler {
	srv := &Server{store: s}
	mux := http.NewServeMux()
	route := func(pattern string, handlers map[string]http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			h, ok := handlers[r.Method]
			if !ok {
				writeError(w, apierr.New(http.StatusMethodNotAllowed, "method_not_allowed", "method %s is not allowed here", r.Method))
				return
			}
			h(w, r)
		})
	}
	route("/health", map[string]http.HandlerFunc{"GET": srv.health})
	route("/_test/reset", map[string]http.HandlerFunc{"POST": srv.reset})
	route("/_test/export", map[string]http.HandlerFunc{"GET": srv.export})
	route("/_test/import", map[string]http.HandlerFunc{"POST": srv.importState})
	route("/auth/signup", map[string]http.HandlerFunc{"POST": srv.signup})
	route("/auth/login", map[string]http.HandlerFunc{"POST": srv.login})
	route("/me", map[string]http.HandlerFunc{"GET": srv.authed(srv.me)})
	route("/payments", map[string]http.HandlerFunc{"POST": srv.sendPayment})
	route("/requests", map[string]http.HandlerFunc{"POST": srv.createRequest, "GET": srv.authed(srv.listRequests)})
	route("/requests/{id}/pay", map[string]http.HandlerFunc{"POST": srv.payRequest})
	route("/requests/{id}/decline", map[string]http.HandlerFunc{"POST": srv.authed(srv.declineRequest)})
	route("/requests/{id}/cancel", map[string]http.HandlerFunc{"POST": srv.authed(srv.cancelRequest)})
	route("/splits", map[string]http.HandlerFunc{"POST": srv.createSplit})
	route("/activity", map[string]http.HandlerFunc{"GET": srv.authed(srv.activity)})
	route("/settlements", map[string]http.HandlerFunc{"POST": srv.settle})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, apierr.NotFound("no such route"))
	})
	return recoverPanics(mux)
}

func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("panic serving %s %s: %v", r.Method, r.URL.Path, p)
				writeError(w, errors.New("panic"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeError(w, err)
		return
	}
	writeRaw(w, status, body)
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeError renders err; anything that is not an apierr.Error is an
// internal failure.
func writeError(w http.ResponseWriter, err error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		log.Printf("internal error: %v", err)
		ae = apierr.New(http.StatusInternalServerError, "internal_error", "internal error")
	}
	var body errorBody
	body.Error.Code, body.Error.Message = ae.Code, ae.Message
	out, _ := json.Marshal(body)
	writeRaw(w, ae.Status, out)
}

// bearerToken extracts the token from an Authorization header.
func bearerToken(r *http.Request) (string, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", apierr.Unauthenticated("missing or malformed bearer token")
	}
	return token, nil
}

// authenticate resolves the caller's user id from the bearer token.
func (s *Server) authenticate(r *http.Request) (string, error) {
	token, err := bearerToken(r)
	if err != nil {
		return "", err
	}
	var uid string
	err = s.store.View(func(tx *store.Tx) error {
		var aerr error
		uid, aerr = tx.Authenticate(token)
		return aerr
	})
	return uid, err
}

// authed wraps a handler that only needs the caller's id.
func (s *Server) authed(h func(w http.ResponseWriter, r *http.Request, caller string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller, err := s.authenticate(r)
		if err != nil {
			writeError(w, err)
			return
		}
		h(w, r, caller)
	}
}
