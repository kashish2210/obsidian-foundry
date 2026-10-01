package acceptance

import (
	"reflect"
	"testing"
	"time"
)

func asofQ(tm time.Time) string { return "as_of=" + qe(tsMicro(tm)) }

func expectMe(t testing.TB, tok, query string, total, heldAmt int64) {
	t.Helper()
	m := meAt(t, tok, query)
	if num(t, m, "balance") != total || num(t, m, "total") != total || num(t, m, "held") != heldAmt || num(t, m, "available") != total-heldAmt {
		t.Fatalf("/me?%s = %v, want total %d held %d available %d", query, m, total, heldAmt, total-heldAmt)
	}
}

// ---------------- R231-R233: holds in time ----------------

func TestR231_R232_R233_HoldLifecycleInTime(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	ta, tb := e.tok["ada"], e.tok["bob"]
	az := mustAuthorize(t, ta, "bob", 2000, nil)
	id := aid(t, az)
	ca := parseTS(t, str(t, az, "created_at"))
	if v, ok := az["closed_at"]; !ok || v != nil {
		t.Fatalf("closed_at must be present and null while open: %v", az)
	}
	tick()
	cap1 := mustCapture(t, tb, id, map[string]any{"amount": 500, "final": false})
	tc := parseTS(t, str(t, cap1, "created_at"))
	if g := getAuthz(t, ta, id); g["closed_at"] != nil {
		t.Fatalf("closed_at after a nonfinal capture must stay null: %v", g)
	}
	tick()
	before := time.Now()
	v := voidA(t, ta, id)
	expect(t, v, 200)
	after := time.Now()
	vm := v.obj(t)
	tv := parseTS(t, str(t, vm, "closed_at"))
	if tv.Before(before.Add(-time.Second)) || tv.After(after.Add(time.Second)) {
		t.Fatalf("closed_at %v must be the void time (between %v and %v)", tv, before, after)
	}
	if got := parseTS(t, str(t, getAuthz(t, ta, id), "closed_at")); !got.Equal(tv) {
		t.Fatalf("closed_at differs between void response and list")
	}
	// ada's views
	ms := time.Millisecond
	expectMe(t, ta, asofQ(ca.Add(-ms)), 10000, 0)
	expectMe(t, ta, asofQ(ca), 10000, 2000) // the hold starts at creation (inclusive)
	expectMe(t, ta, asofQ(ca.Add(tc.Sub(ca)/2)), 10000, 2000)
	expectMe(t, ta, asofQ(tc.Add(-ms)), 10000, 2000)
	expectMe(t, ta, asofQ(tc), 9500, 1500) // a nonfinal capture reduces the hold at capture time
	expectMe(t, ta, asofQ(tv.Add(-ms)), 9500, 1500)
	expectMe(t, ta, asofQ(tv), 9500, 0) // void releases the remainder at its time
	expectMe(t, ta, asofQ(time.Now().Add(time.Hour)), 9500, 0)
	expectMe(t, ta, "", 9500, 0)
	// bob never holds anything and gains at capture time
	expectMe(t, tb, asofQ(tc.Add(-ms)), 2500, 0)
	expectMe(t, tb, asofQ(tc), 3000, 0)
	// known_at: what was known then
	kn := func(k time.Time, asof time.Time) string {
		return "as_of=" + qe(tsMicro(asof)) + "&known_at=" + qe(tsMicro(k))
	}
	expectMe(t, ta, kn(ca.Add(-ms), time.Now().Add(time.Minute)), 10000, 0) // creation not yet known
	mid := ca.Add(tc.Sub(ca) / 2)
	expectMe(t, ta, kn(mid, time.Now().Add(time.Minute)), 10000, 2000)                   // hold known, capture and void not
	expectMe(t, ta, kn(tc, time.Now().Add(time.Minute)), 9500, 1500)                     // capture known, void not
	expectMe(t, ta, kn(tv, time.Now().Add(time.Minute)), 9500, 0)                        // all known
	expectMe(t, ta, kn(time.Now().Add(time.Hour), time.Now().Add(time.Minute)), 9500, 0) // future known_at
	// the deadline is known once creation is known: for as_of beyond the deadline an unclosed hold is gone
	expectMe(t, ta, kn(mid, ca.Add(11*time.Minute)), 10000, 0)
	expectMe(t, ta, kn(mid, ca.Add(9*time.Minute)), 10000, 2000)
	// invariants in every view
	for _, at := range []time.Time{ca.Add(-time.Second), ca, tc, tv, time.Now().Add(time.Hour)} {
		for _, k := range []time.Time{ca.Add(-ms), mid, tc, tv, time.Now().Add(time.Hour)} {
			q := kn(k, at)
			checkMe(t, meAt(t, ta, q))
			if s := sumAt(t, e.tok, q); s != e.total {
				t.Fatalf("sum %d at %s", s, q)
			}
		}
	}
}

func TestR232_R233_FinalCaptureAndExpiryClosedAt(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	ta, tb := e.tok["ada"], e.tok["bob"]
	az := mustAuthorize(t, ta, "bob", 1000, nil)
	time.Sleep(50 * time.Millisecond)
	cp := mustCapture(t, tb, aid(t, az), map[string]any{"amount": 400}) // final: releases 600 at capture time
	tc := parseTS(t, str(t, cp, "created_at"))
	g := getAuthz(t, ta, aid(t, az))
	if !parseTS(t, str(t, g, "closed_at")).Equal(tc) {
		t.Fatalf("final capture closed_at %v must equal the capture time %v", g["closed_at"], cp["created_at"])
	}
	ca := parseTS(t, str(t, az, "created_at"))
	expectMe(t, ta, asofQ(tc.Add(-time.Millisecond)), 10000, 1000)
	expectMe(t, ta, asofQ(tc), 9600, 0)
	_ = ca
	// a second authorization already closed by void keeps its time
	b := mustAuthorize(t, ta, "bob", 300, nil)
	expect(t, voidA(t, ta, aid(t, b)), 200)
	vb := parseTS(t, str(t, getAuthz(t, ta, aid(t, b)), "closed_at"))
	expect(t, voidA(t, ta, aid(t, b)), 200) // voiding again changes nothing
	if !parseTS(t, str(t, getAuthz(t, ta, aid(t, b)), "closed_at")).Equal(vb) {
		t.Fatal("closed_at changed by a repeated void")
	}
}

func TestR232_ExpiryTakesEffectAtDeadline(t *testing.T) {
	reset(t, fixtureAZ(2, nil, azUsers...))
	ta := login(t, "ada@example.com")
	az := mustAuthorize(t, ta, "bob", 1000, nil)
	exp := parseTS(t, str(t, az, "expires_at"))
	ca := parseTS(t, str(t, az, "created_at"))
	if az["closed_at"] != nil {
		t.Fatal("closed_at while open")
	}
	time.Sleep(3200 * time.Millisecond)
	g := getAuthz(t, ta, aid(t, az))
	if g["status"] != "expired" || !parseTS(t, str(t, g, "closed_at")).Equal(exp) {
		t.Fatalf("expired authorization: status %v closed_at %v, expires_at %v", g["status"], g["closed_at"], exp)
	}
	ms := time.Millisecond
	expectMe(t, ta, asofQ(ca), 10000, 1000)
	expectMe(t, ta, asofQ(exp.Add(-ms)), 10000, 1000)
	expectMe(t, ta, asofQ(exp), 10000, 0) // expiry takes effect at expires_at
	expectMe(t, ta, asofQ(exp.Add(time.Hour)), 10000, 0)
	expectMe(t, ta, "", 10000, 0)
	// without as_of the instant the request began is used: a hold that has expired is gone
	// a hold created now with a long lifetime is open at the present and expires at its deadline in the future
	reset(t, fixtureAZ(3600, nil, azUsers...))
	ta = login(t, "ada@example.com")
	az = mustAuthorize(t, ta, "bob", 700, nil)
	exp = parseTS(t, str(t, az, "expires_at"))
	expectMe(t, ta, asofQ(time.Now().Add(30*time.Minute)), 10000, 700)
	expectMe(t, ta, asofQ(exp.Add(-ms)), 10000, 700)
	expectMe(t, ta, asofQ(exp), 10000, 0)
	expectMe(t, ta, asofQ(time.Now().Add(2*time.Hour)), 10000, 0)
	expectMe(t, ta, "", 10000, 700)
}

func TestR232_PartialCaptureThenExpiry(t *testing.T) {
	reset(t, fixtureAZ(3, nil, azUsers...))
	ta := login(t, "ada@example.com")
	tb := login(t, "bob@example.com")
	az := mustAuthorize(t, ta, "bob", 1000, nil)
	exp := parseTS(t, str(t, az, "expires_at"))
	cp := mustCapture(t, tb, aid(t, az), map[string]any{"amount": 300, "final": false})
	tc := parseTS(t, str(t, cp, "created_at"))
	time.Sleep(3500 * time.Millisecond)
	ms := time.Millisecond
	expectMe(t, ta, asofQ(tc), 9700, 700)
	expectMe(t, ta, asofQ(exp.Add(-ms)), 9700, 700)
	expectMe(t, ta, asofQ(exp), 9700, 0) // only the remainder is released
	expectMe(t, ta, "", 9700, 0)
	g := getAuthz(t, ta, aid(t, az))
	if g["status"] != "expired" || !parseTS(t, str(t, g, "closed_at")).Equal(exp) || num(t, g, "captured_amount") != 300 {
		t.Fatalf("%v", g)
	}
}

// ---------------- R234 ----------------

func TestR234_HistoricalOverdraftOnAvailable(t *testing.T) {
	tq := ago(day)
	e := setupP(t, []any{seedP("sp", "ada", "bob", 100, "public", tq)}, fu{"ada", 1000}, fu{"bob", 100}, fu{"cy", 0}, fu{"dee", 0}, fu{"op", 0})
	ta := e.tok["ada"]
	hold := mustAuthorize(t, ta, "cy", 800, nil) // ada: total 1000, available 200
	// currently unaffordable (available 200 < debit 300) wins over any historical finding
	expectErr(t, correct(t, ta, "sp", corrBody(1, 400, tq, "too much")), 409, "insufficient_funds")
	// release the hold: the correction is now affordable today, but at the hold's creation the
	// total (1100 - 400 = 700) would not cover the 800 held: available negative in the past
	expect(t, voidA(t, ta, aid(t, hold)), 200)
	before := stateOf(t, e, "sp")
	k := newKey()
	expectErr(t, post(t, "/payments/sp/corrections", ta, k, corrBody(1, 400, tq, "overdraws available")), 409, "historical_overdraft")
	if !reflect.DeepEqual(before, stateOf(t, e, "sp")) {
		t.Fatal("rejected correction changed state")
	}
	// a smaller increase leaves total >= held at every boundary
	expect(t, post(t, "/payments/sp/corrections", ta, k, corrBody(1, 250, tq, "fits")), 201)
	if balAt(t, ta, "") != 850 {
		t.Fatalf("ada %d", balAt(t, ta, ""))
	}
	expectMe(t, ta, asofQ(time.Now().Add(-time.Hour)), 850, 0)
}

func TestR234_TotalNegativeInThePast(t *testing.T) {
	s := setupH(t)
	mustPay(t, s.e.tok["cy"], "ada", 500, nil)
	// moving the 100 to before ada received any funds (D-3d): total negative at that boundary
	expectErr(t, correct(t, s.e.tok["ada"], "h_q", corrBody(1, 100, s.tIn.Add(-day), "too early")), 409, "historical_overdraft")
}

// ---------------- R235 seeded holds ----------------

func TestR235_SeededOpenHoldCreationTime(t *testing.T) {
	before := time.Now().UTC().Add(-3 * time.Second)
	reset(t, fixtureAZ(nil, []any{seedAZ("a_reset", "ada", "bob", 400, "open", isoIn(3*time.Hour))}, azUsers...))
	ta := login(t, "ada@example.com")
	after := time.Now().UTC().Add(3 * time.Second)
	// assumed created at reset: no hold before it, a hold after it
	expectMe(t, ta, asofQ(before.Add(-time.Hour)), 10000, 0)
	expectMe(t, ta, asofQ(after), 10000, 400)
	expectMe(t, ta, "", 10000, 400)
	// supplied created_at is honoured
	ca := ago(5 * hour)
	az := seedAZ("a_dated", "ada", "bob", 600, "open", isoIn(3*time.Hour))
	az["created_at"] = tsStr(ca)
	reset(t, fixtureAZ(nil, []any{az}, azUsers...))
	ta = login(t, "ada@example.com")
	expectMe(t, ta, asofQ(ca.Add(-time.Second)), 10000, 0)
	expectMe(t, ta, asofQ(ca), 10000, 600)
	expectMe(t, ta, "", 10000, 600)
	g := getAuthz(t, ta, "a_dated")
	if !parseTS(t, str(t, g, "created_at")).Equal(ca) {
		t.Fatalf("created_at %v", g["created_at"])
	}
	// a future created_at is a reset error and changes nothing
	bad := seedAZ("a_future", "ada", "bob", 100, "open", isoIn(3*time.Hour))
	bad["created_at"] = tsStr(time.Now().UTC().Add(2 * time.Hour))
	r, err := sendWith(ctlClient, "POST", "/_test/reset", "", nil, fixtureAZ(nil, []any{bad}, fu{"zed", 5}))
	if err != nil {
		t.Fatal(err)
	}
	expectErr(t, r, 422, "validation_failed")
	expectMe(t, ta, "", 10000, 600) // previous state intact
	// seeded closed holds do not need a lifecycle
	reset(t, fixtureAZ(nil, []any{seedAZ("a_c", "ada", "bob", 100, "captured", isoIn(3*time.Hour)), seedAZ("a_v", "ada", "bob", 100, "voided", isoIn(3*time.Hour))}, azUsers...))
	ta = login(t, "ada@example.com")
	expectMe(t, ta, "", 10000, 0)
	for _, a := range func() []map[string]any { x, _ := listAuthz(t, ta, "?limit=200"); return x }() {
		if _, ok := a["closed_at"]; !ok {
			t.Fatalf("closed_at key: %v", a)
		}
	}
}

// ---------------- R236 statements are money only ----------------

func TestR236_StatementMoneyOnlyAndCapturesOnce(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	ta, tb := e.tok["ada"], e.tok["bob"]
	before := fullStmt(t, ta, "")
	snap := str(t, before, "snapshot")
	a1 := mustAuthorize(t, ta, "bob", 1000, nil)
	if n := len(entriesOf(t, fullStmt(t, ta, ""))); n != 0 {
		t.Fatalf("authorization appeared as %d statement entries", n)
	}
	c1 := mustCapture(t, tb, aid(t, a1), map[string]any{"amount": 300, "final": false})
	en := entriesOf(t, fullStmt(t, ta, ""))
	if len(en) != 1 || entryPID(t, en[0]) != str(t, c1, "payment_id") || en[0]["payment"].(map[string]any)["authorization_id"] != aid(t, a1) || num(t, en[0], "delta") != -300 {
		t.Fatalf("after a nonfinal capture: %v", en)
	}
	c2 := mustCapture(t, tb, aid(t, a1), map[string]any{"amount": 200}) // final: releases 500
	en = entriesOf(t, fullStmt(t, ta, ""))
	if len(en) != 2 {
		t.Fatalf("after the final capture: %v", entryIDs(t, fullStmt(t, ta, "")))
	}
	got := map[string]int{}
	for _, x := range en {
		got[entryPID(t, x)]++
	}
	if got[str(t, c1, "payment_id")] != 1 || got[str(t, c2, "payment_id")] != 1 {
		t.Fatalf("captures must appear exactly once: %v", got)
	}
	a2 := mustAuthorize(t, ta, "cy", 500, nil)
	expect(t, voidA(t, ta, aid(t, a2)), 200)
	if n := len(entriesOf(t, fullStmt(t, ta, ""))); n != 2 {
		t.Fatalf("a void created statement entries: %d", n)
	}
	// the receiver sees the captures once each; the third party sees neither
	if n := len(entriesOf(t, fullStmt(t, tb, ""))); n != 2 {
		t.Fatalf("bob: %d", n)
	}
	if n := len(entriesOf(t, fullStmt(t, e.tok["cy"], ""))); n != 0 {
		t.Fatalf("cy: %d", n)
	}
	// the money view agrees with the statement
	ms := fullStmt(t, ta, "")
	checkStatementConsistent(t, ms)
	if num(t, ms, "closing_balance") != balAt(t, ta, "") {
		t.Fatal("closing != balance")
	}
	// the old snapshot is unchanged after all those lifecycle actions
	old := snapPage(t, ta, snap, "limit=200")
	if !reflect.DeepEqual(stripToken(old), stripToken(before)) || len(entriesOf(t, old)) != 0 {
		t.Fatalf("snapshot changed by lifecycle actions: %v", old)
	}
}

// ---------------- R237 export / import ----------------

func TestR237_ExportImportCarriesHistoryEverything(t *testing.T) {
	a := setupA(t)
	ta, tb := a.e.tok["ada"], a.e.tok["bob"]
	// corrections with retry records
	k1 := newKey()
	b1 := corrBody(1, 300, a.t1, "smaller")
	r1 := post(t, "/payments/s_1/corrections", ta, k1, b1)
	expect(t, r1, 201)
	tick()
	k2 := newKey()
	b2 := corrBody(2, 400, a.t1, "bigger")
	r2 := post(t, "/payments/s_1/corrections", ta, k2, b2)
	expect(t, r2, 201)
	failKey := newKey()
	expectErr(t, post(t, "/payments/s_1/corrections", ta, failKey, corrBody(9, 1, a.t1, "stale")), 409, "stale_revision")
	// holds with a closed lifecycle and an open one
	az := mustAuthorize(t, ta, "cy", 900, nil)
	cap1 := mustCapture(t, a.e.tok["cy"], aid(t, az), map[string]any{"amount": 200, "final": false})
	az2 := mustAuthorize(t, ta, "bob", 100, nil)
	expect(t, voidA(t, ta, aid(t, az2)), 200)
	tick()
	api := mustPay(t, ta, "cy", 25, nil)
	// a snapshot (exported with the state) and its content
	m0 := fullStmt(t, ta, "")
	snap := str(t, m0, "snapshot")
	mustCorrect(t, ta, "s_3", 1, 50, a.t3, "after the snapshot")
	future := time.Now().Add(time.Hour)
	views := func() map[string]any {
		out := map[string]any{}
		for h, tk := range a.e.tok {
			for i, q := range []string{"", asofQ(a.t1), asofQ(a.t2), asofQ(a.t3.Add(time.Second)), asofQ(future)} {
				out["me-"+h+itoa(i)] = meAt(t, tk, q)
			}
			st := fullStmt(t, tk, "")
			out["st-"+h] = stripToken(st)
			out["known-"+h] = meAt(t, tk, "known_at="+qe(tsMicro(parseTS(t, str(t, r1.obj(t), "recorded_at")).Add(time.Microsecond))))
		}
		out["rev"] = revisionsOf(t, ta, "s_1")
		out["rev3"] = revisionsOf(t, ta, "s_3")
		out["az"], _ = listAuthz(t, ta, "?limit=200")
		return out
	}
	want := views()
	// a snapshot taken AFTER the export is not part of it
	exp := doExport(t)
	late := str(t, fullStmt(t, ta, ""), "snapshot")

	// replace the destination with something else entirely, then import
	reset(t, fixtureAZ(nil, nil, fu{"xavier", 5}, fu{"yara", 5}))
	expect(t, doImport(t, exp.Body), 204)
	if got := views(); !reflect.DeepEqual(got, want) {
		for k := range want {
			if !reflect.DeepEqual(got[k], want[k]) {
				t.Fatalf("view %s differs after import:\n got %v\nwant %v", k, got[k], want[k])
			}
		}
	}
	// the exported snapshot still pages the frozen result; the later one is unknown (documented decision)
	old := snapPage(t, ta, snap, "limit=200")
	if !reflect.DeepEqual(stripToken(old), stripToken(m0)) {
		t.Fatalf("exported snapshot differs after import:\n%v\n%v", old, m0)
	}
	expectErr(t, get(t, "/statement?snapshot="+qe(late), ta), 404, "not_found")
	// correction retries: the original revision bodies, even after newer revisions
	for _, c := range []struct {
		k    string
		body map[string]any
		orig resp
	}{{k1, b1, r1}, {k2, b2, r2}} {
		rep := post(t, "/payments/s_1/corrections", ta, c.k, c.body)
		expect(t, rep, 200)
		requireSameJSON(t, c.orig, rep)
	}
	expectErr(t, post(t, "/payments/s_1/corrections", ta, k1, corrBody(1, 301, a.t1, "smaller")), 409, "idempotency_key_reuse")
	// the failed key stays reusable
	expect(t, post(t, "/payments/s_1/corrections", ta, failKey, corrBody(3, 450, a.t1, "now valid")), 201)
	// revision numbering continues, ids do not collide, opening balances are unchanged
	revs := revisionsOf(t, ta, "s_1")
	if len(revs) != 4 || num(t, revs[3], "revision") != 4 {
		t.Fatalf("revisions after import: %v", revs)
	}
	np := mustPay(t, ta, "bob", 1, nil)
	if np["payment_id"] == api["payment_id"] || np["payment_id"] == cap1["payment_id"] || np["payment_id"] == "s_1" {
		t.Fatal("new payment id collides with an imported id")
	}
	if balAt(t, ta, "as_of="+qe(tsStr(a.t1.Add(-time.Hour)))) != 10600 {
		t.Fatal("opening balance changed by import")
	}
	// repeated import restores without duplicating revisions
	expect(t, doImport(t, exp.Body), 204)
	expect(t, doImport(t, exp.Body), 204)
	if got := views(); !reflect.DeepEqual(got, want) {
		t.Fatal("repeated import changed the history")
	}
	if len(revisionsOf(t, ta, "s_1")) != 3 {
		t.Fatal("revisions duplicated by import")
	}
	_ = tb
	// reset clears snapshots and correction records
	reset(t, fixtureP([]string{"u_op"}, []any{seedP("s_1", "ada", "bob", 500, "public", a.t1)}, azUsers...))
	ta2 := login(t, "ada@example.com")
	expectErr(t, get(t, "/statement?snapshot="+qe(snap), ta2), 404, "not_found")
	expect(t, post(t, "/payments/s_1/corrections", ta2, k1, b1), 201)
}
