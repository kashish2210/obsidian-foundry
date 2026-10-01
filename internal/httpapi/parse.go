package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"pocketful/internal/apierr"
	"pocketful/internal/money"
	"pocketful/internal/store"
)

const (
	maxBodyBytes  = 1 << 20
	maxNoteRunes  = 200
	maxKeyLen     = 255
	maxTransfers  = 32
	defaultLimit  = 50
	maxPageLimit  = 200
	maxTestBytes  = 256 << 20
	digitsPattern = `^[0-9]+$`
)

var digits = regexp.MustCompile(digitsPattern)

// readBody reads at most limit bytes of the request body.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return nil, apierr.Malformed("request body could not be read: %v", err)
	}
	return raw, nil
}

// decodeObject parses raw as exactly one JSON object. Numbers stay
// json.Number so their written form never loses exactness.
func decodeObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, apierr.Malformed("body is not valid JSON")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, apierr.Malformed("body has trailing data")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, apierr.Malformed("body must be a JSON object")
	}
	return obj, nil
}

// readObject reads the body as a JSON object. When emptyOK is set an absent
// body counts as {}.
func readObject(w http.ResponseWriter, r *http.Request, emptyOK bool) (map[string]any, error) {
	raw, err := readBody(w, r, maxBodyBytes)
	if err != nil {
		return nil, err
	}
	if emptyOK && len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	return decodeObject(raw)
}

// fingerprint is a canonical text form of a parsed JSON value: key order,
// whitespace and the spelling of numbers do not affect it.
func fingerprint(v any) string {
	var sb strings.Builder
	writeCanonical(&sb, v)
	return sb.String()
}

func writeCanonical(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		sb.WriteString(strconv.FormatBool(t))
	case string:
		sb.WriteString(strconv.Quote(t))
	case json.Number:
		sb.WriteString(money.Canonical(t.String()))
	case []any:
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeCanonical(sb, e)
		}
		sb.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(strconv.Quote(k))
			sb.WriteByte(':')
			writeCanonical(sb, t[k])
		}
		sb.WriteByte('}')
	}
}

// idempotencyKey returns the validated Idempotency-Key header.
func idempotencyKey(r *http.Request) (string, error) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return "", apierr.MissingKey()
	}
	if utf8.RuneCountInString(key) > maxKeyLen {
		return "", apierr.Invalid("Idempotency-Key is longer than %d characters", maxKeyLen)
	}
	return key, nil
}

func amountField(obj map[string]any, key string) (int64, error) {
	v, ok := obj[key]
	if !ok {
		return 0, apierr.Invalid("%s is required", key)
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, apierr.Invalid("%s must be a number", key)
	}
	a, ok := money.ParseIntegral(n.String())
	if !ok || a < 1 || a > money.MaxAmount {
		return 0, apierr.Invalid("%s must be an integer from 1 to %d", key, money.MaxAmount)
	}
	return a, nil
}

func noteField(obj map[string]any) (string, error) {
	v, ok := obj["note"]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", apierr.Invalid("note must be a string")
	}
	if utf8.RuneCountInString(s) > maxNoteRunes {
		return "", apierr.Invalid("note is longer than %d characters", maxNoteRunes)
	}
	return s, nil
}

func visibilityField(obj map[string]any) (string, error) {
	v, ok := obj["visibility"]
	if !ok {
		return store.VisibilityPublic, nil
	}
	s, ok := v.(string)
	if !ok || (s != store.VisibilityPublic && s != store.VisibilityPrivate) {
		return "", apierr.Invalid("visibility must be %q or %q", store.VisibilityPublic, store.VisibilityPrivate)
	}
	return s, nil
}

// stringField reads a required string; a value of another type is malformed.
func stringField(obj map[string]any, key string) (string, error) {
	v, ok := obj[key]
	if !ok {
		return "", apierr.Invalid("%s is required", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", apierr.Malformed("%s must be a string", key)
	}
	return s, nil
}

func parsePayment(obj map[string]any) (store.PaymentInput, error) {
	var in store.PaymentInput
	var err error
	if in.ToHandle, err = stringField(obj, "to_handle"); err != nil {
		return in, err
	}
	if in.Amount, err = amountField(obj, "amount"); err != nil {
		return in, err
	}
	if in.Note, err = noteField(obj); err != nil {
		return in, err
	}
	in.Visibility, err = visibilityField(obj)
	return in, err
}

func parseRequest(obj map[string]any) (store.RequestInput, error) {
	var in store.RequestInput
	var err error
	if in.PayerHandle, err = stringField(obj, "payer_handle"); err != nil {
		return in, err
	}
	if in.Amount, err = amountField(obj, "amount"); err != nil {
		return in, err
	}
	in.Note, err = noteField(obj)
	return in, err
}

func parseSplit(obj map[string]any) (store.SplitInput, error) {
	var in store.SplitInput
	var err error
	if in.Amount, err = amountField(obj, "amount"); err != nil {
		return in, err
	}
	raw, ok := obj["participant_handles"]
	if !ok {
		return in, apierr.Invalid("participant_handles is required")
	}
	list, ok := raw.([]any)
	if !ok {
		return in, apierr.Malformed("participant_handles must be an array")
	}
	seen := map[string]bool{}
	for _, e := range list {
		h, ok := e.(string)
		if !ok {
			return in, apierr.Malformed("participant_handles must contain strings")
		}
		if seen[h] {
			return in, apierr.Invalid("participant_handles contains a duplicate")
		}
		seen[h] = true
		in.Handles = append(in.Handles, h)
	}
	if len(in.Handles) == 0 {
		return in, apierr.Invalid("participant_handles must not be empty")
	}
	in.Note, err = noteField(obj)
	return in, err
}

func parseTransfers(obj map[string]any) ([]store.Transfer, error) {
	raw, ok := obj["transfers"]
	if !ok {
		return nil, apierr.Invalid("transfers is required")
	}
	list, ok := raw.([]any)
	if !ok || len(list) < 1 || len(list) > maxTransfers {
		return nil, apierr.Invalid("transfers must be an array of 1 to %d objects", maxTransfers)
	}
	entries := make([]map[string]any, len(list))
	for i, e := range list {
		if entries[i], ok = e.(map[string]any); !ok {
			return nil, apierr.Invalid("transfers[%d] must be an object", i)
		}
	}
	transfers := make([]store.Transfer, len(entries))
	for i, e := range entries {
		transfers[i] = parseTransfer(e)
	}
	return transfers, nil
}

// parseTransfer never fails; a bad entry carries its error in Invalid so
// Settle can report the first failing entry in input order.
func parseTransfer(e map[string]any) store.Transfer {
	t := store.Transfer{}
	fail := func(err error) store.Transfer {
		var ae *apierr.Error
		if errors.As(err, &ae) && ae.Status == http.StatusBadRequest {
			err = apierr.Invalid("%s", ae.Message)
		}
		t.Invalid = err
		return t
	}
	var ferr error
	if t.FromHandle, ferr = stringField(e, "from_handle"); ferr != nil {
		return fail(ferr)
	}
	if t.ToHandle, ferr = stringField(e, "to_handle"); ferr != nil {
		return fail(ferr)
	}
	if t.Amount, ferr = amountField(e, "amount"); ferr != nil {
		return fail(ferr)
	}
	if t.Note, ferr = noteField(e); ferr != nil {
		return fail(ferr)
	}
	if t.Visibility, ferr = visibilityField(e); ferr != nil {
		return fail(ferr)
	}
	return t
}

// intParam reads an optional plain-digits integer query parameter.
func intParam(q url.Values, name string, def, min, max int) (int, error) {
	vals, ok := q[name]
	if !ok {
		return def, nil
	}
	s := vals[0]
	if !digits.MatchString(s) {
		return 0, apierr.Invalid("%s must be plain decimal digits", name)
	}
	if len(s) > 15 {
		if max == 0 {
			return int(^uint(0) >> 1), nil
		}
		return 0, apierr.Invalid("%s is out of range", name)
	}
	n, _ := strconv.Atoi(s)
	if n < min || (max > 0 && n > max) {
		return 0, apierr.Invalid("%s is out of range", name)
	}
	return n, nil
}

func parsePage(q url.Values) (store.Page, error) {
	limit, err := intParam(q, "limit", defaultLimit, 1, maxPageLimit)
	if err != nil {
		return store.Page{}, err
	}
	offset, err := intParam(q, "offset", 0, 0, 0)
	return store.Page{Limit: limit, Offset: offset}, err
}
