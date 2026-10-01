package acceptance

import (
	"testing"
)

func tr(from, to string, amt any) map[string]any {
	return map[string]any{"from_handle": from, "to_handle": to, "amount": amt}
}

func batch(ts ...any) map[string]any { return map[string]any{"transfers": ts} }

func settle(t testing.TB, tok string, body any) resp {
	t.Helper()
	return postK(t, "/settlements", tok, body)
}

func settleEnv(t testing.TB) *env {
	return setupUsers(t, []string{"u_op"}, fu{"ada", 1000}, fu{"bob", 500}, fu{"cy", 0}, fu{"dee", 0}, fu{"op", 0})
}

func TestR114_SettlementPermissions(t *testing.T) {
	e := settleEnv(t)
	good := batch(tr("ada", "bob", 10))
	expectErr(t, post(t, "/settlements", "", newKey(), good), 401, "unauthenticated")
	expectErr(t, post(t, "/settlements", "bogus", newKey(), good), 401, "unauthenticated")
	for _, h := range []string{"ada", "bob", "cy", "dee"} {
		expectErr(t, settle(t, e.tok[h], good), 403, "forbidden")
	}
	if bal(t, e.tok["ada"]) != 1000 {
		t.Fatal("forbidden settlement moved money")
	}
	expectErr(t, call(t, "POST", "/settlements", e.tok["op"], nil, good), 400, "missing_idempotency_key")
	expect(t, settle(t, e.tok["op"], good), 201)
	// default: nobody is an operator
	e2 := setupUsers(t, nil, fu{"ada", 1000}, fu{"bob", 0}, fu{"op", 0})
	expectErr(t, settle(t, e2.tok["op"], good), 403, "forbidden")
	expectErr(t, settle(t, e2.tok["ada"], good), 403, "forbidden")
}

func TestR112_MultipleOperatorsAndOperatorIsNotSuperuser(t *testing.T) {
	e := setupUsers(t, []string{"u_op", "u_cy"}, fu{"ada", 1000}, fu{"bob", 0}, fu{"cy", 0}, fu{"op", 0})
	good := batch(tr("ada", "bob", 10))
	expect(t, settle(t, e.tok["op"], good), 201)
	expect(t, settle(t, e.tok["cy"], batch(tr("ada", "bob", 11))), 201)
	expectErr(t, settle(t, e.tok["bob"], good), 403, "forbidden")
	// An operator does not gain access to others' requests or private items.
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 5))
	expectErr(t, postK(t, "/requests/"+rq+"/pay", e.tok["op"], map[string]any{}), 403, "forbidden")
	expectErr(t, postNoKey(t, "/requests/"+rq+"/decline", e.tok["op"], nil), 403, "forbidden")
	expectErr(t, postNoKey(t, "/requests/"+rq+"/cancel", e.tok["op"], nil), 403, "forbidden")
	if out, _ := listReq(t, e.tok["op"], ""); len(out) != 0 {
		t.Fatalf("operator sees requests: %v", out)
	}
	mustPay(t, e.tok["ada"], "bob", 1, map[string]any{"visibility": "private"})
	out, _ := activity(t, e.tok["op"], "")
	for _, it := range out {
		if it["visibility"] == "private" {
			t.Fatalf("operator sees private item: %v", it)
		}
	}
}

func TestR115_R119_SettlementShape(t *testing.T) {
	e := settleEnv(t)
	r := settle(t, e.tok["op"], batch(tr("ada", "bob", 100), tr("bob", "cy", 50)))
	expect(t, r, 201)
	m := r.obj(t)
	sid := str(t, m, "settlement_id")
	commit := str(t, m, "committed_at")
	cts := checkTS(t, commit)
	ps, ok := m["payments"].([]any)
	if !ok || len(ps) != 2 {
		t.Fatalf("payments: %s", r)
	}
	for i, want := range [][3]any{{"ada", "bob", int64(100)}, {"bob", "cy", int64(50)}} {
		p := ps[i].(map[string]any)
		if p["from_handle"] != want[0] || p["to_handle"] != want[1] || num(t, p, "amount") != want[2].(int64) {
			t.Fatalf("payment %d: %v", i, p)
		}
		if !isNull(p, "request_id") || p["settlement_id"] != sid || p["note"] != "" || p["visibility"] != "public" || p["currency"] != "EUR" {
			t.Fatalf("payment %d fields: %v", i, p)
		}
		if str(t, p, "payment_id") == "" {
			t.Fatal("no payment id")
		}
		if !checkTS(t, str(t, p, "created_at")).Equal(cts) || p["created_at"] != commit {
			t.Fatalf("created_at %v != committed_at %v", p["created_at"], commit)
		}
	}
	if ps[0].(map[string]any)["payment_id"] == ps[1].(map[string]any)["payment_id"] {
		t.Fatal("same payment id for distinct members")
	}
	if bal(t, e.tok["ada"]) != 900 || bal(t, e.tok["bob"]) != 550 || bal(t, e.tok["cy"]) != 50 || bal(t, e.tok["op"]) != 0 {
		t.Fatal("balances wrong")
	}
	// members in the feed carry the settlement id; ordinary payments carry null (R113)
	plain := mustPay(t, e.tok["ada"], "dee", 1, nil)
	if !isNull(plain, "settlement_id") {
		t.Fatalf("ordinary payment settlement_id: %v", plain)
	}
	a, _ := activity(t, e.tok["cy"], "?limit=200")
	found := 0
	for _, it := range a {
		if it["settlement_id"] == sid {
			found++
		} else if !isNull(it, "settlement_id") {
			t.Fatalf("unexpected settlement_id %v", it["settlement_id"])
		}
	}
	if found != 2 {
		t.Fatalf("cy sees %d members of the settlement, want 2 (public)", found)
	}
}

func TestR115_NoteVisibilityAndBounds(t *testing.T) {
	e := settleEnv(t)
	tk := e.tok["op"]
	r := settle(t, tk, batch(
		map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 10, "note": "hello 🎉", "visibility": "private", "bogus": 1},
		tr("ada", "cy", 10)))
	expect(t, r, 201)
	ps := r.obj(t)["payments"].([]any)
	if ps[0].(map[string]any)["note"] != "hello 🎉" || ps[0].(map[string]any)["visibility"] != "private" || ps[1].(map[string]any)["visibility"] != "public" {
		t.Fatalf("%v", ps)
	}
	// unknown top-level fields ignored
	expect(t, settle(t, tk, map[string]any{"transfers": []any{tr("ada", "bob", 1)}, "extra": true}), 201)
	// 32 ok, 33 not
	var t32 []any
	for i := 0; i < 32; i++ {
		t32 = append(t32, tr("ada", "bob", 1))
	}
	expect(t, settle(t, tk, batch(t32...)), 201)
	expectErr(t, settle(t, tk, batch(append(t32, tr("ada", "bob", 1))...)), 422, "validation_failed")
	// exactly affordable amount
	e = settleEnv(t)
	expect(t, settle(t, e.tok["op"], batch(tr("ada", "bob", 1000))), 201)
	if bal(t, e.tok["ada"]) != 0 {
		t.Fatal("ada should be 0")
	}
}

func TestR116_MalformedBatchShapes(t *testing.T) {
	e := settleEnv(t)
	tk := e.tok["op"]
	for _, b := range []any{
		map[string]any{},
		map[string]any{"transfers": nil},
		map[string]any{"transfers": "x"},
		map[string]any{"transfers": 5},
		map[string]any{"transfers": map[string]any{"from_handle": "ada"}},
		map[string]any{"transfers": []any{}},
		batch("ada"),
		batch(nil),
		batch(5),
		batch([]any{}),
		batch(tr("ada", "bob", 1), "x"),
		batch(map[string]any{}),
		batch(map[string]any{"from_handle": "ada", "amount": 1}),
		batch(map[string]any{"to_handle": "bob", "amount": 1}),
		batch(map[string]any{"from_handle": "ada", "to_handle": "bob"}),
	} {
		expectErr(t, settle(t, tk, b), 422, "validation_failed")
	}
	for _, a := range []any{0, -1, 1000000001, 1.5, "10", true, nil} {
		expectErr(t, settle(t, tk, batch(tr("ada", "bob", a))), 422, "validation_failed")
	}
	expectErr(t, settle(t, tk, batch(map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 1, "note": strs(201)})), 422, "validation_failed")
	expectErr(t, settle(t, tk, batch(map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 1, "note": nil})), 422, "validation_failed")
	expectErr(t, settle(t, tk, batch(map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 1, "visibility": "friends"})), 422, "validation_failed")
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 1), map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 1, "visibility": "zzz"})), 422, "validation_failed")
	// unparseable
	expectErr(t, settle(t, tk, `{"transfers":[`), 400, "malformed_request")
	if e.sum(t) != e.total || bal(t, e.tok["ada"]) != 1000 {
		t.Fatal("rejected settlements changed balances")
	}
}

func strs(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func TestR116_EntryErrorsInInputOrderBeforeFunds(t *testing.T) {
	e := settleEnv(t)
	tk := e.tok["op"]
	// unknown handle vs. insufficient funds -> 404
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 999999), tr("ada", "ghost", 1))), 404, "not_found")
	expectErr(t, settle(t, tk, batch(tr("ghost", "bob", 1))), 404, "not_found")
	expectErr(t, settle(t, tk, batch(tr("ada", "ghost", 1))), 404, "not_found")
	// self transfer vs. insufficient funds -> 422 self_payment
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 999999), tr("cy", "cy", 1))), 422, "self_payment")
	expectErr(t, settle(t, tk, batch(tr("cy", "cy", 1))), 422, "self_payment")
	// first failing entry wins, in input order
	expectErr(t, settle(t, tk, batch(tr("ada", "ada", 1), tr("ada", "ghost", 1))), 422, "self_payment")
	expectErr(t, settle(t, tk, batch(tr("ada", "ghost", 1), tr("ada", "ada", 1))), 404, "not_found")
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 0), tr("ada", "ghost", 1))), 422, "validation_failed")
	expectErr(t, settle(t, tk, batch(tr("ada", "ghost", 1), tr("ada", "bob", 0))), 404, "not_found")
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 1), tr("ada", "bob", 1000000001), tr("ada", "ghost", 1))), 422, "validation_failed")
	// an entry error in a valid-looking batch that would otherwise succeed
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 1), tr("ada", "ghost", 1))), 404, "not_found")
	if e.sum(t) != e.total || bal(t, e.tok["ada"]) != 1000 || bal(t, e.tok["bob"]) != 500 {
		t.Fatal("balances changed")
	}
	// nothing was created
	if a, _ := activity(t, e.tok["ada"], ""); len(a) != 0 {
		t.Fatalf("payments created: %v", a)
	}
	// finally insufficient funds when entries are fine
	expectErr(t, settle(t, tk, batch(tr("ada", "bob", 1001))), 409, "insufficient_funds")
}

func TestR117_NettedAffordability(t *testing.T) {
	e := settleEnv(t) // ada 1000, bob 500, cy 0, dee 0
	tk := e.tok["op"]
	// cy has nothing but passes 300 through: net 0, affordable in either order
	expect(t, settle(t, tk, batch(tr("ada", "cy", 300), tr("cy", "dee", 300))), 201)
	expect(t, settle(t, tk, batch(tr("cy", "dee", 200), tr("ada", "cy", 200))), 201)
	if bal(t, e.tok["cy"]) != 0 || bal(t, e.tok["dee"]) != 500 || bal(t, e.tok["ada"]) != 500 {
		t.Fatalf("balances: cy=%d dee=%d ada=%d", bal(t, e.tok["cy"]), bal(t, e.tok["dee"]), bal(t, e.tok["ada"]))
	}
	// a cycle among wallets with nothing to spare nets to zero
	e = setupUsers(t, []string{"u_op"}, fu{"ada", 0}, fu{"bob", 0}, fu{"cy", 0}, fu{"op", 0})
	expect(t, settle(t, e.tok["op"], batch(tr("ada", "bob", 500), tr("bob", "cy", 500), tr("cy", "ada", 500))), 201)
	if e.sum(t) != 0 {
		t.Fatal("sum")
	}
	expect(t, settle(t, e.tok["op"], batch(tr("ada", "bob", 7), tr("bob", "ada", 7))), 201)
	// a wallet that goes negative overall fails even if other wallets are rich
	e = settleEnv(t)
	expectErr(t, settle(t, e.tok["op"], batch(tr("ada", "bob", 600), tr("ada", "cy", 401))), 409, "insufficient_funds")
	expectErr(t, settle(t, e.tok["op"], batch(tr("cy", "ada", 1))), 409, "insufficient_funds")
	expectErr(t, settle(t, e.tok["op"], batch(tr("bob", "ada", 400), tr("bob", "cy", 151), tr("ada", "bob", 50))), 409, "insufficient_funds")
	expect(t, settle(t, e.tok["op"], batch(tr("bob", "ada", 400), tr("bob", "cy", 100), tr("ada", "bob", 50))), 201)
	if bal(t, e.tok["bob"]) != 50 || bal(t, e.tok["ada"]) != 1350 || bal(t, e.tok["cy"]) != 100 {
		t.Fatal("netted balances wrong")
	}
	// operator may move money of wallets it does not own, including its own wallet as a party
	e = setupUsers(t, []string{"u_op"}, fu{"ada", 10}, fu{"op", 100})
	expect(t, settle(t, e.tok["op"], batch(tr("op", "ada", 100))), 201)
}

func TestR118_AllOrNothingAndFailedClaimsNoKey(t *testing.T) {
	e := settleEnv(t)
	tk := e.tok["op"]
	k := newKey()
	bad := batch(tr("ada", "bob", 600), tr("ada", "cy", 500)) // ada overdrawn by 100
	expectErr(t, post(t, "/settlements", tk, k, bad), 409, "insufficient_funds")
	if bal(t, e.tok["ada"]) != 1000 || bal(t, e.tok["bob"]) != 500 || bal(t, e.tok["cy"]) != 0 {
		t.Fatal("partial commit")
	}
	for _, h := range []string{"ada", "bob", "cy", "dee"} {
		if a, _ := activity(t, e.tok[h], ""); len(a) != 0 {
			t.Fatalf("%s sees payments from a failed settlement", h)
		}
	}
	// same key, different (valid) body: the failed attempt claimed nothing -> first use 201
	good := batch(tr("ada", "bob", 600), tr("ada", "cy", 400))
	r := post(t, "/settlements", tk, k, good)
	expect(t, r, 201)
	// the same key is now claimed
	expectErr(t, post(t, "/settlements", tk, k, bad), 409, "idempotency_key_reuse")
	rep := post(t, "/settlements", tk, k, good)
	expect(t, rep, 200)
	requireSameJSON(t, r, rep)
	// validation failure likewise claims nothing
	k2 := newKey()
	expectErr(t, post(t, "/settlements", tk, k2, batch(tr("bob", "ghost", 1))), 404, "not_found")
	expect(t, post(t, "/settlements", tk, k2, batch(tr("bob", "cy", 1))), 201)
	k3 := newKey()
	expectErr(t, post(t, "/settlements", tk, k3, batch()), 422, "validation_failed")
	expect(t, post(t, "/settlements", tk, k3, batch(tr("bob", "cy", 2))), 201)
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR120_ConstituentVisibility(t *testing.T) {
	e := settleEnv(t)
	r := settle(t, e.tok["op"], batch(
		map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 10, "visibility": "private"},
		map[string]any{"from_handle": "bob", "to_handle": "cy", "amount": 5, "visibility": "public"},
		map[string]any{"from_handle": "ada", "to_handle": "cy", "amount": 7, "visibility": "private"}))
	expect(t, r, 201)
	ps := r.obj(t)["payments"].([]any)
	p0, p1, p2 := str(t, ps[0].(map[string]any), "payment_id"), str(t, ps[1].(map[string]any), "payment_id"), str(t, ps[2].(map[string]any), "payment_id")
	want := map[string][]bool{ // p0 priv ada->bob, p1 pub bob->cy, p2 priv ada->cy
		"ada": {true, true, true},
		"bob": {true, true, false},
		"cy":  {false, true, true},
		"dee": {false, true, false},
		"op":  {false, true, false}, // operator is not a party: only public items
	}
	for h, w := range want {
		a, _ := activity(t, e.tok[h], "?limit=200")
		got := idSet(t, a, "payment_id")
		for i, id := range []string{p0, p1, p2} {
			if got[id] != w[i] {
				t.Fatalf("%s: payment %d visible=%v want %v", h, i, got[id], w[i])
			}
		}
	}
	// the operator still receives every receipt in the response itself
	if len(ps) != 3 {
		t.Fatal("receipts")
	}
}

func TestR121_SettlementReplay(t *testing.T) {
	e := settleEnv(t)
	k := newKey()
	body := batch(tr("ada", "bob", 100), tr("bob", "cy", 50))
	first := post(t, "/settlements", e.tok["op"], k, body)
	expect(t, first, 201)
	for i := 0; i < 3; i++ {
		rep := post(t, "/settlements", e.tok["op"], k, body)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
	}
	if bal(t, e.tok["ada"]) != 900 || bal(t, e.tok["cy"]) != 50 {
		t.Fatal("replay moved money")
	}
	a, _ := activity(t, e.tok["ada"], "")
	if len(a) != 2 { // both members are public, so ada sees the whole batch
		t.Fatalf("ada sees %d", len(a))
	}
	// different body under the same key
	expectErr(t, post(t, "/settlements", e.tok["op"], k, batch(tr("ada", "bob", 101), tr("bob", "cy", 50))), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/settlements", e.tok["op"], k, batch(tr("bob", "cy", 50), tr("ada", "bob", 100))), 409, "idempotency_key_reuse") // array order matters
	// replay returns the original response even after wallets changed
	mustPay(t, e.tok["ada"], "dee", bal(t, e.tok["ada"]), nil)
	rep := post(t, "/settlements", e.tok["op"], k, body)
	expect(t, rep, 200)
	requireSameJSON(t, first, rep)
	// an operator's key does not interact with another operator's
	e2 := setupUsers(t, []string{"u_op", "u_dee"}, fu{"ada", 1000}, fu{"bob", 500}, fu{"dee", 0}, fu{"op", 0})
	a1 := post(t, "/settlements", e2.tok["op"], "same", batch(tr("ada", "bob", 1)))
	a2 := post(t, "/settlements", e2.tok["dee"], "same", batch(tr("ada", "bob", 2)))
	expect(t, a1, 201)
	expect(t, a2, 201)
}

func TestR119_SettlementIdsUniqueAcrossBatches(t *testing.T) {
	e := settleEnv(t)
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		r := settle(t, e.tok["op"], batch(tr("ada", "bob", 1), tr("bob", "cy", 1)))
		expect(t, r, 201)
		m := r.obj(t)
		id := str(t, m, "settlement_id")
		if seen[id] {
			t.Fatal("settlement id reused")
		}
		seen[id] = true
		for _, p := range m["payments"].([]any) {
			pid := str(t, p.(map[string]any), "payment_id")
			if seen[pid] {
				t.Fatal("payment id reused")
			}
			seen[pid] = true
		}
		tick()
	}
}
