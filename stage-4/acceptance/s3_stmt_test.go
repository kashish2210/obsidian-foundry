package acceptance

import (
	"reflect"
	"testing"
	"time"
)

// ---------------- R198-R201 payment timestamps and seeding ----------------

func TestR198_R199_SeededCreatedAtAndDefault(t *testing.T) {
	d1 := ago(3 * day)
	before := time.Now().UTC().Add(-2 * time.Second)
	fx := fixtureCur("EUR", 2, nil, azUsers...)
	fx["payments"] = []any{
		seedP("sp_dated", "ada", "bob", 100, "public", d1),
		map[string]any{"id": "sp_undated", "from_user_id": "u_ada", "to_user_id": "u_bob", "amount": 50, "note": "", "visibility": "public"},
	}
	reset(t, fx)
	after := time.Now().UTC().Add(2 * time.Second)
	ta := login(t, "ada@example.com")
	items, _ := activity(t, ta, "?limit=200")
	by := map[string]map[string]any{}
	for _, it := range items {
		by[str(t, it, "payment_id")] = it
	}
	if got := parseTS(t, str(t, by["sp_dated"], "created_at")); !got.Equal(d1) {
		t.Fatalf("seeded created_at %v != %v", got, d1)
	}
	und := parseTS(t, str(t, by["sp_undated"], "created_at"))
	if und.Before(before) || und.After(after) {
		t.Fatalf("omitted created_at must be the reset time, got %v", und)
	}
	// a later API payment is later than the undated seeded one
	time.Sleep(20 * time.Millisecond)
	p := mustPay(t, ta, "bob", 1, nil)
	if !parseTS(t, str(t, p, "created_at")).After(und) && !parseTS(t, str(t, p, "created_at")).Equal(und) {
		t.Fatal("API payment must not be earlier than the seeded one")
	}
	// activity keeps ordering by created_at, newest first
	items, _ = activity(t, ta, "?limit=200")
	for i := 1; i < len(items); i++ {
		if parseTS(t, str(t, items[i-1], "created_at")).Before(parseTS(t, str(t, items[i], "created_at"))) {
			t.Fatal("activity not ordered by created_at")
		}
	}
}

func TestR198_EveryPaymentEndpointCarriesCreatedAt(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"op", 0})
	p := mustPay(t, e.tok["ada"], "bob", 10, nil)
	parseTS(t, str(t, p, "created_at"))
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 5))
	r := postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{})
	expect(t, r, 201)
	parseTS(t, str(t, r.obj(t), "created_at"))
	st := settle(t, e.tok["op"], batch(tr("ada", "bob", 1)))
	expect(t, st, 201)
	parseTS(t, str(t, st.obj(t)["payments"].([]any)[0].(map[string]any), "created_at"))
	a := mustAuthorize(t, e.tok["ada"], "bob", 10, nil)
	c := mustCapture(t, e.tok["bob"], aid(t, a), map[string]any{})
	parseTS(t, str(t, c, "created_at"))
	items, _ := activity(t, e.tok["ada"], "?limit=200")
	for _, it := range items {
		parseTS(t, str(t, it, "created_at"))
	}
	for _, en := range entriesOf(t, fullStmt(t, e.tok["ada"], "")) {
		parseTS(t, str(t, en["payment"].(map[string]any), "created_at"))
	}
}

func TestR200_SeededCreatedAtValidation(t *testing.T) {
	e := setupA(t)
	snap := sumAt(t, e.e.tok, "")
	bad := []any{tsStr(time.Now().UTC().Add(2 * time.Hour)), tsStr(time.Now().UTC().Add(10 * 24 * time.Hour)),
		"2026-01-01T00:00:00", "2026-01-01", "garbage", ""}
	for _, v := range bad {
		fx := fixtureCur("EUR", 2, nil, fu{"zed", 5}, fu{"yan", 5})
		fx["payments"] = []any{map[string]any{"id": "x1", "from_user_id": "u_zed", "to_user_id": "u_yan", "amount": 1, "note": "", "visibility": "public", "created_at": v}}
		r, err := sendWith(ctlClient, "POST", "/_test/reset", "", nil, fx)
		if err != nil {
			t.Fatal(err)
		}
		expectErr(t, r, 422, "validation_failed")
		// nothing changed: the old session and state are intact
		if sumAt(t, e.e.tok, "") != snap || balAt(t, e.e.tok["ada"], "") != 10000 {
			t.Fatalf("state changed by rejected reset (created_at=%v)", v)
		}
	}
	// exactly "now minus a moment" is fine
	fx := fixtureCur("EUR", 2, nil, fu{"zed", 5}, fu{"yan", 5})
	fx["payments"] = []any{seedP("x1", "zed", "yan", 1, "public", time.Now().UTC().Add(-time.Second))}
	reset(t, fx)
}

func TestR201_BalancesNotChangedByLoadingAndOpening(t *testing.T) {
	e := setupA(t)
	if balAt(t, e.e.tok["ada"], "") != 10000 || balAt(t, e.e.tok["bob"], "") != 2500 || balAt(t, e.e.tok["cy"], "") != 500 {
		t.Fatal("seeded payments changed the fixture balances")
	}
	// opening = seeded balance - net of seeded payments (received - sent)
	pre := e.t1.Add(-time.Hour)
	for h, want := range map[string]int64{"ada": 10600, "bob": 2200, "cy": 200, "dee": 0, "op": 0} {
		if got := balAt(t, e.e.tok[h], "as_of="+qe(tsStr(pre))); got != want {
			t.Fatalf("%s opening %d want %d", h, got, want)
		}
		st := fullStmt(t, e.e.tok[h], "")
		if num(t, st, "opening_balance") != want {
			t.Fatalf("%s statement opening %d want %d", h, num(t, st, "opening_balance"), want)
		}
	}
	// corrections never change the opening balances
	mustCorrect(t, e.e.tok["ada"], "s_1", 1, 300, e.t1, "less")
	if balAt(t, e.e.tok["ada"], "as_of="+qe(tsStr(pre))) != 10600 || balAt(t, e.e.tok["bob"], "as_of="+qe(tsStr(pre))) != 2200 {
		t.Fatal("a correction changed an opening balance")
	}
	// a new account opens at zero
	su := signup(t, "zed@example.com", "longenough")
	expect(t, su, 201)
	zt := str(t, su.obj(t), "token")
	if balAt(t, zt, "as_of="+qe(tsStr(ago(100*day)))) != 0 {
		t.Fatal("new account must open at 0")
	}
	if num(t, fullStmt(t, zt, ""), "opening_balance") != 0 {
		t.Fatal("statement opening of a new account")
	}
}

// ---------------- R202-R205 /me as_of / known_at ----------------

func TestR202_InvalidInstants(t *testing.T) {
	e := setupA(t)
	tk := e.e.tok["ada"]
	for _, v := range []string{"", "2026-09-24", "2026-09-24T13:20:00", "garbage", "1700000000", "now", "2026-13-45T00:00:00%2B00:00",
		"2026-09-24T25:00:00%2B00:00", "2026-09-24T13:20:00%2B25:00", "13:20:00%2B00:00"} {
		for _, key := range []string{"as_of", "known_at"} {
			expectErr(t, get(t, "/me?"+key+"="+v, tk), 422, "validation_failed")
		}
	}
	expectErr(t, get(t, "/me?as_of=&known_at="+qe(tsStr(time.Now())), tk), 422, "validation_failed")
	expectErr(t, get(t, "/me?as_of="+qe(tsStr(time.Now()))+"&known_at=", tk), 422, "validation_failed")
	expectErr(t, get(t, "/statement?from=", tk), 422, "validation_failed")
	expectErr(t, get(t, "/statement?to=2026-09-24", tk), 422, "validation_failed")
	expectErr(t, get(t, "/statement?known_at=2026-09-24T13:20:00", tk), 422, "validation_failed")
	expectErr(t, get(t, "/statement?from=garbage", tk), 422, "validation_failed")
	expect(t, get(t, "/me?as_of="+qe(tsStr(time.Now())), tk), 200)
}

func TestR203_NoTemporalParamsKeepsFieldsAndNoAsOfKey(t *testing.T) {
	e := setupA(t)
	m := meAt(t, e.e.tok["ada"], "")
	if _, has := m["as_of"]; has {
		t.Fatalf("as_of must be absent: %v", m)
	}
	if _, has := m["known_at"]; has {
		t.Fatalf("known_at must be absent: %v", m)
	}
	for _, k := range []string{"user_id", "display_name", "handle", "balance", "total", "available", "held", "currency", "minor_units"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %s: %v", k, m)
		}
	}
	checkMe(t, m)
	// unknown parameters are still ignored
	expect(t, get(t, "/me?zzz=1", e.e.tok["ada"]), 200)
}

func TestR204_AsOfInclusiveAndBounds(t *testing.T) {
	e := setupA(t)
	asof := func(tm time.Time) string { return "as_of=" + qe(tsStr(tm)) }
	// ada: opening 10600; s_1 -500 @t1; s_3 -100 @t3
	cases := []struct {
		who  string
		at   time.Time
		want int64
	}{
		{"ada", e.t1.Add(-time.Second), 10600}, // before the earliest -> opening
		{"ada", e.t1, 10100},                   // inclusive at exactly created_at
		{"ada", e.t1.Add(time.Second), 10100},
		{"ada", e.t3.Add(-time.Second), 10100},
		{"ada", e.t3, 10000},
		{"ada", time.Now().Add(24 * time.Hour), 10000}, // after the latest -> current
		{"bob", e.t1.Add(-time.Second), 2200},
		{"bob", e.t1, 2700},
		{"bob", e.t2.Add(-time.Second), 2700},
		{"bob", e.t2, 2500},
		{"cy", e.t2, 400},
		{"cy", e.t3.Add(-time.Second), 400},
		{"cy", e.t3, 500},
		{"dee", e.t2, 0},
	}
	for _, c := range cases {
		m := meAt(t, e.e.tok[c.who], asof(c.at))
		if num(t, m, "balance") != c.want || num(t, m, "total") != c.want {
			t.Fatalf("%s as_of %s: %v want %d", c.who, tsStr(c.at), m, c.want)
		}
		checkMe(t, m)
	}
	// sum of every wallet equals the seeded total at every instant
	for _, at := range []time.Time{e.t1.Add(-day), e.t1, e.t2, e.t3, e.t3.Add(day), time.Now().Add(time.Hour)} {
		if s := sumAt(t, e.e.tok, asof(at)); s != e.e.total {
			t.Fatalf("sum at %s = %d want %d", tsStr(at), s, e.e.total)
		}
	}
	// sub-second precision and offsets
	if got := balAt(t, e.e.tok["ada"], "as_of="+qe(tsMicro(e.t1.Add(-time.Microsecond)))); got != 10600 {
		t.Fatalf("1us before: %d", got)
	}
	off := e.t1.In(time.FixedZone("", 2*3600)).Format("2006-01-02T15:04:05-07:00")
	if got := balAt(t, e.e.tok["ada"], "as_of="+qe(off)); got != 10100 {
		t.Fatalf("offset form %s: %d", off, got)
	}
	// new payments after as_of do not change it
	mustPay(t, e.e.tok["ada"], "bob", 7, nil)
	if got := balAt(t, e.e.tok["ada"], asof(e.t3)); got != 10000 {
		t.Fatal("as_of in the past changed by a later payment")
	}
	if got := balAt(t, e.e.tok["ada"], ""); got != 9993 {
		t.Fatal("current")
	}
}

func TestR205_EchoVerbatim(t *testing.T) {
	e := setupA(t)
	for _, v := range []string{
		"2026-09-24T13:20:00+02:00",
		"2026-09-24T13:20:00.500+00:00",
		"2026-09-24T13:20:00.123456-05:30",
		tsStr(e.t1),
	} {
		m := meAt(t, e.e.tok["ada"], "as_of="+qe(v)+"&known_at="+qe(v))
		if m["as_of"] != v || m["known_at"] != v {
			t.Fatalf("not echoed verbatim: as_of=%v known_at=%v want %s", m["as_of"], m["known_at"], v)
		}
	}
	m := meAt(t, e.e.tok["ada"], "as_of="+qe("2026-09-24T13:20:00+02:00"))
	if _, has := m["known_at"]; has {
		t.Fatal("known_at must be absent when not supplied")
	}
	m = meAt(t, e.e.tok["ada"], "known_at="+qe("2026-09-24T13:20:00+02:00"))
	if _, has := m["as_of"]; has {
		t.Fatal("as_of must be absent when not supplied")
	}
}

// ---------------- R206-R212 statements ----------------

func TestR206_StatementShapeAndDefaults(t *testing.T) {
	e := setupA(t)
	m := fullStmt(t, e.e.tok["ada"], "")
	for _, k := range []string{"opening_balance", "entries", "closing_balance", "has_more", "snapshot"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %s: %v", k, m)
		}
	}
	if s, ok := m["snapshot"].(string); !ok || s == "" {
		t.Fatal("snapshot must be a non-empty string")
	}
	if m["has_more"] != false {
		t.Fatal("has_more")
	}
	// defaults: from = the wallet's opening, to = now -> closing = current balance
	if num(t, m, "opening_balance") != 10600 || num(t, m, "closing_balance") != 10000 {
		t.Fatalf("defaults: %v", m)
	}
	es := entriesOf(t, m)
	if len(es) != 2 {
		t.Fatalf("ada entries: %d", len(es))
	}
	for _, en := range es {
		for _, k := range []string{"payment", "delta", "balance_after", "revision", "effective_at", "recorded_at"} {
			if _, ok := en[k]; !ok {
				t.Fatalf("entry lacks %s: %v", k, en)
			}
		}
		if num(t, en, "revision") != 1 {
			t.Fatalf("revision %v", en["revision"])
		}
		p := en["payment"].(map[string]any)
		ca := parseTS(t, str(t, p, "created_at"))
		if !parseTS(t, str(t, en, "effective_at")).Equal(ca) || !parseTS(t, str(t, en, "recorded_at")).Equal(ca) {
			t.Fatalf("r1 effective_at/recorded_at must equal created_at: %v", en)
		}
	}
	if es[0]["payment"].(map[string]any)["payment_id"] != "s_1" || num(t, es[0], "delta") != -500 || num(t, es[0], "balance_after") != 10100 {
		t.Fatalf("first entry: %v", es[0])
	}
	if es[1]["payment"].(map[string]any)["payment_id"] != "s_3" || num(t, es[1], "delta") != -100 || num(t, es[1], "balance_after") != 10000 {
		t.Fatalf("second entry: %v", es[1])
	}
	// payment objects keep their stage-2 shape
	p := es[0]["payment"].(map[string]any)
	if p["from_handle"] != "ada" || p["to_handle"] != "bob" || num(t, p, "amount") != 500 || p["visibility"] != "public" || p["currency"] != "EUR" {
		t.Fatalf("payment: %v", p)
	}
	checkStatementConsistent(t, m)
	// received payments are positive
	bm := fullStmt(t, e.e.tok["bob"], "")
	if num(t, entriesOf(t, bm)[0], "delta") != 500 || num(t, entriesOf(t, bm)[1], "delta") != -200 {
		t.Fatalf("bob deltas: %v", bm)
	}
	checkStatementConsistent(t, bm)
	// unauthenticated
	expectErr(t, get(t, "/statement", ""), 401, "unauthenticated")
}

func TestR206_R208_HalfOpenWindow(t *testing.T) {
	e := setupA(t)
	tk := e.e.tok["ada"]
	win := func(from, to time.Time) map[string]any {
		return fullStmt(t, tk, "from="+qe(tsStr(from))+"&to="+qe(tsStr(to)))
	}
	// [t1, t3): includes s_1 (from inclusive), excludes s_3 (to exclusive)
	m := win(e.t1, e.t3)
	if ids := entryIDs(t, m); !strEq(ids, []string{"s_1"}) {
		t.Fatalf("[t1,t3) = %v", ids)
	}
	if num(t, m, "opening_balance") != 10600 || num(t, m, "closing_balance") != 10100 {
		t.Fatalf("opening/closing: %v", m)
	}
	// (t1, t3] shifted by a second: excludes s_1, includes s_3
	m = win(e.t1.Add(time.Second), e.t3.Add(time.Second))
	if ids := entryIDs(t, m); !strEq(ids, []string{"s_3"}) {
		t.Fatalf("window: %v", ids)
	}
	if num(t, m, "opening_balance") != 10100 || num(t, m, "closing_balance") != 10000 {
		t.Fatalf("opening/closing: %v", m)
	}
	// to defaulting: from only
	m = fullStmt(t, tk, "from="+qe(tsStr(e.t3)))
	if ids := entryIDs(t, m); !strEq(ids, []string{"s_3"}) || num(t, m, "opening_balance") != 10100 || num(t, m, "closing_balance") != 10000 {
		t.Fatalf("from only: %v", m)
	}
	// from defaulting: to only
	m = fullStmt(t, tk, "to="+qe(tsStr(e.t3)))
	if ids := entryIDs(t, m); !strEq(ids, []string{"s_1"}) || num(t, m, "opening_balance") != 10600 || num(t, m, "closing_balance") != 10100 {
		t.Fatalf("to only: %v", m)
	}
	// windows that tile the history chain: closing of one equals opening of the next
	a := win(e.t1.Add(-day), e.t2)
	b := win(e.t2, e.t3.Add(day))
	if num(t, a, "closing_balance") != num(t, b, "opening_balance") {
		t.Fatalf("tiling: %v %v", a, b)
	}
	checkStatementConsistent(t, a)
	checkStatementConsistent(t, b)
	// the same payment is in exactly one of two adjacent windows
	seen := map[string]int{}
	for _, id := range append(entryIDs(t, a), entryIDs(t, b)...) {
		seen[id]++
	}
	if seen["s_1"] != 1 || seen["s_3"] != 1 {
		t.Fatalf("payments duplicated or lost across adjacent windows: %v", seen)
	}
}

func TestR207_TiesOrderedByPaymentId(t *testing.T) {
	at := ago(2 * day)
	e := setupP(t, []any{
		seedP("t_c", "bob", "ada", 5, "public", at),
		seedP("t_a", "ada", "bob", 10, "public", at),
		seedP("t_b", "ada", "cy", 20, "private", at),
		seedP("t_0", "cy", "ada", 1, "public", at.Add(-time.Hour)),
		seedP("t_z", "ada", "bob", 3, "public", at.Add(time.Hour)),
	}, azUsers...)
	m := fullStmt(t, e.tok["ada"], "")
	if ids := entryIDs(t, m); !strEq(ids, []string{"t_0", "t_a", "t_b", "t_c", "t_z"}) {
		t.Fatalf("order: %v", ids)
	}
	checkStatementConsistent(t, m)
	// the same order in every pagination of the same window
	var paged []string
	for off := 0; off < 5; off++ {
		pg := stmt(t, e.tok["ada"], "limit=1&offset="+itoa(off))
		paged = append(paged, entryIDs(t, pg)...)
	}
	if !strEq(paged, []string{"t_0", "t_a", "t_b", "t_c", "t_z"}) {
		t.Fatalf("paged order: %v", paged)
	}
	// ties are deterministic across repeated reads
	for i := 0; i < 5; i++ {
		if !strEq(entryIDs(t, fullStmt(t, e.tok["ada"], "")), []string{"t_0", "t_a", "t_b", "t_c", "t_z"}) {
			t.Fatal("tie order not stable")
		}
	}
	// the balance after each tied entry follows that order:
	es := entriesOf(t, m)
	run := num(t, m, "opening_balance")
	for _, en := range es {
		run += num(t, en, "delta")
		if num(t, en, "balance_after") != run {
			t.Fatal("balance_after must follow the tie order")
		}
	}
}

func TestR208_SumInvariantEveryUserEveryWindow(t *testing.T) {
	e := setupA(t)
	bounds := []time.Time{e.t1.Add(-day), e.t1, e.t1.Add(time.Second), e.t2, e.t3, e.t3.Add(time.Hour), time.Now().Add(day)}
	for h, tk := range e.e.tok {
		for i := range bounds {
			for j := range bounds {
				m := fullStmt(t, tk, "from="+qe(tsStr(bounds[i]))+"&to="+qe(tsStr(bounds[j])))
				checkStatementConsistent(t, m)
				if num(t, m, "opening_balance") != balAt(t, tk, "as_of="+qe(tsStr(bounds[i].Add(-time.Microsecond)))) && i <= j {
					// opening is the balance immediately before from
					t.Fatalf("%s window %d..%d: opening %d", h, i, j, num(t, m, "opening_balance"))
				}
			}
		}
	}
}

func TestR209_PaginationNeverChangesBalances(t *testing.T) {
	// 7 payments for ada
	var seeds []any
	base := ago(3 * day)
	for i := 0; i < 7; i++ {
		seeds = append(seeds, seedP("pg_"+itoa(i), "ada", "bob", int64(10+i), "public", base.Add(time.Duration(i)*time.Minute)))
	}
	e := setupP(t, seeds, fu{"ada", 5000}, fu{"bob", 91}, fu{"cy", 0})
	tk := e.tok["ada"]
	full := fullStmt(t, tk, "")
	allIDs := entryIDs(t, full)
	if len(allIDs) != 7 {
		t.Fatal("setup")
	}
	byID := map[string]map[string]any{}
	for _, en := range entriesOf(t, full) {
		byID[entryPID(t, en)] = en
	}
	var gathered []string
	for off := 0; off < 7; off += 3 {
		pg := stmt(t, tk, "limit=3&offset="+itoa(off))
		if num(t, pg, "opening_balance") != num(t, full, "opening_balance") || num(t, pg, "closing_balance") != num(t, full, "closing_balance") {
			t.Fatalf("page at offset %d changed opening/closing: %v", off, pg)
		}
		wantMore := off+3 < 7
		if pg["has_more"] != wantMore {
			t.Fatalf("offset %d has_more=%v want %v", off, pg["has_more"], wantMore)
		}
		for _, en := range entriesOf(t, pg) {
			gathered = append(gathered, entryPID(t, en))
			if num(t, en, "balance_after") != num(t, byID[entryPID(t, en)], "balance_after") {
				t.Fatalf("balance_after changed by pagination: %v", en)
			}
		}
	}
	if !strEq(gathered, allIDs) {
		t.Fatalf("pages %v != %v", gathered, allIDs)
	}
	// exact fit, past the end, limit boundaries
	if pg := stmt(t, tk, "limit=7"); pg["has_more"] != false || len(entriesOf(t, pg)) != 7 {
		t.Fatal("exact fit")
	}
	if pg := stmt(t, tk, "limit=6"); pg["has_more"] != true {
		t.Fatal("has_more with 1 left")
	}
	pg := stmt(t, tk, "offset=7")
	if pg["has_more"] != false || len(entriesOf(t, pg)) != 0 || num(t, pg, "opening_balance") != num(t, full, "opening_balance") || num(t, pg, "closing_balance") != num(t, full, "closing_balance") {
		t.Fatalf("offset at end: %v", pg)
	}
	if pg := stmt(t, tk, "offset=500"); pg["has_more"] != false || len(entriesOf(t, pg)) != 0 {
		t.Fatal("offset beyond the end")
	}
	// default limit 50
	// validation of limit/offset follows stage 1
	for _, q := range []string{"limit=0", "limit=201", "limit=-1", "limit=abc", "limit=", "limit=1e2", "limit=+4", "limit=4.0", "offset=-1", "offset=", "offset=1e1", "offset=+1", "offset=1.0"} {
		expectErr(t, get(t, "/statement?"+q, tk), 422, "validation_failed")
	}
	expect(t, get(t, "/statement?limit=200", tk), 200)
}

func TestR209_DefaultLimit50(t *testing.T) {
	var seeds []any
	base := ago(5 * day)
	for i := 0; i < 55; i++ {
		seeds = append(seeds, seedP("dl_"+pad3(i), "ada", "bob", 1, "public", base.Add(time.Duration(i)*time.Minute)))
	}
	e := setupP(t, seeds, fu{"ada", 5000}, fu{"bob", 55})
	m := stmt(t, e.tok["ada"], "")
	if len(entriesOf(t, m)) != 50 || m["has_more"] != true {
		t.Fatalf("default limit: %d %v", len(entriesOf(t, m)), m["has_more"])
	}
	m2 := stmt(t, e.tok["ada"], "offset=50")
	if len(entriesOf(t, m2)) != 5 || m2["has_more"] != false {
		t.Fatalf("tail: %d", len(entriesOf(t, m2)))
	}
	if num(t, m2, "opening_balance") != num(t, m, "opening_balance") || num(t, m2, "closing_balance") != num(t, m, "closing_balance") {
		t.Fatal("tail changed the window balances")
	}
	if num(t, entriesOf(t, m2)[4], "balance_after") != num(t, m2, "closing_balance") {
		t.Fatal("last entry balance_after must equal closing")
	}
}

func pad3(i int) string {
	s := itoa(i)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}

func TestR210_OnlyOwnPayments(t *testing.T) {
	e := setupA(t)
	// s_1 (ada->bob public), s_2 (bob->cy private), s_3 (ada->cy public)
	want := map[string][]string{"ada": {"s_1", "s_3"}, "bob": {"s_1", "s_2"}, "cy": {"s_2", "s_3"}, "dee": {}, "op": {}}
	for h, ids := range want {
		got := entryIDs(t, fullStmt(t, e.e.tok[h], ""))
		if !strEq(got, ids) {
			t.Fatalf("%s statement %v want %v", h, got, ids)
		}
	}
	// a new public payment between others never appears for a third party
	mustPay(t, e.e.tok["ada"], "bob", 5, nil)
	if got := entryIDs(t, fullStmt(t, e.e.tok["cy"], "")); len(got) != 2 {
		t.Fatalf("cy sees a public payment between others: %v", got)
	}
	dm := fullStmt(t, e.e.tok["dee"], "")
	if len(entriesOf(t, dm)) != 0 || num(t, dm, "opening_balance") != 0 || num(t, dm, "closing_balance") != 0 {
		t.Fatalf("dee: %v", dm)
	}
	// a private payment is in both parties' statements (feed rules do not apply)
	pv := mustPay(t, e.e.tok["ada"], "dee", 9, map[string]any{"visibility": "private"})
	for _, h := range []string{"ada", "dee"} {
		if !idSetFromStmt(t, fullStmt(t, e.e.tok[h], ""))[str(t, pv, "payment_id")] {
			t.Fatalf("%s lacks its private payment", h)
		}
	}
	// the statement still contains a payment which the feed hides from... nobody else: feed vs statement parity for parties
	if idSetFromStmt(t, fullStmt(t, e.e.tok["cy"], ""))[str(t, pv, "payment_id")] {
		t.Fatal("cy sees someone else's private payment")
	}
}

func idSetFromStmt(t testing.TB, m map[string]any) map[string]bool {
	t.Helper()
	s := map[string]bool{}
	for _, id := range entryIDs(t, m) {
		s[id] = true
	}
	return s
}

func TestR211_CorrectionsOneEntryPerPaymentZeroAmount(t *testing.T) {
	e := setupA(t)
	tk := e.e.tok["ada"]
	// no corrections: statement identical to the uncorrected history
	before := fullStmt(t, tk, "")
	// correct s_1 to 300 effective at the same instant, then to 0
	r2 := mustCorrect(t, tk, "s_1", 1, 300, e.t1, "less")
	m := fullStmt(t, tk, "")
	if ids := entryIDs(t, m); !strEq(ids, []string{"s_1", "s_3"}) {
		t.Fatalf("one entry per payment: %v", ids)
	}
	en := entriesOf(t, m)[0]
	if num(t, en, "revision") != 2 || num(t, en, "delta") != -300 || num(t, en["payment"].(map[string]any), "amount") != 300 {
		t.Fatalf("selected revision not applied: %v", en)
	}
	if !parseTS(t, str(t, en, "recorded_at")).Equal(parseTS(t, str(t, r2, "recorded_at"))) {
		t.Fatal("recorded_at of the selected revision")
	}
	checkStatementConsistent(t, m)
	if num(t, m, "closing_balance") != 10200 {
		t.Fatalf("closing %d", num(t, m, "closing_balance"))
	}
	// reversal to zero: still an entry, delta 0
	mustCorrect(t, tk, "s_1", 2, 0, e.t1, "reverse")
	m = fullStmt(t, tk, "")
	es := entriesOf(t, m)
	if len(es) != 2 || entryPID(t, es[0]) != "s_1" || num(t, es[0], "delta") != 0 || num(t, es[0], "revision") != 3 || num(t, es[0]["payment"].(map[string]any), "amount") != 0 {
		t.Fatalf("zero-amount revision must stay as a zero entry: %v", es)
	}
	if num(t, es[0], "balance_after") != num(t, m, "opening_balance") {
		t.Fatal("balance_after of the zero entry")
	}
	checkStatementConsistent(t, m)
	if num(t, m, "closing_balance") != 10500 {
		t.Fatalf("closing %d", num(t, m, "closing_balance"))
	}
	// bob sees the same payment once
	bm := fullStmt(t, e.e.tok["bob"], "")
	n := 0
	for _, id := range entryIDs(t, bm) {
		if id == "s_1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("s_1 appears %d times for bob", n)
	}
	_ = before
	if reflect.DeepEqual(before, m) {
		t.Fatal("statement unchanged by corrections")
	}
}

func TestR212_EmptyWindows(t *testing.T) {
	e := setupA(t)
	tk := e.e.tok["ada"]
	// from > to: empty, opening == closing, no error
	m := fullStmt(t, tk, "from="+qe(tsStr(e.t3))+"&to="+qe(tsStr(e.t1)))
	if len(entriesOf(t, m)) != 0 || num(t, m, "opening_balance") != num(t, m, "closing_balance") || m["has_more"] != false {
		t.Fatalf("from > to: %v", m)
	}
	// from == to: empty
	m = fullStmt(t, tk, "from="+qe(tsStr(e.t1))+"&to="+qe(tsStr(e.t1)))
	if len(entriesOf(t, m)) != 0 || num(t, m, "opening_balance") != num(t, m, "closing_balance") {
		t.Fatalf("from == to: %v", m)
	}
	// a window with no payments inside
	m = fullStmt(t, tk, "from="+qe(tsStr(e.t1.Add(time.Hour)))+"&to="+qe(tsStr(e.t3.Add(-time.Hour))))
	if len(entriesOf(t, m)) != 0 || num(t, m, "opening_balance") != 10100 || num(t, m, "closing_balance") != 10100 {
		t.Fatalf("quiet window: %v", m)
	}
	// future instants allowed
	m = fullStmt(t, tk, "from="+qe(tsStr(time.Now().Add(24*time.Hour)))+"&to="+qe(tsStr(time.Now().Add(48*time.Hour))))
	if len(entriesOf(t, m)) != 0 || num(t, m, "opening_balance") != 10000 || num(t, m, "closing_balance") != 10000 {
		t.Fatalf("future window: %v", m)
	}
	// a window entirely before all history
	m = fullStmt(t, tk, "to="+qe(tsStr(e.t1.Add(-time.Hour))))
	if len(entriesOf(t, m)) != 0 || num(t, m, "opening_balance") != 10600 || num(t, m, "closing_balance") != 10600 {
		t.Fatalf("before history: %v", m)
	}
}
