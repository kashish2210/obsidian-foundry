package acceptance

import (
	"encoding/json"
	"testing"
	"time"
)

func meObj(t testing.TB, tok string) map[string]any {
	t.Helper()
	r := get(t, "/me", tok)
	expect(t, r, 200)
	return r.obj(t)
}

func avail(t testing.TB, tok string) int64 { t.Helper(); return num(t, meObj(t, tok), "available") }
func held(t testing.TB, tok string) int64  { t.Helper(); return num(t, meObj(t, tok), "held") }
func total(t testing.TB, tok string) int64 { t.Helper(); return num(t, meObj(t, tok), "total") }

func aid(t testing.TB, m map[string]any) string { t.Helper(); return str(t, m, "authorization_id") }

func isoIn(d time.Duration) string {
	return time.Now().UTC().Add(d).Format("2006-01-02T15:04:05") + "+00:00"
}

func authorize(t testing.TB, tok, to string, amount any, extra map[string]any) resp {
	t.Helper()
	b := map[string]any{"to_handle": to, "amount": amount}
	for k, v := range extra {
		b[k] = v
	}
	return postK(t, "/authorizations", tok, b)
}

func mustAuthorize(t testing.TB, tok, to string, amount int64, extra map[string]any) map[string]any {
	t.Helper()
	r := authorize(t, tok, to, amount, extra)
	expect(t, r, 201)
	return r.obj(t)
}

func capture(t testing.TB, tok, id string, body any) resp {
	t.Helper()
	return postK(t, "/authorizations/"+id+"/capture", tok, body)
}

func mustCapture(t testing.TB, tok, id string, body any) map[string]any {
	t.Helper()
	r := capture(t, tok, id, body)
	expect(t, r, 201)
	return r.obj(t)
}

func voidA(t testing.TB, tok, id string) resp {
	t.Helper()
	return postNoKey(t, "/authorizations/"+id+"/void", tok, nil)
}

func listAuthz(t testing.TB, tok, q string) ([]map[string]any, bool) {
	t.Helper()
	return list(t, "/authorizations"+q, tok, "authorizations")
}

func getAuthz(t testing.TB, tok, id string) map[string]any {
	t.Helper()
	items, _ := listAuthz(t, tok, "?limit=200")
	for _, it := range items {
		if it["authorization_id"] == id {
			return it
		}
	}
	t.Fatalf("authorization %s not listed", id)
	return nil
}

// fixtureAZ builds a stage-2 fixture with optional ttl (0 = omitted) and seeded authorizations.
func fixtureAZ(ttl any, azs []any, users ...fu) map[string]any {
	f := fixtureCur("EUR", 2, []string{"u_op"}, users...)
	if ttl != nil {
		f["authorization_ttl_seconds"] = ttl
	}
	if azs != nil {
		f["authorizations"] = azs
	}
	return f
}

func seedAZ(id, from, to string, amount int64, status, expires string) map[string]any {
	return map[string]any{"id": id, "from_user_id": "u_" + from, "to_user_id": "u_" + to, "amount": amount,
		"note": "seed " + id, "visibility": "public", "status": status, "expires_at": expires}
}

func setupAZ(t testing.TB, ttl any, azs []any, users ...fu) *env {
	t.Helper()
	reset(t, fixtureAZ(ttl, azs, users...))
	e := &env{tok: map[string]string{}}
	for _, u := range users {
		e.tok[u.h] = login(t, u.h+"@example.com")
		e.total += u.bal
	}
	return e
}

func ints(v any) []string {
	out := []string{}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// checkInvariantsMe validates the per-read invariants of one /me object.
func checkMe(t testing.TB, m map[string]any) {
	t.Helper()
	tot, av, h, b := num(t, m, "total"), num(t, m, "available"), num(t, m, "held"), num(t, m, "balance")
	if tot != b || av != tot-h || av < 0 || h < 0 {
		t.Fatalf("/me invariant broken: %v", m)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
