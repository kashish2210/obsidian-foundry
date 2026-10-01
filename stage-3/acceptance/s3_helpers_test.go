package acceptance

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"
)

// ---- time helpers ----

func tsStr(tm time.Time) string { return tm.UTC().Format("2006-01-02T15:04:05") + "+00:00" }

func tsMicro(tm time.Time) string { return tm.UTC().Format("2006-01-02T15:04:05.000000") + "+00:00" }

func qe(s string) string { return url.QueryEscape(s) }

func parseTS(t testing.TB, s string) time.Time { t.Helper(); return checkTS(t, s) }

func ago(d time.Duration) time.Time { return time.Now().UTC().Truncate(time.Second).Add(-d) }

const (
	day  = 24 * time.Hour
	hour = time.Hour
)

// ---- seeding ----

func seedP(id, from, to string, amount int64, vis string, at time.Time) map[string]any {
	return map[string]any{"id": id, "from_user_id": "u_" + from, "to_user_id": "u_" + to, "amount": amount,
		"note": "seed " + id, "visibility": vis, "created_at": tsStr(at)}
}

func fixtureP(ops []string, payments []any, users ...fu) map[string]any {
	f := fixtureCur("EUR", 2, ops, users...)
	if payments != nil {
		f["payments"] = payments
	}
	return f
}

func setupP(t testing.TB, payments []any, users ...fu) *env {
	t.Helper()
	reset(t, fixtureP([]string{"u_op"}, payments, users...))
	e := &env{tok: map[string]string{}}
	for _, u := range users {
		e.tok[u.h] = login(t, u.h+"@example.com")
		e.total += u.bal
	}
	return e
}

// scenario A: three seeded payments at known instants.
//
//	s_1 ada->bob 500 public  at D-5d
//	s_2 bob->cy  200 private at D-4d
//	s_3 ada->cy  100 public  at D-3d
//
// finals ada 10000, bob 2500, cy 500, dee 0, op 0
// opening: ada 10600, bob 2200, cy 200
type scenA struct {
	e          *env
	t1, t2, t3 time.Time
}

func setupA(t testing.TB) *scenA {
	t.Helper()
	s := &scenA{t1: ago(5 * day), t2: ago(4 * day), t3: ago(3 * day)}
	s.e = setupP(t, []any{
		seedP("s_1", "ada", "bob", 500, "public", s.t1),
		seedP("s_2", "bob", "cy", 200, "private", s.t2),
		seedP("s_3", "ada", "cy", 100, "public", s.t3),
	}, azUsers...)
	return s
}

// ---- reads ----

func meAt(t testing.TB, tok, query string) map[string]any {
	t.Helper()
	p := "/me"
	if query != "" {
		p += "?" + query
	}
	r := get(t, p, tok)
	expect(t, r, 200)
	return r.obj(t)
}

func balAt(t testing.TB, tok, query string) int64 {
	t.Helper()
	return num(t, meAt(t, tok, query), "balance")
}

func stmt(t testing.TB, tok, query string) map[string]any {
	t.Helper()
	p := "/statement"
	if query != "" {
		p += "?" + query
	}
	r := get(t, p, tok)
	expect(t, r, 200)
	return r.obj(t)
}

func entriesOf(t testing.TB, m map[string]any) []map[string]any {
	t.Helper()
	arr, ok := m["entries"].([]any)
	if !ok {
		t.Fatalf("entries is not an array: %v", m)
	}
	out := []map[string]any{}
	for _, a := range arr {
		out = append(out, a.(map[string]any))
	}
	return out
}

func entryPID(t testing.TB, e map[string]any) string {
	t.Helper()
	return str(t, e["payment"].(map[string]any), "payment_id")
}

func entryIDs(t testing.TB, m map[string]any) []string {
	t.Helper()
	var ids []string
	for _, e := range entriesOf(t, m) {
		ids = append(ids, entryPID(t, e))
	}
	return ids
}

func strEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkStatementConsistent verifies opening + deltas == closing and running balance_after on a full-window read.
func checkStatementConsistent(t testing.TB, m map[string]any) {
	t.Helper()
	run := num(t, m, "opening_balance")
	for _, e := range entriesOf(t, m) {
		d := num(t, e, "delta")
		run += d
		if num(t, e, "balance_after") != run {
			t.Fatalf("balance_after %d != running %d in %v", num(t, e, "balance_after"), run, e)
		}
		pa := num(t, e["payment"].(map[string]any), "amount")
		if d != pa && d != -pa {
			t.Fatalf("delta %d does not match payment amount %d", d, pa)
		}
	}
	if run != num(t, m, "closing_balance") {
		t.Fatalf("opening + deltas = %d but closing_balance = %d", run, num(t, m, "closing_balance"))
	}
}

func fullStmt(t testing.TB, tok, query string) map[string]any {
	t.Helper()
	if query != "" {
		query += "&"
	}
	return stmt(t, tok, query+"limit=200")
}

// ---- corrections ----

func corrBody(rev int64, amount any, eff any, reason any) map[string]any {
	if tm, ok := eff.(time.Time); ok {
		eff = tsStr(tm)
	}
	return map[string]any{"expected_revision": rev, "amount": amount, "effective_at": eff, "reason": reason}
}

func correct(t testing.TB, tok, pid string, body any) resp {
	t.Helper()
	return postK(t, "/payments/"+pid+"/corrections", tok, body)
}

func mustCorrect(t testing.TB, tok, pid string, rev int64, amount int64, eff time.Time, reason string) map[string]any {
	t.Helper()
	r := correct(t, tok, pid, corrBody(rev, amount, eff, reason))
	expect(t, r, 201)
	return r.obj(t)
}

func revisionsOf(t testing.TB, tok, pid string) []map[string]any {
	t.Helper()
	r := get(t, "/payments/"+pid+"/revisions", tok)
	expect(t, r, 200)
	arr, ok := r.obj(t)["revisions"].([]any)
	if !ok {
		t.Fatalf("revisions: %s", r)
	}
	out := []map[string]any{}
	for _, a := range arr {
		out = append(out, a.(map[string]any))
	}
	return out
}

// sumAt sums balance of every user token at the given query (as_of/known_at).
func sumAt(t testing.TB, toks map[string]string, query string) int64 {
	t.Helper()
	var s int64
	for _, tk := range toks {
		s += balAt(t, tk, query)
	}
	return s
}

func readFileT(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
