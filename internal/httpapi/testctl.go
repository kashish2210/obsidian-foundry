package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"pocketful/internal/apierr"
	"pocketful/internal/money"
	"pocketful/internal/store"
)

const (
	exportTrack         = "pocketful"
	exportFormatVersion = 1
)

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	raw, err := readBody(w, r, maxTestBytes)
	if err != nil {
		writeError(w, err)
		return
	}
	if !json.Valid(raw) {
		writeError(w, apierr.Malformed("body is not valid JSON"))
		return
	}
	var f store.Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		writeError(w, apierr.Invalid("fixture is invalid: %v", err))
		return
	}
	if err := s.store.Reset(f); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request) {
	state, err := s.store.Export()
	if err != nil {
		writeError(w, err)
		return
	}
	var out bytes.Buffer
	out.WriteString(`{"track":"` + exportTrack + `","format_version":1,"state":`)
	out.Write(state)
	out.WriteByte('}')
	writeRaw(w, http.StatusOK, out.Bytes())
}

func (s *Server) importState(w http.ResponseWriter, r *http.Request) {
	raw, err := readBody(w, r, maxTestBytes)
	if err != nil {
		writeError(w, err)
		return
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		writeError(w, apierr.Malformed("body is not a valid JSON object"))
		return
	}
	var track string
	if err := json.Unmarshal(envelope["track"], &track); err != nil || track != exportTrack {
		writeError(w, apierr.Invalid("track must be %q", exportTrack))
		return
	}
	if v, ok := money.ParseIntegral(string(envelope["format_version"])); !ok || v != exportFormatVersion {
		writeError(w, apierr.Invalid("format_version must be %d", exportFormatVersion))
		return
	}
	state := bytes.TrimSpace(envelope["state"])
	if len(state) == 0 || state[0] != '{' {
		writeError(w, apierr.Invalid("state must be an object"))
		return
	}
	if err := s.store.Import(state); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
