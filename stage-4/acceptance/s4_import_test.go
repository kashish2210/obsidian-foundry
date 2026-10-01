package acceptance

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func noRefundOf(t testing.TB, tok string) {
	t.Helper()
	items, _ := activity(t, tok, "?limit=200")
	for _, it := range items {
		if !isNull(it, "refund_of") {
			t.Fatalf("imported payment must have refund_of: null: %v", it)
		}
	}
	for _, en := range entriesOf(t, fullStmt(t, tok, "")) {
		if p := en["payment"].(map[string]any); !isNull(p, "refund_of") {
			t.Fatalf("statement payment refund_of: %v", p)
		}
	}
}

func sumTotals(t testing.TB, toks map[string]string, want int64) {
	t.Helper()
	for _, q := range []string{"", asofQ(ago(3650 * day)), asofQ(time.Now().Add(day)), asofQ(ago(day)), "known_at=" + qe(tsMicro(ago(3650*day)))} {
		if s := sumAt(t, toks, q); s != want {
			t.Fatalf("sum %d != %d for %q", s, want, q)
		}
	}
}

func TestR262_Stage1ExportImports(t *testing.T) {
	var meta s1Meta
	raw := loadMeta(t, "stage1", &meta)
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	expect(t, doImport(t, raw), 204)
	toks := meta.Tokens
	for _, tk := range toks {
		noRefundOf(t, tk)
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
	sumTotals(t, toks, 13000)
	// retries and tokens
	expect(t, post(t, "/payments", toks["ada"], meta.Pay.Key, meta.Pay.Body), 200)
	// refunds work on imported payments (receiver bob)
	pid := str(t, meta.Pay.Response, "payment_id")
	rf := mustRefund(t, toks["bob"], pid, 100)
	if rf["refund_of"] != pid || !isNull(rf, "settlement_id") {
		t.Fatalf("refund of an imported payment: %v", rf)
	}
	expectErr(t, refund(t, toks["bob"], pid, 1), 422, "refund_exceeds_payment")
	// the imported settlement keeps its membership: a batch needs every member; singles are immutable
	mem := meta.Settlement.Response["payments"].([]any)[0].(map[string]any)
	commit := parseTS(t, str(t, meta.Settlement.Response, "committed_at"))
	expectErr(t, correct(t, toks["ada"], str(t, mem, "payment_id"), corrBody(1, 10, commit, "x")), 422, "linked_payment_immutable")
	var items []any
	for _, m := range meta.Settlement.Response["payments"].([]any) {
		items = append(items, item(str(t, m.(map[string]any), "payment_id"), 1, 0, commit, "reverse"))
	}
	expect(t, cbatch(t, toks["op"], cbody(items...)), 201)
	// an ordinary imported payment is corrected by the operator
	expect(t, cbatch(t, toks["op"], cbody(item("p_seed", 1, 400, mustParseCreated(t, toks["ada"], "p_seed"), "op fix"))), 201)
	sumTotals(t, toks, 13000)
	for _, tk := range toks {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
}

func mustParseCreated(t testing.TB, tok, pid string) time.Time {
	t.Helper()
	items, _ := activity(t, tok, "?limit=200")
	for _, it := range items {
		if it["payment_id"] == pid {
			return parseTS(t, str(t, it, "created_at"))
		}
	}
	t.Fatalf("payment %s not visible", pid)
	return time.Time{}
}

func TestR262_Stage2ExportImports(t *testing.T) {
	var meta s2Meta
	raw := loadMeta(t, "stage2", &meta)
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	expect(t, doImport(t, raw), 204)
	toks := meta.Tokens
	for _, tk := range toks {
		noRefundOf(t, tk)
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
	sumTotals(t, toks, 13000)
	ada := meAt(t, toks["ada"], "")
	if num(t, ada, "held") != 550 {
		t.Fatalf("holds lost: %v", ada)
	}
	for i, r := range meta.Replays {
		rep := post(t, r.Path, toks[r.Tok], r.Key, r.Body)
		if rep.Status != 200 || !sameJSON(mustJSON(r.Response), rep.Body) {
			t.Fatalf("replay %d %s: %s", i, r.Path, rep)
		}
	}
	// a capture can be refunded, not corrected; the authorization stays as it is
	cid := meta.OpenHold.CapturePaymentID
	rf := mustRefund(t, toks["cy"], cid, 50)
	if rf["refund_of"] != cid {
		t.Fatalf("refund: %v", rf)
	}
	g := getAuthz(t, toks["ada"], meta.OpenHold.AuthorizationID)
	if g["status"] != "open" || num(t, g, "remaining_amount") != 250 || num(t, meAt(t, toks["ada"], ""), "held") != 550 {
		t.Fatalf("a refund changed the authorization: %v", g)
	}
	expectErr(t, correct(t, toks["ada"], cid, corrBody(1, 10, ago(time.Minute), "x")), 422, "linked_payment_immutable")
	var capBatch = cbatch(t, toks["op"], cbody(item(cid, 1, 10, ago(time.Minute), "x")))
	expectErr(t, capBatch, 422, "linked_payment_immutable")
	// the settlement imported from stage 2 keeps its membership
	for _, r := range meta.Replays {
		if r.Path == "/settlements" {
			var items []any
			commit := parseTS(t, str(t, r.Response, "committed_at"))
			for _, m := range r.Response["payments"].([]any) {
				items = append(items, item(str(t, m.(map[string]any), "payment_id"), 1, 0, commit, "reverse"))
			}
			expect(t, cbatch(t, toks["op"], cbody(items...)), 201)
		}
	}
	sumTotals(t, toks, 13000)
	for _, tk := range toks {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
}

type s3Replay struct {
	Path     string          `json:"path"`
	Tok      string          `json:"tok"`
	Key      string          `json:"key"`
	Body     json.RawMessage `json:"body"`
	Response json.RawMessage `json:"response"`
}

type s3Meta struct {
	Tokens       map[string]string `json:"tokens"`
	Replays      []s3Replay        `json:"replays"`
	Snapshot     string            `json:"snapshot"`
	SnapshotPage json.RawMessage   `json:"snapshot_page"`
	Settlement   struct {
		ID         string   `json:"id"`
		PaymentIDs []string `json:"payment_ids"`
	} `json:"settlement"`
	Failed struct {
		Key  string          `json:"key"`
		Body json.RawMessage `json:"body"`
		Tok  string          `json:"tok"`
	} `json:"failed"`
}

func TestR262_Stage3ExportImports(t *testing.T) {
	var meta s3Meta
	raw := loadMeta(t, "stage3", &meta)
	reset(t, fixtureAZ(nil, nil, fu{"x", 1}))
	expect(t, doImport(t, raw), 204)
	toks := meta.Tokens
	for _, tk := range toks {
		noRefundOf(t, tk)
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
	sumTotals(t, toks, 13000)
	// the snapshot taken in stage 3 still pages its frozen entries
	pg := get(t, "/statement?limit=200&snapshot="+qe(meta.Snapshot), toks["ada"])
	expect(t, pg, 200)
	got, _ := decodeNum(pg.Body)
	want, _ := decodeNum(meta.SnapshotPage)
	gm, wm := got.(map[string]any), want.(map[string]any)
	delete(gm, "snapshot")
	delete(wm, "snapshot")
	if !reflect.DeepEqual(gm, wm) {
		t.Fatalf("snapshot page differs after import:\n%v\n%v", gm, wm)
	}
	// ... and differs from the live statement (a later correction happened)
	live := fullStmt(t, toks["ada"], "")
	if reflect.DeepEqual(stripToken(live), gm) {
		t.Fatal("the snapshot must be frozen, not live")
	}
	expectErr(t, get(t, "/statement?snapshot="+qe(meta.Snapshot), toks["bob"]), 404, "not_found")
	// every stage-3 retry replays its original response
	for i, r := range meta.Replays {
		var body any
		_ = json.Unmarshal(r.Body, &body)
		rep := post(t, r.Path, toks[r.Tok], r.Key, body)
		if rep.Status != 200 || !sameJSON(r.Response, rep.Body) {
			t.Fatalf("replay %d %s after import: %s", i, r.Path, rep)
		}
	}
	// corrections and revisions survive: s3_p1 has r2 (smaller); refunds are bounded by the CORRECTED amount
	revs := revisionsOf(t, toks["ada"], "s3_p1")
	if len(revs) != 2 || num(t, revs[1], "amount") != 300 {
		t.Fatalf("s3_p1 revisions: %v", revs)
	}
	mustRefund(t, toks["bob"], "s3_p1", 300)
	expectErr(t, refund(t, toks["bob"], "s3_p1", 1), 422, "refund_exceeds_payment")
	// settlement membership survives: incomplete batch, then the whole settlement
	var settleResp map[string]any
	for _, r := range meta.Replays {
		if r.Path == "/settlements" {
			_ = json.Unmarshal(r.Response, &settleResp)
		}
	}
	mems := settleResp["payments"].([]any)
	commit := parseTS(t, str(t, settleResp, "committed_at"))
	first := mems[0].(map[string]any)
	expectErr(t, cbatch(t, toks["op"], cbody(item(str(t, first, "payment_id"), 1, 0, commit, "partial"))), 422, "incomplete_settlement")
	expectErr(t, correct(t, toks["ada"], str(t, first, "payment_id"), corrBody(1, 0, commit, "single")), 422, "linked_payment_immutable")
	var items []any
	for _, m := range mems {
		items = append(items, item(str(t, m.(map[string]any), "payment_id"), 1, 0, commit, "reverse"))
	}
	mustBatch(t, toks["op"], cbody(items...))
	// the batch reversed a settlement but old snapshots did not move
	pg2 := get(t, "/statement?limit=200&snapshot="+qe(meta.Snapshot), toks["ada"])
	got2, _ := decodeNum(pg2.Body)
	gm2 := got2.(map[string]any)
	delete(gm2, "snapshot")
	if !reflect.DeepEqual(gm2, wm) {
		t.Fatal("snapshot changed by a batch after import")
	}
	// failed key reusable
	var fb any
	_ = json.Unmarshal(meta.Failed.Body, &fb)
	fm := fb.(map[string]any)
	fm["amount"] = 10
	expect(t, post(t, "/payments", toks[meta.Failed.Tok], meta.Failed.Key, fm), 201)
	sumTotals(t, toks, 13000)
	for _, tk := range toks {
		checkStatementConsistent(t, fullStmt(t, tk, ""))
	}
}

// ---------------- export / import of refunds and batches ----------------

func TestR262_ExportImportCarriesRefundsAndBatches(t *testing.T) {
	s := setupS(t)
	op, ta, tb, tc := s.e.tok["op"], s.e.tok["ada"], s.e.tok["bob"], s.e.tok["cy"]
	// refunds of an ordinary payment, a settlement member
	k1 := newKey()
	r1 := post(t, "/payments/b_1/refunds", tb, k1, map[string]any{"amount": 150})
	expect(t, r1, 201)
	mustRefund(t, tb, "b_1", 50)
	k2 := newKey()
	r2 := post(t, "/payments/"+s.m[0]+"/refunds", tb, k2, map[string]any{"amount": 30})
	expect(t, r2, 201)
	// a batch (with a retry record) and a failed batch key
	tick()
	kb := newKey()
	x := s.commit.Add(-time.Hour)
	bbody := cbody(item(s.m[0], 1, 90, x, "settle"), item(s.m[1], 1, 45, x, "settle"), item(s.m[2], 1, 18, x, "settle"), item("b_2", 1, 280, s.t2, "ordinary"))
	rb := post(t, "/correction-batches", op, kb, bbody)
	expect(t, rb, 201)
	failKey := newKey()
	expectErr(t, post(t, "/correction-batches", op, failKey, cbody(item("b_3", 9, 90, s.t3, "stale"))), 409, "stale_revision")
	// a snapshot, then a later correction
	m0 := fullStmt(t, ta, "")
	snap := str(t, m0, "snapshot")
	mustCorrect(t, tc, "b_3", 1, 90, s.t3, "later")
	future := time.Now().Add(time.Hour)
	views := func() map[string]any {
		out := map[string]any{}
		for h, tk := range s.e.tok {
			for i, q := range []string{"", asofQ(s.t1), asofQ(s.t3), asofQ(x), asofQ(future)} {
				out["me-"+h+itoa(i)] = meAt(t, tk, q)
			}
			out["st-"+h] = stripToken(fullStmt(t, tk, ""))
		}
		out["rev-b1"] = revisionsOf(t, ta, "b_1")
		out["rev-m0"] = revisionsOf(t, ta, s.m[0])
		out["rev-b2"] = revisionsOf(t, tb, "b_2")
		return out
	}
	want := views()
	exp := doExport(t)
	late := str(t, fullStmt(t, ta, ""), "snapshot")

	reset(t, fixtureAZ(nil, nil, fu{"xavier", 5}))
	expect(t, doImport(t, exp.Body), 204)
	if got := views(); !reflect.DeepEqual(got, want) {
		for k := range want {
			if !reflect.DeepEqual(got[k], want[k]) {
				t.Fatalf("view %s differs after import:\n got %v\nwant %v", k, got[k], want[k])
			}
		}
	}
	// refund totals survive: b_1 had 200 refunded, so 301 more cannot fit and 300 can
	expectErr(t, refund(t, tb, "b_1", 301), 422, "refund_exceeds_payment")
	mustRefund(t, tb, "b_1", 300)
	expectErr(t, refund(t, tb, "b_1", 1), 422, "refund_exceeds_payment")
	// retries: refunds and the batch return their original bodies
	for _, c := range []struct {
		path, tok, key string
		body           any
		orig           resp
	}{
		{"/payments/b_1/refunds", tb, k1, map[string]any{"amount": 150}, r1},
		{"/payments/" + s.m[0] + "/refunds", tb, k2, map[string]any{"amount": 30}, r2},
		{"/correction-batches", op, kb, bbody, rb},
	} {
		rep := post(t, c.path, c.tok, c.key, c.body)
		expect(t, rep, 200)
		requireSameJSON(t, c.orig, rep)
	}
	expectErr(t, post(t, "/correction-batches", op, kb, cbody(item("b_2", 2, 1, s.t2, "x"))), 409, "idempotency_key_reuse")
	// the failed key is reusable; batch revisions keep their id
	expect(t, post(t, "/correction-batches", op, failKey, cbody(item("b_3", 2, 80, s.t3, "now valid"))), 201)
	bid := str(t, rb.obj(t), "correction_batch_id")
	if rv := revisionsOf(t, ta, s.m[0]); rv[1]["correction_batch_id"] != bid {
		t.Fatalf("batch id after import: %v", rv[1])
	}
	// snapshots: the exported one pages, the later one is unknown
	old := snapPage(t, ta, snap, "limit=200")
	if !reflect.DeepEqual(stripToken(old), stripToken(m0)) {
		t.Fatal("exported snapshot differs after import")
	}
	expectErr(t, get(t, "/statement?snapshot="+qe(late), ta), 404, "not_found")
	// repeated import does not duplicate refunds or revisions
	expect(t, doImport(t, exp.Body), 204)
	expect(t, doImport(t, exp.Body), 204)
	if got := views(); !reflect.DeepEqual(got, want) {
		t.Fatal("repeated import changed the history")
	}
	// ids do not collide
	np := mustPay(t, ta, "bob", 1, nil)
	for _, en := range entriesOf(t, fullStmt(t, ta, "")) {
		if entryPID(t, en) == str(t, np, "payment_id") && en["payment"].(map[string]any)["refund_of"] != nil {
			t.Fatal("new payment collides with a refund id")
		}
	}
	sumTotals(t, s.e.tok, s.e.total)
}

// ---------------- R263 ----------------

func TestR263_InvariantsAfterMixedOperations(t *testing.T) {
	s := setupS(t)
	op := s.e.tok["op"]
	ta, tb, tc, td := s.e.tok["ada"], s.e.tok["bob"], s.e.tok["cy"], s.e.tok["dee"]
	// a mix of refunds, single corrections, batches, authorizations and settlements
	p1 := mustPay(t, ta, "bob", 200, nil)
	p2 := mustPay(t, tb, "cy", 150, map[string]any{"visibility": "private"})
	mustRefund(t, tb, str(t, p1, "payment_id"), 80)
	mustRefund(t, tc, str(t, p2, "payment_id"), 40)
	mustCorrect(t, ta, str(t, p1, "payment_id"), 1, 150, parseTS(t, str(t, p1, "created_at")), "single")
	az := mustAuthorize(t, tc, "dee", 300, nil)
	mustCapture(t, td, aid(t, az), map[string]any{"amount": 120, "final": false})
	x := s.commit.Add(-30 * time.Minute)
	mustBatch(t, op, cbody(item(s.m[0], 1, 60, x, "b"), item(s.m[1], 1, 30, x, "b"), item(s.m[2], 1, 10, x, "b"),
		item("b_1", 1, 450, s.t1, "b"), item(str(t, p2, "payment_id"), 1, 140, parseTS(t, str(t, p2, "created_at")), "b")))
	expect(t, settle(t, op, batch(tr("ada", "dee", 25), tr("dee", "cy", 5))), 201)
	expectErr(t, refund(t, td, str(t, p1, "payment_id"), 10), 403, "forbidden") // dee is not the receiver
	mustRefund(t, tb, str(t, p1, "payment_id"), 20)                             // 80 + 20 <= 150

	// the sum of totals equals the seeded total in every historical view
	var knowns []string
	for _, k := range []time.Time{ago(3650 * day), s.t1, x, time.Now(), time.Now().Add(time.Hour)} {
		knowns = append(knowns, "known_at="+qe(tsMicro(k)))
	}
	for _, at := range []time.Time{ago(3650 * day), s.t1, s.t2, s.t3, x, s.commit, time.Now(), time.Now().Add(time.Hour)} {
		for _, k := range append(knowns, "") {
			q := asofQ(at)
			if k != "" {
				q += "&" + k
			}
			if got := sumAt(t, s.e.tok, q); got != s.e.total {
				t.Fatalf("sum of totals %d != seeded %d at %s", got, s.e.total, q)
			}
		}
	}
	payments := map[string]map[string]any{}
	var boundaries []time.Time
	for h, tk := range s.e.tok {
		m := meAt(t, tk, "")
		if num(t, m, "available") < 0 || num(t, m, "held") < 0 || num(t, m, "total") != num(t, m, "balance") || num(t, m, "available") != num(t, m, "total")-num(t, m, "held") {
			t.Fatalf("%s now: %v", h, m)
		}
		st := fullStmt(t, tk, "")
		checkStatementConsistent(t, st)
		if num(t, st, "opening_balance") < 0 || num(t, st, "closing_balance") != num(t, m, "balance") {
			t.Fatalf("%s: %v", h, st)
		}
		for _, en := range entriesOf(t, st) {
			if num(t, en, "balance_after") < 0 {
				t.Fatalf("%s negative after %v", h, en)
			}
			p := en["payment"].(map[string]any)
			payments[str(t, p, "payment_id")] = p
			boundaries = append(boundaries, parseTS(t, str(t, en, "effective_at")))
		}
	}
	// total and available are never negative at any boundary (effective instants and hold events)
	for _, a := range func() []map[string]any { x, _ := listAuthz(t, tc, "?limit=200"); return x }() {
		boundaries = append(boundaries, parseTS(t, str(t, a, "created_at")))
	}
	for _, at := range boundaries {
		for h, tk := range s.e.tok {
			for _, off := range []time.Duration{0, -time.Microsecond} {
				m := meAt(t, tk, asofQ(at.Add(off)))
				if num(t, m, "total") < 0 || num(t, m, "available") < 0 {
					t.Fatalf("%s negative at %v: %v", h, at.Add(off), m)
				}
			}
		}
	}
	// no payment has refunds exceeding its corrected amount
	refunded := map[string]int64{}
	for _, p := range payments {
		if r, ok := p["refund_of"].(string); ok {
			refunded[r] += num(t, p, "amount")
		}
	}
	if len(refunded) == 0 {
		t.Fatal("setup: no refunds seen")
	}
	for pid, sum := range refunded {
		tgt, ok := payments[pid]
		if !ok {
			continue // the target is not in this union of statements
		}
		if sum > num(t, tgt, "amount") {
			t.Fatalf("payment %s: refunds %d exceed the corrected amount %d", pid, sum, num(t, tgt, "amount"))
		}
	}
}
