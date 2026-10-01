package acceptance

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// scenario H (history with overdraft traps)
//
//	h_in  cy->ada  1000 at D-2d
//	h_q   ada->bob  100 at D-1d
//	h_out bob->cy   500 at D-12h
//
// finals: ada 900, bob 0, cy 500; openings: ada 0, bob 400, cy 1000
type scenH struct {
	e             *env
	tIn, tQ, tOut time.Time
}

func setupH(t testing.TB) *scenH {
	t.Helper()
	s := &scenH{tIn: ago(2 * day), tQ: ago(1 * day), tOut: ago(12 * hour)}
	s.e = setupP(t, []any{
		seedP("h_in", "cy", "ada", 1000, "public", s.tIn),
		seedP("h_q", "ada", "bob", 100, "public", s.tQ),
		seedP("h_out", "bob", "cy", 500, "private", s.tOut),
	}, fu{"ada", 900}, fu{"bob", 0}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
	return s
}

// state captures everything a failed correction must leave untouched.
func stateOf(t testing.TB, e *env, pids ...string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for h, tk := range e.tok {
		st := fullStmt(t, tk, "")
		delete(st, "snapshot")
		out["stmt-"+h] = st
		out["me-"+h] = meAt(t, tk, "")
		out["past-"+h] = balAt(t, tk, "as_of="+qe(tsStr(ago(36*hour))))
	}
	for _, pid := range pids {
		out["rev-"+pid] = revisionsOf(t, e.tok["ada"], pid)
	}
	return out
}

func TestR213_CorrectionAuthAndKeys(t *testing.T) {
	s := setupH(t)
	body := corrBody(1, 150, s.tQ, "fix")
	k := newKey()
	expectErr(t, post(t, "/payments/h_q/corrections", "", k, body), 401, "unauthenticated")
	expectErr(t, post(t, "/payments/h_q/corrections", "bogus", k, body), 401, "unauthenticated")
	expectErr(t, post(t, "/payments/h_q/corrections", s.e.tok["bob"], k, body), 403, "forbidden") // receiver
	expectErr(t, post(t, "/payments/h_q/corrections", s.e.tok["cy"], k, body), 403, "forbidden")  // third party (public payment)
	expectErr(t, post(t, "/payments/h_q/corrections", s.e.tok["op"], k, body), 403, "forbidden")
	expectErr(t, post(t, "/payments/nope/corrections", s.e.tok["ada"], k, body), 404, "not_found")
	expectErr(t, call(t, "POST", "/payments/h_q/corrections", s.e.tok["ada"], nil, body), 400, "missing_idempotency_key")
	expectErr(t, call(t, "POST", "/payments/h_q/corrections", s.e.tok["ada"], map[string]string{"Idempotency-Key": ""}, body), 400, "missing_idempotency_key")
	expectErr(t, post(t, "/payments/h_q/corrections", s.e.tok["ada"], longKey(256), body), 422, "validation_failed")
	expectErr(t, post(t, "/payments/h_q/corrections", s.e.tok["ada"], k, `{`), 400, "malformed_request")
	expectErr(t, post(t, "/payments/h_q/corrections", s.e.tok["ada"], k, `[]`), 400, "malformed_request")
	if revs := revisionsOf(t, s.e.tok["ada"], "h_q"); len(revs) != 1 {
		t.Fatalf("rejected requests created revisions: %v", revs)
	}
	// the sender succeeds, and the failed attempts did not claim the key
	expect(t, post(t, "/payments/h_q/corrections", s.e.tok["ada"], k, body), 201)
}

func longKey(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'k'
	}
	return string(b)
}

func TestR214_CorrectionValidation(t *testing.T) {
	s := setupH(t)
	tk := s.e.tok["ada"]
	before := stateOf(t, s.e, "h_q")
	valid := func() map[string]any { return corrBody(1, 150, s.tQ, "fix") }
	mut := func(f func(m map[string]any)) map[string]any {
		m := valid()
		f(m)
		return m
	}
	var bad []map[string]any
	for _, k := range []string{"expected_revision", "amount", "effective_at", "reason"} {
		k := k
		bad = append(bad, mut(func(m map[string]any) { delete(m, k) }))
	}
	for _, v := range []any{0, -1, 1.5, "1", true, nil, []any{}, 0.0} {
		v := v
		bad = append(bad, mut(func(m map[string]any) { m["expected_revision"] = v }))
	}
	for _, v := range []any{-1, 1000000001, 1.5, "5", true, nil, []any{}} {
		v := v
		bad = append(bad, mut(func(m map[string]any) { m["amount"] = v }))
	}
	for _, v := range []any{"", longKey(201), 5, true, nil, []any{"x"}} {
		v := v
		bad = append(bad, mut(func(m map[string]any) { m["reason"] = v }))
	}
	for _, v := range []any{"2026-09-24", "2026-09-24T13:20:00", "garbage", "", 5, true, nil,
		tsStr(time.Now().UTC().Add(2 * time.Hour)), tsStr(time.Now().UTC().Add(30 * day))} {
		v := v
		bad = append(bad, mut(func(m map[string]any) { m["effective_at"] = v }))
	}
	for i, b := range bad {
		r := correct(t, tk, "h_q", b)
		if r.Status != 422 {
			t.Fatalf("case %d %v: want 422, got %s", i, b, r)
		}
		expectErr(t, r, 422, "validation_failed")
	}
	if !reflect.DeepEqual(before, stateOf(t, s.e, "h_q")) {
		t.Fatal("a rejected correction changed state")
	}
	// boundaries that are valid by range: reason of 1 and 200 chars; amount 1000000000 is only unaffordable
	expect(t, correct(t, tk, "h_q", mut(func(m map[string]any) { m["reason"] = "x" })), 201)
	expect(t, correct(t, tk, "h_q", mut(func(m map[string]any) { m["expected_revision"] = 2; m["reason"] = longKey(200) })), 201)
	expectErr(t, correct(t, tk, "h_q", mut(func(m map[string]any) { m["expected_revision"] = 3; m["amount"] = 1000000000 })), 409, "insufficient_funds")
	// 200 characters, not bytes
	expect(t, correct(t, tk, "h_q", mut(func(m map[string]any) { m["expected_revision"] = 3; m["reason"] = repeatRune("🎉", 200) })), 201)
	expectErr(t, correct(t, tk, "h_q", mut(func(m map[string]any) { m["expected_revision"] = 4; m["reason"] = repeatRune("🎉", 201) })), 422, "validation_failed")
	// unknown fields are ignored
	expect(t, correct(t, tk, "h_q", mut(func(m map[string]any) { m["expected_revision"] = 4; m["zzz"] = []int{1} })), 201)
	// an effective time an hour after the payment's own instant (still before h_out) is fine
	expect(t, correct(t, tk, "h_q", corrBody(5, 150, s.tQ.Add(time.Hour), "later effective")), 201)
}

func repeatRune(r string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += r
	}
	return out
}

func TestR215_CorrectionResponseAndRecordedTimes(t *testing.T) {
	s := setupH(t)
	tk := s.e.tok["ada"]
	r := correct(t, tk, "h_q", corrBody(1, 150, s.tQ, "corrected amount"))
	expect(t, r, 201)
	m := r.obj(t)
	if m["payment_id"] != "h_q" || num(t, m, "revision") != 2 || num(t, m, "amount") != 150 || m["reason"] != "corrected amount" {
		t.Fatalf("body: %v", m)
	}
	if !parseTS(t, str(t, m, "effective_at")).Equal(s.tQ) {
		t.Fatalf("effective_at %v", m["effective_at"])
	}
	rec := parseTS(t, str(t, m, "recorded_at"))
	if d := time.Since(rec); d < -5*time.Second || d > 30*time.Second {
		t.Fatalf("recorded_at must be server time, got %v (now %v)", rec, time.Now())
	}
	// strictly increasing recorded times, even for rapid corrections
	prev := rec
	amounts := []int64{120, 160, 100, 130, 110}
	for i, a := range amounts {
		x := mustCorrect(t, tk, "h_q", int64(i+2), a, s.tQ, "again")
		cur := parseTS(t, str(t, x, "recorded_at"))
		if !cur.After(prev) {
			t.Fatalf("recorded_at not strictly increasing: %v then %v", prev, cur)
		}
		if num(t, x, "revision") != int64(i+3) {
			t.Fatalf("revision %v", x["revision"])
		}
		prev = cur
	}
	// parties and visibility never change; revisions are immutable and ordered
	revs := revisionsOf(t, tk, "h_q")
	if len(revs) != 7 {
		t.Fatalf("%d revisions", len(revs))
	}
	var last time.Time
	for i, rv := range revs {
		if num(t, rv, "revision") != int64(i+1) {
			t.Fatalf("order: %v", rv)
		}
		rt := parseTS(t, str(t, rv, "recorded_at"))
		if i > 0 && !rt.After(last) {
			t.Fatal("recorded times must strictly increase")
		}
		last = rt
	}
	en := entriesOf(t, fullStmt(t, tk, ""))
	for _, e := range en {
		if entryPID(t, e) == "h_q" {
			p := e["payment"].(map[string]any)
			if p["from_handle"] != "ada" || p["to_handle"] != "bob" || p["visibility"] != "public" || p["from_user_id"] != "u_ada" {
				t.Fatalf("a correction changed parties or visibility: %v", p)
			}
		}
	}
}

func TestR216_StaleRevisionAndReplay(t *testing.T) {
	s := setupH(t)
	tk := s.e.tok["ada"]
	k := newKey()
	body := corrBody(1, 150, s.tQ, "first")
	first := post(t, "/payments/h_q/corrections", tk, k, body)
	expect(t, first, 201)
	// stale (behind) and ahead
	expectErr(t, correct(t, tk, "h_q", corrBody(1, 160, s.tQ, "stale")), 409, "stale_revision")
	expectErr(t, correct(t, tk, "h_q", corrBody(3, 160, s.tQ, "ahead")), 409, "stale_revision")
	expectErr(t, correct(t, tk, "h_q", corrBody(99, 160, s.tQ, "ahead")), 409, "stale_revision")
	// a newer revision now exists
	r3 := correct(t, tk, "h_q", corrBody(2, 170, s.tQ, "second"))
	expect(t, r3, 201)
	// the replay returns the ORIGINAL revision body, with 200
	for i := 0; i < 3; i++ {
		rep := post(t, "/payments/h_q/corrections", tk, k, body)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
		if num(t, rep.obj(t), "revision") != 2 {
			t.Fatalf("replay body %s", rep)
		}
	}
	// whitespace/order independent replay
	expect(t, post(t, "/payments/h_q/corrections", tk, k, `{ "reason":"first","effective_at":"`+tsStr(s.tQ)+`", "amount": 1.5e2, "expected_revision":1 }`), 200)
	// different body under the same key
	expectErr(t, post(t, "/payments/h_q/corrections", tk, k, corrBody(1, 151, s.tQ, "first")), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/payments/h_q/corrections", tk, k, corrBody(1, 150, s.tQ, "other")), 409, "idempotency_key_reuse")
	// R71: a claimed key is resolved before validation
	expectErr(t, post(t, "/payments/h_q/corrections", tk, k, map[string]any{"amount": -1}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/payments/h_q/corrections", tk, k, map[string]any{}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/payments/h_q/corrections", tk, k, `{`), 400, "malformed_request")
	// a failed (stale) attempt claims no key: the key works once the expected revision is right
	k2 := newKey()
	expectErr(t, post(t, "/payments/h_q/corrections", tk, k2, corrBody(2, 111, s.tQ, "late")), 409, "stale_revision")
	expect(t, post(t, "/payments/h_q/corrections", tk, k2, corrBody(3, 111, s.tQ, "late")), 201)
	// the same key on a different payment is a different path
	pb := mustPay(t, tk, "bob", 10, nil)
	expect(t, post(t, "/payments/"+str(t, pb, "payment_id")+"/corrections", tk, k, body), 201)
	// keys are scoped per user: bob may use the same key on his own payment
	pbob := mustPay(t, s.e.tok["bob"], "cy", 10, nil)
	expect(t, post(t, "/payments/"+str(t, pbob, "payment_id")+"/corrections", s.e.tok["bob"], k, corrBody(1, 5, time.Now().UTC().Add(-time.Second), "mine")), 201)
}

func TestR217_IncreaseDebitsSenderDecreaseDebitsReceiver(t *testing.T) {
	a := setupA(t)
	ta, tb := a.e.tok["ada"], a.e.tok["bob"]
	chk := func(ada, bob int64, label string) {
		t.Helper()
		if balAt(t, ta, "") != ada || balAt(t, tb, "") != bob {
			t.Fatalf("%s: ada=%d bob=%d want %d/%d", label, balAt(t, ta, ""), balAt(t, tb, ""), ada, bob)
		}
		if a.e.sum(t) != a.e.total {
			t.Fatalf("%s: sum", label)
		}
		m := meAt(t, ta, "")
		if num(t, m, "available") != ada || num(t, m, "total") != ada {
			t.Fatalf("%s: /me %v", label, m)
		}
		// the statement agrees with the current balance
		if num(t, fullStmt(t, ta, ""), "closing_balance") != ada || num(t, fullStmt(t, tb, ""), "closing_balance") != bob {
			t.Fatalf("%s: statements disagree with /me", label)
		}
	}
	chk(10000, 2500, "start")
	mustCorrect(t, ta, "s_1", 1, 800, a.t1, "increase") // +300 from the sender
	chk(9700, 2800, "increase to 800")
	mustCorrect(t, ta, "s_1", 2, 200, a.t1, "decrease") // -600: the receiver gives back
	chk(10300, 2200, "decrease to 200")
	mustCorrect(t, ta, "s_1", 3, 0, a.t1, "reverse") // zero reverses the whole payment
	chk(10500, 2000, "reversed")
	mustCorrect(t, ta, "s_1", 4, 500, a.t1, "restore")
	chk(10000, 2500, "restored")
	// the receiver cannot correct, and the other wallets are untouched
	if balAt(t, a.e.tok["cy"], "") != 500 {
		t.Fatal("cy touched")
	}
	// current spendable amounts reflect the correction immediately
	mustCorrect(t, ta, "s_1", 5, 0, a.t1, "reverse again")
	expectErr(t, sendPay(t, tb, "cy", 2001, nil), 409, "insufficient_funds")
	mustPay(t, tb, "cy", 2000, nil)
}

func TestR218_InsufficientFundsFirst(t *testing.T) {
	s := setupH(t)
	ta, tb := s.e.tok["ada"], s.e.tok["bob"]
	before := stateOf(t, s.e, "h_q")
	// sender cannot afford: also historically negative, yet insufficient_funds wins
	expectErr(t, correct(t, ta, "h_q", corrBody(1, 2000, s.tIn.Add(-time.Hour), "too much")), 409, "insufficient_funds")
	// receiver cannot give back (bob holds 0)
	expectErr(t, correct(t, ta, "h_q", corrBody(1, 0, s.tQ, "reverse")), 409, "insufficient_funds")
	if !reflect.DeepEqual(before, stateOf(t, s.e, "h_q")) {
		t.Fatal("failed corrections changed state")
	}
	// against AVAILABLE, not total: a hold of 800 leaves ada with 100 available
	mustAuthorize(t, ta, "cy", 800, nil)
	expectErr(t, correct(t, ta, "h_q", corrBody(1, 250, s.tQ, "debit 150")), 409, "insufficient_funds")
	expect(t, correct(t, ta, "h_q", corrBody(1, 200, s.tQ, "debit 100")), 201) // exactly the available amount
	if m := meAt(t, ta, ""); num(t, m, "total") != 800 || num(t, m, "held") != 800 || num(t, m, "available") != 0 {
		t.Fatalf("ada: %v", m)
	}
	// the receiver's available also governs a decrease: bob's funds are held
	mustPay(t, s.e.tok["cy"], "bob", 100, nil)
	mustAuthorize(t, tb, "dee", 100, nil)
	expectErr(t, correct(t, ta, "h_q", corrBody(2, 0, s.tQ, "give back 200")), 409, "insufficient_funds")
}

func TestR218_R234_HistoricalOverdraft(t *testing.T) {
	s := setupH(t)
	ta := s.e.tok["ada"]
	mustPay(t, s.e.tok["cy"], "ada", 500, nil) // ada now holds 1400 and can afford the debit
	before := stateOf(t, s.e, "h_q")
	k := newKey()
	// effective at the instant ada receives 1000: +1000 - 1100 = -100 at that boundary
	expectErr(t, post(t, "/payments/h_q/corrections", ta, k, corrBody(1, 1100, s.tIn, "too early")), 409, "historical_overdraft")
	// earlier than any funds
	expectErr(t, correct(t, ta, "h_q", corrBody(1, 100, s.tIn.Add(-time.Hour), "before funds")), 409, "historical_overdraft")
	if !reflect.DeepEqual(before, stateOf(t, s.e, "h_q")) {
		t.Fatal("failed corrections changed state")
	}
	// the failed key was not claimed: the same key with a body that fits now succeeds.
	// 1000 at that very instant combines with the +1000 received at the same instant: balance 0, not negative.
	ok := post(t, "/payments/h_q/corrections", ta, k, corrBody(1, 1000, s.tIn, "simultaneous"))
	expect(t, ok, 201)
	if num(t, meAt(t, ta, ""), "balance") != 500 {
		t.Fatalf("ada %d", num(t, meAt(t, ta, ""), "balance"))
	}
	// at the boundary the combined balance is exactly 0
	if balAt(t, ta, "as_of="+qe(tsStr(s.tIn))) != 0 {
		t.Fatalf("balance at the boundary: %d", balAt(t, ta, "as_of="+qe(tsStr(s.tIn))))
	}
	if balAt(t, ta, "as_of="+qe(tsStr(s.tIn.Add(-time.Second)))) != 0 {
		t.Fatal("opening")
	}
	// a further correction that dips below zero one second earlier is refused
	expectErr(t, correct(t, ta, "h_q", corrBody(2, 1000, s.tIn.Add(-time.Second), "earlier")), 409, "historical_overdraft")
	// decrease side: bob would be negative at h_out's time after the payment is reversed
	mustPay(t, ta, "bob", 300, nil)
	expectErr(t, correct(t, ta, "h_q", corrBody(2, 0, s.tQ, "reverse")), 409, "historical_overdraft")
	// sum invariant everywhere, also after the successful corrections
	for _, at := range []time.Time{s.tIn.Add(-day), s.tIn, s.tQ, s.tOut, time.Now()} {
		if got := sumAt(t, s.e.tok, "as_of="+qe(tsStr(at))); got != s.e.total {
			t.Fatalf("sum at %s = %d want %d", tsStr(at), got, s.e.total)
		}
	}
}

func TestR219_FailuresLeaveNoTraceAndSumHolds(t *testing.T) {
	s := setupH(t)
	ta := s.e.tok["ada"]
	mustPay(t, s.e.tok["cy"], "ada", 500, nil)
	before := stateOf(t, s.e, "h_q", "h_in")
	bodies := []struct {
		body any
		code string
		st   int
	}{
		{corrBody(1, 1100, s.tIn, "x"), "historical_overdraft", 409},
		{corrBody(9, 150, s.tQ, "x"), "stale_revision", 409},
		{corrBody(1, 100000, s.tQ, "x"), "insufficient_funds", 409},
		{corrBody(1, -5, s.tQ, "x"), "validation_failed", 422},
	}
	var keys []string
	for _, b := range bodies {
		k := newKey()
		keys = append(keys, k)
		expectErr(t, post(t, "/payments/h_q/corrections", ta, k, b.body), b.st, b.code)
	}
	if !reflect.DeepEqual(before, stateOf(t, s.e, "h_q", "h_in")) {
		t.Fatal("failures left a trace")
	}
	// none of those keys is claimed: each works as a first use with a valid body
	for i, k := range keys {
		expect(t, post(t, "/payments/h_q/corrections", ta, k, corrBody(int64(i+1), 100+int64(i), s.tQ, "fine")), 201)
	}
	// the sum invariant in every historical view, including known_at views between recorded times
	var instants []time.Time
	for _, tm := range []time.Time{s.tIn.Add(-day), s.tIn, s.tQ, s.tOut, time.Now().Add(time.Hour)} {
		instants = append(instants, tm)
	}
	revs := revisionsOf(t, ta, "h_q")
	var knowns []time.Time
	for _, rv := range revs {
		rt := parseTS(t, str(t, rv, "recorded_at"))
		knowns = append(knowns, rt.Add(-time.Millisecond), rt, rt.Add(time.Millisecond))
	}
	knowns = append(knowns, time.Now().Add(time.Hour), s.tIn)
	for _, at := range instants {
		for _, kn := range knowns {
			q := "as_of=" + qe(tsStr(at)) + "&known_at=" + qe(tsMicro(kn))
			if got := sumAt(t, s.e.tok, q); got != s.e.total {
				t.Fatalf("sum %d != %d for %s", got, s.e.total, q)
			}
		}
	}
}

func TestR220_OriginalPaymentAndReceiptsUnchanged(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	tk := e.tok["ada"]
	k := newKey()
	body := map[string]any{"to_handle": "bob", "amount": 100, "note": "orig", "visibility": "public"}
	orig := post(t, "/payments", tk, k, body)
	expect(t, orig, 201)
	pid := str(t, orig.obj(t), "payment_id")
	ca := parseTS(t, str(t, orig.obj(t), "created_at"))
	before, _ := activity(t, tk, "?limit=200")
	mustCorrect(t, tk, pid, 1, 40, ca, "smaller")
	mustCorrect(t, tk, pid, 2, 0, ca, "gone")
	after, _ := activity(t, tk, "?limit=200")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("/activity changed after corrections:\n%v\n%v", before, after)
	}
	for _, it := range after {
		if it["payment_id"] == pid && num(t, it, "amount") != 100 {
			t.Fatalf("activity must show the original amount: %v", it)
		}
	}
	if len(after) != 1 {
		t.Fatal("corrections must not create feed items")
	}
	// the original idempotent response is replayed unchanged
	rep := post(t, "/payments", tk, k, body)
	expect(t, rep, 200)
	requireSameJSON(t, orig, rep)
	if num(t, rep.obj(t), "amount") != 100 {
		t.Fatal("replay of the original POST shows a corrected amount")
	}
	// the other party's feed too
	bb, _ := activity(t, e.tok["bob"], "?limit=200")
	if len(bb) != 1 || num(t, bb[0], "amount") != 100 {
		t.Fatalf("bob's feed: %v", bb)
	}
	// the statement shows the correction
	en := entriesOf(t, fullStmt(t, tk, ""))
	if len(en) != 1 || num(t, en[0], "delta") != 0 || num(t, en[0], "revision") != 3 {
		t.Fatalf("statement: %v", en)
	}
}

func TestR221_ConcurrentCorrectionsOneWinner(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	tk := e.tok["ada"]
	p := mustPay(t, tk, "bob", 100, nil)
	pid := str(t, p, "payment_id")
	ca := parseTS(t, str(t, p, "created_at"))
	var mu sync.Mutex
	var winner int64
	rs := fanout(20, func(i int) (resp, error) {
		amt := int64(30 + i)
		r, err := send("POST", "/payments/"+pid+"/corrections", tk, map[string]string{"Idempotency-Key": newKey()},
			corrBody(1, amt, ca, "race"))
		if err == nil && r.Status == 201 {
			mu.Lock()
			winner = amt
			mu.Unlock()
		}
		return r, err
	})
	n201 := 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatal(x.err)
		}
		switch x.r.Status {
		case 201:
			n201++
		case 409:
			expectErr(t, x.r, 409, "stale_revision")
		default:
			t.Fatalf("%s", x.r)
		}
	}
	if n201 != 1 {
		t.Fatalf("%d corrections succeeded, want exactly one", n201)
	}
	revs := revisionsOf(t, tk, pid)
	if len(revs) != 2 || num(t, revs[1], "amount") != winner {
		t.Fatalf("revisions %v winner %d", revs, winner)
	}
	if balAt(t, tk, "") != 10000-winner || balAt(t, e.tok["bob"], "") != 2500+winner {
		t.Fatalf("balances do not reflect exactly one correction (winner %d)", winner)
	}
	// the same key and body sent concurrently: one 201, the rest 200 with the same body
	k := newKey()
	body := corrBody(2, 77, ca, "same key")
	rs = fanout(20, func(int) (resp, error) {
		return send("POST", "/payments/"+pid+"/corrections", tk, map[string]string{"Idempotency-Key": k}, body)
	})
	onlyOneCreated(t, rs)
	if revs := revisionsOf(t, tk, pid); len(revs) != 3 {
		t.Fatalf("%d revisions", len(revs))
	}
	if balAt(t, tk, "") != 10000-77 {
		t.Fatal("balance after the same-key race")
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR221_ConcurrentCorrectionsAndPaymentsKeepInvariants(t *testing.T) {
	env := setupUsers(t, []string{"u_op"}, fu{"ada", 5000}, fu{"bob", 5000}, fu{"cy", 0})
	e := env.tok
	var pids []string
	var cas []time.Time
	for i := 0; i < 5; i++ {
		p := mustPay(t, e["ada"], "bob", 100, nil)
		pids = append(pids, str(t, p, "payment_id"))
		cas = append(cas, parseTS(t, str(t, p, "created_at")))
	}
	var wg sync.WaitGroup
	var bad sync.Map
	for w := 0; w < 40; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			i := w % 5
			var r resp
			var err error
			if w%2 == 0 {
				r, err = send("POST", "/payments/"+pids[i]+"/corrections", e["ada"], map[string]string{"Idempotency-Key": newKey()},
					corrBody(1+int64(w/10%2), int64(50+w), cas[i], "c"))
			} else {
				r, err = send("POST", "/payments", e["bob"], map[string]string{"Idempotency-Key": newKey()}, map[string]any{"to_handle": "cy", "amount": 10})
			}
			if err != nil {
				bad.Store("err", err.Error())
			} else if r.Status >= 500 {
				bad.Store("5xx", r.String())
			}
		}(w)
	}
	wg.Wait()
	bad.Range(func(k, v any) bool { t.Errorf("%v %v", k, v); return true })
	if env.sum(t) != env.total {
		t.Fatal("sum")
	}
	for _, tk := range e {
		if balAt(t, tk, "") < 0 {
			t.Fatal("negative")
		}
	}
	for _, tk := range e {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
	for _, pid := range pids {
		revs := revisionsOf(t, e["ada"], pid)
		for i, rv := range revs {
			if num(t, rv, "revision") != int64(i+1) {
				t.Fatal("revision numbering broken")
			}
		}
	}
}

func TestR222_R223_Revisions(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...).tok
	p := mustPay(t, e["ada"], "bob", 100, map[string]any{"visibility": "public"})
	pid := str(t, p, "payment_id")
	ca := parseTS(t, str(t, p, "created_at"))
	revs := revisionsOf(t, e["ada"], pid)
	if len(revs) != 1 {
		t.Fatal("r1 only")
	}
	r1 := revs[0]
	if r1["payment_id"] != pid || num(t, r1, "revision") != 1 || num(t, r1, "amount") != 100 || r1["reason"] != "" {
		t.Fatalf("r1: %v", r1)
	}
	if !parseTS(t, str(t, r1, "effective_at")).Equal(ca) || !parseTS(t, str(t, r1, "recorded_at")).Equal(ca) {
		t.Fatalf("r1 effective_at and recorded_at must equal created_at: %v", r1)
	}
	for _, k := range []string{"payment_id", "revision", "amount", "effective_at", "recorded_at", "reason"} {
		if _, ok := r1[k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
	tick()
	c := mustCorrect(t, e["ada"], pid, 1, 60, ca, "reduce")
	revs = revisionsOf(t, e["bob"], pid) // the receiver may read it
	if len(revs) != 2 || num(t, revs[1], "revision") != 2 || num(t, revs[1], "amount") != 60 || revs[1]["reason"] != "reduce" {
		t.Fatalf("revisions: %v", revs)
	}
	if !parseTS(t, str(t, revs[1], "recorded_at")).Equal(parseTS(t, str(t, c, "recorded_at"))) {
		t.Fatal("recorded_at differs from the correction response")
	}
	if !parseTS(t, str(t, revs[1], "recorded_at")).After(ca) {
		t.Fatal("r2 recorded after r1")
	}
	// r1 is immutable
	if !reflect.DeepEqual(revs[0], r1) {
		t.Fatalf("r1 changed: %v vs %v", revs[0], r1)
	}
	// third parties: 404 even for a public payment; unknown 404; no token 401
	expectErr(t, get(t, "/payments/"+pid+"/revisions", e["cy"]), 404, "not_found")
	expectErr(t, get(t, "/payments/"+pid+"/revisions", e["op"]), 404, "not_found")
	expectErr(t, get(t, "/payments/nope/revisions", e["ada"]), 404, "not_found")
	expectErr(t, get(t, "/payments/"+pid+"/revisions", ""), 401, "unauthenticated")
	// the feed still shows the payment publicly to cy
	if f, _ := activity(t, e["cy"], ""); len(f) != 1 {
		t.Fatal("public payment must still be visible in the feed")
	}
	// private payment: same
	pp := mustPay(t, e["ada"], "bob", 5, map[string]any{"visibility": "private"})
	expectErr(t, get(t, "/payments/"+str(t, pp, "payment_id")+"/revisions", e["cy"]), 404, "not_found")
}

func TestR224_KnownAt(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...).tok
	ta, tb := e["ada"], e["bob"]
	p := mustPay(t, ta, "bob", 100, nil)
	pid := str(t, p, "payment_id")
	ca := parseTS(t, str(t, p, "created_at"))
	tick()
	c2 := mustCorrect(t, ta, pid, 1, 40, ca, "reduce")
	rc2 := parseTS(t, str(t, c2, "recorded_at"))
	tick()
	c3 := mustCorrect(t, ta, pid, 2, 70, ca, "up")
	rc3 := parseTS(t, str(t, c3, "recorded_at"))
	kn := func(tm time.Time) string { return "known_at=" + qe(tsMicro(tm)) }
	// before the payment was recorded: contributes nothing
	pre := ca.Add(-time.Millisecond)
	if balAt(t, ta, kn(pre)) != 10000 || balAt(t, tb, kn(pre)) != 2500 {
		t.Fatal("a payment recorded after known_at must contribute nothing")
	}
	if len(entriesOf(t, fullStmt(t, ta, kn(pre)))) != 0 {
		t.Fatal("no entry before it was known")
	}
	st := fullStmt(t, ta, kn(pre))
	if num(t, st, "opening_balance") != 10000 || num(t, st, "closing_balance") != 10000 {
		t.Fatalf("%v", st)
	}
	// exactly at recorded_at (inclusive): r1
	at1 := balAt(t, ta, kn(ca))
	if at1 != 9900 {
		t.Fatalf("known_at == r1.recorded_at must select r1: %d", at1)
	}
	// between r1 and r2: r1 is selected
	mid := ca.Add(rc2.Sub(ca) / 2)
	if balAt(t, ta, kn(mid)) != 9900 || balAt(t, tb, kn(mid)) != 2600 {
		t.Fatal("known_at between r1 and r2 must select r1")
	}
	en := entriesOf(t, fullStmt(t, ta, kn(mid)))
	if len(en) != 1 || num(t, en[0], "revision") != 1 || num(t, en[0], "delta") != -100 || num(t, en[0]["payment"].(map[string]any), "amount") != 100 {
		t.Fatalf("r1 expected: %v", en)
	}
	// at r2's recorded time (inclusive) r2, just before it r1
	if balAt(t, ta, kn(rc2)) != 9960 {
		t.Fatalf("known_at == r2.recorded_at must select r2: %d", balAt(t, ta, kn(rc2)))
	}
	if balAt(t, ta, kn(rc2.Add(-time.Microsecond))) != 9900 {
		t.Fatal("1us before r2 is still r1")
	}
	// between r2 and r3: r2
	mid2 := rc2.Add(rc3.Sub(rc2) / 2)
	if balAt(t, ta, kn(mid2)) != 9960 {
		t.Fatal("between r2 and r3 selects r2")
	}
	en = entriesOf(t, fullStmt(t, ta, kn(mid2)))
	if len(en) != 1 || num(t, en[0], "revision") != 2 || num(t, en[0]["payment"].(map[string]any), "amount") != 40 {
		t.Fatalf("r2 expected: %v", en)
	}
	// at/after r3 and in the future: r3 = the current value; both queries may be in the future
	for _, tm := range []time.Time{rc3, time.Now().Add(time.Hour), time.Now().Add(100 * day)} {
		if balAt(t, ta, kn(tm)) != 9930 || balAt(t, ta, "") != 9930 {
			t.Fatalf("known_at %v: %d", tm, balAt(t, ta, kn(tm)))
		}
	}
	if balAt(t, ta, "as_of="+qe(tsStr(time.Now().Add(24*time.Hour)))+"&"+kn(time.Now().Add(48*time.Hour))) != 9930 {
		t.Fatal("future as_of and known_at")
	}
	// as_of and known_at combine: a payment recorded later but effective earlier
	if balAt(t, ta, "as_of="+qe(tsStr(ca.Add(-time.Second)))) != 10000 {
		t.Fatal("as_of before the payment's effective time")
	}
	// the echo covers known_at and as_of
	m := meAt(t, ta, "as_of="+qe(tsStr(ca))+"&"+kn(mid))
	if m["known_at"] != tsMicro(mid) || m["as_of"] != tsStr(ca) {
		t.Fatalf("echo: %v", m)
	}
	// sums hold at every known_at
	for _, tm := range []time.Time{pre, ca, mid, rc2, mid2, rc3} {
		if got := sumAt(t, e, kn(tm)); got != 13000 {
			t.Fatalf("sum at %v: %d", tm, got)
		}
	}
}

func TestR224_R225_BackdatingMovesPaymentAcrossWindowAndOrder(t *testing.T) {
	s := setupH(t)
	// cy corrects h_in (cy->ada 1000 at D-2d): move its effective time two days earlier
	tc := s.e.tok["cy"]
	win := "from=" + qe(tsStr(s.tIn.Add(-time.Hour))) + "&to=" + qe(tsStr(s.tIn.Add(time.Hour)))
	if ids := entryIDs(t, fullStmt(t, tc, win)); !strEq(ids, []string{"h_in"}) {
		t.Fatalf("window before the correction: %v", ids)
	}
	newEff := s.tIn.Add(-2 * day)
	mustCorrect(t, tc, "h_in", 1, 1000, newEff, "recorded late")
	// it left the old window and entered the early one
	if ids := entryIDs(t, fullStmt(t, tc, win)); len(ids) != 0 {
		t.Fatalf("payment still in the old window: %v", ids)
	}
	early := "from=" + qe(tsStr(newEff.Add(-time.Hour))) + "&to=" + qe(tsStr(newEff.Add(time.Hour)))
	m := fullStmt(t, tc, early)
	if ids := entryIDs(t, m); !strEq(ids, []string{"h_in"}) {
		t.Fatalf("early window: %v", ids)
	}
	en := entriesOf(t, m)[0]
	if !parseTS(t, str(t, en, "effective_at")).Equal(newEff) || num(t, en, "revision") != 2 {
		t.Fatalf("entry: %v", en)
	}
	// ordering uses the selected effective_at: h_in is now the oldest entry for ada
	ids := entryIDs(t, fullStmt(t, s.e.tok["ada"], ""))
	if !strEq(ids, []string{"h_in", "h_q"}) {
		t.Fatalf("order: %v", ids)
	}
	// with known_at before the correction the payment sits in its old place
	rv := revisionsOf(t, tc, "h_in")
	rec := parseTS(t, str(t, rv[1], "recorded_at"))
	old := fullStmt(t, tc, win+"&known_at="+qe(tsMicro(rec.Add(-time.Millisecond))))
	if ids := entryIDs(t, old); !strEq(ids, []string{"h_in"}) || num(t, entriesOf(t, old)[0], "revision") != 1 {
		t.Fatalf("known_at before the correction: %v", ids)
	}
	// moving it LATER than ada's other payment would leave ada negative at h_q's time
	expectErr(t, correct(t, tc, "h_in", corrBody(2, 1000, s.tOut, "later")), 409, "historical_overdraft")
	for _, tk := range s.e.tok {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
}
