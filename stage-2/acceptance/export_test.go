package acceptance

import (
	"encoding/json"
	"reflect"
	"testing"
)

func doExport(t testing.TB) resp {
	t.Helper()
	r, err := sendWith(ctlClient, "GET", "/_test/export", "", nil, nil)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	expect(t, r, 200)
	return r
}

func doImport(t testing.TB, body any) resp {
	t.Helper()
	r, err := sendWith(ctlClient, "POST", "/_test/import", "", nil, body)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return r
}

// rich state: signup, payments (public/private), request lifecycle, split, settlement, a failed key.
type richState struct {
	e        *env
	zedTok   string
	zedID    string
	replays  []replayCase
	failKey  string
	failBody map[string]any
	ids      map[string]bool
}

type replayCase struct {
	method, path, tok, key string
	body                   any
	orig                   resp
}

func buildRich(t testing.TB) *richState {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
	s := &richState{e: e, ids: map[string]bool{}}
	su := signup(t, "zed@example.com", "longenough")
	expect(t, su, 201)
	s.zedTok = str(t, su.obj(t), "token")
	s.zedID = str(t, su.obj(t), "user_id")
	add := func(method, path, tok string, body any, want int) resp {
		k := newKey()
		r := post(t, path, tok, k, body)
		expect(t, r, want)
		s.replays = append(s.replays, replayCase{method, path, tok, k, body, r})
		return r
	}
	add("POST", "/payments", e.tok["ada"], map[string]any{"to_handle": "bob", "amount": 1000, "note": "héllo 🎉"}, 201)
	add("POST", "/payments", e.tok["ada"], map[string]any{"to_handle": "zed", "amount": 300, "visibility": "private"}, 201)
	add("POST", "/payments", e.tok["bob"], map[string]any{"to_handle": "cy", "amount": 50}, 201)
	rq := add("POST", "/requests", e.tok["bob"], map[string]any{"payer_handle": "ada", "amount": 1200, "note": "taxi"}, 201)
	add("POST", "/requests/"+rid(t, rq.obj(t))+"/pay", e.tok["ada"], map[string]any{"visibility": "private"}, 201)
	rq2 := add("POST", "/requests", e.tok["cy"], map[string]any{"payer_handle": "ada", "amount": 10}, 201)
	expect(t, postNoKey(t, "/requests/"+rid(t, rq2.obj(t))+"/cancel", e.tok["cy"], nil), 200)
	rq3 := add("POST", "/requests", e.tok["cy"], map[string]any{"payer_handle": "bob", "amount": 11}, 201)
	expect(t, postNoKey(t, "/requests/"+rid(t, rq3.obj(t))+"/decline", e.tok["bob"], nil), 200)
	add("POST", "/requests", e.tok["cy"], map[string]any{"payer_handle": "dee", "amount": 99}, 201) // stays pending
	add("POST", "/splits", e.tok["ada"], map[string]any{"amount": 1000, "participant_handles": []string{"ada", "bob", "cy"}, "note": "dinner"}, 201)
	add("POST", "/settlements", e.tok["op"], batch(tr("ada", "bob", 100), tr("bob", "cy", 50),
		map[string]any{"from_handle": "cy", "to_handle": "dee", "amount": 5, "visibility": "private"}), 201)
	// a failed (4xx) key that must stay reusable
	s.failKey = newKey()
	s.failBody = map[string]any{"to_handle": "ada", "amount": 5000}
	expectErr(t, post(t, "/payments", e.tok["dee"], s.failKey, s.failBody), 409, "insufficient_funds")
	return s
}

// snapshot reads the observable state of every user (via their tokens) into comparable values.
func snapshot(t testing.TB, toks map[string]string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for h, tok := range toks {
		me := get(t, "/me", tok)
		expect(t, me, 200)
		mv, _ := decodeNum(me.Body)
		a := get(t, "/activity?limit=200", tok)
		expect(t, a, 200)
		av, _ := decodeNum(a.Body)
		rq := get(t, "/requests?limit=200", tok)
		expect(t, rq, 200)
		rv, _ := decodeNum(rq.Body)
		out[h] = map[string]any{"me": mv, "activity": byID(av, "payments", "payment_id"), "requests": byID(rv, "requests", "request_id")}
	}
	return out
}

func byID(v any, key, idk string) map[string]any {
	m := map[string]any{}
	arr, _ := v.(map[string]any)[key].([]any)
	for _, it := range arr {
		o := it.(map[string]any)
		m[o[idk].(string)] = o
	}
	return m
}

func (s *richState) tokens() map[string]string {
	m := map[string]string{"zed": s.zedTok}
	for k, v := range s.e.tok {
		m[k] = v
	}
	return m
}

func TestR104_ExportShape(t *testing.T) {
	buildRich(t)
	r := doExport(t) // no auth header sent
	m := r.obj(t)
	if m["track"] != "pocketful" {
		t.Fatalf("track: %v", m["track"])
	}
	if n, ok := m["format_version"].(json.Number); !ok || n.String() != "1" {
		t.Fatalf("format_version: %v", m["format_version"])
	}
	if _, ok := m["state"].(map[string]any); !ok {
		t.Fatalf("state must be an object: %.200s", r.Body)
	}
	// import of an unchanged export is accepted (R105)
	expect(t, doImport(t, r.Body), 204)
	// an export of an empty-ish fixture also works
	reset(t, fixtureCur("JPY", 0, nil, fu{"ada", 1}))
	expect(t, doImport(t, doExport(t).Body), 204)
}

func TestR105_R106_R109_ImportRestoresEverything(t *testing.T) {
	s := buildRich(t)
	before := snapshot(t, s.tokens())
	exp := doExport(t)

	// Replace with a completely different world, then import.
	reset(t, fixtureCur("BHD", 3, nil, fu{"xavier", 123}, fu{"yara", 9}))
	xtok := login(t, "xavier@example.com")
	expect(t, doImport(t, exp.Body), 204)

	// R111: the previous destination data and credentials are gone
	expectErr(t, get(t, "/me", xtok), 401, "unauthenticated")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "xavier@example.com", "password": pw}), 401, "unauthenticated")
	expectErr(t, sendPay(t, s.e.tok["ada"], "xavier", 1, nil), 404, "not_found")

	// R109: existing bearer tokens still work, and everything observable is identical
	after := snapshot(t, s.tokens())
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state differs after import.\nbefore=%v\nafter=%v", before, after)
	}
	me := get(t, "/me", s.e.tok["ada"]).obj(t)
	if me["currency"] != "EUR" || num(t, me, "minor_units") != 2 {
		t.Fatalf("currency lost: %v", me)
	}
	// hashed-password logins
	for _, em := range []string{"ada@example.com", "bob@example.com", "op@example.com"} {
		expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": em, "password": pw}), 200)
	}
	expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "zed@example.com", "password": "longenough"}), 200)
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "zed@example.com", "password": "wrongwrong"}), 401, "unauthenticated")
	expectErr(t, signup(t, "zed@example.com", "longenough"), 409, "email_taken")
	// nothing was replayed against net balances
	var sum int64
	for _, tk := range s.tokens() {
		sum += bal(t, tk)
	}
	if sum != s.e.total {
		t.Fatalf("sum after import %d != %d", sum, s.e.total)
	}

	// R106: repeating it restores the same state, nothing duplicated
	expect(t, doImport(t, exp.Body), 204)
	expect(t, doImport(t, exp.Body), 204)
	if again := snapshot(t, s.tokens()); !reflect.DeepEqual(before, again) {
		t.Fatalf("state differs after repeated import")
	}
	// import is replacement: post-export writes vanish
	mustPay(t, s.e.tok["ada"], "bob", 7, nil)
	expect(t, doImport(t, exp.Body), 204)
	if again := snapshot(t, s.tokens()); !reflect.DeepEqual(before, again) {
		t.Fatalf("import did not replace later state")
	}
}

func TestR110_R70_RetriesAndReceiptsSurviveImport(t *testing.T) {
	s := buildRich(t)
	exp := doExport(t)
	reset(t, stdFixture())
	expect(t, doImport(t, exp.Body), 204)
	for i, c := range s.replays {
		rep := post(t, c.path, c.tok, c.key, c.body)
		if rep.Status != 200 {
			t.Fatalf("replay %d (%s) after import: %s", i, c.path, rep)
		}
		requireSameJSON(t, c.orig, rep)
		// a changed body under the same key is still a conflict
		if m, ok := c.body.(map[string]any); ok {
			alt := map[string]any{}
			for k, v := range m {
				alt[k] = v
			}
			alt["note"] = "changed after import"
			expectErr(t, post(t, c.path, c.tok, c.key, alt), 409, "idempotency_key_reuse")
		}
	}
	var sum int64
	for _, tk := range s.tokens() {
		sum += bal(t, tk)
	}
	if sum != s.e.total {
		t.Fatal("replays after import changed the money supply")
	}
	// the failed key is still reusable (first use): ada funds dee, then the same body succeeds
	mustPay(t, s.e.tok["ada"], "dee", 5000, nil)
	expect(t, post(t, "/payments", s.e.tok["dee"], s.failKey, s.failBody), 201)
}

func TestR111_NewIdsDoNotCollideAfterImport(t *testing.T) {
	s := buildRich(t)
	exp := doExport(t)
	existing := map[string]bool{}
	for _, tk := range s.tokens() {
		a, _ := activity(t, tk, "?limit=200")
		for id := range idSet(t, a, "payment_id") {
			existing[id] = true
		}
		q, _ := listReq(t, tk, "?limit=200")
		for id := range idSet(t, q, "request_id") {
			existing[id] = true
		}
	}
	reset(t, stdFixture())
	expect(t, doImport(t, exp.Body), 204)
	fresh := map[string]string{}
	p := mustPay(t, s.e.tok["ada"], "bob", 1, nil)
	fresh["payment"] = str(t, p, "payment_id")
	q := mustRequest(t, s.e.tok["ada"], "bob", 1)
	fresh["request"] = rid(t, q)
	sp := postK(t, "/splits", s.e.tok["ada"], map[string]any{"amount": 3, "participant_handles": []string{"bob", "cy"}})
	expect(t, sp, 201)
	for _, r := range sp.obj(t)["requests"].([]any) {
		fresh["split-req-"+rid(t, r.(map[string]any))] = rid(t, r.(map[string]any))
	}
	st := settle(t, s.e.tok["op"], batch(tr("ada", "bob", 1)))
	expect(t, st, 201)
	fresh["settlement-payment"] = str(t, st.obj(t)["payments"].([]any)[0].(map[string]any), "payment_id")
	for what, id := range fresh {
		if existing[id] {
			t.Fatalf("new %s id %q collides with an imported id", what, id)
		}
	}
	// ids of signup
	su := signup(t, "later@example.com", "longenough")
	expect(t, su, 201)
	if su.obj(t)["user_id"] == s.zedID || su.obj(t)["user_id"] == "u_ada" {
		t.Fatal("new user id collides")
	}
	// the payments created before import are still intact alongside the new ones
	a, _ := activity(t, s.e.tok["ada"], "?limit=200")
	if !idSet(t, a, "payment_id")[fresh["payment"]] {
		t.Fatal("new payment missing")
	}
	// unique among themselves
	seen := map[string]bool{}
	for _, id := range fresh {
		if seen[id] {
			t.Fatal("new ids not unique")
		}
		seen[id] = true
	}
	if e := snapshot(t, s.tokens()); e == nil {
		t.Fatal("snapshot")
	}
}

func TestR122_OperatorAndSettlementsPreserved(t *testing.T) {
	s := buildRich(t)
	beforeAct, _ := activity(t, s.e.tok["cy"], "?limit=200")
	var memberIDs = map[string]string{}
	for _, it := range beforeAct {
		if it["settlement_id"] != nil {
			memberIDs[str(t, it, "payment_id")] = str(t, it, "settlement_id")
		}
	}
	if len(memberIDs) == 0 {
		t.Fatal("setup: no settlement members seen")
	}
	exp := doExport(t)
	reset(t, fixtureCur("EUR", 2, nil, fu{"ada", 1}))
	expect(t, doImport(t, exp.Body), 204)
	afterAct, _ := activity(t, s.e.tok["cy"], "?limit=200")
	got := map[string]string{}
	for _, it := range afterAct {
		if it["settlement_id"] != nil {
			got[str(t, it, "payment_id")] = str(t, it, "settlement_id")
		}
	}
	if !reflect.DeepEqual(memberIDs, got) {
		t.Fatalf("settlement membership changed: %v -> %v", memberIDs, got)
	}
	// operator permissions persist; non-operators are still refused
	expect(t, settle(t, s.e.tok["op"], batch(tr("ada", "bob", 1))), 201)
	expectErr(t, settle(t, s.e.tok["ada"], batch(tr("ada", "bob", 1))), 403, "forbidden")
}

func TestR107_InvalidImports(t *testing.T) {
	s := buildRich(t)
	exp := doExport(t)
	before := snapshot(t, s.tokens())
	var valid map[string]any
	d, _ := decodeNum(exp.Body)
	valid = d.(map[string]any)
	clone := func(mut func(m map[string]any)) map[string]any {
		raw, _ := json.Marshal(valid)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mut(m)
		return m
	}
	// unparseable
	for _, b := range []string{`{`, `nope`, ``, `{"track":`} {
		expectErr(t, doImport(t, b), 400, "malformed_request")
	}
	bads := map[string]map[string]any{
		"no track":       clone(func(m map[string]any) { delete(m, "track") }),
		"wrong track":    clone(func(m map[string]any) { m["track"] = "other" }),
		"no version":     clone(func(m map[string]any) { delete(m, "format_version") }),
		"wrong version":  clone(func(m map[string]any) { m["format_version"] = 2 }),
		"version string": clone(func(m map[string]any) { m["format_version"] = "1" }),
		"no state":       clone(func(m map[string]any) { delete(m, "state") }),
		"state null":     clone(func(m map[string]any) { m["state"] = nil }),
		"state string":   clone(func(m map[string]any) { m["state"] = "x" }),
		"state array":    clone(func(m map[string]any) { m["state"] = []any{} }),
		"state garbage":  clone(func(m map[string]any) { m["state"] = map[string]any{"garbage": true} }),
	}
	for name, b := range bads {
		r := doImport(t, b)
		if r.Status != 422 {
			t.Fatalf("%s: want 422 validation_failed, got %s", name, r)
		}
		expectErr(t, r, 422, "validation_failed")
		// destination unchanged after every rejected import
		if !reflect.DeepEqual(before, snapshot(t, s.tokens())) {
			t.Fatalf("%s: destination changed by a rejected import", name)
		}
	}
	expectErr(t, doImport(t, `{}`), 422, "validation_failed")
	if !reflect.DeepEqual(before, snapshot(t, s.tokens())) {
		t.Fatal("destination changed by rejected imports")
	}
	// extra unknown top-level fields are tolerated
	ok := clone(func(m map[string]any) { m["extra"] = true })
	expect(t, doImport(t, ok), 204)
}

func TestR108_ExportIsASnapshot(t *testing.T) {
	s := buildRich(t)
	before := snapshot(t, s.tokens())
	exp1 := doExport(t)
	exp1Copy := append([]byte(nil), exp1.Body...)
	// source writes after the export must not leak into it
	mustPay(t, s.e.tok["ada"], "bob", 11, nil)
	expect(t, postNoKey(t, "/auth/signup", "", map[string]any{"email": "late@example.com", "password": "longenough", "display_name": "L"}), 201)
	if string(exp1.Body) != string(exp1Copy) {
		t.Fatal("export body mutated")
	}
	// exporting is read-only: a second export without writes equals the first state on import
	_ = doExport(t)
	_ = doExport(t)
	expect(t, doImport(t, exp1Copy), 204)
	if !reflect.DeepEqual(before, snapshot(t, s.tokens())) {
		t.Fatal("importing the earlier export did not give the snapshot state")
	}
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "late@example.com", "password": "longenough"}), 401, "unauthenticated")
	// export did not disturb the state
	again := snapshot(t, s.tokens())
	_ = doExport(t)
	if !reflect.DeepEqual(again, snapshot(t, s.tokens())) {
		t.Fatal("export changed state")
	}
}

func TestR111_ResetClearsImportedState(t *testing.T) {
	s := buildRich(t)
	exp := doExport(t)
	reset(t, stdFixture())
	expect(t, doImport(t, exp.Body), 204)
	expect(t, get(t, "/me", s.zedTok), 200)
	reset(t, stdFixture())
	expectErr(t, get(t, "/me", s.zedTok), 401, "unauthenticated")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "zed@example.com", "password": "longenough"}), 401, "unauthenticated")
	tk := login(t, "ada@example.com")
	if bal(t, tk) != 10000 {
		t.Fatal("reset did not restore fixture")
	}
	if a, _ := activity(t, tk, ""); len(a) != 0 {
		t.Fatal("payments survived reset")
	}
	// old idempotency records are gone too
	c := s.replays[0]
	r := post(t, c.path, tk, c.key, c.body)
	expect(t, r, 201)
}

func TestR109_CurrencyPreservedAcrossImport(t *testing.T) {
	for _, c := range []struct {
		cur string
		mu  int
	}{{"JPY", 0}, {"BHD", 3}} {
		reset(t, fixtureCur(c.cur, c.mu, nil, fu{"ada", 5000}, fu{"bob", 0}))
		ta := login(t, "ada@example.com")
		mustPay(t, ta, "bob", 1000, nil)
		exp := doExport(t)
		reset(t, stdFixture())
		expect(t, doImport(t, exp.Body), 204)
		me := get(t, "/me", ta).obj(t)
		if me["currency"] != c.cur || num(t, me, "minor_units") != int64(c.mu) || num(t, me, "balance") != 4000 {
			t.Fatalf("%s: %v", c.cur, me)
		}
		p := mustPay(t, ta, "bob", 1, nil)
		if p["currency"] != c.cur {
			t.Fatal("currency of new payment")
		}
	}
}

func TestR109_SeededFixtureExportRoundTrip(t *testing.T) {
	fx := fixtureCur("EUR", 2, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"op", 0})
	fx["payments"] = []any{map[string]any{"id": "p_1", "from_user_id": "u_ada", "to_user_id": "u_bob", "amount": 500, "note": "coffee", "visibility": "private"}}
	fx["requests"] = []any{map[string]any{"id": "rq_1", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 1200, "note": "taxi", "status": "pending"}}
	reset(t, fx)
	toks := map[string]string{"ada": login(t, "ada@example.com"), "bob": login(t, "bob@example.com")}
	before := snapshot(t, toks)
	exp := doExport(t)
	reset(t, stdFixture())
	expect(t, doImport(t, exp.Body), 204)
	if !reflect.DeepEqual(before, snapshot(t, toks)) {
		t.Fatal("seeded state changed across export/import")
	}
	// the seeded pending request remains payable exactly once, with its fixture id
	expect(t, postK(t, "/requests/rq_1/pay", toks["ada"], map[string]any{}), 201)
	expectErr(t, postK(t, "/requests/rq_1/pay", toks["ada"], map[string]any{}), 409, "request_not_pending")
}
