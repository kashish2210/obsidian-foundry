package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var baseURL = func() string {
	if v := os.Getenv("BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://localhost:8080"
}()

var transport = &http.Transport{MaxIdleConnsPerHost: 200, MaxIdleConns: 200}

// Normal requests must answer within 5 s, control calls within 10 s (spec §2).
var client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
var ctlClient = &http.Client{Transport: transport, Timeout: 10 * time.Second}

type resp struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r resp) String() string { return fmt.Sprintf("%d %s", r.Status, string(r.Body)) }

func decodeNum(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func (r resp) obj(t testing.TB) map[string]any {
	t.Helper()
	v, err := decodeNum(r.Body)
	m, ok := v.(map[string]any)
	if err != nil || !ok {
		t.Fatalf("response is not a JSON object: %s (%v)", r, err)
	}
	return m
}

func encodeBody(body any) io.Reader {
	switch b := body.(type) {
	case nil:
		return nil
	case string:
		return strings.NewReader(b)
	case []byte:
		return bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			panic(err)
		}
		return bytes.NewReader(raw)
	}
}

// send is goroutine-safe (never calls t.Fatal).
func send(method, path, tok string, hdr map[string]string, body any) (resp, error) {
	return sendWith(client, method, path, tok, hdr, body)
}

func sendWith(c *http.Client, method, path, tok string, hdr map[string]string, body any) (resp, error) {
	rd := encodeBody(body)
	req, err := http.NewRequest(method, baseURL+path, rd)
	if err != nil {
		return resp{}, err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return resp{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return resp{res.StatusCode, b, res.Header}, err
}

func call(t testing.TB, method, path, tok string, hdr map[string]string, body any) resp {
	t.Helper()
	r, err := send(method, path, tok, hdr, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return r
}

func get(t testing.TB, path, tok string) resp { t.Helper(); return call(t, "GET", path, tok, nil, nil) }

// postNoKey is a POST without an idempotency key (decline/cancel/auth).
func postNoKey(t testing.TB, path, tok string, body any) resp {
	t.Helper()
	return call(t, "POST", path, tok, nil, body)
}

// post sends a POST with an idempotency key.
func post(t testing.TB, path, tok, key string, body any) resp {
	t.Helper()
	return call(t, "POST", path, tok, map[string]string{"Idempotency-Key": key}, body)
}

var keyCtr int64

func newKey() string {
	return fmt.Sprintf("k-%d-%d", time.Now().UnixNano(), atomic.AddInt64(&keyCtr, 1))
}

func postK(t testing.TB, path, tok string, body any) resp {
	t.Helper()
	return post(t, path, tok, newKey(), body)
}

func expect(t testing.TB, r resp, status int) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("want status %d, got %s", status, r)
	}
}

func expectErr(t testing.TB, r resp, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("want %d %s, got %s", status, code, r)
	}
	m := r.obj(t)
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("error body lacks error object: %s", r)
	}
	if e["code"] != code {
		t.Fatalf("want error code %q, got %s", code, r)
	}
	if _, ok := e["message"].(string); !ok {
		t.Fatalf("error body lacks string message: %s", r)
	}
}

func str(t testing.TB, m map[string]any, k string) string {
	t.Helper()
	s, ok := m[k].(string)
	if !ok {
		t.Fatalf("field %q is not a string in %v", k, m)
	}
	return s
}

func num(t testing.TB, m map[string]any, k string) int64 {
	t.Helper()
	n, ok := m[k].(json.Number)
	if !ok {
		t.Fatalf("field %q is not a number in %v", k, m)
	}
	i, err := n.Int64()
	if err != nil {
		t.Fatalf("field %q not an integer: %v", k, n)
	}
	return i
}

func isNull(m map[string]any, k string) bool {
	v, ok := m[k]
	return ok && v == nil
}

func sameJSON(a, b []byte) bool {
	x, e1 := decodeNum(a)
	y, e2 := decodeNum(b)
	return e1 == nil && e2 == nil && reflect.DeepEqual(x, y)
}

func requireSameJSON(t testing.TB, a, b resp) {
	t.Helper()
	if !sameJSON(a.Body, b.Body) {
		t.Fatalf("bodies differ:\n  %s\n  %s", a.Body, b.Body)
	}
}

var tsRe = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?[+-]\d\d:\d\d$`)

func checkTS(t testing.TB, s string) time.Time {
	t.Helper()
	if !tsRe.MatchString(s) {
		t.Fatalf("timestamp %q is not RFC 3339 with explicit numeric offset", s)
	}
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("timestamp %q does not parse: %v", s, err)
	}
	return ts
}

// ---- fixtures ----

type fu struct {
	h   string
	bal int64
}

const pw = "correct horse"

func fixtureCur(cur string, mu int, ops []string, users ...fu) map[string]any {
	us := []any{}
	for _, u := range users {
		us = append(us, map[string]any{
			"id": "u_" + u.h, "email": u.h + "@example.com", "password": pw,
			"display_name": strings.ToUpper(u.h[:1]) + u.h[1:], "handle": u.h, "balance": u.bal,
		})
	}
	f := map[string]any{"currency": cur, "minor_units": mu, "users": us}
	if ops != nil {
		f["settlement_operator_ids"] = ops
	}
	return f
}

func stdUsers() []fu {
	return []fu{{"ada", 10000}, {"bob", 2500}, {"cy", 500}, {"dee", 0}, {"op", 0}}
}

func stdFixture() map[string]any {
	return fixtureCur("EUR", 2, []string{"u_op"}, stdUsers()...)
}

func reset(t testing.TB, fx any) {
	t.Helper()
	r, err := sendWith(ctlClient, "POST", "/_test/reset", "", nil, fx)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if r.Status != 204 {
		t.Fatalf("reset: want 204, got %s", r)
	}
}

type env struct {
	tok   map[string]string
	total int64
}

func login(t testing.TB, email string) string {
	t.Helper()
	r := postNoKey(t, "/auth/login", "", map[string]any{"email": email, "password": pw})
	expect(t, r, 200)
	return str(t, r.obj(t), "token")
}

// setupUsers resets with the given users and logs each one in.
func setupUsers(t testing.TB, ops []string, users ...fu) *env {
	t.Helper()
	reset(t, fixtureCur("EUR", 2, ops, users...))
	e := &env{tok: map[string]string{}}
	for _, u := range users {
		e.tok[u.h] = login(t, u.h+"@example.com")
		e.total += u.bal
	}
	return e
}

func setup(t testing.TB) *env { t.Helper(); return setupUsers(t, []string{"u_op"}, stdUsers()...) }

func bal(t testing.TB, tok string) int64 {
	t.Helper()
	r := get(t, "/me", tok)
	expect(t, r, 200)
	return num(t, r.obj(t), "balance")
}

func (e *env) sum(t testing.TB) int64 {
	t.Helper()
	var s int64
	for _, tok := range e.tok {
		s += bal(t, tok)
	}
	return s
}

func sendPay(t testing.TB, tok, to string, amount any, extra map[string]any) resp {
	t.Helper()
	b := map[string]any{"to_handle": to, "amount": amount}
	for k, v := range extra {
		b[k] = v
	}
	return postK(t, "/payments", tok, b)
}

func mustPay(t testing.TB, tok, to string, amount int64, extra map[string]any) map[string]any {
	t.Helper()
	r := sendPay(t, tok, to, amount, extra)
	expect(t, r, 201)
	return r.obj(t)
}

func mustRequest(t testing.TB, tok, payer string, amount int64) map[string]any {
	t.Helper()
	r := postK(t, "/requests", tok, map[string]any{"payer_handle": payer, "amount": amount, "note": "n"})
	expect(t, r, 201)
	return r.obj(t)
}

func list(t testing.TB, path, tok, key string) ([]map[string]any, bool) {
	t.Helper()
	r := get(t, path, tok)
	expect(t, r, 200)
	m := r.obj(t)
	arr, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%s: %q is not an array: %s", path, key, r)
	}
	hm, ok := m["has_more"].(bool)
	if !ok {
		t.Fatalf("%s: has_more is not a bool: %s", path, r)
	}
	out := []map[string]any{}
	for _, a := range arr {
		out = append(out, a.(map[string]any))
	}
	return out, hm
}

func listReq(t testing.TB, tok, q string) ([]map[string]any, bool) {
	t.Helper()
	return list(t, "/requests"+q, tok, "requests")
}

func activity(t testing.TB, tok, q string) ([]map[string]any, bool) {
	t.Helper()
	return list(t, "/activity"+q, tok, "payments")
}

func idSet(t testing.TB, items []map[string]any, key string) map[string]bool {
	t.Helper()
	s := map[string]bool{}
	for _, it := range items {
		s[str(t, it, key)] = true
	}
	return s
}

func tick() { time.Sleep(1100 * time.Millisecond) }
