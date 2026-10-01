package acceptance

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestR240_RefundAuthAndKeys(t *testing.T) {
	e := setupF(t)
	p := mustPay(t, e.tok["ada"], "bob", 500, nil)
	pid := str(t, p, "payment_id")
	path := "/payments/" + pid + "/refunds"
	k := newKey()
	body := map[string]any{"amount": 100}
	expectErr(t, post(t, path, "", k, body), 401, "unauthenticated")
	expectErr(t, post(t, path, "bogus", k, body), 401, "unauthenticated")
	expectErr(t, post(t, path, e.tok["ada"], k, body), 403, "forbidden") // the sender
	expectErr(t, post(t, path, e.tok["cy"], k, body), 403, "forbidden")  // a third party
	expectErr(t, post(t, path, e.tok["op"], k, body), 403, "forbidden")
	expectErr(t, post(t, "/payments/nope/refunds", e.tok["bob"], k, body), 404, "not_found")
	expectErr(t, call(t, "POST", path, e.tok["bob"], nil, body), 400, "missing_idempotency_key")
	expectErr(t, call(t, "POST", path, e.tok["bob"], map[string]string{"Idempotency-Key": ""}, body), 400, "missing_idempotency_key")
	expectErr(t, post(t, path, e.tok["bob"], longKey(256), body), 422, "validation_failed")
	expectErr(t, post(t, path, e.tok["bob"], k, `{`), 400, "malformed_request")
	expectErr(t, post(t, path, e.tok["bob"], k, `[]`), 400, "malformed_request")
	if balAt(t, e.tok["bob"], "") != 3000 || len(refundsOf(t, e.tok["ada"], pid)) != 0 {
		t.Fatal("rejected requests changed state")
	}
	// the receiver succeeds; the failed attempts did not claim the key
	first := post(t, path, e.tok["bob"], k, body)
	expect(t, first, 201)
	for i := 0; i < 2; i++ {
		rep := post(t, path, e.tok["bob"], k, body)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
	}
	expectErr(t, post(t, path, e.tok["bob"], k, map[string]any{"amount": 101}), 409, "idempotency_key_reuse")
	expect(t, post(t, path, e.tok["bob"], k, `{ "amount": 1e2 }`), 200)
	expectErr(t, post(t, path, e.tok["bob"], k, map[string]any{}), 409, "idempotency_key_reuse") // R71: claimed key first
	if balAt(t, e.tok["bob"], "") != 2900 || balAt(t, e.tok["ada"], "") != 9600 {
		t.Fatal("replays moved money again")
	}
	// keys are scoped per user: ada may use the same key for her own payment's refund... (she is not the receiver there)
	// the key is also scoped to the path: the same key and body on another payment is new
	p2 := mustPay(t, e.tok["ada"], "bob", 300, nil)
	expect(t, post(t, "/payments/"+str(t, p2, "payment_id")+"/refunds", e.tok["bob"], k, body), 201)
	// unknown fields are ignored
	expect(t, postK(t, path, e.tok["bob"], map[string]any{"amount": 1, "zzz": []int{1}}), 201)
}

func TestR241_RefundValidationAndPrecedence(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
	p := mustPay(t, e.tok["ada"], "bob", 500, nil)
	pid := str(t, p, "payment_id")
	before := stateOfF(t, e)
	// invalid amounts
	for _, a := range []any{0, -1, "5", true, nil, 10.5, 1000000001, []any{1}} {
		expectErr(t, refund(t, e.tok["bob"], pid, a), 422, "validation_failed")
	}
	expectErr(t, postK(t, "/payments/"+pid+"/refunds", e.tok["bob"], map[string]any{}), 422, "validation_failed")
	// integral spellings are valid amounts
	for _, raw := range []string{`{"amount":1e2}`, `{"amount":100.0}`} {
		expect(t, postK(t, "/payments/"+pid+"/refunds", e.tok["bob"], raw), 201)
	}
	// precedence 1: amount validation outranks 404 and 403
	expectErr(t, refund(t, e.tok["bob"], "nope", 0), 422, "validation_failed")
	expectErr(t, refund(t, e.tok["ada"], pid, 0), 422, "validation_failed")
	expectErr(t, refund(t, e.tok["cy"], pid, "x"), 422, "validation_failed")
	// precedence 2: 404/403 outrank everything below
	expectErr(t, refund(t, e.tok["bob"], "nope", 10), 404, "not_found")
	expectErr(t, refund(t, e.tok["ada"], pid, 99999), 403, "forbidden")
	expectErr(t, refund(t, e.tok["cy"], pid, 99999), 403, "forbidden")
	// refund of a refund
	rfs := refundsOf(t, e.tok["ada"], pid)
	if len(rfs) != 2 {
		t.Fatalf("refunds: %v", rfs)
	}
	rid1 := str(t, rfs[0], "payment_id")
	// the refund's receiver is ada
	expectErr(t, refund(t, e.tok["ada"], rid1, 10), 422, "invalid_refund_target")
	// precedence 3: invalid_refund_target outranks exceeds (and insufficient funds)
	expectErr(t, refund(t, e.tok["ada"], rid1, 99999), 422, "invalid_refund_target")
	// ... but the 403 for a non-receiver outranks it
	expectErr(t, refund(t, e.tok["bob"], rid1, 10), 403, "forbidden")
	expectErr(t, refund(t, e.tok["cy"], rid1, 10), 403, "forbidden")
	// precedence 4: exceeds outranks insufficient funds. dee holds exactly 100; refunding 150 of a 100 payment exceeds
	pd := mustPay(t, e.tok["ada"], "dee", 100, nil)
	expectErr(t, refund(t, e.tok["dee"], str(t, pd, "payment_id"), 101), 422, "refund_exceeds_payment")
	mustPay(t, e.tok["dee"], "cy", 100, nil) // dee is now broke
	expectErr(t, refund(t, e.tok["dee"], str(t, pd, "payment_id"), 101), 422, "refund_exceeds_payment")
	// precedence 5: insufficient funds last
	expectErr(t, refund(t, e.tok["dee"], str(t, pd, "payment_id"), 100), 409, "insufficient_funds")
	// cumulative: 500 payment already refunded 200
	expectErr(t, refund(t, e.tok["bob"], pid, 301), 422, "refund_exceeds_payment")
	mustRefund(t, e.tok["bob"], pid, 300)
	expectErr(t, refund(t, e.tok["bob"], pid, 1), 422, "refund_exceeds_payment")
	if reflect.DeepEqual(before, stateOfF(t, e)) {
		t.Fatal("setup: expected changes from the successful refunds")
	}
}

func stateOfF(t testing.TB, e *env) map[string]any {
	t.Helper()
	out := map[string]any{}
	for h, tk := range e.tok {
		st := fullStmt(t, tk, "")
		delete(st, "snapshot")
		out["stmt-"+h] = st
		out["me-"+h] = meAt(t, tk, "")
	}
	return out
}

func TestR241_ValidTargetsAndFloorAfterCorrection(t *testing.T) {
	e := setupF(t)
	// direct payment
	d := mustPay(t, e.tok["ada"], "bob", 300, nil)
	mustRefund(t, e.tok["bob"], str(t, d, "payment_id"), 300)
	// request payment
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	rp := postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{})
	expect(t, rp, 201)
	mustRefund(t, e.tok["bob"], str(t, rp.obj(t), "payment_id"), 40)
	// capture
	az := mustAuthorize(t, e.tok["ada"], "bob", 400, nil)
	cp := mustCapture(t, e.tok["bob"], aid(t, az), map[string]any{"amount": 250})
	mustRefund(t, e.tok["bob"], str(t, cp, "payment_id"), 250)
	// settlement member
	st := settle(t, e.tok["op"], batch(tr("ada", "cy", 80)))
	expect(t, st, 201)
	mem := st.obj(t)["payments"].([]any)[0].(map[string]any)
	mustRefund(t, e.tok["cy"], str(t, mem, "payment_id"), 80)
	// a correction lowers the corrected amount; refunds are bounded by the CURRENT amount
	p := mustPay(t, e.tok["ada"], "bob", 500, nil)
	pid := str(t, p, "payment_id")
	mustCorrect(t, e.tok["ada"], pid, 1, 300, parseTS(t, str(t, p, "created_at")), "smaller")
	mustRefund(t, e.tok["bob"], pid, 200)
	expectErr(t, refund(t, e.tok["bob"], pid, 101), 422, "refund_exceeds_payment") // 300 - 200 = 100 left
	mustRefund(t, e.tok["bob"], pid, 100)
	expectErr(t, refund(t, e.tok["bob"], pid, 1), 422, "refund_exceeds_payment")
	// a correction that RAISES the amount re-opens refund room
	mustCorrect(t, e.tok["ada"], pid, 2, 350, parseTS(t, str(t, p, "created_at")), "bigger")
	mustRefund(t, e.tok["bob"], pid, 50)
	expectErr(t, refund(t, e.tok["bob"], pid, 1), 422, "refund_exceeds_payment")
	// a zero-amount payment cannot be refunded at all
	z := mustPay(t, e.tok["ada"], "bob", 10, nil)
	mustCorrect(t, e.tok["ada"], str(t, z, "payment_id"), 1, 0, parseTS(t, str(t, z, "created_at")), "reverse")
	expectErr(t, refund(t, e.tok["bob"], str(t, z, "payment_id"), 1), 422, "refund_exceeds_payment")
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR242_RefundShapeAndRefundOfNull(t *testing.T) {
	e := setupF(t)
	p := mustPay(t, e.tok["ada"], "bob", 500, map[string]any{"note": "dinner ✓", "visibility": "private"})
	pid := str(t, p, "payment_id")
	if !isNull(p, "refund_of") {
		t.Fatalf("an ordinary payment carries refund_of: null: %v", p)
	}
	k := newKey()
	r := post(t, "/payments/"+pid+"/refunds", e.tok["bob"], k, map[string]any{"amount": 200})
	expect(t, r, 201)
	m := r.obj(t)
	if m["from_handle"] != "bob" || m["to_handle"] != "ada" || m["from_user_id"] != "u_bob" || m["to_user_id"] != "u_ada" ||
		num(t, m, "amount") != 200 || m["note"] != "dinner ✓" || m["visibility"] != "private" || m["currency"] != "EUR" {
		t.Fatalf("refund payment: %v", m)
	}
	if m["refund_of"] != pid {
		t.Fatalf("refund_of: %v", m["refund_of"])
	}
	for _, k := range []string{"request_id", "authorization_id", "settlement_id"} {
		if !isNull(m, k) {
			t.Fatalf("%s must be present and null: %v", k, m)
		}
	}
	if str(t, m, "payment_id") == pid || str(t, m, "payment_id") == "" {
		t.Fatal("refund needs its own id")
	}
	parseTS(t, str(t, m, "created_at"))
	rep := post(t, "/payments/"+pid+"/refunds", e.tok["bob"], k, map[string]any{"amount": 200})
	expect(t, rep, 200)
	requireSameJSON(t, r, rep)
	if balAt(t, e.tok["ada"], "") != 9700 || balAt(t, e.tok["bob"], "") != 2800 {
		t.Fatal("balances")
	}
	// refund_of is null everywhere else: every payment shape in every response
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 20))
	rp := postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{})
	expect(t, rp, 201)
	az := mustAuthorize(t, e.tok["ada"], "bob", 50, nil)
	cp := mustCapture(t, e.tok["bob"], aid(t, az), map[string]any{})
	st := settle(t, e.tok["op"], batch(tr("ada", "cy", 5)))
	expect(t, st, 201)
	shapes := []map[string]any{p, rp.obj(t), cp, st.obj(t)["payments"].([]any)[0].(map[string]any)}
	for _, s := range shapes {
		if !isNull(s, "refund_of") {
			t.Fatalf("refund_of must be null: %v", s)
		}
	}
	items, _ := activity(t, e.tok["ada"], "?limit=200")
	for _, it := range items {
		if it["payment_id"] == m["payment_id"] {
			if it["refund_of"] != pid {
				t.Fatalf("feed item of the refund: %v", it)
			}
			continue
		}
		if !isNull(it, "refund_of") {
			t.Fatalf("feed item refund_of: %v", it)
		}
	}
	for _, en := range entriesOf(t, fullStmt(t, e.tok["ada"], "")) {
		pm := en["payment"].(map[string]any)
		if _, ok := pm["refund_of"]; !ok {
			t.Fatalf("statement payment lacks refund_of: %v", pm)
		}
	}
	// seeded payments carry null as well
	reset(t, fixtureP([]string{"u_op"}, []any{seedP("sp1", "ada", "bob", 10, "public", ago(day))}, azUsers...))
	ta := login(t, "ada@example.com")
	its, _ := activity(t, ta, "")
	if len(its) != 1 || !isNull(its[0], "refund_of") {
		t.Fatalf("seeded payment: %v", its)
	}
}

func TestR243_RefundNeedsAvailableFunds(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 0}, fu{"cy", 0}, fu{"dee", 0}, fu{"op", 0})
	p := mustPay(t, e.tok["ada"], "bob", 100, nil)
	pid := str(t, p, "payment_id")
	mustPay(t, e.tok["bob"], "cy", 70, nil) // bob holds 30
	before := stateOfF(t, e)
	k := newKey()
	expectErr(t, post(t, "/payments/"+pid+"/refunds", e.tok["bob"], k, map[string]any{"amount": 50}), 409, "insufficient_funds")
	if !reflect.DeepEqual(before, stateOfF(t, e)) {
		t.Fatal("a failed refund changed state")
	}
	// no key was claimed: the same key works with a body that fits
	expect(t, post(t, "/payments/"+pid+"/refunds", e.tok["bob"], k, map[string]any{"amount": 30}), 201)
	if balAt(t, e.tok["bob"], "") != 0 || balAt(t, e.tok["ada"], "") != 9930 {
		t.Fatal("balances")
	}
	// the receiver's money may be HELD by an open authorization
	p2 := mustPay(t, e.tok["ada"], "bob", 300, nil)
	mustAuthorize(t, e.tok["bob"], "dee", 250, nil) // bob: total 300, held 250, available 50
	expectErr(t, refund(t, e.tok["bob"], str(t, p2, "payment_id"), 51), 409, "insufficient_funds")
	mustRefund(t, e.tok["bob"], str(t, p2, "payment_id"), 50) // exactly the available amount
	if m := meAt(t, e.tok["bob"], ""); num(t, m, "total") != 250 || num(t, m, "held") != 250 || num(t, m, "available") != 0 {
		t.Fatalf("bob: %v", m)
	}
	expectErr(t, refund(t, e.tok["bob"], str(t, p2, "payment_id"), 1), 409, "insufficient_funds")
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR244_RefundsNeverReopenOrRestore(t *testing.T) {
	e := setupF(t)
	// request stays paid
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	rp := postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{})
	expect(t, rp, 201)
	rpid := str(t, rp.obj(t), "payment_id")
	mustRefund(t, e.tok["bob"], rpid, 100)
	out, _ := listReq(t, e.tok["ada"], "?limit=200")
	if len(out) != 1 || out[0]["status"] != "paid" || out[0]["payment_id"] != rpid {
		t.Fatalf("a refund changed the request: %v", out)
	}
	expectErr(t, postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{}), 409, "request_not_pending")
	expectErr(t, postNoKey(t, "/requests/"+rq+"/cancel", e.tok["bob"], nil), 409, "request_not_pending")
	// capture: the authorization stays closed and the released hold is not restored
	az := mustAuthorize(t, e.tok["ada"], "bob", 1000, nil)
	cp := mustCapture(t, e.tok["bob"], aid(t, az), map[string]any{"amount": 200}) // final: releases 800
	mustRefund(t, e.tok["bob"], str(t, cp, "payment_id"), 200)
	g := getAuthz(t, e.tok["ada"], aid(t, az))
	if g["status"] != "captured" || num(t, g, "captured_amount") != 200 || num(t, g, "remaining_amount") != 0 || len(ints(g["payment_ids"])) != 1 {
		t.Fatalf("authorization after the refund: %v", g)
	}
	if held(t, e.tok["ada"]) != 0 {
		t.Fatal("the released hold was restored")
	}
	expectErr(t, capture(t, e.tok["bob"], aid(t, az), map[string]any{"amount": 1}), 409, "authorization_not_open")
	// open partial authorization: a refund of a nonfinal capture leaves the remainder as it was
	az2 := mustAuthorize(t, e.tok["ada"], "bob", 600, nil)
	c1 := mustCapture(t, e.tok["bob"], aid(t, az2), map[string]any{"amount": 100, "final": false})
	mustRefund(t, e.tok["bob"], str(t, c1, "payment_id"), 100)
	g2 := getAuthz(t, e.tok["ada"], aid(t, az2))
	if g2["status"] != "open" || num(t, g2, "remaining_amount") != 500 || num(t, g2, "captured_amount") != 100 || held(t, e.tok["ada"]) != 500 {
		t.Fatalf("open authorization after a refund of its capture: %v", g2)
	}
	// settlement membership is unchanged
	st := settle(t, e.tok["op"], batch(tr("ada", "cy", 60), tr("cy", "dee", 10)))
	expect(t, st, 201)
	sm := st.obj(t)
	mem := sm["payments"].([]any)[0].(map[string]any)
	mustRefund(t, e.tok["cy"], str(t, mem, "payment_id"), 60)
	items, _ := activity(t, e.tok["ada"], "?limit=200")
	for _, it := range items {
		if it["payment_id"] == mem["payment_id"] && it["settlement_id"] != sm["settlement_id"] {
			t.Fatalf("member lost its settlement: %v", it)
		}
		if it["refund_of"] != nil && it["settlement_id"] != nil {
			t.Fatalf("a refund must not become a settlement member: %v", it)
		}
	}
	rep := post(t, "/settlements", e.tok["op"], "k-"+newKey(), batch(tr("ada", "cy", 1)))
	expect(t, rep, 201)
	// the settlement's original receipt replays unchanged
	k := newKey()
	body := batch(tr("ada", "dee", 2), tr("dee", "cy", 1))
	first := post(t, "/settlements", e.tok["op"], k, body)
	expect(t, first, 201)
	mustRefund(t, e.tok["dee"], str(t, first.obj(t)["payments"].([]any)[0].(map[string]any), "payment_id"), 1)
	again := post(t, "/settlements", e.tok["op"], k, body)
	expect(t, again, 200)
	requireSameJSON(t, first, again)
	if len(again.obj(t)["payments"].([]any)) != 2 {
		t.Fatal("membership changed")
	}
}

func TestR245_RefundIsAnOrdinaryPayment(t *testing.T) {
	e := setupF(t)
	ta, tb, tc := e.tok["ada"], e.tok["bob"], e.tok["cy"]
	pub := mustPay(t, ta, "bob", 400, map[string]any{"visibility": "public"})
	priv := mustPay(t, ta, "bob", 300, map[string]any{"visibility": "private"})
	tick()
	before := time.Now().UTC()
	r1 := mustRefund(t, tb, str(t, pub, "payment_id"), 100)
	time.Sleep(50 * time.Millisecond) // distinct instants (the clock may be coarse)
	r2 := mustRefund(t, tb, str(t, priv, "payment_id"), 50)
	// feed: public refund visible to a third party, private refund only to its parties
	for h, want := range map[string][]bool{"ada": {true, true}, "bob": {true, true}, "cy": {true, false}, "dee": {true, false}} {
		items, _ := activity(t, e.tok[h], "?limit=200")
		s := idSet(t, items, "payment_id")
		if s[str(t, r1, "payment_id")] != want[0] || s[str(t, r2, "payment_id")] != want[1] {
			t.Fatalf("%s feed visibility: %v", h, s)
		}
	}
	// statements: both parties, with signs and r1 times
	for h, sign := range map[string]int64{"ada": 1, "bob": -1} {
		st := fullStmt(t, e.tok[h], "")
		checkStatementConsistent(t, st)
		found := 0
		for _, en := range entriesOf(t, st) {
			if entryPID(t, en) == str(t, r1, "payment_id") {
				found++
				ca := parseTS(t, str(t, r1, "created_at"))
				if num(t, en, "delta") != sign*100 || num(t, en, "revision") != 1 ||
					!parseTS(t, str(t, en, "effective_at")).Equal(ca) || !parseTS(t, str(t, en, "recorded_at")).Equal(ca) {
					t.Fatalf("%s entry: %v", h, en)
				}
				if en["payment"].(map[string]any)["refund_of"] != pub["payment_id"] {
					t.Fatal("refund_of in the statement")
				}
			}
		}
		if found != 1 {
			t.Fatalf("%s sees the refund %d times", h, found)
		}
	}
	// the statement of a third party never has it
	for _, id := range entryIDs(t, fullStmt(t, tc, "")) {
		if id == str(t, r1, "payment_id") {
			t.Fatal("cy sees a payment between others in a statement")
		}
	}
	// as_of / known_at: the refund counts only from its time
	rc := parseTS(t, str(t, r1, "created_at"))
	if balAt(t, ta, asofQ(rc.Add(-time.Millisecond))) != 10000-700 || balAt(t, ta, asofQ(rc)) != 10000-700+100 {
		t.Fatalf("as_of around the refund: %d %d", balAt(t, ta, asofQ(rc.Add(-time.Millisecond))), balAt(t, ta, asofQ(rc)))
	}
	if balAt(t, ta, "known_at="+qe(tsMicro(rc.Add(-time.Millisecond)))) != 10000-700 {
		t.Fatal("known_at before the refund was recorded")
	}
	if before.After(time.Now()) {
		t.Fatal("clock")
	}
	// revisions readable by the parties, 404 for a third party; r1 only
	for _, h := range []string{"ada", "bob"} {
		revs := revisionsOf(t, e.tok[h], str(t, r1, "payment_id"))
		if len(revs) != 1 || num(t, revs[0], "amount") != 100 || revs[0]["reason"] != "" {
			t.Fatalf("revisions: %v", revs)
		}
	}
	expectErr(t, get(t, "/payments/"+str(t, r1, "payment_id")+"/revisions", tc), 404, "not_found")
	// R250: a refund payment cannot be corrected
	expectErr(t, correct(t, tb, str(t, r1, "payment_id"), corrBody(1, 50, rc, "x")), 422, "linked_payment_immutable")
	expectErr(t, correct(t, ta, str(t, r1, "payment_id"), corrBody(1, 50, rc, "x")), 403, "forbidden")
	// sum invariant at several instants
	for _, at := range []time.Time{rc.Add(-day), rc.Add(-time.Millisecond), rc, time.Now().Add(time.Hour)} {
		if s := sumAt(t, e.tok, asofQ(at)); s != e.total {
			t.Fatalf("sum at %v: %d", at, s)
		}
	}
}

func TestR246_ConcurrentRefundsNeverExceed(t *testing.T) {
	e := setupF(t)
	p := mustPay(t, e.tok["ada"], "bob", 100, nil)
	pid := str(t, p, "payment_id")
	var mu sync.Mutex
	var ok int
	rs := fanout(20, func(int) (resp, error) {
		r, err := send("POST", "/payments/"+pid+"/refunds", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"amount": 30})
		if err == nil && r.Status == 201 {
			mu.Lock()
			ok++
			mu.Unlock()
		}
		return r, err
	})
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
		case 422:
			expectErr(t, x.r, 422, "refund_exceeds_payment")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if ok != 3 {
		t.Fatalf("%d refunds of 30 succeeded for a payment of 100, want exactly 3", ok)
	}
	if got := len(refundsOf(t, e.tok["ada"], pid)); got != 3 {
		t.Fatalf("%d refund payments recorded", got)
	}
	if balAt(t, e.tok["ada"], "") != 10000-100+90 || balAt(t, e.tok["bob"], "") != 2500+100-90 {
		t.Fatal("balances do not match exactly three refunds")
	}
	// many tiny refunds fill the payment exactly and no more
	p2 := mustPay(t, e.tok["ada"], "bob", 10, nil)
	ok = 0
	rs = fanout(25, func(int) (resp, error) {
		r, err := send("POST", "/payments/"+str(t, p2, "payment_id")+"/refunds", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"amount": 1})
		if err == nil && r.Status == 201 {
			mu.Lock()
			ok++
			mu.Unlock()
		}
		return r, err
	})
	for _, x := range rs {
		if x.err != nil || (x.r.Status != 201 && x.r.Status != 422) {
			t.Fatalf("%v %s", x.err, x.r)
		}
	}
	if ok != 10 {
		t.Fatalf("%d refunds of 1, want exactly 10", ok)
	}
	// the same key concurrently: one 201, the rest 200
	p3 := mustPay(t, e.tok["ada"], "bob", 50, nil)
	k := newKey()
	rs = fanout(20, func(int) (resp, error) {
		return send("POST", "/payments/"+str(t, p3, "payment_id")+"/refunds", e.tok["bob"], map[string]string{"Idempotency-Key": k}, map[string]any{"amount": 20})
	})
	onlyOneCreated(t, rs)
	if got := len(refundsOf(t, e.tok["ada"], str(t, p3, "payment_id"))); got != 1 {
		t.Fatalf("%d refunds for one key", got)
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR246_ConcurrentRefundAndCorrectionKeepFloor(t *testing.T) {
	for round := 0; round < 4; round++ {
		e := setupF(t)
		p := mustPay(t, e.tok["ada"], "bob", 100, nil)
		pid := str(t, p, "payment_id")
		ca := parseTS(t, str(t, p, "created_at"))
		fanout(20, func(i int) (resp, error) {
			if i%2 == 0 {
				return send("POST", "/payments/"+pid+"/refunds", e.tok["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"amount": 20})
			}
			return send("POST", "/payments/"+pid+"/corrections", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()},
				corrBody(1, int64(30+i), ca, "lower"))
		})
		// whatever the interleaving, refunds never exceed the final corrected amount
		revs := revisionsOf(t, e.tok["ada"], pid)
		cur := num(t, revs[len(revs)-1], "amount")
		var refunded int64
		for _, rf := range refundsOf(t, e.tok["ada"], pid) {
			refunded += num(t, rf, "amount")
		}
		if refunded > cur {
			t.Fatalf("round %d: refunded %d exceeds the corrected amount %d", round, refunded, cur)
		}
		if e.sum(t) != e.total {
			t.Fatal("sum")
		}
	}
}

// ---------------- R250 / R251 ----------------

func TestR250_SingleCorrectionTargets(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	ta, tb := e.tok["ada"], e.tok["bob"]
	// direct and request payments: allowed
	d := mustPay(t, ta, "bob", 100, nil)
	mustCorrect(t, ta, str(t, d, "payment_id"), 1, 80, parseTS(t, str(t, d, "created_at")), "ok")
	rq := rid(t, mustRequest(t, tb, "ada", 60))
	rp := postK(t, "/requests/"+rq+"/pay", ta, map[string]any{})
	expect(t, rp, 201)
	mustCorrect(t, ta, str(t, rp.obj(t), "payment_id"), 1, 50, parseTS(t, str(t, rp.obj(t), "created_at")), "ok")
	// capture, refund payment, settlement member: 422 linked_payment_immutable
	az := mustAuthorize(t, ta, "bob", 300, nil)
	cp := mustCapture(t, tb, aid(t, az), map[string]any{"amount": 100})
	cc := parseTS(t, str(t, cp, "created_at"))
	expectErr(t, correct(t, ta, str(t, cp, "payment_id"), corrBody(1, 50, cc, "x")), 422, "linked_payment_immutable")
	rf := mustRefund(t, tb, str(t, d, "payment_id"), 10)
	expectErr(t, correct(t, tb, str(t, rf, "payment_id"), corrBody(1, 5, parseTS(t, str(t, rf, "created_at")), "x")), 422, "linked_payment_immutable")
	st := settle(t, e.tok["op"], batch(tr("ada", "bob", 20)))
	expect(t, st, 201)
	mem := st.obj(t)["payments"].([]any)[0].(map[string]any)
	expectErr(t, correct(t, ta, str(t, mem, "payment_id"), corrBody(1, 5, parseTS(t, str(t, st.obj(t), "committed_at")), "x")), 422, "linked_payment_immutable")
	// no revisions were added
	for _, pid := range []string{str(t, cp, "payment_id"), str(t, mem, "payment_id")} {
		if len(revisionsOf(t, ta, pid)) != 1 {
			t.Fatal("rejected correction added a revision")
		}
	}
	// the 403 for a non-sender still outranks the linked check
	expectErr(t, correct(t, e.tok["cy"], str(t, cp, "payment_id"), corrBody(1, 5, cc, "x")), 403, "forbidden")
}

func TestR251_CorrectionFloorIsTheRefundedAmount(t *testing.T) {
	e := setupF(t)
	ta, tb := e.tok["ada"], e.tok["bob"]
	p := mustPay(t, ta, "bob", 500, nil)
	pid := str(t, p, "payment_id")
	ca := parseTS(t, str(t, p, "created_at"))
	mustRefund(t, tb, pid, 120)
	mustRefund(t, tb, pid, 80) // refunded 200
	before := stateOfF(t, e)
	k := newKey()
	expectErr(t, post(t, "/payments/"+pid+"/corrections", ta, k, corrBody(1, 199, ca, "too low")), 422, "refund_exceeds_payment")
	expectErr(t, correct(t, ta, pid, corrBody(1, 0, ca, "reverse")), 422, "refund_exceeds_payment")
	if !reflect.DeepEqual(before, stateOfF(t, e)) || len(revisionsOf(t, ta, pid)) != 1 {
		t.Fatal("a rejected correction changed state")
	}
	// equal to the refunded amount is allowed (and the key was not claimed)
	expect(t, post(t, "/payments/"+pid+"/corrections", ta, k, corrBody(1, 200, ca, "exactly the refunded amount")), 201)
	// now no further refund fits and a raise is fine
	expectErr(t, refund(t, tb, pid, 1), 422, "refund_exceeds_payment")
	expect(t, correct(t, ta, pid, corrBody(2, 260, ca, "raise")), 201)
	mustRefund(t, tb, pid, 60)
	expectErr(t, correct(t, ta, pid, corrBody(3, 259, ca, "below")), 422, "refund_exceeds_payment")
	// correction debits are checked against AVAILABLE: ada's money is held by an open authorization
	q := mustPay(t, ta, "cy", 10, nil)
	ta2 := e.tok["ada"]
	_ = ta2
	mustAuthorize(t, ta, "dee", 9700, nil) // ada total ~ 9800-ish; leave little available
	av := avail(t, ta)
	expectErr(t, correct(t, ta, str(t, q, "payment_id"), corrBody(1, 10+av+1, parseTS(t, str(t, q, "created_at")), "too big")), 409, "insufficient_funds")
	expect(t, correct(t, ta, str(t, q, "payment_id"), corrBody(1, 10+av, parseTS(t, str(t, q, "created_at")), "exactly available")), 201)
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}
