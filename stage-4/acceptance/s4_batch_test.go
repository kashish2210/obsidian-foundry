package acceptance

import (
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func codeOf(t testing.TB, r resp) any {
	t.Helper()
	return r.obj(t)["error"].(map[string]any)["code"]
}

// ---------------- R252 ----------------

func TestR252_BatchAuth(t *testing.T) {
	s := setupS(t)
	good := cbody(item("b_1", 1, 450, s.t1, "fix"))
	k := newKey()
	expectErr(t, post(t, "/correction-batches", "", k, good), 401, "unauthenticated")
	expectErr(t, post(t, "/correction-batches", "bogus", k, good), 401, "unauthenticated")
	for _, h := range []string{"ada", "bob", "cy", "dee"} { // even the sender of the payment
		expectErr(t, post(t, "/correction-batches", s.e.tok[h], k, good), 403, "forbidden")
	}
	expectErr(t, call(t, "POST", "/correction-batches", s.e.tok["op"], nil, good), 400, "missing_idempotency_key")
	expectErr(t, call(t, "POST", "/correction-batches", s.e.tok["op"], map[string]string{"Idempotency-Key": ""}, good), 400, "missing_idempotency_key")
	expectErr(t, post(t, "/correction-batches", s.e.tok["op"], longKey(256), good), 422, "validation_failed")
	expectErr(t, post(t, "/correction-batches", s.e.tok["op"], k, `{`), 400, "malformed_request")
	expectErr(t, post(t, "/correction-batches", s.e.tok["op"], k, `[]`), 400, "malformed_request")
	if len(revisionsOf(t, s.e.tok["ada"], "b_1")) != 1 {
		t.Fatal("rejected requests changed history")
	}
	// unknown fields are ignored at both levels
	it := item("b_1", 1, 450, s.t1, "fix")
	it["zzz"] = []int{1}
	r := post(t, "/correction-batches", s.e.tok["op"], k, map[string]any{"corrections": []any{it}, "extra": true})
	expect(t, r, 201)
	// with no operators configured, nobody may use it
	reset(t, fixtureCur("EUR", 2, nil, fu{"ada", 100}, fu{"bob", 100}))
	ta := login(t, "ada@example.com")
	expectErr(t, post(t, "/correction-batches", ta, newKey(), good), 403, "forbidden")
}

// ---------------- R253 ----------------

func TestR253_BatchShape(t *testing.T) {
	s := setupS(t)
	op := s.e.tok["op"]
	bad := []any{
		map[string]any{}, map[string]any{"corrections": nil}, map[string]any{"corrections": "x"}, map[string]any{"corrections": 5},
		map[string]any{"corrections": map[string]any{"payment_id": "b_1"}}, map[string]any{"corrections": []any{}},
		cbody("b_1"), cbody(nil), cbody(5), cbody([]any{}), cbody(item("b_1", 1, 450, s.t1, "x"), "str"),
		cbody(item("b_1", 1, 450, s.t1, "x"), item("b_1", 1, 440, s.t1, "dup")), // duplicate payment_id
	}
	for i, b := range bad {
		r := cbatch(t, op, b)
		if r.Status != 422 {
			t.Fatalf("case %d: want 422, got %s", i, r)
		}
		expectErr(t, r, 422, "validation_failed")
	}
	// 33 entries (even with valid-looking ids) is a shape error
	var many []any
	for i := 0; i < 33; i++ {
		many = append(many, item("x"+itoa(i), 1, 1, s.t1, "r"))
	}
	expectErr(t, cbatch(t, op, cbody(many...)), 422, "validation_failed")
	if len(revisionsOf(t, s.e.tok["ada"], "b_1")) != 1 {
		t.Fatal("rejected batches changed history")
	}
	// exactly 32 distinct payments is accepted
	var items []any
	for i := 0; i < 32; i++ {
		p := mustPay(t, s.e.tok["ada"], "bob", 1, nil)
		items = append(items, item(str(t, p, "payment_id"), 1, 0, parseTS(t, str(t, p, "created_at")), "reverse"))
	}
	m := mustBatch(t, op, cbody(items...))
	if arr, ok := m["revisions"].([]any); !ok || len(arr) != 32 {
		t.Fatalf("32 revisions expected: %v", m["revisions"])
	}
	// one single item is fine too
	expect(t, cbatch(t, op, cbody(item("b_2", 1, 300, s.t2, "same amount"))), 201)
}

// ---------------- R254 ----------------

func TestR254_ItemErrors(t *testing.T) {
	s := setupS(t)
	op := s.e.tok["op"]
	ta, tb := s.e.tok["ada"], s.e.tok["bob"]
	good := func() map[string]any { return item("b_1", 1, 450, s.t1, "fix") }
	mut := func(f func(m map[string]any)) map[string]any { m := good(); f(m); return m }
	var invalid []map[string]any
	for _, k := range []string{"payment_id", "expected_revision", "amount", "effective_at", "reason"} {
		k := k
		invalid = append(invalid, mut(func(m map[string]any) { delete(m, k) }))
	}
	for _, v := range []any{0, -1, 1.5, "1", true, nil} {
		v := v
		invalid = append(invalid, mut(func(m map[string]any) { m["expected_revision"] = v }))
	}
	for _, v := range []any{-1, 1000000001, 1.5, "5", true, nil} {
		v := v
		invalid = append(invalid, mut(func(m map[string]any) { m["amount"] = v }))
	}
	for _, v := range []any{"", longKey(201), 5, nil, true} {
		v := v
		invalid = append(invalid, mut(func(m map[string]any) { m["reason"] = v }))
	}
	for _, v := range []any{"2026-09-24", "2026-09-24T13:20:00", "garbage", "", 5, nil, tsStr(time.Now().UTC().Add(2 * time.Hour))} {
		v := v
		invalid = append(invalid, mut(func(m map[string]any) { m["effective_at"] = v }))
	}
	for _, v := range []any{5, true, nil, []any{}} {
		v := v
		invalid = append(invalid, mut(func(m map[string]any) { m["payment_id"] = v }))
	}
	for i, it := range invalid {
		r := cbatch(t, op, cbody(it))
		if r.Status != 422 {
			t.Fatalf("invalid case %d %v: want 422, got %s", i, it, r)
		}
		expectErr(t, r, 422, "validation_failed")
	}
	// unknown payment
	expectErr(t, cbatch(t, op, cbody(item("nope", 1, 5, s.t1, "x"))), 404, "not_found")
	// stale
	expectErr(t, cbatch(t, op, cbody(item("b_1", 2, 450, s.t1, "x"))), 409, "stale_revision")
	expectErr(t, cbatch(t, op, cbody(item("b_1", 99, 450, s.t1, "x"))), 409, "stale_revision")
	// capture and refund payments are immutable
	az := mustAuthorize(t, ta, "bob", 300, nil)
	cp := mustCapture(t, tb, aid(t, az), map[string]any{"amount": 100})
	expectErr(t, cbatch(t, op, cbody(item(str(t, cp, "payment_id"), 1, 50, parseTS(t, str(t, cp, "created_at")), "x"))), 422, "linked_payment_immutable")
	rf := mustRefund(t, tb, "b_1", 100)
	expectErr(t, cbatch(t, op, cbody(item(str(t, rf, "payment_id"), 1, 50, parseTS(t, str(t, rf, "created_at")), "x"))), 422, "linked_payment_immutable")
	// below the refunded amount
	expectErr(t, cbatch(t, op, cbody(item("b_1", 1, 99, s.t1, "too low"))), 422, "refund_exceeds_payment")
	expect(t, cbatch(t, op, cbody(item("b_1", 1, 100, s.t1, "exactly refunded"))), 201) // equal is allowed
	// 5xx never; the earlier rejections left nothing behind
	if len(revisionsOf(t, ta, str(t, cp, "payment_id"))) != 1 || len(revisionsOf(t, tb, str(t, rf, "payment_id"))) != 1 {
		t.Fatal("rejected items changed history")
	}
}

// ---------------- R255 ----------------

func TestR255_OperatorCorrectsOthersButReadsNothing(t *testing.T) {
	s := setupS(t)
	op, ta, tb, tc := s.e.tok["op"], s.e.tok["ada"], s.e.tok["bob"], s.e.tok["cy"]
	rq := rid(t, mustRequest(t, tb, "ada", 40))
	rp := postK(t, "/requests/"+rq+"/pay", ta, map[string]any{})
	expect(t, rp, 201)
	rpid := str(t, rp.obj(t), "payment_id")
	adaB, bobB, cyB := balAt(t, ta, ""), balAt(t, tb, ""), balAt(t, tc, "")
	m := mustBatch(t, op, cbody(
		item("b_1", 1, 450, s.t1, "ordinary"),
		item("b_2", 1, 250, s.t2, "ordinary 2"),
		item(rpid, 1, 30, parseTS(t, str(t, rp.obj(t), "created_at")), "request payment")))
	if len(m["revisions"].([]any)) != 3 {
		t.Fatalf("%v", m)
	}
	// the operator is not a party: no read access to revisions or statements
	expectErr(t, get(t, "/payments/b_1/revisions", op), 404, "not_found")
	expectErr(t, get(t, "/payments/"+rpid+"/revisions", op), 404, "not_found")
	if st := fullStmt(t, op, ""); len(entriesOf(t, st)) != 0 {
		t.Fatalf("operator statement: %v", st)
	}
	// ... and no extra access to private activity
	if items, _ := activity(t, op, "?limit=200"); len(items) > 0 {
		for _, it := range items {
			if it["visibility"] == "private" {
				t.Fatalf("operator sees a private item: %v", it)
			}
		}
	}
	// the parties see the new revision; a third party does not
	for _, h := range []string{"ada", "bob"} {
		revs := revisionsOf(t, s.e.tok[h], "b_1")
		if len(revs) != 2 || num(t, revs[1], "amount") != 450 {
			t.Fatalf("%s revisions: %v", h, revs)
		}
	}
	expectErr(t, get(t, "/payments/b_1/revisions", tc), 404, "not_found")
	// money moved: b_1 500->450 (bob gives back 50), b_2 300->250 (cy gives back 50), request payment 40->30 (bob... receiver gives 10)
	if balAt(t, ta, "") != adaB+50+10 || balAt(t, tb, "") != bobB-50+50-10 || balAt(t, tc, "") != cyB-50 {
		t.Fatalf("balances after batch: ada %d bob %d cy %d", balAt(t, ta, ""), balAt(t, tb, ""), balAt(t, tc, ""))
	}
	if s.e.sum(t) != s.e.total {
		t.Fatal("sum")
	}
}

// ---------------- R256 ----------------

func TestR256_SettlementCompleteness(t *testing.T) {
	s := setupS(t)
	op := s.e.tok["op"]
	x := s.commit.Add(-time.Hour)
	mk := func(i int, amt int64, eff any) map[string]any { return item(s.m[i], 1, amt, eff, "settle fix") }
	before := stateOfS(t, s, "b_1", s.m[0])
	// incomplete: a subset of the members
	expectErr(t, cbatch(t, op, cbody(mk(0, 100, x))), 422, "incomplete_settlement")
	expectErr(t, cbatch(t, op, cbody(mk(0, 100, x), mk(1, 50, x))), 422, "incomplete_settlement")
	expectErr(t, cbatch(t, op, cbody(mk(2, 20, x), item("b_1", 1, 450, s.t1, "ordinary ok"))), 422, "incomplete_settlement")
	// completeness is checked before the identical-instant rule
	expectErr(t, cbatch(t, op, cbody(mk(0, 100, x), mk(1, 50, x.Add(time.Second)))), 422, "incomplete_settlement")
	// all members, but different instants -> validation_failed
	expectErr(t, cbatch(t, op, cbody(mk(0, 100, x), mk(1, 50, x.Add(time.Second)), mk(2, 20, x))), 422, "validation_failed")
	expectErr(t, cbatch(t, op, cbody(mk(0, 100, x), mk(1, 50, x), mk(2, 20, x.Add(-time.Microsecond)))), 422, "validation_failed")
	if !reflect.DeepEqual(before, stateOfS(t, s, "b_1", s.m[0])) {
		t.Fatal("rejected batches changed state")
	}
	// single corrections of members stay immutable; nonmembers stay correctable
	expectErr(t, correct(t, s.e.tok["ada"], s.m[0], corrBody(1, 90, x, "x")), 422, "linked_payment_immutable")
	expect(t, correct(t, s.e.tok["ada"], "b_1", corrBody(1, 480, s.t1, "single ok")), 201)
	// every member, one instant spelled with different offsets: accepted
	plus2 := x.In(time.FixedZone("", 2*3600)).Format("2006-01-02T15:04:05.000000-07:00")
	minus5 := x.In(time.FixedZone("", -5*3600)).Format("2006-01-02T15:04:05.000000-07:00")
	if !strings.Contains(plus2, "+02:00") || !strings.Contains(minus5, "-05:00") {
		t.Fatal("test spelling")
	}
	m := mustBatch(t, op, cbody(mk(0, 100, tsMicro(x)), mk(1, 50, plus2), mk(2, 20, minus5)))
	revs := m["revisions"].([]any)
	if len(revs) != 3 {
		t.Fatal("three revisions")
	}
	// the new effective instants are the same instant
	for i, r := range revs {
		if !parseTS(t, str(t, r.(map[string]any), "effective_at")).Equal(x.Truncate(time.Microsecond)) {
			t.Fatalf("revision %d effective_at %v", i, r.(map[string]any)["effective_at"])
		}
	}
	// the batch changes the members' amounts: reverse the whole settlement
	s2 := setupS(t)
	x2 := s2.commit.Add(-time.Hour)
	ada0, bob0, cy0, dee0 := balAt(t, s2.e.tok["ada"], ""), balAt(t, s2.e.tok["bob"], ""), balAt(t, s2.e.tok["cy"], ""), balAt(t, s2.e.tok["dee"], "")
	mustBatch(t, s2.e.tok["op"], cbody(item(s2.m[0], 1, 0, x2, "reverse"), item(s2.m[1], 1, 0, x2, "reverse"), item(s2.m[2], 1, 0, x2, "reverse")))
	if balAt(t, s2.e.tok["ada"], "") != ada0+100 || balAt(t, s2.e.tok["bob"], "") != bob0-100+50 ||
		balAt(t, s2.e.tok["cy"], "") != cy0-50+20 || balAt(t, s2.e.tok["dee"], "") != dee0-20 {
		t.Fatal("reversing the settlement moved the wrong amounts")
	}
	if s2.e.sum(t) != s2.e.total {
		t.Fatal("sum")
	}
	// membership is unchanged and the members still appear (with zero) in statements
	for _, en := range entriesOf(t, fullStmt(t, s2.e.tok["ada"], "")) {
		if entryPID(t, en) == s2.m[0] {
			if num(t, en, "delta") != 0 || num(t, en, "revision") != 2 {
				t.Fatalf("member entry: %v", en)
			}
			if en["payment"].(map[string]any)["settlement_id"] != s2.sid {
				t.Fatal("membership lost")
			}
		}
	}
}

// ---------------- R257 ----------------

func TestR257_ItemErrorsFirstInInputOrder(t *testing.T) {
	s := setupS(t)
	op := s.e.tok["op"]
	before := stateOfS(t, s, "b_1", "b_2")
	stale := item("b_1", 5, 450, s.t1, "x")
	invalid := item("b_2", 1, -1, s.t2, "x")
	notFound := item("nope", 1, 10, s.t1, "x")
	for _, c := range []struct {
		name  string
		items []any
		st    int
		code  string
	}{
		{"stale then invalid", []any{stale, invalid}, 409, "stale_revision"},
		{"invalid then stale", []any{invalid, stale}, 422, "validation_failed"},
		{"404 then stale", []any{notFound, stale}, 404, "not_found"},
		{"stale then 404", []any{stale, notFound}, 409, "stale_revision"},
		{"404 then invalid", []any{notFound, invalid}, 404, "not_found"},
		{"invalid then 404", []any{invalid, notFound}, 422, "validation_failed"},
		// within one item: validation, then 404
		{"invalid and unknown in one item", []any{item("nope", 1, -5, s.t1, "x")}, 422, "validation_failed"},
	} {
		expectErr(t, cbatch(t, op, cbody(c.items...)), c.st, c.code)
		_ = c.name
	}
	// within one item: 404, then linked, then stale, then refund floor
	az := mustAuthorize(t, s.e.tok["ada"], "bob", 300, nil)
	cp := mustCapture(t, s.e.tok["bob"], aid(t, az), map[string]any{"amount": 100})
	cc := parseTS(t, str(t, cp, "created_at"))
	expectErr(t, cbatch(t, op, cbody(item(str(t, cp, "payment_id"), 9, 50, cc, "x"))), 422, "linked_payment_immutable") // linked before stale
	mustRefund(t, s.e.tok["bob"], "b_1", 100)
	expectErr(t, cbatch(t, op, cbody(item("b_1", 9, 50, s.t1, "x"))), 409, "stale_revision")         // stale before the refund floor
	expectErr(t, cbatch(t, op, cbody(item("b_1", 1, 50, s.t1, "x"))), 422, "refund_exceeds_payment") // floor last among item errors
	// item errors outrank settlement completeness and everything after
	x := s.commit.Add(-time.Hour)
	expectErr(t, cbatch(t, op, cbody(item(s.m[0], 1, 100, x, "x"), notFound)), 404, "not_found")
	expectErr(t, cbatch(t, op, cbody(item(s.m[0], 1, 100, x, "x"), item("b_2", 7, 1, s.t2, "x"))), 409, "stale_revision")
	// completeness outranks the identical-instant rule (checked in R256) and affordability
	expectErr(t, cbatch(t, op, cbody(item(s.m[0], 1, 1000000000, x, "x"))), 422, "incomplete_settlement")
	// the instant rule outranks affordability
	expectErr(t, cbatch(t, op, cbody(item(s.m[0], 1, 1000000000, x, "x"), item(s.m[1], 1, 50, x.Add(time.Second), "x"), item(s.m[2], 1, 20, x, "x"))), 422, "validation_failed")
	// with a clean instant it is then an affordability error
	expectErr(t, cbatch(t, op, cbody(item(s.m[0], 1, 1000000000, x, "x"), item(s.m[1], 1, 50, x, "x"), item(s.m[2], 1, 20, x, "x"))), 409, "insufficient_funds")
	// nothing changed except the refund made above
	_ = before
	if len(revisionsOf(t, s.e.tok["bob"], "b_2")) != 1 {
		t.Fatal("history changed by rejected batches")
	}
	if len(revisionsOf(t, s.e.tok["ada"], "b_1")) != 1 || len(revisionsOf(t, s.e.tok["ada"], s.m[0])) != 1 {
		t.Fatal("rejected batches added revisions")
	}
}

// scenario C: combined affordability
//
//	P1 ada->bob 50 at D-2d, P2 bob->ada 60 at D-1d, P3 ada->cy 10 at D-3d
//	finals ada 100, bob 100, cy 10 ; openings ada 100, bob 110, cy 0
func setupC(t testing.TB) (*env, time.Time, time.Time, time.Time) {
	t.Helper()
	t3, t1, t2 := ago(3*day), ago(2*day), ago(day)
	e := setupP(t, []any{
		seedP("c_1", "ada", "bob", 50, "public", t1),
		seedP("c_2", "bob", "ada", 60, "public", t2),
		seedP("c_3", "ada", "cy", 10, "public", t3),
	}, fu{"ada", 100}, fu{"bob", 100}, fu{"cy", 10}, fu{"dee", 0}, fu{"op", 0})
	return e, t1, t2, t3
}

func TestR257_AffordabilityIsCombined(t *testing.T) {
	// each item alone is unaffordable today, together they net to zero
	e, _, t2, _ := setupC(t)
	one := item("c_1", 1, 200, t2, "raise c_1") // ada pays +150 (has 100)
	two := item("c_2", 1, 210, t2, "raise c_2") // bob pays +150 (has 100)
	expectErr(t, correct(t, e.tok["ada"], "c_1", corrBody(1, 200, t2, "alone")), 409, "insufficient_funds")
	expectErr(t, correct(t, e.tok["bob"], "c_2", corrBody(1, 210, t2, "alone")), 409, "insufficient_funds")
	expectErr(t, cbatch(t, e.tok["op"], cbody(one)), 409, "insufficient_funds")
	expectErr(t, cbatch(t, e.tok["op"], cbody(two)), 409, "insufficient_funds")
	before := stateOfF(t, e)
	m := mustBatch(t, e.tok["op"], cbody(one, two))
	if len(m["revisions"].([]any)) != 2 {
		t.Fatal("two revisions")
	}
	if balAt(t, e.tok["ada"], "") != 100 || balAt(t, e.tok["bob"], "") != 100 {
		t.Fatalf("combined effect must net to zero: ada %d bob %d", balAt(t, e.tok["ada"], ""), balAt(t, e.tok["bob"], ""))
	}
	if reflect.DeepEqual(before, stateOfF(t, e)) {
		t.Fatal("the batch did nothing")
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR257_CombinedAffordabilityReverse(t *testing.T) {
	// each item alone is affordable today (ada pays +80 of her 100), together they are not (160)
	e2, _, t22, _ := setupC(t)
	expect(t, cbatch(t, e2.tok["op"], cbody(item("c_1", 1, 130, t22, "alone"))), 201)
	e3, _, t23, _ := setupC(t)
	expect(t, cbatch(t, e3.tok["op"], cbody(item("c_3", 1, 90, t23, "alone"))), 201)
	e, _, t2, _ := setupC(t)
	before := stateOfF(t, e)
	expectErr(t, cbatch(t, e.tok["op"], cbody(item("c_1", 1, 130, t2, "both"), item("c_3", 1, 90, t2, "both"))), 409, "insufficient_funds")
	expectErr(t, cbatch(t, e.tok["op"], cbody(item("c_3", 1, 90, t2, "both"), item("c_1", 1, 130, t2, "both"))), 409, "insufficient_funds")
	if !reflect.DeepEqual(before, stateOfF(t, e)) {
		t.Fatal("a rejected batch changed state")
	}
}

func TestR257_AffordabilityUsesAvailable(t *testing.T) {
	e, _, t2, _ := setupC(t)
	mustAuthorize(t, e.tok["ada"], "dee", 60, nil) // ada: total 100, available 40
	expectErr(t, cbatch(t, e.tok["op"], cbody(item("c_1", 1, 100, t2, "debit 50"))), 409, "insufficient_funds")
	expect(t, cbatch(t, e.tok["op"], cbody(item("c_1", 1, 90, t2, "debit 40"))), 201)
}

func TestR257_HistoricalOverdraftLast(t *testing.T) {
	s := setupH(t)
	mustPay(t, s.e.tok["cy"], "ada", 500, nil) // ada can afford the debit today
	op := s.e.tok["op"]
	before := stateOfF(t, s.e)
	k := newKey()
	expectErr(t, post(t, "/correction-batches", op, k, cbody(item("h_q", 1, 1100, s.tIn, "too early"))), 409, "historical_overdraft")
	expectErr(t, cbatch(t, op, cbody(item("h_q", 1, 100, s.tIn.Add(-time.Hour), "before funds"))), 409, "historical_overdraft")
	// unaffordable today wins over a historical problem
	expectErr(t, cbatch(t, op, cbody(item("h_q", 1, 100000, s.tIn.Add(-time.Hour), "huge"))), 409, "insufficient_funds")
	if !reflect.DeepEqual(before, stateOfF(t, s.e)) {
		t.Fatal("rejected batches changed state")
	}
	// the key was not claimed: it works with a body that fits (same-instant movements combine)
	expect(t, post(t, "/correction-batches", op, k, cbody(item("h_q", 1, 1000, s.tIn, "simultaneous"))), 201)
	if balAt(t, s.e.tok["ada"], "as_of="+qe(tsStr(s.tIn))) != 0 {
		t.Fatal("the combined balance at the boundary must be exactly zero")
	}
	// bob would be negative at h_out's time if the payment were reversed
	mustPay(t, s.e.tok["ada"], "bob", 300, nil)
	expectErr(t, cbatch(t, op, cbody(item("h_q", 2, 0, s.tQ, "reverse"))), 409, "historical_overdraft")
}

// ---------------- R258 / R259 ----------------

func TestR258_RejectedBatchLeavesNoTraceAndClaimsNoKey(t *testing.T) {
	s := setupS(t)
	op := s.e.tok["op"]
	before := stateOfS(t, s, "b_1", "b_2", s.m[0])
	bodies := []struct {
		body any
		st   int
		code string
	}{
		{cbody(item("b_1", 9, 450, s.t1, "x")), 409, "stale_revision"},
		{cbody(item("b_1", 1, 450, s.t1, "x"), item("nope", 1, 5, s.t1, "x")), 404, "not_found"},
		{cbody(item("b_1", 1, -1, s.t1, "x")), 422, "validation_failed"},
		{cbody(item(s.m[0], 1, 90, s.commit, "x")), 422, "incomplete_settlement"},
		{cbody(item("b_1", 1, 1000000000, s.t1, "x")), 409, "insufficient_funds"},
	}
	var keys []string
	for _, b := range bodies {
		k := newKey()
		keys = append(keys, k)
		expectErr(t, post(t, "/correction-batches", op, k, b.body), b.st, b.code)
	}
	if !reflect.DeepEqual(before, stateOfS(t, s, "b_1", "b_2", s.m[0])) {
		t.Fatal("rejected batches left a trace")
	}
	// no key was claimed
	for i, k := range keys {
		expect(t, post(t, "/correction-batches", op, k, cbody(item("b_1", int64(i+1), 440+int64(i), s.t1, "fine"))), 201)
	}
}

func TestR259_BatchResponseRevisionsAndReplay(t *testing.T) {
	s := setupS(t)
	op, ta, tb := s.e.tok["op"], s.e.tok["ada"], s.e.tok["bob"]
	// a single correction first: its revision has correction_batch_id null
	single := mustCorrect(t, ta, "b_1", 1, 480, s.t1, "single")
	if v, ok := single["correction_batch_id"]; ok && v != nil {
		t.Fatalf("a single correction must not carry a batch id: %v", single)
	}
	tick()
	k := newKey()
	body := cbody(item("b_2", 1, 250, s.t2, "second first"), item("b_1", 2, 450, s.t1, "then first"))
	r := post(t, "/correction-batches", op, k, body)
	expect(t, r, 201)
	m := r.obj(t)
	bid := str(t, m, "correction_batch_id")
	rec := parseTS(t, str(t, m, "recorded_at"))
	revs := m["revisions"].([]any)
	if bid == "" || len(revs) != 2 {
		t.Fatalf("%v", m)
	}
	// input order, one shared recorded_at, each exposing correction_batch_id
	wantPids := []string{"b_2", "b_1"}
	wantRev := []int64{2, 3}
	wantAmt := []int64{250, 450}
	for i, x := range revs {
		rv := x.(map[string]any)
		if rv["payment_id"] != wantPids[i] || num(t, rv, "revision") != wantRev[i] || num(t, rv, "amount") != wantAmt[i] || rv["correction_batch_id"] != bid {
			t.Fatalf("revision %d: %v", i, rv)
		}
		if !parseTS(t, str(t, rv, "recorded_at")).Equal(rec) {
			t.Fatalf("revisions must share recorded_at: %v vs %v", rv["recorded_at"], m["recorded_at"])
		}
		for _, f := range []string{"effective_at", "reason"} {
			if _, ok := rv[f]; !ok {
				t.Fatalf("revision lacks %s", f)
			}
		}
	}
	// strictly later than every member's previous recorded_at
	b1 := revisionsOf(t, ta, "b_1")
	b2 := revisionsOf(t, tb, "b_2")
	if len(b1) != 3 || len(b2) != 2 {
		t.Fatalf("revisions: %d %d", len(b1), len(b2))
	}
	if !rec.After(parseTS(t, str(t, b1[1], "recorded_at"))) || !rec.After(parseTS(t, str(t, b2[0], "recorded_at"))) {
		t.Fatal("batch recorded_at must be strictly after each member's previous recorded_at")
	}
	// GET revisions: batch id on batch revisions, null on r1 and on the single correction
	for i, rv := range b1 {
		v, has := rv["correction_batch_id"]
		if !has {
			t.Fatalf("revision %d lacks correction_batch_id (must be present, null when none)", i)
		}
		if (i == 2) != (v != nil) || (i == 2 && v != bid) {
			t.Fatalf("revision %d correction_batch_id %v", i, v)
		}
	}
	if v, has := b2[0]["correction_batch_id"]; !has || v != nil {
		t.Fatalf("r1 must carry correction_batch_id: null: %v", b2[0])
	}
	if b2[1]["correction_batch_id"] != bid {
		t.Fatal("batch revision id in GET revisions")
	}
	// replay: 200 with the original body; a different body: 409; claimed key before validation
	for i := 0; i < 2; i++ {
		rep := post(t, "/correction-batches", op, k, body)
		expect(t, rep, 200)
		requireSameJSON(t, r, rep)
	}
	expectErr(t, post(t, "/correction-batches", op, k, cbody(item("b_2", 1, 251, s.t2, "x"), item("b_1", 2, 450, s.t1, "x"))), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/correction-batches", op, k, map[string]any{"corrections": []any{}}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/correction-batches", op, k, map[string]any{}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/correction-batches", op, k, `{`), 400, "malformed_request")
	if len(revisionsOf(t, ta, "b_1")) != 3 {
		t.Fatal("replays added revisions")
	}
	// the same key with the same body on a different operator is independent (keys are per user)
	// effective_at spelled differently is the same instant but a different JSON value: key reuse
	body2 := cbody(item("b_2", 2, 240, s.t2, "again"))
	k2 := newKey()
	expect(t, post(t, "/correction-batches", op, k2, body2), 201)
	alt := cbody(item("b_2", 2, 240, s.t2.In(time.FixedZone("", 3600)).Format("2006-01-02T15:04:05-07:00"), "again"))
	expectErr(t, post(t, "/correction-batches", op, k2, alt), 409, "idempotency_key_reuse")
}

// ---------------- R260 ----------------

func TestR260_OriginalsAndSnapshotsUnchanged(t *testing.T) {
	s := setupS(t)
	op, ta, tb := s.e.tok["op"], s.e.tok["ada"], s.e.tok["bob"]
	orig := post(t, "/payments", ta, "orig-key", map[string]any{"to_handle": "bob", "amount": 100, "note": "orig"})
	expect(t, orig, 201)
	opid := str(t, orig.obj(t), "payment_id")
	snapA := fullStmt(t, ta, "")
	snap := str(t, snapA, "snapshot")
	feedBefore, _ := activity(t, ta, "?limit=200")
	x := s.commit.Add(-time.Hour)
	mustBatch(t, op, cbody(
		item(opid, 1, 40, parseTS(t, str(t, orig.obj(t), "created_at")), "orig"),
		item("b_1", 1, 400, s.t1, "seed"),
		item(s.m[0], 1, 0, x, "settle"), item(s.m[1], 1, 0, x, "settle"), item(s.m[2], 1, 0, x, "settle")))
	// the feed shows the original amounts; the original POST and the settlement replay are untouched
	feedAfter, _ := activity(t, ta, "?limit=200")
	if !reflect.DeepEqual(feedBefore, feedAfter) {
		t.Fatalf("/activity changed:\n%v\n%v", feedBefore, feedAfter)
	}
	rep := post(t, "/payments", ta, "orig-key", map[string]any{"to_handle": "bob", "amount": 100, "note": "orig"})
	expect(t, rep, 200)
	requireSameJSON(t, orig, rep)
	srep := post(t, "/settlements", op, s.settlementKey, s.settlementBody)
	expect(t, srep, 200)
	requireSameJSON(t, s.settlementResp, srep)
	// an older snapshot keeps paging its frozen entries
	old := snapPage(t, ta, snap, "limit=200")
	if !reflect.DeepEqual(stripToken(old), stripToken(snapA)) {
		t.Fatal("snapshot changed by a batch")
	}
	// a fresh statement and /me reflect the batch
	fresh := fullStmt(t, ta, "")
	if reflect.DeepEqual(stripToken(fresh), stripToken(snapA)) {
		t.Fatal("new statement does not reflect the batch")
	}
	found := false
	for _, en := range entriesOf(t, fresh) {
		if entryPID(t, en) == opid {
			found = true
			if num(t, en, "revision") != 2 || num(t, en["payment"].(map[string]any), "amount") != 40 || num(t, en, "delta") != -40 {
				t.Fatalf("fresh entry: %v", en)
			}
		}
	}
	if !found {
		t.Fatal("payment missing")
	}
	checkStatementConsistent(t, fresh)
	if num(t, fresh, "closing_balance") != balAt(t, ta, "") || s.e.sum(t) != s.e.total {
		t.Fatal("closing/sum")
	}
	// known_at before the batch still sees the old revisions
	revs := revisionsOf(t, tb, opid)
	rt := parseTS(t, str(t, revs[1], "recorded_at"))
	old2 := fullStmt(t, ta, "known_at="+qe(tsMicro(rt.Add(-time.Millisecond))))
	for _, en := range entriesOf(t, old2) {
		if entryPID(t, en) == opid && num(t, en, "revision") != 1 {
			t.Fatal("known_at before the batch must select r1")
		}
	}
}

// ---------------- R261 ----------------

func TestR261_ConcurrentSingleAndBatchOneWinner(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
	p := mustPay(t, e.tok["ada"], "bob", 100, nil)
	pid := str(t, p, "payment_id")
	pc := parseTS(t, str(t, p, "created_at"))
	var qs []map[string]any
	for i := 0; i < 10; i++ {
		qs = append(qs, mustPay(t, e.tok["ada"], "bob", 10, nil))
	}
	var wins int64
	rs := fanout(20, func(i int) (resp, error) {
		var r resp
		var err error
		if i%2 == 0 {
			r, err = send("POST", "/payments/"+pid+"/corrections", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()}, corrBody(1, int64(50+i), pc, "single"))
		} else {
			q := qs[i/2]
			r, err = send("POST", "/correction-batches", e.tok["op"], map[string]string{"Idempotency-Key": newKey()},
				cbody(item(pid, 1, int64(50+i), pc, "batch"), item(str(t, q, "payment_id"), 1, 5, parseTS(t, str(t, q, "created_at")), "batch")))
		}
		if err == nil && r.Status == 201 {
			atomic.AddInt64(&wins, 1)
		}
		return r, err
	})
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
		case 409:
			expectErr(t, x.r, 409, "stale_revision")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if wins != 1 {
		t.Fatalf("%d writers succeeded on the shared revision, want exactly one", wins)
	}
	if got := len(revisionsOf(t, e.tok["ada"], pid)); got != 2 {
		t.Fatalf("%d revisions of the shared payment", got)
	}
	// a losing batch must not have touched its other member
	changed := 0
	for _, q := range qs {
		if len(revisionsOf(t, e.tok["ada"], str(t, q, "payment_id"))) != 1 {
			changed++
		}
	}
	if changed > 1 {
		t.Fatalf("%d batch members changed; losing batches must be all-or-nothing", changed)
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR261_ConcurrentBatchesSharingOnePayment(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
	var ps []map[string]any
	for i := 0; i < 6; i++ {
		ps = append(ps, mustPay(t, e.tok["ada"], "bob", 20, nil))
	}
	var mu sync.Mutex
	var winners []int
	fanout(5, func(i int) (resp, error) {
		// batch i covers payments i and i+1: neighbours share exactly one payment
		a, b := ps[i], ps[i+1]
		r, err := send("POST", "/correction-batches", e.tok["op"], map[string]string{"Idempotency-Key": newKey()},
			cbody(item(str(t, a, "payment_id"), 1, 10, parseTS(t, str(t, a, "created_at")), "chain"), item(str(t, b, "payment_id"), 1, 10, parseTS(t, str(t, b, "created_at")), "chain")))
		if err == nil && r.Status == 201 {
			mu.Lock()
			winners = append(winners, i)
			mu.Unlock()
		}
		return r, err
	})
	// the winners are pairwise disjoint: no payment was corrected twice from revision 1
	seen := map[int]bool{}
	for _, w := range winners {
		for _, j := range []int{w, w + 1} {
			if seen[j] {
				t.Fatalf("payment %d corrected by two winning batches %v", j, winners)
			}
			seen[j] = true
		}
	}
	for j, p := range ps {
		n := len(revisionsOf(t, e.tok["ada"], str(t, p, "payment_id")))
		if seen[j] != (n == 2) || n > 2 {
			t.Fatalf("payment %d has %d revisions, winners %v", j, n, winners)
		}
	}
	// the same key concurrently: one 201, the rest identical 200s
	p := mustPay(t, e.tok["ada"], "bob", 30, nil)
	k := newKey()
	body := cbody(item(str(t, p, "payment_id"), 1, 7, parseTS(t, str(t, p, "created_at")), "same"))
	rs := fanout(20, func(int) (resp, error) {
		return send("POST", "/correction-batches", e.tok["op"], map[string]string{"Idempotency-Key": k}, body)
	})
	onlyOneCreated(t, rs)
	if len(revisionsOf(t, e.tok["ada"], str(t, p, "payment_id"))) != 2 {
		t.Fatal("one key, one batch")
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}
