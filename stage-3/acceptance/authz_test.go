package acceptance

import (
	"strings"
	"testing"
	"time"
)

var azUsers = []fu{{"ada", 10000}, {"bob", 2500}, {"cy", 500}, {"dee", 0}, {"op", 0}}

func azSetup(t testing.TB) *env { t.Helper(); return setupAZ(t, nil, nil, azUsers...) }

func TestR173_MeFields(t *testing.T) {
	e := azSetup(t)
	m := meObj(t, e.tok["ada"])
	if num(t, m, "balance") != 10000 || num(t, m, "total") != 10000 || num(t, m, "available") != 10000 || num(t, m, "held") != 0 {
		t.Fatalf("no holds: %v", m)
	}
	if m["currency"] != "EUR" || num(t, m, "minor_units") != 2 || m["handle"] != "ada" {
		t.Fatalf("old fields lost: %v", m)
	}
	mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	m = meObj(t, e.tok["ada"])
	if num(t, m, "balance") != 10000 || num(t, m, "total") != 10000 || num(t, m, "available") != 8000 || num(t, m, "held") != 2000 {
		t.Fatalf("with hold: %v", m)
	}
	checkMe(t, m)
	// the receiver is unaffected by the hold
	if b := meObj(t, e.tok["bob"]); num(t, b, "total") != 2500 || num(t, b, "available") != 2500 || num(t, b, "held") != 0 {
		t.Fatalf("bob: %v", b)
	}
}

func TestR163_HoldMovesNoMoney(t *testing.T) {
	e := azSetup(t)
	mustAuthorize(t, e.tok["ada"], "bob", 3000, nil)
	mustAuthorize(t, e.tok["ada"], "cy", 1000, nil)
	if total(t, e.tok["ada"]) != 10000 || total(t, e.tok["bob"]) != 2500 || total(t, e.tok["cy"]) != 500 {
		t.Fatal("holds moved money")
	}
	var sum int64
	for _, tk := range e.tok {
		sum += total(t, tk)
	}
	if sum != e.total {
		t.Fatalf("sum %d != %d", sum, e.total)
	}
	if held(t, e.tok["ada"]) != 4000 {
		t.Fatal("held should sum open holds")
	}
}

func TestR167_TTL(t *testing.T) {
	// default 600
	e := setupAZ(t, nil, nil, azUsers...)
	a := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	c, x := checkTS(t, str(t, a, "created_at")), checkTS(t, str(t, a, "expires_at"))
	if d := x.Sub(c); d != 600*time.Second {
		t.Fatalf("default ttl: expires_at-created_at = %v", d)
	}
	// custom
	e = setupAZ(t, 3600, nil, azUsers...)
	a = mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	if d := checkTS(t, str(t, a, "expires_at")).Sub(checkTS(t, str(t, a, "created_at"))); d != 3600*time.Second {
		t.Fatalf("custom ttl: %v", d)
	}
	// invalid values -> 422 and state unchanged
	for _, bad := range []any{0, -1, -600, 1.5, "600", true} {
		r, err := sendWith(ctlClient, "POST", "/_test/reset", "", nil, fixtureAZ(bad, nil, azUsers...))
		if err != nil {
			t.Fatal(err)
		}
		expectErr(t, r, 422, "validation_failed")
		if meObj(t, e.tok["ada"])["handle"] != "ada" { // previous tokens/state still valid
			t.Fatal("state changed by rejected reset")
		}
	}
	// integral spellings of a positive integer
	reset(t, fixtureAZ(1, nil, azUsers...))
}

func TestR168_R169_R170_SeededAuthorizations(t *testing.T) {
	azs := []any{
		seedAZ("a_open", "ada", "bob", 2000, "open", isoIn(2*time.Hour)),
		seedAZ("a_past", "ada", "bob", 1500, "open", isoIn(-2*time.Hour)),
		seedAZ("a_cap", "ada", "bob", 500, "captured", isoIn(2*time.Hour)),
		seedAZ("a_void", "ada", "cy", 700, "voided", isoIn(2*time.Hour)),
		seedAZ("a_exp", "ada", "bob", 300, "expired", isoIn(-3*time.Hour)),
	}
	e := setupAZ(t, nil, azs, azUsers...)
	m := meObj(t, e.tok["ada"])
	if num(t, m, "total") != 10000 || num(t, m, "held") != 2000 || num(t, m, "available") != 8000 || num(t, m, "balance") != 10000 {
		t.Fatalf("only the open, unexpired hold counts: %v", m)
	}
	items, _ := listAuthz(t, e.tok["ada"], "?limit=200")
	by := map[string]map[string]any{}
	for _, it := range items {
		by[aid(t, it)] = it
	}
	if len(by) != 5 {
		t.Fatalf("ada lists %d seeded authorizations", len(by))
	}
	for id, st := range map[string]string{"a_open": "open", "a_past": "expired", "a_cap": "captured", "a_void": "voided", "a_exp": "expired"} {
		if by[id]["status"] != st {
			t.Fatalf("%s status %v want %s", id, by[id]["status"], st)
		}
	}
	if by["a_open"]["from_handle"] != "ada" || by["a_open"]["to_handle"] != "bob" || num(t, by["a_open"], "amount") != 2000 || by["a_open"]["note"] != "seed a_open" {
		t.Fatalf("seeded fields: %v", by["a_open"])
	}
	// R170: a past open entry matches status=expired only
	ex, _ := listAuthz(t, e.tok["ada"], "?status=expired&limit=200")
	if s := idSet(t, ex, "authorization_id"); !s["a_past"] || !s["a_exp"] || len(s) != 2 {
		t.Fatalf("expired filter: %v", s)
	}
	op, _ := listAuthz(t, e.tok["ada"], "?status=open&limit=200")
	if s := idSet(t, op, "authorization_id"); !s["a_open"] || len(s) != 1 {
		t.Fatalf("open filter: %v", s)
	}
	// seeded holds are not feed items
	if a, _ := activity(t, e.tok["ada"], ""); len(a) != 0 {
		t.Fatalf("authorizations leaked into the feed: %v", a)
	}
	// the released funds of the past-open entry are spendable, the open hold is not
	expectErr(t, sendPay(t, e.tok["ada"], "cy", 8001, nil), 409, "insufficient_funds")
	mustPay(t, e.tok["ada"], "cy", 8000, nil)
	// the receiver captures a seeded open hold by its fixture id; the expired one cannot be captured
	p := mustCapture(t, e.tok["bob"], "a_open", map[string]any{})
	if p["authorization_id"] != "a_open" || num(t, p, "amount") != 2000 {
		t.Fatalf("capture of seeded: %v", p)
	}
	r := capture(t, e.tok["bob"], "a_past", map[string]any{})
	expectCaptureClosed(t, r)
	// A seeded entry whose stored status is already "expired": both rows of the spec table apply
	// (not open / past expires_at), so either 409 code is accepted here (the clock-expired cases above are strict, R239).
	r = capture(t, e.tok["bob"], "a_exp", map[string]any{})
	if r.Status != 409 {
		t.Fatalf("want 409, got %s", r)
	}
	if c := r.obj(t)["error"].(map[string]any)["code"]; c != "authorization_expired" && c != "authorization_not_open" {
		t.Fatalf("code %v", c)
	}
	expectErr(t, capture(t, e.tok["bob"], "a_void", map[string]any{}), 403, "forbidden") // bob is not a_void's receiver (cy is)
	expectErr(t, capture(t, e.tok["cy"], "a_void", map[string]any{}), 409, "authorization_not_open")
	expectErr(t, capture(t, e.tok["bob"], "a_cap", map[string]any{}), 409, "authorization_not_open")
	expectErr(t, voidA(t, e.tok["ada"], "a_past"), 409, "authorization_not_open")
}

// R239: a capture on an expired authorization (clock-expired or seeded expired) is exactly
// 409 authorization_expired.
func expectCaptureClosed(t testing.TB, r resp) {
	t.Helper()
	expectErr(t, r, 409, "authorization_expired")
}

func TestR169_SeededHoldsOverBalance(t *testing.T) {
	ok := func(azs []any) {
		t.Helper()
		reset(t, fixtureAZ(nil, azs, fu{"ada", 1000}, fu{"bob", 0}))
	}
	bad := func(azs []any) {
		t.Helper()
		e := setupAZ(t, nil, nil, fu{"zed", 50})
		r, err := sendWith(ctlClient, "POST", "/_test/reset", "", nil, fixtureAZ(nil, azs, fu{"ada", 1000}, fu{"bob", 0}))
		if err != nil {
			t.Fatal(err)
		}
		expectErr(t, r, 422, "validation_failed")
		if total(t, e.tok["zed"]) != 50 {
			t.Fatal("rejected reset changed state")
		}
	}
	h := isoIn(3 * time.Hour)
	bad([]any{seedAZ("a1", "ada", "bob", 600, "open", h), seedAZ("a2", "ada", "bob", 600, "open", h)})
	bad([]any{seedAZ("a1", "ada", "bob", 1001, "open", h)})
	ok([]any{seedAZ("a1", "ada", "bob", 600, "open", h), seedAZ("a2", "ada", "bob", 400, "open", h)}) // exactly the balance
	// expired/captured/voided entries and clock-expired open entries do not count
	ok([]any{seedAZ("a1", "ada", "bob", 900, "open", h), seedAZ("a2", "ada", "bob", 900, "open", isoIn(-2*time.Hour)),
		seedAZ("a3", "ada", "bob", 900, "captured", h), seedAZ("a4", "ada", "bob", 900, "voided", h), seedAZ("a5", "ada", "bob", 900, "expired", h)})
	ta := login(t, "ada@example.com")
	if held(t, ta) != 900 || avail(t, ta) != 100 {
		t.Fatalf("held=%d avail=%d", held(t, ta), avail(t, ta))
	}
}

func TestR170_LazyExpiry(t *testing.T) {
	e := setupAZ(t, 1, nil, azUsers...)
	a := mustAuthorize(t, e.tok["ada"], "bob", 3000, nil)
	id := aid(t, a)
	if held(t, e.tok["ada"]) != 3000 || avail(t, e.tok["ada"]) != 7000 {
		t.Fatal("hold not placed")
	}
	time.Sleep(2500 * time.Millisecond)
	m := meObj(t, e.tok["ada"])
	if num(t, m, "held") != 0 || num(t, m, "available") != 10000 || num(t, m, "total") != 10000 {
		t.Fatalf("expiry not reflected: %v", m)
	}
	got := getAuthz(t, e.tok["ada"], id)
	if got["status"] != "expired" || num(t, got, "remaining_amount") != 0 {
		t.Fatalf("expired auth: %v", got)
	}
	if o, _ := listAuthz(t, e.tok["ada"], "?status=open"); len(o) != 0 {
		t.Fatal("expired authorization still matches open")
	}
	if o, _ := listAuthz(t, e.tok["bob"], "?status=expired&direction=incoming"); len(o) != 1 {
		t.Fatal("expired filter for receiver")
	}
	expectCaptureClosed(t, capture(t, e.tok["bob"], id, map[string]any{}))
	expectErr(t, voidA(t, e.tok["ada"], id), 409, "authorization_not_open")
	// the released funds can be spent
	mustPay(t, e.tok["ada"], "cy", 10000, nil)
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR170_ExpiryOnWriteWithoutPriorRead(t *testing.T) {
	e := setupAZ(t, 1, nil, azUsers...)
	mustAuthorize(t, e.tok["ada"], "bob", 10000, nil)
	expectErr(t, sendPay(t, e.tok["ada"], "cy", 1, nil), 409, "insufficient_funds")
	time.Sleep(2500 * time.Millisecond)
	// first call after the deadline is a write: it must see the released funds
	mustPay(t, e.tok["ada"], "cy", 10000, nil)
}

func TestR171_ExpiryOfPartiallyCapturedKeepsRecords(t *testing.T) {
	e := setupAZ(t, 3, nil, azUsers...)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	p := mustCapture(t, e.tok["bob"], id, map[string]any{"amount": 700, "final": false})
	time.Sleep(3500 * time.Millisecond)
	ada := meObj(t, e.tok["ada"])
	if num(t, ada, "held") != 0 || num(t, ada, "total") != 9300 || num(t, ada, "available") != 9300 {
		t.Fatalf("only the remainder is released: %v", ada)
	}
	if total(t, e.tok["bob"]) != 3200 {
		t.Fatal("captured money must stay with the receiver")
	}
	got := getAuthz(t, e.tok["bob"], id)
	ids := ints(got["payment_ids"])
	if got["status"] != "expired" || num(t, got, "captured_amount") != 700 || len(ids) != 1 || ids[0] != p["payment_id"] || num(t, got, "remaining_amount") != 0 {
		t.Fatalf("records lost: %v", got)
	}
	a2, _ := activity(t, e.tok["bob"], "")
	if len(a2) != 1 {
		t.Fatal("capture payment must remain in the feed")
	}
}

func TestR172_AvailableGovernsInsufficientFunds(t *testing.T) {
	e := setupAZ(t, nil, nil, fu{"ada", 1000}, fu{"bob", 0}, fu{"cy", 0}, fu{"op", 0})
	mustAuthorize(t, e.tok["ada"], "bob", 600, nil) // available 400
	// payments
	expectErr(t, sendPay(t, e.tok["ada"], "cy", 401, nil), 409, "insufficient_funds")
	// request pay
	rq := rid(t, mustRequest(t, e.tok["cy"], "ada", 401))
	expectErr(t, postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{}), 409, "insufficient_funds")
	rq2 := rid(t, mustRequest(t, e.tok["cy"], "ada", 400))
	expect(t, postK(t, "/requests/"+rq2+"/pay", e.tok["ada"], map[string]any{}), 201)
	// authorizations
	expectErr(t, authorize(t, e.tok["ada"], "cy", 1, nil), 409, "insufficient_funds")
	if total(t, e.tok["ada"]) != 600 || held(t, e.tok["ada"]) != 600 || avail(t, e.tok["ada"]) != 0 {
		t.Fatalf("state: %v", meObj(t, e.tok["ada"]))
	}
	// settlements: net debit against available
	e = setupAZ(t, nil, nil, fu{"ada", 1000}, fu{"bob", 0}, fu{"cy", 0}, fu{"op", 0})
	mustAuthorize(t, e.tok["ada"], "bob", 600, nil)
	expectErr(t, settle(t, e.tok["op"], batch(tr("ada", "cy", 401))), 409, "insufficient_funds")
	expect(t, settle(t, e.tok["op"], batch(tr("ada", "cy", 400))), 201)
	// credits in the same batch count: available 0 + 100 incoming - 100 outgoing
	expect(t, settle(t, e.tok["op"], batch(tr("cy", "ada", 100), tr("ada", "bob", 100))), 201)
	expectErr(t, settle(t, e.tok["op"], batch(tr("cy", "ada", 100), tr("ada", "bob", 101))), 409, "insufficient_funds")
	// the hold itself survived: bob can still capture 600
	mustCapture(t, e.tok["bob"], func() string {
		items, _ := listAuthz(t, e.tok["bob"], "?status=open")
		return aid(t, items[0])
	}(), map[string]any{})
}

func TestR164_CaptureMaySpendHeldMoney(t *testing.T) {
	e := setupAZ(t, nil, nil, fu{"ada", 1000}, fu{"bob", 0})
	a := mustAuthorize(t, e.tok["ada"], "bob", 1000, nil) // available 0
	expectErr(t, sendPay(t, e.tok["ada"], "bob", 1, nil), 409, "insufficient_funds")
	p := mustCapture(t, e.tok["bob"], aid(t, a), map[string]any{})
	if num(t, p, "amount") != 1000 || total(t, e.tok["ada"]) != 0 || total(t, e.tok["bob"]) != 1000 || held(t, e.tok["ada"]) != 0 {
		t.Fatal("capture of fully held funds")
	}
}

func TestR174_PaymentsLeaveNoHold(t *testing.T) {
	e := azSetup(t)
	mustPay(t, e.tok["ada"], "bob", 100, nil)
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 50))
	expect(t, postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{}), 201)
	expect(t, postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 30, "participant_handles": []string{"bob", "ada"}}), 201)
	if held(t, e.tok["ada"]) != 0 {
		t.Fatal("ordinary operations left a hold")
	}
	if o, _ := listAuthz(t, e.tok["ada"], ""); len(o) != 0 {
		t.Fatal("ordinary operations created authorizations")
	}
}

func TestR175_AuthorizationIdOnPayments(t *testing.T) {
	e := azSetup(t)
	plain := mustPay(t, e.tok["ada"], "bob", 10, nil)
	if !isNull(plain, "authorization_id") {
		t.Fatalf("payment authorization_id must be present and null: %v", plain)
	}
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	pr := postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{})
	expect(t, pr, 201)
	if !isNull(pr.obj(t), "authorization_id") || pr.obj(t)["request_id"] != rq {
		t.Fatalf("request payment: %s", pr)
	}
	st := settle(t, e.tok["op"], batch(tr("ada", "bob", 1)))
	expect(t, st, 201)
	if !isNull(st.obj(t)["payments"].([]any)[0].(map[string]any), "authorization_id") {
		t.Fatal("settlement member authorization_id")
	}
	a := mustAuthorize(t, e.tok["ada"], "bob", 500, map[string]any{"note": "deposit ✓", "visibility": "private"})
	p := mustCapture(t, e.tok["bob"], aid(t, a), map[string]any{"amount": 300})
	if p["authorization_id"] != aid(t, a) || !isNull(p, "request_id") || !isNull(p, "settlement_id") {
		t.Fatalf("capture payment: %v", p)
	}
	if num(t, p, "amount") != 300 || p["note"] != "deposit ✓" || p["visibility"] != "private" || p["from_handle"] != "ada" || p["to_handle"] != "bob" ||
		p["currency"] != "EUR" || p["from_user_id"] != "u_ada" || p["to_user_id"] != "u_bob" {
		t.Fatalf("capture payment fields: %v", p)
	}
	checkTS(t, str(t, p, "created_at"))
	// feed rule: private capture is visible to its parties only
	for h, want := range map[string]bool{"ada": true, "bob": true, "cy": false, "dee": false, "op": false} {
		items, _ := activity(t, e.tok[h], "?limit=200")
		got := idSet(t, items, "payment_id")[str(t, p, "payment_id")]
		if got != want {
			t.Fatalf("%s sees capture=%v want %v", h, got, want)
		}
		for _, it := range items {
			if it["payment_id"] == p["payment_id"] && it["authorization_id"] != aid(t, a) {
				t.Fatalf("feed item lacks authorization_id: %v", it)
			}
		}
	}
	// public authorization -> public capture visible to all
	a2 := mustAuthorize(t, e.tok["ada"], "bob", 50, nil)
	p2 := mustCapture(t, e.tok["bob"], aid(t, a2), map[string]any{})
	items, _ := activity(t, e.tok["dee"], "?limit=200")
	if !idSet(t, items, "payment_id")[str(t, p2, "payment_id")] {
		t.Fatal("public capture invisible to third party")
	}
}

func TestR177_AuthorizationShape(t *testing.T) {
	e := azSetup(t)
	r := postK(t, "/authorizations", e.tok["ada"], map[string]any{"to_handle": "bob", "amount": 2000, "note": "deposit", "visibility": "private", "bogus": 1})
	expect(t, r, 201)
	a := r.obj(t)
	want := map[string]any{"from_user_id": "u_ada", "from_handle": "ada", "to_user_id": "u_bob", "to_handle": "bob", "currency": "EUR",
		"note": "deposit", "visibility": "private", "status": "open"}
	for k, v := range want {
		if a[k] != v {
			t.Fatalf("%s = %v want %v (%v)", k, a[k], v, a)
		}
	}
	if str(t, a, "authorization_id") == "" || len(aid(t, a)) > 64 {
		t.Fatal("id")
	}
	if num(t, a, "amount") != 2000 || num(t, a, "captured_amount") != 0 || num(t, a, "remaining_amount") != 2000 {
		t.Fatalf("amounts: %v", a)
	}
	if !isNull(a, "payment_id") {
		t.Fatalf("payment_id must be null: %v", a)
	}
	if ids, ok := a["payment_ids"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("payment_ids must be an empty array: %v", a["payment_ids"])
	}
	checkTS(t, str(t, a, "created_at"))
	checkTS(t, str(t, a, "expires_at"))
	// defaults
	d := mustAuthorize(t, e.tok["ada"], "bob", 5, nil)
	if d["note"] != "" || d["visibility"] != "public" {
		t.Fatalf("defaults: %v", d)
	}
	if aid(t, d) == aid(t, a) {
		t.Fatal("ids not unique")
	}
	// GET shows the same object
	got := getAuthz(t, e.tok["bob"], aid(t, a))
	requireSameJSON(t, r, resp{Body: mustJSON(got)})
}

func TestR178_AuthorizeValidation(t *testing.T) {
	e := azSetup(t)
	tk := e.tok["ada"]
	for _, a := range []any{0, -1, 1000000001, 1.5, "100", true, nil} {
		expectErr(t, authorize(t, tk, "bob", a, nil), 422, "validation_failed")
	}
	expectErr(t, postK(t, "/authorizations", tk, map[string]any{"to_handle": "bob"}), 422, "validation_failed")
	expectErr(t, authorize(t, tk, "ada", 10, nil), 422, "self_payment")
	expectErr(t, authorize(t, tk, "ghost", 10, nil), 404, "not_found")
	expectErr(t, authorize(t, tk, "bob", 10, map[string]any{"note": strings.Repeat("a", 201)}), 422, "validation_failed")
	expectErr(t, authorize(t, tk, "bob", 10, map[string]any{"note": nil}), 422, "validation_failed")
	expectErr(t, authorize(t, tk, "bob", 10, map[string]any{"visibility": "friends"}), 422, "validation_failed")
	expectErr(t, authorize(t, tk, "dee", 10001, nil), 409, "insufficient_funds")
	expectErr(t, postK(t, "/authorizations", tk, map[string]any{"to_handle": 5, "amount": 1}), 400, "malformed_request")
	expectErr(t, postK(t, "/authorizations", tk, `{`), 400, "malformed_request")
	expectErr(t, post(t, "/authorizations", "", newKey(), map[string]any{"to_handle": "bob", "amount": 1}), 401, "unauthenticated")
	expect(t, authorize(t, tk, "bob", 10, map[string]any{"note": strings.Repeat("🎉", 200)}), 201)
	expect(t, authorize(t, tk, "bob", 9990, nil), 201) // exactly the available remainder
	if held(t, tk) != 10000 {
		t.Fatalf("held %d", held(t, tk))
	}
	expectErr(t, authorize(t, tk, "bob", 1, nil), 409, "insufficient_funds")
	if o, _ := listAuthz(t, e.tok["cy"], ""); len(o) != 0 {
		t.Fatal("failed authorizations created records")
	}
}

func TestR179_AuthorizationNeverInFeed(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	for _, h := range []string{"ada", "bob", "cy"} {
		if items, _ := activity(t, e.tok[h], ""); len(items) != 0 {
			t.Fatalf("%s sees the hold in the feed", h)
		}
	}
	mustCapture(t, e.tok["bob"], aid(t, a), map[string]any{"amount": 40})
	for _, h := range []string{"ada", "bob", "cy"} {
		if items, _ := activity(t, e.tok[h], ""); len(items) != 1 {
			t.Fatalf("%s sees %d items; only the capture payment belongs in the feed", h, len(items))
		}
	}
}

func TestR180_CaptureBodyAndPermissions(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	for _, b := range []any{
		map[string]any{"amount": 0}, map[string]any{"amount": -5}, map[string]any{"amount": 1.5},
		map[string]any{"amount": "100"}, map[string]any{"amount": true}, map[string]any{"amount": nil},
	} {
		expectErr(t, capture(t, e.tok["bob"], id, b), 422, "validation_failed")
	}
	for _, f := range []any{"false", "true", 1, 0, []any{}, map[string]any{}} {
		expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 10, "final": f}), 400, "malformed_request")
	}
	expectErr(t, capture(t, e.tok["bob"], id, `{`), 400, "malformed_request")
	expectErr(t, capture(t, e.tok["bob"], id, `[]`), 400, "malformed_request")
	expectErr(t, capture(t, e.tok["ada"], id, map[string]any{}), 403, "forbidden") // the payer cannot capture
	expectErr(t, capture(t, e.tok["cy"], id, map[string]any{}), 403, "forbidden")  // third party
	expectErr(t, capture(t, e.tok["op"], id, map[string]any{}), 403, "forbidden")
	expectErr(t, capture(t, e.tok["bob"], "nope", map[string]any{}), 404, "not_found")
	expectErr(t, post(t, "/authorizations/"+id+"/capture", "", newKey(), map[string]any{}), 401, "unauthenticated")
	expectErr(t, call(t, "POST", "/authorizations/"+id+"/capture", e.tok["bob"], nil, map[string]any{}), 400, "missing_idempotency_key")
	if total(t, e.tok["bob"]) != 2500 || held(t, e.tok["ada"]) != 2000 {
		t.Fatal("rejected captures changed state")
	}
	// unknown fields are ignored; an omitted body object {} means full remainder
	p := mustCapture(t, e.tok["bob"], id, map[string]any{"zzz": 1})
	if num(t, p, "amount") != 2000 {
		t.Fatalf("default amount: %v", p)
	}
	// integral spellings of the amount
	a2 := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	r := capture(t, e.tok["bob"], aid(t, a2), `{"amount":1e3,"final":false}`)
	expect(t, r, 201)
	if num(t, r.obj(t), "amount") != 1000 {
		t.Fatalf("1e3: %s", r)
	}
	r = capture(t, e.tok["bob"], aid(t, a2), `{"amount":500.0,"final":false}`)
	expect(t, r, 201)
}

func TestR181_FinalCaptureReleasesRemainder(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	if avail(t, e.tok["ada"]) != 8000 {
		t.Fatal("hold")
	}
	p := mustCapture(t, e.tok["bob"], id, map[string]any{"amount": 1500})
	ada := meObj(t, e.tok["ada"])
	if num(t, ada, "total") != 8500 || num(t, ada, "held") != 0 || num(t, ada, "available") != 8500 {
		t.Fatalf("remainder must be released in the same step: %v", ada)
	}
	if total(t, e.tok["bob"]) != 4000 {
		t.Fatal("receiver credit")
	}
	got := getAuthz(t, e.tok["ada"], id)
	ids := ints(got["payment_ids"])
	if got["status"] != "captured" || num(t, got, "captured_amount") != 1500 || got["payment_id"] != p["payment_id"] ||
		num(t, got, "remaining_amount") != 0 || len(ids) != 1 || ids[0] != p["payment_id"] {
		t.Fatalf("after final capture: %v", got)
	}
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 1}), 409, "authorization_not_open")
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{}), 409, "authorization_not_open")
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 1, "final": false}), 409, "authorization_not_open")
	if total(t, e.tok["bob"]) != 4000 {
		t.Fatal("closed hold moved money again")
	}
	// full default capture
	a2 := mustAuthorize(t, e.tok["ada"], "cy", 700, nil)
	p2 := mustCapture(t, e.tok["cy"], aid(t, a2), map[string]any{})
	if num(t, p2, "amount") != 700 || held(t, e.tok["ada"]) != 0 {
		t.Fatal("default capture amount")
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR182_PartialCaptures(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	p1 := mustCapture(t, e.tok["bob"], id, map[string]any{"amount": 700, "final": false})
	g := getAuthz(t, e.tok["bob"], id)
	if g["status"] != "open" || num(t, g, "captured_amount") != 700 || num(t, g, "remaining_amount") != 1300 || g["payment_id"] != p1["payment_id"] || len(ints(g["payment_ids"])) != 1 {
		t.Fatalf("after first partial: %v", g)
	}
	ada := meObj(t, e.tok["ada"])
	if num(t, ada, "held") != 1300 || num(t, ada, "total") != 9300 || num(t, ada, "available") != 8000 {
		t.Fatalf("remainder must stay held: %v", ada)
	}
	p2 := mustCapture(t, e.tok["bob"], id, map[string]any{"amount": 600, "final": false})
	// exceeds the *remaining* amount, not the original
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 701, "final": false}), 422, "capture_exceeds_authorization")
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 701}), 422, "capture_exceeds_authorization")
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 2000}), 422, "capture_exceeds_authorization")
	g = getAuthz(t, e.tok["bob"], id)
	if num(t, g, "captured_amount") != 1300 || num(t, g, "remaining_amount") != 700 || g["status"] != "open" || g["payment_id"] != p2["payment_id"] {
		t.Fatalf("after second partial: %v", g)
	}
	// the default amount is the remainder; with final:false the entire remainder closes it
	p3 := mustCapture(t, e.tok["bob"], id, map[string]any{"final": false})
	if num(t, p3, "amount") != 700 {
		t.Fatalf("default remainder: %v", p3)
	}
	g = getAuthz(t, e.tok["bob"], id)
	ids := ints(g["payment_ids"])
	if g["status"] != "captured" || num(t, g, "captured_amount") != 2000 || num(t, g, "remaining_amount") != 0 ||
		g["payment_id"] != p3["payment_id"] || len(ids) != 3 || ids[0] != p1["payment_id"] || ids[1] != p2["payment_id"] || ids[2] != p3["payment_id"] {
		t.Fatalf("after closing: %v", g)
	}
	if total(t, e.tok["ada"]) != 8000 || total(t, e.tok["bob"]) != 4500 || held(t, e.tok["ada"]) != 0 {
		t.Fatal("balances after three captures")
	}
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 1, "final": false}), 409, "authorization_not_open")
	// an explicit amount equal to the remainder with final:false also closes it
	b := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	mustCapture(t, e.tok["bob"], aid(t, b), map[string]any{"amount": 40, "final": false})
	mustCapture(t, e.tok["bob"], aid(t, b), map[string]any{"amount": 60, "final": false})
	if getAuthz(t, e.tok["ada"], aid(t, b))["status"] != "captured" {
		t.Fatal("capturing the whole remainder must close the authorization")
	}
	// a final capture of less than the remainder releases the rest
	c := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	mustCapture(t, e.tok["bob"], aid(t, c), map[string]any{"amount": 30, "final": false})
	held0 := held(t, e.tok["ada"])
	mustCapture(t, e.tok["bob"], aid(t, c), map[string]any{"amount": 10, "final": true})
	g = getAuthz(t, e.tok["ada"], aid(t, c))
	if g["status"] != "captured" || num(t, g, "captured_amount") != 40 || held(t, e.tok["ada"]) != held0-70 {
		t.Fatalf("final capture must release the remainder: %v held %d->%d", g, held0, held(t, e.tok["ada"]))
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR183_CaptureErrorPrecedence(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 1000, nil)
	id := aid(t, a)
	mustCapture(t, e.tok["bob"], id, map[string]any{"amount": 400})
	// closed: an over-large amount reports the state, not the amount
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 99999}), 409, "authorization_not_open")
	// not the receiver outranks state and amount
	expectErr(t, capture(t, e.tok["ada"], id, map[string]any{"amount": 1}), 403, "forbidden")
	expectErr(t, capture(t, e.tok["cy"], id, map[string]any{"amount": 99999}), 403, "forbidden")
	expectErr(t, capture(t, e.tok["cy"], "ghost", map[string]any{}), 404, "not_found")
	// open: invalid amount kinds
	b := mustAuthorize(t, e.tok["ada"], "bob", 1000, nil)
	expectErr(t, capture(t, e.tok["bob"], aid(t, b), map[string]any{"amount": 1001}), 422, "capture_exceeds_authorization")
	expectErr(t, capture(t, e.tok["bob"], aid(t, b), map[string]any{"amount": 0}), 422, "validation_failed")
	expectErr(t, capture(t, e.tok["bob"], aid(t, b), map[string]any{"amount": -1}), 422, "validation_failed")
	// exactly the remainder is fine
	expect(t, capture(t, e.tok["bob"], aid(t, b), map[string]any{"amount": 1000}), 201)
	// voided, then capture
	c := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	expect(t, voidA(t, e.tok["ada"], aid(t, c)), 200)
	expectErr(t, capture(t, e.tok["bob"], aid(t, c), map[string]any{}), 409, "authorization_not_open")
	// expired by clock
	reset(t, fixtureAZ(1, nil, azUsers...))
	tk := map[string]string{"ada": login(t, "ada@example.com"), "bob": login(t, "bob@example.com")}
	d := mustAuthorize(t, tk["ada"], "bob", 100, nil)
	time.Sleep(2500 * time.Millisecond)
	expectCaptureClosed(t, capture(t, tk["bob"], aid(t, d), map[string]any{}))
	expectCaptureClosed(t, capture(t, tk["bob"], aid(t, d), map[string]any{"amount": 99999}))
	expectErr(t, capture(t, tk["ada"], aid(t, d), map[string]any{}), 403, "forbidden")
}

func TestR184_CaptureReplayBodies(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	k := newKey()
	path := "/authorizations/" + id + "/capture"
	first := post(t, path, e.tok["bob"], k, map[string]any{})
	expect(t, first, 201)
	// {} and {"amount":2000} are different JSON values even though they mean the same capture
	expectErr(t, post(t, path, e.tok["bob"], k, map[string]any{"amount": 2000}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, path, e.tok["bob"], k, map[string]any{"final": true}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, path, e.tok["bob"], k, map[string]any{"amount": 1}), 409, "idempotency_key_reuse")
	// replay after the authorization closed: 200 with the original payment
	for i := 0; i < 3; i++ {
		rep := post(t, path, e.tok["bob"], k, `{ }`)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
	}
	if total(t, e.tok["bob"]) != 4500 {
		t.Fatal("replay moved money")
	}
	// the other way around
	b := mustAuthorize(t, e.tok["ada"], "bob", 500, nil)
	pb := "/authorizations/" + aid(t, b) + "/capture"
	k2 := newKey()
	expect(t, post(t, pb, e.tok["bob"], k2, map[string]any{"amount": 500}), 201)
	expectErr(t, post(t, pb, e.tok["bob"], k2, map[string]any{}), 409, "idempotency_key_reuse")
	expect(t, post(t, pb, e.tok["bob"], k2, `{"amount": 5e2}`), 200)
	// partial-capture replays return their own original bodies, in any order
	c := mustAuthorize(t, e.tok["ada"], "bob", 500, nil)
	pc := "/authorizations/" + aid(t, c) + "/capture"
	ka, kb := newKey(), newKey()
	ra := post(t, pc, e.tok["bob"], ka, map[string]any{"amount": 100, "final": false})
	rb := post(t, pc, e.tok["bob"], kb, map[string]any{"amount": 200, "final": false})
	expect(t, ra, 201)
	expect(t, rb, 201)
	repa := post(t, pc, e.tok["bob"], ka, map[string]any{"amount": 100, "final": false})
	repb := post(t, pc, e.tok["bob"], kb, map[string]any{"amount": 200, "final": false})
	expect(t, repa, 200)
	expect(t, repb, 200)
	requireSameJSON(t, ra, repa)
	requireSameJSON(t, rb, repb)
	if total(t, e.tok["bob"]) != 2500+2000+500+300 {
		t.Fatal("balances after replays")
	}
	// key reuse after a 4xx is a first use; a claimed key is resolved before validation (R71)
	k3 := newKey()
	d := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	pd := "/authorizations/" + aid(t, d) + "/capture"
	expectErr(t, post(t, pd, e.tok["bob"], k3, map[string]any{"amount": 101}), 422, "capture_exceeds_authorization")
	expect(t, post(t, pd, e.tok["bob"], k3, map[string]any{"amount": 100}), 201)
	expectErr(t, post(t, pd, e.tok["bob"], k3, map[string]any{"amount": -1}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, pd, e.tok["bob"], k3, map[string]any{"amount": 101}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, pd, e.tok["bob"], k3, map[string]any{"amount": 5, "final": "x"}), 409, "idempotency_key_reuse")
}

func TestR185_Void(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	expectErr(t, voidA(t, e.tok["bob"], id), 403, "forbidden") // receiver may not void
	expectErr(t, voidA(t, e.tok["cy"], id), 403, "forbidden")
	expectErr(t, voidA(t, e.tok["op"], id), 403, "forbidden")
	expectErr(t, voidA(t, e.tok["ada"], "nope"), 404, "not_found")
	expectErr(t, post(t, "/authorizations/"+id+"/void", "", "", nil), 401, "unauthenticated")
	if held(t, e.tok["ada"]) != 2000 {
		t.Fatal("rejected voids released the hold")
	}
	r := voidA(t, e.tok["ada"], id)
	expect(t, r, 200)
	m := r.obj(t)
	if m["status"] != "voided" || aid(t, m) != id || num(t, m, "remaining_amount") != 0 || num(t, m, "amount") != 2000 || num(t, m, "captured_amount") != 0 {
		t.Fatalf("void: %v", m)
	}
	if held(t, e.tok["ada"]) != 0 || avail(t, e.tok["ada"]) != 10000 || total(t, e.tok["ada"]) != 10000 {
		t.Fatal("hold not released")
	}
	// voiding again is 200 with the current state
	r2 := voidA(t, e.tok["ada"], id)
	expect(t, r2, 200)
	if r2.obj(t)["status"] != "voided" {
		t.Fatalf("second void: %s", r2)
	}
	expect(t, call(t, "POST", "/authorizations/"+id+"/void", e.tok["ada"], nil, map[string]any{}), 200)
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{}), 409, "authorization_not_open")
	// captured -> not_open
	b := mustAuthorize(t, e.tok["ada"], "bob", 100, nil)
	mustCapture(t, e.tok["bob"], aid(t, b), map[string]any{})
	expectErr(t, voidA(t, e.tok["ada"], aid(t, b)), 409, "authorization_not_open")
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR186_VoidReleasesOnlyRemainder(t *testing.T) {
	e := azSetup(t)
	a := mustAuthorize(t, e.tok["ada"], "bob", 2000, nil)
	id := aid(t, a)
	p := mustCapture(t, e.tok["bob"], id, map[string]any{"amount": 700, "final": false})
	ada := meObj(t, e.tok["ada"])
	if num(t, ada, "held") != 1300 || num(t, ada, "total") != 9300 {
		t.Fatalf("%v", ada)
	}
	r := voidA(t, e.tok["ada"], id)
	expect(t, r, 200)
	m := r.obj(t)
	ids := ints(m["payment_ids"])
	if m["status"] != "voided" || num(t, m, "captured_amount") != 700 || num(t, m, "remaining_amount") != 0 || len(ids) != 1 || ids[0] != p["payment_id"] || m["payment_id"] != p["payment_id"] {
		t.Fatalf("void of partially captured: %v", m)
	}
	ada = meObj(t, e.tok["ada"])
	if num(t, ada, "held") != 0 || num(t, ada, "total") != 9300 || num(t, ada, "available") != 9300 {
		t.Fatalf("only the remainder is released; the captured part stays moved: %v", ada)
	}
	if total(t, e.tok["bob"]) != 3200 {
		t.Fatal("captured money must stay with the receiver")
	}
	expectErr(t, capture(t, e.tok["bob"], id, map[string]any{"amount": 1}), 409, "authorization_not_open")
	if a, _ := activity(t, e.tok["bob"], ""); len(a) != 1 {
		t.Fatal("capture payment must remain")
	}
}

func TestR187_R188_ListScopeOrderAndFilters(t *testing.T) {
	e := azSetup(t)
	ids := []string{}
	for i := 0; i < 3; i++ {
		ids = append(ids, aid(t, mustAuthorize(t, e.tok["ada"], "bob", int64(10+i), nil)))
		tick()
	}
	other := aid(t, mustAuthorize(t, e.tok["cy"], "dee", 5, nil))
	for _, h := range []string{"ada", "bob"} {
		out, more := listAuthz(t, e.tok[h], "")
		if more || len(out) != 3 {
			t.Fatalf("%s sees %d more=%v", h, len(out), more)
		}
		for i, it := range out {
			if it["authorization_id"] != ids[2-i] {
				t.Fatalf("%s order at %d: %v", h, i, it["authorization_id"])
			}
		}
	}
	for _, h := range []string{"op", "ada", "bob"} {
		out, _ := listAuthz(t, e.tok[h], "")
		if idSet(t, out, "authorization_id")[other] {
			t.Fatalf("%s sees another pair's authorization", h)
		}
	}
	if out, _ := listAuthz(t, e.tok["op"], ""); len(out) != 0 {
		t.Fatal("op")
	}
	// direction
	if out, _ := listAuthz(t, e.tok["ada"], "?direction=outgoing"); len(out) != 3 {
		t.Fatal("ada outgoing")
	}
	if out, _ := listAuthz(t, e.tok["ada"], "?direction=incoming"); len(out) != 0 {
		t.Fatal("ada incoming")
	}
	if out, _ := listAuthz(t, e.tok["bob"], "?direction=incoming"); len(out) != 3 {
		t.Fatal("bob incoming")
	}
	if out, _ := listAuthz(t, e.tok["bob"], "?direction=outgoing"); len(out) != 0 {
		t.Fatal("bob outgoing")
	}
	// status
	expect(t, voidA(t, e.tok["ada"], ids[0]), 200)
	mustCapture(t, e.tok["bob"], ids[1], map[string]any{})
	for st, id := range map[string]string{"voided": ids[0], "captured": ids[1], "open": ids[2]} {
		out, _ := listAuthz(t, e.tok["ada"], "?status="+st)
		if len(out) != 1 || out[0]["authorization_id"] != id || out[0]["status"] != st {
			t.Fatalf("status=%s: %v", st, out)
		}
	}
	if out, _ := listAuthz(t, e.tok["ada"], "?status=expired"); len(out) != 0 {
		t.Fatal("expired")
	}
	if out, _ := listAuthz(t, e.tok["ada"], "?status=open&direction=incoming"); len(out) != 0 {
		t.Fatal("combo")
	}
	for _, q := range []string{"direction=sideways", "direction=", "direction=INCOMING", "status=done", "status=", "status=OPEN",
		"limit=0", "limit=201", "limit=abc", "limit=1e2", "limit=4.0", "limit=+4", "limit=", "offset=-1", "offset=1e1", "offset=+1", "offset=1.0", "offset="} {
		expectErr(t, get(t, "/authorizations?"+q, e.tok["ada"]), 422, "validation_failed")
	}
	expectErr(t, get(t, "/authorizations", ""), 401, "unauthenticated")
	// unknown params ignored
	expect(t, get(t, "/authorizations?zzz=1", e.tok["ada"]), 200)
}

func TestR188_Pagination(t *testing.T) {
	e := setupAZ(t, nil, nil, fu{"ada", 100000}, fu{"bob", 0})
	all := map[string]bool{}
	for i := 0; i < 5; i++ {
		all[aid(t, mustAuthorize(t, e.tok["ada"], "bob", 1, nil))] = true
	}
	a, more := listAuthz(t, e.tok["bob"], "?limit=2&offset=0")
	b, more2 := listAuthz(t, e.tok["bob"], "?limit=2&offset=2")
	c, more3 := listAuthz(t, e.tok["bob"], "?limit=2&offset=4")
	if len(a) != 2 || !more || len(b) != 2 || !more2 || len(c) != 1 || more3 {
		t.Fatalf("paging %d/%v %d/%v %d/%v", len(a), more, len(b), more2, len(c), more3)
	}
	seen := map[string]bool{}
	for _, pg := range [][]map[string]any{a, b, c} {
		for _, it := range pg {
			seen[aid(t, it)] = true
		}
	}
	if len(seen) != 5 {
		t.Fatal("coverage")
	}
	if out, m := listAuthz(t, e.tok["bob"], "?limit=5"); len(out) != 5 || m {
		t.Fatal("exact fit")
	}
	if out, m := listAuthz(t, e.tok["bob"], "?offset=5"); len(out) != 0 || m {
		t.Fatal("offset at end")
	}
	if out, _ := listAuthz(t, e.tok["bob"], ""); len(out) != 5 {
		t.Fatal("default")
	}
}

func TestR164_HeldFundsCannotFundNewAuthorizations(t *testing.T) {
	e := setupAZ(t, nil, nil, fu{"ada", 1000}, fu{"bob", 0})
	mustAuthorize(t, e.tok["ada"], "bob", 1000, nil)
	expectErr(t, authorize(t, e.tok["ada"], "bob", 1, nil), 409, "insufficient_funds")
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 1))
	expectErr(t, postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{}), 409, "insufficient_funds")
	if avail(t, e.tok["ada"]) != 0 || held(t, e.tok["ada"]) != 1000 {
		t.Fatal("state")
	}
	// void frees the money again
	items, _ := listAuthz(t, e.tok["ada"], "")
	expect(t, voidA(t, e.tok["ada"], aid(t, items[0])), 200)
	expect(t, postK(t, "/requests/"+rq+"/pay", e.tok["ada"], map[string]any{}), 201)
}
