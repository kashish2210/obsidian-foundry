package acceptance

import (
	"reflect"
	"testing"
	"time"
)

// stripToken returns a copy of a statement without the snapshot token.
func stripToken(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		if k != "snapshot" {
			out[k] = v
		}
	}
	return out
}

func snapPage(t testing.TB, tok, snap, extra string) map[string]any {
	t.Helper()
	q := "snapshot=" + qe(snap)
	if extra != "" {
		q += "&" + extra
	}
	return stmt(t, tok, q)
}

func TestR226_SnapshotFrozenAcrossWrites(t *testing.T) {
	a := setupA(t)
	ta := a.e.tok["ada"]
	m0 := fullStmt(t, ta, "")
	snap := str(t, m0, "snapshot")
	if snap == "" {
		t.Fatal("no snapshot")
	}
	again := fullStmt(t, ta, "")
	if str(t, again, "snapshot") == snap {
		t.Fatal("each first read must return a fresh token")
	}
	// the token pages the exact result
	pg := snapPage(t, ta, snap, "limit=200")
	if !reflect.DeepEqual(stripToken(pg), stripToken(m0)) {
		t.Fatalf("snapshot page differs from the first read:\n%v\n%v", pg, m0)
	}
	// payments, corrections and lifecycle events after the read do not change it
	time.Sleep(20 * time.Millisecond)
	mustPay(t, ta, "bob", 5, nil)
	mustPay(t, a.e.tok["cy"], "ada", 3, nil)
	mustCorrect(t, ta, "s_1", 1, 300, a.t1, "smaller")
	az := mustAuthorize(t, ta, "bob", 200, nil)
	mustCapture(t, a.e.tok["bob"], aid(t, az), map[string]any{"amount": 50})
	pg2 := snapPage(t, ta, snap, "limit=200")
	if !reflect.DeepEqual(stripToken(pg2), stripToken(m0)) {
		t.Fatalf("snapshot changed after later activity:\n%v\n%v", pg2, m0)
	}
	// a new read reflects the changes
	m1 := fullStmt(t, ta, "")
	if reflect.DeepEqual(stripToken(m1), stripToken(m0)) {
		t.Fatal("fresh statement must see the new state")
	}
	if len(entriesOf(t, m1)) != 5 { // s_1, s_3, two new payments, the capture
		t.Fatalf("entries: %v", entryIDs(t, m1))
	}
	// paging through a snapshot with limit 1
	var ids []string
	for off := 0; off < 2; off++ {
		p := snapPage(t, ta, snap, "limit=1&offset="+itoa(off))
		if num(t, p, "opening_balance") != num(t, m0, "opening_balance") || num(t, p, "closing_balance") != num(t, m0, "closing_balance") {
			t.Fatal("snapshot pages changed the window balances")
		}
		if p["has_more"] != (off == 0) {
			t.Fatalf("offset %d has_more %v", off, p["has_more"])
		}
		ids = append(ids, entryIDs(t, p)...)
	}
	if !strEq(ids, []string{"s_1", "s_3"}) {
		t.Fatalf("snapshot paging %v", ids)
	}
	// the default `to` was frozen: a payment made after the read is not in the snapshot window
	if pg3 := snapPage(t, ta, snap, ""); len(entriesOf(t, pg3)) != 2 {
		t.Fatalf("default limit page: %d", len(entriesOf(t, pg3)))
	}
}

func TestR226_SnapshotOfWindowedReadAndFirstPage(t *testing.T) {
	a := setupA(t)
	tb := a.e.tok["bob"]
	w := "from=" + qe(tsStr(a.t1.Add(-time.Hour))) + "&to=" + qe(tsStr(a.t3.Add(time.Hour)))
	first := stmt(t, tb, w+"&limit=1")
	if first["has_more"] != true || len(entriesOf(t, first)) != 1 {
		t.Fatalf("first page: %v", first)
	}
	snap := str(t, first, "snapshot")
	mustCorrect(t, a.e.tok["ada"], "s_1", 1, 100, a.t1, "x")
	p2 := snapPage(t, tb, snap, "limit=1&offset=1")
	if p2["has_more"] != false || len(entriesOf(t, p2)) != 1 || entryPID(t, entriesOf(t, p2)[0]) != "s_2" {
		t.Fatalf("second page: %v", p2)
	}
	// the first entry still shows the ORIGINAL amount through the snapshot
	p1 := snapPage(t, tb, snap, "limit=1")
	e1 := entriesOf(t, p1)[0]
	if num(t, e1, "delta") != 500 || num(t, e1["payment"].(map[string]any), "amount") != 500 || num(t, e1, "revision") != 1 {
		t.Fatalf("snapshot saw the later correction: %v", e1)
	}
	// opening and closing are the full window's and identical on every page
	if num(t, p2, "opening_balance") != num(t, first, "opening_balance") || num(t, p2, "closing_balance") != num(t, first, "closing_balance") {
		t.Fatal("page balances")
	}
	// balance_after on later pages continues from the full-window running balance
	if num(t, entriesOf(t, p2)[0], "balance_after") != num(t, first, "closing_balance") {
		t.Fatalf("balance_after on page 2: %v vs closing %v", entriesOf(t, p2)[0], first["closing_balance"])
	}
	// past the end
	end := snapPage(t, tb, snap, "offset=2")
	if end["has_more"] != false || len(entriesOf(t, end)) != 0 {
		t.Fatalf("offset at end: %v", end)
	}
	end = snapPage(t, tb, snap, "offset=99&limit=5")
	if end["has_more"] != false || len(entriesOf(t, end)) != 0 {
		t.Fatal("offset beyond the end")
	}
}

func TestR227_SnapshotAcceptsOnlyLimitAndOffset(t *testing.T) {
	a := setupA(t)
	ta := a.e.tok["ada"]
	snap := str(t, fullStmt(t, ta, ""), "snapshot")
	now := qe(tsStr(time.Now()))
	for _, extra := range []string{"from=" + now, "to=" + now, "known_at=" + now, "from=", "to=", "known_at=",
		"from=garbage", "limit=1&from=" + now, "offset=0&known_at=" + now} {
		expectErr(t, get(t, "/statement?snapshot="+qe(snap)+"&"+extra, ta), 422, "validation_failed")
	}
	// limit/offset are fine and keep their range rules; unknown parameters are ignored
	expect(t, get(t, "/statement?snapshot="+qe(snap)+"&limit=1&offset=1&zzz=1", ta), 200)
	for _, q := range []string{"limit=0", "limit=201", "offset=-1", "limit=abc", "offset=1e1"} {
		expectErr(t, get(t, "/statement?snapshot="+qe(snap)+"&"+q, ta), 422, "validation_failed")
	}
	// still usable after the rejected requests
	if len(entriesOf(t, snapPage(t, ta, snap, "limit=200"))) != 2 {
		t.Fatal("snapshot damaged by rejected requests")
	}
}

func TestR228_SnapshotTokenOwnership(t *testing.T) {
	a := setupA(t)
	ta, tb := a.e.tok["ada"], a.e.tok["bob"]
	snap := str(t, fullStmt(t, ta, ""), "snapshot")
	expectErr(t, get(t, "/statement?snapshot="+qe(snap), tb), 404, "not_found") // another user's token
	expectErr(t, get(t, "/statement?snapshot="+qe(snap)+"&limit=5", a.e.tok["cy"]), 404, "not_found")
	expectErr(t, get(t, "/statement?snapshot=does-not-exist", ta), 404, "not_found") // unknown
	expectErr(t, get(t, "/statement?snapshot="+qe(snap)+"x", ta), 404, "not_found")
	expectErr(t, get(t, "/statement?snapshot="+qe(snap), ""), 401, "unauthenticated")
	// the owner still can
	expect(t, get(t, "/statement?snapshot="+qe(snap), ta), 200)
	// a token from before a reset is gone
	reset(t, fixtureP([]string{"u_op"}, []any{
		seedP("s_1", "ada", "bob", 500, "public", a.t1),
		seedP("s_2", "bob", "cy", 200, "private", a.t2),
		seedP("s_3", "ada", "cy", 100, "public", a.t3),
	}, azUsers...))
	ta2 := login(t, "ada@example.com")
	expectErr(t, get(t, "/statement?snapshot="+qe(snap), ta2), 404, "not_found")
	expectErr(t, get(t, "/statement?snapshot="+qe(snap), ta), 401, "unauthenticated") // the old session is gone as well
	// a fresh one works
	fresh := str(t, fullStmt(t, ta2, ""), "snapshot")
	expect(t, get(t, "/statement?snapshot="+qe(fresh), ta2), 200)
}

func TestR228_FinalPartialPageAndHasMore(t *testing.T) {
	var seeds []any
	base := ago(2 * day)
	for i := 0; i < 5; i++ {
		seeds = append(seeds, seedP("sn_"+itoa(i), "ada", "bob", 1, "public", base.Add(time.Duration(i)*time.Minute)))
	}
	e := setupP(t, seeds, fu{"ada", 100}, fu{"bob", 5}, fu{"cy", 0})
	tk := e.tok["ada"]
	snap := str(t, stmt(t, tk, "limit=2"), "snapshot")
	for _, c := range []struct {
		q    string
		n    int
		more bool
	}{{"limit=2&offset=0", 2, true}, {"limit=2&offset=2", 2, true}, {"limit=2&offset=4", 1, false}, {"limit=5", 5, false},
		{"limit=4", 4, true}, {"offset=5", 0, false}, {"offset=6&limit=3", 0, false}, {"offset=1000", 0, false}, {"limit=200&offset=4", 1, false}} {
		p := snapPage(t, tk, snap, c.q)
		if len(entriesOf(t, p)) != c.n || p["has_more"] != c.more {
			t.Fatalf("%s: %d entries has_more=%v, want %d/%v", c.q, len(entriesOf(t, p)), p["has_more"], c.n, c.more)
		}
		checkOpeningClosing(t, p)
	}
}

func checkOpeningClosing(t testing.TB, p map[string]any) {
	t.Helper()
	if _, ok := p["opening_balance"]; !ok {
		t.Fatal("opening_balance missing")
	}
	if _, ok := p["closing_balance"]; !ok {
		t.Fatal("closing_balance missing")
	}
}

// ---------------- R229 linked payments ----------------

func TestR229_SettlementMembersAndCapturesAreImmutable(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, azUsers...)
	st := settle(t, e.tok["op"], batch(tr("ada", "bob", 100), tr("bob", "cy", 50)))
	expect(t, st, 201)
	sm := st.obj(t)
	commit := parseTS(t, str(t, sm, "committed_at"))
	members := sm["payments"].([]any)
	m0, m1 := members[0].(map[string]any), members[1].(map[string]any)
	// settlement members: the original sender gets 422 linked_payment_immutable
	body := corrBody(1, 10, commit, "try")
	expectErr(t, correct(t, e.tok["ada"], str(t, m0, "payment_id"), body), 422, "linked_payment_immutable")
	expectErr(t, correct(t, e.tok["bob"], str(t, m1, "payment_id"), body), 422, "linked_payment_immutable")
	// each member's r1 uses committed_at for both effective_at and recorded_at
	for _, who := range []struct {
		tok string
		m   map[string]any
	}{{e.tok["ada"], m0}, {e.tok["bob"], m0}, {e.tok["bob"], m1}, {e.tok["cy"], m1}} {
		revs := revisionsOf(t, who.tok, str(t, who.m, "payment_id"))
		if len(revs) != 1 || !parseTS(t, str(t, revs[0], "effective_at")).Equal(commit) || !parseTS(t, str(t, revs[0], "recorded_at")).Equal(commit) {
			t.Fatalf("member r1: %v committed_at %v", revs, sm["committed_at"])
		}
		if num(t, revs[0], "amount") != num(t, who.m, "amount") || revs[0]["reason"] != "" {
			t.Fatalf("r1: %v", revs[0])
		}
	}
	// no revision was created and balances are unchanged
	if len(revisionsOf(t, e.tok["ada"], str(t, m0, "payment_id"))) != 1 || balAt(t, e.tok["ada"], "") != 9900 {
		t.Fatal("rejected corrections changed state")
	}
	// captures
	az := mustAuthorize(t, e.tok["ada"], "bob", 400, nil)
	cp := mustCapture(t, e.tok["bob"], aid(t, az), map[string]any{"amount": 100, "final": false})
	expectErr(t, correct(t, e.tok["ada"], str(t, cp, "payment_id"), corrBody(1, 50, parseTS(t, str(t, cp, "created_at")), "x")), 422, "linked_payment_immutable")
	cp2 := mustCapture(t, e.tok["bob"], aid(t, az), map[string]any{})
	expectErr(t, correct(t, e.tok["ada"], str(t, cp2, "payment_id"), corrBody(1, 50, parseTS(t, str(t, cp2, "created_at")), "x")), 422, "linked_payment_immutable")
	if rv := revisionsOf(t, e.tok["bob"], str(t, cp, "payment_id")); len(rv) != 1 {
		t.Fatal("capture gained a revision")
	}
	// payments created by paying a request ARE ordinary and can be corrected
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 20))
	pr := postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{})
	expect(t, pr, 201)
	pid := str(t, pr.obj(t), "payment_id")
	c := correct(t, e.tok["ada"], pid, corrBody(1, 15, parseTS(t, str(t, pr.obj(t), "created_at")), "fix"))
	expect(t, c, 201)
	// ordinary payments too
	pp := mustPay(t, e.tok["ada"], "bob", 10, nil)
	expect(t, correct(t, e.tok["ada"], str(t, pp, "payment_id"), corrBody(1, 5, parseTS(t, str(t, pp, "created_at")), "fix")), 201)
	// statements contain each capture exactly once, with links
	n := 0
	for _, en := range entriesOf(t, fullStmt(t, e.tok["ada"], "")) {
		if p := en["payment"].(map[string]any); p["authorization_id"] == aid(t, az) {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("captures in the statement: %d want 2", n)
	}
}

// ---------------- R230 imports of stage-1 / stage-2 exports ----------------

func loadMeta(t testing.TB, name string, v any) []byte {
	t.Helper()
	raw := readFileT(t, "testdata/"+name+"_export.json")
	mraw := readFileT(t, "testdata/"+name+"_meta.json")
	if err := jsonUnmarshal(mraw, v); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestR230_Stage1ExportImportsWithHistory(t *testing.T) {
	var meta s1Meta
	raw := loadMeta(t, "stage1", &meta)
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	expect(t, doImport(t, raw), 204)
	toks := meta.Tokens
	// tokens survive; every statement balances and ends at the current balance
	want := map[string]int64{"ada": 10500, "bob": 2000, "cy": 500, "op": 0} // seeded balance minus seeded payments (R201)
	for h, tk := range toks {
		st := fullStmt(t, tk, "")
		checkStatementConsistent(t, st)
		if num(t, st, "opening_balance") != want[h] {
			t.Fatalf("%s opening %d want %d", h, num(t, st, "opening_balance"), want[h])
		}
		if num(t, st, "closing_balance") != balAt(t, tk, "") {
			t.Fatalf("%s closing %d != balance %d", h, num(t, st, "closing_balance"), balAt(t, tk, ""))
		}
		far := "as_of=" + qe(tsStr(ago(3650*day)))
		if balAt(t, tk, far) != want[h] {
			t.Fatalf("%s as_of long ago %d", h, balAt(t, tk, far))
		}
	}
	// the imported seeded and API payments have r1 from created_at
	items, _ := activity(t, toks["ada"], "?limit=200")
	if len(items) < 3 {
		t.Fatalf("imported payments: %d", len(items))
	}
	for _, it := range items {
		revs := revisionsOf(t, toks["ada"], str(t, it, "payment_id"))
		ca := parseTS(t, str(t, it, "created_at"))
		if len(revs) != 1 || !parseTS(t, str(t, revs[0], "effective_at")).Equal(ca) || !parseTS(t, str(t, revs[0], "recorded_at")).Equal(ca) ||
			num(t, revs[0], "amount") != num(t, it, "amount") || revs[0]["reason"] != "" {
			t.Fatalf("r1 of imported payment: %v vs %v", revs, it)
		}
	}
	// the sum invariant in historical views
	for _, q := range []string{"", "as_of=" + qe(tsStr(ago(3650*day))), "as_of=" + qe(tsStr(time.Now().Add(day))), "known_at=" + qe(tsStr(ago(3650*day)))} {
		if s := sumAt(t, toks, q); s != 13000 && q != "known_at="+qe(tsStr(ago(3650*day))) {
			t.Fatalf("sum %d for %q", s, q)
		}
	}
	// retries and tokens keep working
	expect(t, post(t, "/payments", toks["ada"], meta.Pay.Key, meta.Pay.Body), 200)
	rs := post(t, "/settlements", toks["op"], meta.Settlement.Key, meta.Settlement.Body)
	expect(t, rs, 200)
	commit := parseTS(t, str(t, meta.Settlement.Response, "committed_at"))
	// a settlement member of the import: r1 times equal committed_at, and it is immutable
	mem := meta.Settlement.Response["payments"].([]any)[0].(map[string]any)
	rv := revisionsOf(t, toks["ada"], str(t, mem, "payment_id"))
	if len(rv) != 1 || !parseTS(t, str(t, rv[0], "effective_at")).Equal(commit) || !parseTS(t, str(t, rv[0], "recorded_at")).Equal(commit) {
		t.Fatalf("imported member r1: %v", rv)
	}
	expectErr(t, correct(t, toks["ada"], str(t, mem, "payment_id"), corrBody(1, 10, commit, "x")), 422, "linked_payment_immutable")
	// an ordinary imported payment can be corrected, and history stays consistent
	pid := str(t, meta.Pay.Response, "payment_id")
	mustCorrect(t, toks["ada"], pid, 1, 60, parseTS(t, str(t, meta.Pay.Response, "created_at")), "after import")
	for _, tk := range toks {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
	if s := sumAt(t, toks, ""); s != 13000 {
		t.Fatalf("sum after correction: %d", s)
	}
}

type s2Replay struct {
	Path     string         `json:"path"`
	Tok      string         `json:"tok"`
	Key      string         `json:"key"`
	Body     map[string]any `json:"body"`
	Response map[string]any `json:"response"`
}

type s2Meta struct {
	Tokens  map[string]string `json:"tokens"`
	Replays []s2Replay        `json:"replays"`
	Failed  struct {
		Key  string         `json:"key"`
		Body map[string]any `json:"body"`
		Tok  string         `json:"tok"`
	} `json:"failed"`
	OpenHold struct {
		AuthorizationID  string `json:"authorization_id"`
		CapturePaymentID string `json:"capture_payment_id"`
	} `json:"open_hold"`
	Capture3PaymentID string `json:"capture3_payment_id"`
	SeedOpenID        string `json:"seed_open_id"`
}

func TestR230_Stage2ExportImportsWithAuthorizationsAndCaptures(t *testing.T) {
	var meta s2Meta
	raw := loadMeta(t, "stage2", &meta)
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	expect(t, doImport(t, raw), 204)
	toks := meta.Tokens
	want := map[string]int64{"ada": 10500, "bob": 2000, "cy": 500, "op": 0}
	for h, tk := range toks {
		st := fullStmt(t, tk, "")
		checkStatementConsistent(t, st)
		if num(t, st, "opening_balance") != want[h] {
			t.Fatalf("%s opening %d want %d", h, num(t, st, "opening_balance"), want[h])
		}
		if num(t, st, "closing_balance") != balAt(t, tk, "") {
			t.Fatalf("%s closing != balance", h)
		}
	}
	if s := sumAt(t, toks, ""); s != 13000 {
		t.Fatalf("sum %d", s)
	}
	// stage-2 holds survive: ada holds the open remainder (250) and the seeded 300
	ada := meAt(t, toks["ada"], "")
	if num(t, ada, "held") != 550 || num(t, ada, "available") != num(t, ada, "total")-550 {
		t.Fatalf("ada: %v", ada)
	}
	// historical /me agrees at present and far in the past
	ma := meAt(t, toks["ada"], "as_of="+qe(tsStr(time.Now().Add(time.Minute))))
	if num(t, ma, "held") != 550 || num(t, ma, "total") != num(t, ada, "total") {
		t.Fatalf("as_of now: %v", ma)
	}
	if far := meAt(t, toks["ada"], "as_of="+qe(tsStr(ago(3650*day)))); num(t, far, "held") != 0 || num(t, far, "total") != 10500 {
		t.Fatalf("as_of long ago: %v", far)
	}
	// authorizations expose closed_at (null while open) and keep their capture records
	open := getAuthz(t, toks["ada"], meta.OpenHold.AuthorizationID)
	if v, ok := open["closed_at"]; !ok || v != nil {
		t.Fatalf("open hold closed_at: %v", open)
	}
	if open["status"] != "open" || num(t, open, "captured_amount") != 150 || num(t, open, "remaining_amount") != 250 || len(ints(open["payment_ids"])) != 1 {
		t.Fatalf("open hold: %v", open)
	}
	seed := getAuthz(t, toks["ada"], meta.SeedOpenID)
	if v, ok := seed["closed_at"]; !ok || v != nil {
		t.Fatalf("seeded open hold closed_at: %v", seed)
	}
	items, _ := listAuthz(t, toks["ada"], "?limit=200")
	for _, a := range items {
		if _, ok := a["closed_at"]; !ok {
			t.Fatalf("authorization lacks closed_at: %v", a)
		}
	}
	// captures appear exactly once in statements, with their link, and are immutable
	for _, cid := range []string{meta.OpenHold.CapturePaymentID, meta.Capture3PaymentID} {
		n := 0
		for h, tk := range toks {
			if h == "op" {
				continue
			}
			for _, en := range entriesOf(t, fullStmt(t, tk, "")) {
				if entryPID(t, en) == cid {
					n++
					if en["payment"].(map[string]any)["authorization_id"] == nil {
						t.Fatalf("capture lost its link: %v", en)
					}
				}
			}
		}
		if n != 2 { // payer and receiver
			t.Fatalf("capture %s appears %d times across both parties' statements, want 2", cid, n)
		}
		revs := revisionsOf(t, toks["ada"], cid)
		if len(revs) != 1 {
			t.Fatalf("capture revisions: %v", revs)
		}
		expectErr(t, correct(t, toks["ada"], cid, corrBody(1, 1, parseTS(t, str(t, revs[0], "recorded_at")), "x")), 422, "linked_payment_immutable")
	}
	// holds and releases are not statement entries
	for _, en := range entriesOf(t, fullStmt(t, toks["ada"], "")) {
		if en["payment"].(map[string]any)["payment_id"] == nil {
			t.Fatal("non-payment entry")
		}
	}
	// every stage-2 retry replays its original response
	for i, r := range meta.Replays {
		rep := post(t, r.Path, toks[r.Tok], r.Key, r.Body)
		if rep.Status != 200 {
			t.Fatalf("replay %d %s after import: %s", i, r.Path, rep)
		}
		orig := mustJSON(r.Response)
		if !sameJSON(orig, rep.Body) {
			t.Fatalf("replay %d %s differs from the original:\n%s\n%s", i, r.Path, orig, rep.Body)
		}
	}
	// the failed key is reusable and the seeded pending request is payable
	expect(t, post(t, "/payments", toks[meta.Failed.Tok], meta.Failed.Key, map[string]any{"to_handle": "ada", "amount": 10}), 201)
	expect(t, postK(t, "/requests/rq_1/pay", toks["ada"], map[string]any{}), 201)
	// the open hold stays usable: capture the rest
	fin := mustCapture(t, toks["cy"], meta.OpenHold.AuthorizationID, map[string]any{})
	if num(t, fin, "amount") != 250 {
		t.Fatalf("capture of the remainder: %v", fin)
	}
	if s := sumAt(t, toks, ""); s != 13000 {
		t.Fatalf("sum at the end %d", s)
	}
	for _, tk := range toks {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
}
