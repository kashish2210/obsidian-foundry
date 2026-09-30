package acceptance

import (
	"strings"
	"testing"
)

func TestR72_Me(t *testing.T) {
	e := setup(t)
	me := get(t, "/me", e.tok["ada"]).obj(t)
	if me["user_id"] != "u_ada" || me["display_name"] != "Ada" || me["handle"] != "ada" ||
		num(t, me, "balance") != 10000 || me["currency"] != "EUR" || num(t, me, "minor_units") != 2 {
		t.Fatalf("/me: %v", me)
	}
}

func TestR73_PaymentShapeAndDefaults(t *testing.T) {
	e := setup(t)
	r := postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": "bob", "amount": 1500})
	expect(t, r, 201)
	p := r.obj(t)
	want := map[string]any{"from_user_id": "u_ada", "from_handle": "ada", "to_user_id": "u_bob", "to_handle": "bob",
		"currency": "EUR", "note": "", "visibility": "public"}
	for k, v := range want {
		if p[k] != v {
			t.Fatalf("%s = %v want %v in %v", k, p[k], v, p)
		}
	}
	if num(t, p, "amount") != 1500 || str(t, p, "payment_id") == "" {
		t.Fatalf("bad payment %v", p)
	}
	if !isNull(p, "request_id") {
		t.Fatalf("request_id must be present and null: %v", p)
	}
	if !isNull(p, "settlement_id") {
		t.Fatalf("settlement_id must be present and null: %v", p)
	}
	checkTS(t, str(t, p, "created_at"))
	if bal(t, e.tok["ada"]) != 8500 || bal(t, e.tok["bob"]) != 4000 {
		t.Fatal("balances not moved")
	}
	// explicit fields
	p2 := mustPay(t, e.tok["ada"], "cy", 5, map[string]any{"note": "dinner", "visibility": "private"})
	if p2["note"] != "dinner" || p2["visibility"] != "private" {
		t.Fatalf("explicit fields lost %v", p2)
	}
	if str(t, p2, "payment_id") == str(t, p, "payment_id") {
		t.Fatal("payment ids not unique")
	}
}

func TestR74_InsufficientFunds(t *testing.T) {
	e := setup(t)
	expectErr(t, sendPay(t, e.tok["bob"], "ada", 2501, nil), 409, "insufficient_funds")
	if bal(t, e.tok["bob"]) != 2500 || bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("failed payment changed balances")
	}
	mustPay(t, e.tok["bob"], "ada", 2500, nil) // exactly the balance
	if bal(t, e.tok["bob"]) != 0 {
		t.Fatal("bob should be at 0")
	}
	expectErr(t, sendPay(t, e.tok["bob"], "ada", 1, nil), 409, "insufficient_funds")
	expectErr(t, sendPay(t, e.tok["dee"], "ada", 1, nil), 409, "insufficient_funds")
}

func TestR75_PaymentAmountValidation(t *testing.T) {
	e := setup(t)
	for _, a := range []any{0, -1, 1000000001, 1.5, "100", true, nil} {
		expectErr(t, sendPay(t, e.tok["ada"], "bob", a, nil), 422, "validation_failed")
	}
	expectErr(t, postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": "bob"}), 422, "validation_failed")
	if e.sum(t) != e.total || bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("invalid payments changed balances")
	}
}

func TestR76_SelfPayment(t *testing.T) {
	e := setup(t)
	expectErr(t, sendPay(t, e.tok["ada"], "ada", 10, nil), 422, "self_payment")
	if bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("balance changed")
	}
	a, _ := activity(t, e.tok["ada"], "")
	if len(a) != 0 {
		t.Fatal("self payment left a trace")
	}
}

func TestR77_NoteLength(t *testing.T) {
	e := setup(t)
	mustPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": strings.Repeat("a", 200)})
	expectErr(t, sendPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": strings.Repeat("a", 201)}), 422, "validation_failed")
	// characters, not bytes: 200 emoji are 800 bytes but 200 characters
	mustPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": strings.Repeat("🎉", 200)})
	expectErr(t, sendPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": strings.Repeat("🎉", 201)}), 422, "validation_failed")
	// 2-byte characters
	mustPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": strings.Repeat("é", 200)})
	// same rule on requests and splits
	expect(t, postK(t, "/requests", e.tok["ada"], map[string]any{"payer_handle": "bob", "amount": 1, "note": strings.Repeat("🎉", 200)}), 201)
	expectErr(t, postK(t, "/requests", e.tok["ada"], map[string]any{"payer_handle": "bob", "amount": 1, "note": strings.Repeat("a", 201)}), 422, "validation_failed")
	expect(t, postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 1, "participant_handles": []string{"bob"}, "note": strings.Repeat("a", 200)}), 201)
	expectErr(t, postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 1, "participant_handles": []string{"bob"}, "note": strings.Repeat("a", 201)}), 422, "validation_failed")
}

func TestR45_NoteAndVisibilityTypes(t *testing.T) {
	e := setup(t)
	for _, n := range []any{nil, 5, true, []string{"x"}, map[string]any{"a": 1}} {
		expectErr(t, sendPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": n}), 422, "validation_failed")
		expectErr(t, postK(t, "/requests", e.tok["ada"], map[string]any{"payer_handle": "bob", "amount": 1, "note": n}), 422, "validation_failed")
		expectErr(t, postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 1, "participant_handles": []string{"bob"}, "note": n}), 422, "validation_failed")
	}
	for _, v := range []any{"friends", "PUBLIC", "Private", "", nil, 1, true} {
		expectErr(t, sendPay(t, e.tok["ada"], "bob", 1, map[string]any{"visibility": v}), 422, "validation_failed")
	}
	rq := mustRequest(t, e.tok["bob"], "ada", 5)
	for _, v := range []any{"friends", "PUBLIC", "", nil, 1, true} {
		expectErr(t, postK(t, "/requests/"+str(t, rq, "request_id")+"/pay", e.tok["ada"], map[string]any{"visibility": v}), 422, "validation_failed")
	}
	if bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("balance changed by invalid payments")
	}
	// the request is still pending after an invalid pay attempt
	out, _ := listReq(t, e.tok["ada"], "?status=pending")
	if len(out) != 1 {
		t.Fatalf("request not pending: %v", out)
	}
}

func TestR78_PaymentRecipientLookup(t *testing.T) {
	e := setup(t)
	expectErr(t, sendPay(t, e.tok["ada"], "nobody", 10, nil), 404, "not_found")
	expectErr(t, postK(t, "/payments", e.tok["ada"], map[string]any{"amount": 10}), 422, "validation_failed")
	if bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("balance changed")
	}
}

func TestR40_PaymentWrongTypes(t *testing.T) {
	e := setup(t)
	expectErr(t, postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": 5, "amount": 10}), 400, "malformed_request")
	expectErr(t, postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": []string{"bob"}, "amount": 10}), 400, "malformed_request")
	expectErr(t, postK(t, "/requests", e.tok["ada"], map[string]any{"payer_handle": 5, "amount": 10}), 400, "malformed_request")
	expectErr(t, postK(t, "/splits", e.tok["ada"], map[string]any{"participant_handles": "bob", "amount": 10}), 400, "malformed_request")
	expectErr(t, postK(t, "/splits", e.tok["ada"], map[string]any{"participant_handles": []any{1}, "amount": 10}), 400, "malformed_request")
	expectErr(t, postK(t, "/splits", e.tok["ada"], map[string]any{"participant_handles": map[string]any{"a": 1}, "amount": 10}), 400, "malformed_request")
}

func TestR79_FailedPaymentLeavesNoTrace(t *testing.T) {
	e := setup(t)
	sendPay(t, e.tok["bob"], "ada", 999999, nil) // insufficient
	sendPay(t, e.tok["bob"], "ghost", 10, nil)
	sendPay(t, e.tok["bob"], "bob", 10, nil)
	sendPay(t, e.tok["bob"], "ada", -1, nil)
	for _, h := range []string{"ada", "bob", "cy"} {
		a, _ := activity(t, e.tok[h], "")
		if len(a) != 0 {
			t.Fatalf("%s sees %d items after only failed payments", h, len(a))
		}
	}
	if e.sum(t) != e.total || bal(t, e.tok["bob"]) != 2500 {
		t.Fatal("balances changed")
	}
	// a successful payment is visible in both wallets and in the feed for both
	p := mustPay(t, e.tok["ada"], "bob", 10, nil)
	for _, h := range []string{"ada", "bob"} {
		a, _ := activity(t, e.tok[h], "")
		if len(a) != 1 || a[0]["payment_id"] != p["payment_id"] {
			t.Fatalf("%s feed after payment: %v", h, a)
		}
	}
	if bal(t, e.tok["ada"]) != 9990 || bal(t, e.tok["bob"]) != 2510 {
		t.Fatal("balances wrong")
	}
}

func TestR80_NoteVerbatim(t *testing.T) {
	e := setup(t)
	notes := []string{
		"  leading and trailing  ",
		"\ttab\nnewline\r\n",
		"<script>alert('x')</script> &amp; \"quoted\" \\ back",
		"日本語のメモ 🎉👨‍👩‍👧‍👦 é",
		"é vs é", // combining vs precomposed must not be normalised
		"é",
		"é",
		"   ",
		"",
		"a​b", // zero-width space must not be stripped
	}
	for _, n := range notes {
		p := mustPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": n})
		if p["note"] != n {
			t.Fatalf("note %q came back as %q", n, p["note"])
		}
	}
	items, _ := activity(t, e.tok["bob"], "?limit=200")
	got := map[string]int{}
	for _, it := range items {
		got[it["note"].(string)]++
	}
	for _, n := range notes {
		if got[n] == 0 {
			t.Fatalf("note %q not in feed verbatim; feed has %v", n, got)
		}
	}
	rq := postK(t, "/requests", e.tok["ada"], map[string]any{"payer_handle": "bob", "amount": 1, "note": " é🎉 "})
	expect(t, rq, 201)
	if rq.obj(t)["note"] != " é🎉 " {
		t.Fatalf("request note altered: %s", rq)
	}
}

func TestR25_PaymentVisibleImmediatelyAndBalancesConsistent(t *testing.T) {
	e := setup(t)
	for i := 0; i < 5; i++ {
		mustPay(t, e.tok["ada"], "bob", 100, nil)
		mustPay(t, e.tok["bob"], "cy", 50, nil)
	}
	if bal(t, e.tok["ada"]) != 9500 || bal(t, e.tok["bob"]) != 2500+500-250 || bal(t, e.tok["cy"]) != 750 {
		t.Fatal("balances inconsistent")
	}
	if e.sum(t) != e.total {
		t.Fatal("sum changed")
	}
}
