package acceptance

import (
	"testing"
)

func rid(t testing.TB, m map[string]any) string { t.Helper(); return str(t, m, "request_id") }

func TestR81_RequestShape(t *testing.T) {
	e := setup(t)
	r := postK(t, "/requests", e.tok["bob"], map[string]any{"payer_handle": "ada", "amount": 1200, "note": "taxi"})
	expect(t, r, 201)
	m := r.obj(t)
	want := map[string]any{"requester_id": "u_bob", "requester_handle": "bob", "payer_id": "u_ada", "payer_handle": "ada",
		"currency": "EUR", "note": "taxi", "status": "pending"}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("%s = %v want %v (%v)", k, m[k], v, m)
		}
	}
	if num(t, m, "amount") != 1200 || !isNull(m, "payment_id") || rid(t, m) == "" {
		t.Fatalf("bad request %v", m)
	}
	checkTS(t, str(t, m, "created_at"))
	// note default
	m2 := mustRequest(t, e.tok["bob"], "ada", 5)
	_ = m2
	r3 := postK(t, "/requests", e.tok["bob"], map[string]any{"payer_handle": "ada", "amount": 5})
	expect(t, r3, 201)
	if r3.obj(t)["note"] != "" {
		t.Fatalf("note default not empty: %s", r3)
	}
	// nothing moved
	if bal(t, e.tok["ada"]) != 10000 || bal(t, e.tok["bob"]) != 2500 {
		t.Fatal("creating requests moved money")
	}
}

func TestR82_RequestValidation(t *testing.T) {
	e := setup(t)
	for _, a := range []any{0, -5, 1000000001, 2.5, "10", true, nil} {
		expectErr(t, postK(t, "/requests", e.tok["bob"], map[string]any{"payer_handle": "ada", "amount": a}), 422, "validation_failed")
	}
	expectErr(t, postK(t, "/requests", e.tok["bob"], map[string]any{"payer_handle": "bob", "amount": 5}), 422, "self_request")
	expectErr(t, postK(t, "/requests", e.tok["bob"], map[string]any{"payer_handle": "ghost", "amount": 5}), 404, "not_found")
	expectErr(t, postK(t, "/requests", e.tok["bob"], map[string]any{"amount": 5}), 422, "validation_failed")
	out, _ := listReq(t, e.tok["bob"], "")
	if len(out) != 0 {
		t.Fatalf("failed requests left traces: %v", out)
	}
}

func TestR83_R27_RequestMayExceedPayerBalance(t *testing.T) {
	e := setup(t)
	rq := mustRequest(t, e.tok["ada"], "cy", 1200) // cy holds 500
	id := rid(t, rq)
	expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["cy"], map[string]any{}), 409, "insufficient_funds")
	if bal(t, e.tok["cy"]) != 500 || bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("failed pay changed balances")
	}
	out, _ := listReq(t, e.tok["cy"], "?status=pending")
	if len(out) != 1 {
		t.Fatalf("request no longer pending: %v", out)
	}
	// money arrives later; same request becomes payable
	mustPay(t, e.tok["ada"], "cy", 700, nil)
	p := postK(t, "/requests/"+id+"/pay", e.tok["cy"], map[string]any{})
	expect(t, p, 201)
	if bal(t, e.tok["cy"]) != 0 || bal(t, e.tok["ada"]) != 10000-700+1200 {
		t.Fatal("balances wrong after late pay")
	}
	// huge request is fine
	mustRequest(t, e.tok["ada"], "dee", 1000000000)
}

func TestR84_PayRequest(t *testing.T) {
	e := setup(t)
	rq := mustRequest(t, e.tok["bob"], "ada", 1200)
	id := rid(t, rq)
	r := postK(t, "/requests/"+id+"/pay", e.tok["ada"], map[string]any{"visibility": "private"})
	expect(t, r, 201)
	p := r.obj(t)
	if p["request_id"] != id || p["visibility"] != "private" || num(t, p, "amount") != 1200 ||
		p["from_handle"] != "ada" || p["to_handle"] != "bob" || p["from_user_id"] != "u_ada" || p["to_user_id"] != "u_bob" ||
		p["currency"] != "EUR" || !isNull(p, "settlement_id") {
		t.Fatalf("payment from pay: %v", p)
	}
	if p["note"] != "n" { // note copied? spec silent on note; do not assert more than presence
		if _, ok := p["note"].(string); !ok {
			t.Fatalf("note missing: %v", p)
		}
	}
	checkTS(t, str(t, p, "created_at"))
	if bal(t, e.tok["ada"]) != 8800 || bal(t, e.tok["bob"]) != 3700 {
		t.Fatal("balances wrong")
	}
	for _, h := range []string{"ada", "bob"} {
		out, _ := listReq(t, e.tok[h], "")
		if len(out) != 1 || out[0]["status"] != "paid" || out[0]["payment_id"] != p["payment_id"] {
			t.Fatalf("%s request view: %v", h, out)
		}
	}
	// default visibility is public, body may be empty object or absent
	rq2 := mustRequest(t, e.tok["bob"], "ada", 10)
	r2 := postK(t, "/requests/"+rid(t, rq2)+"/pay", e.tok["ada"], map[string]any{})
	expect(t, r2, 201)
	if r2.obj(t)["visibility"] != "public" {
		t.Fatalf("default visibility: %s", r2)
	}
	rq3 := mustRequest(t, e.tok["bob"], "ada", 10)
	r3 := postK(t, "/requests/"+rid(t, rq3)+"/pay", e.tok["ada"], nil)
	if r3.Status != 201 && r3.Status != 400 {
		t.Fatalf("pay without body: %s", r3) // accept either reading; state checked below
	}
	if r3.Status == 201 && r3.obj(t)["visibility"] != "public" {
		t.Fatalf("default visibility without body: %s", r3)
	}
}

func TestR85_PayErrors(t *testing.T) {
	e := setup(t)
	rq := mustRequest(t, e.tok["bob"], "ada", 100)
	id := rid(t, rq)
	// not the payer: requester and third party -> 403; unknown -> 404
	expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["bob"], map[string]any{}), 403, "forbidden")
	expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["cy"], map[string]any{}), 403, "forbidden")
	expectErr(t, postK(t, "/requests/does-not-exist/pay", e.tok["ada"], map[string]any{}), 404, "not_found")
	if bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("forbidden pay moved money")
	}
	expect(t, postK(t, "/requests/"+id+"/pay", e.tok["ada"], map[string]any{}), 201)
	// not pending: paid
	expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["ada"], map[string]any{}), 409, "request_not_pending")
	if bal(t, e.tok["ada"]) != 9900 {
		t.Fatal("second pay moved money")
	}
	// declined and cancelled
	r2 := mustRequest(t, e.tok["bob"], "ada", 100)
	expect(t, postNoKey(t, "/requests/"+rid(t, r2)+"/decline", e.tok["ada"], nil), 200)
	expectErr(t, postK(t, "/requests/"+rid(t, r2)+"/pay", e.tok["ada"], map[string]any{}), 409, "request_not_pending")
	r3 := mustRequest(t, e.tok["bob"], "ada", 100)
	expect(t, postNoKey(t, "/requests/"+rid(t, r3)+"/cancel", e.tok["bob"], nil), 200)
	expectErr(t, postK(t, "/requests/"+rid(t, r3)+"/pay", e.tok["ada"], map[string]any{}), 409, "request_not_pending")
	// insufficient
	r4 := mustRequest(t, e.tok["ada"], "dee", 1)
	expectErr(t, postK(t, "/requests/"+rid(t, r4)+"/pay", e.tok["dee"], map[string]any{}), 409, "insufficient_funds")
	if e.sum(t) != e.total {
		t.Fatal("sum changed")
	}
}

func TestR86_NonPartiesAre403NotFound404(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))   // requester bob, payer ada
	for _, who := range []string{"bob", "cy", "op", "dee"} { // anyone but the payer
		expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok[who], map[string]any{}), 403, "forbidden")
		expectErr(t, postNoKey(t, "/requests/"+id+"/decline", e.tok[who], nil), 403, "forbidden")
	}
	for _, who := range []string{"ada", "cy", "op", "dee"} { // anyone but the requester
		expectErr(t, postNoKey(t, "/requests/"+id+"/cancel", e.tok[who], nil), 403, "forbidden")
	}
	// the request is untouched
	out, _ := listReq(t, e.tok["ada"], "")
	if len(out) != 1 || out[0]["status"] != "pending" {
		t.Fatalf("request changed: %v", out)
	}
	for _, act := range []string{"decline", "cancel"} {
		expectErr(t, postNoKey(t, "/requests/nope/"+act, e.tok["ada"], nil), 404, "not_found")
	}
}

func TestR89_Decline(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	r := postNoKey(t, "/requests/"+id+"/decline", e.tok["ada"], nil)
	expect(t, r, 200)
	m := r.obj(t)
	if m["status"] != "declined" || m["request_id"] != id || !isNull(m, "payment_id") || m["payer_handle"] != "ada" {
		t.Fatalf("decline: %v", m)
	}
	// twice is 200 with current state
	r2 := postNoKey(t, "/requests/"+id+"/decline", e.tok["ada"], nil)
	expect(t, r2, 200)
	if r2.obj(t)["status"] != "declined" {
		t.Fatalf("second decline: %s", r2)
	}
	// with an empty JSON object body too
	expect(t, postNoKey(t, "/requests/"+id+"/decline", e.tok["ada"], map[string]any{}), 200)
	// cancelled or paid -> 409
	c := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	expect(t, postNoKey(t, "/requests/"+c+"/cancel", e.tok["bob"], nil), 200)
	expectErr(t, postNoKey(t, "/requests/"+c+"/decline", e.tok["ada"], nil), 409, "request_not_pending")
	p := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	expect(t, postK(t, "/requests/"+p+"/pay", e.tok["ada"], map[string]any{}), 201)
	expectErr(t, postNoKey(t, "/requests/"+p+"/decline", e.tok["ada"], nil), 409, "request_not_pending")
	// declining does not need funds
	d := rid(t, mustRequest(t, e.tok["ada"], "dee", 99999))
	expect(t, postNoKey(t, "/requests/"+d+"/decline", e.tok["dee"], nil), 200)
	if e.sum(t) != e.total {
		t.Fatal("sum changed")
	}
}

func TestR90_Cancel(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	r := postNoKey(t, "/requests/"+id+"/cancel", e.tok["bob"], nil)
	expect(t, r, 200)
	if r.obj(t)["status"] != "cancelled" || r.obj(t)["request_id"] != id {
		t.Fatalf("cancel: %s", r)
	}
	r2 := postNoKey(t, "/requests/"+id+"/cancel", e.tok["bob"], nil)
	expect(t, r2, 200)
	if r2.obj(t)["status"] != "cancelled" {
		t.Fatalf("second cancel: %s", r2)
	}
	d := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	expect(t, postNoKey(t, "/requests/"+d+"/decline", e.tok["ada"], nil), 200)
	expectErr(t, postNoKey(t, "/requests/"+d+"/cancel", e.tok["bob"], nil), 409, "request_not_pending")
	p := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	expect(t, postK(t, "/requests/"+p+"/pay", e.tok["ada"], map[string]any{}), 201)
	expectErr(t, postNoKey(t, "/requests/"+p+"/cancel", e.tok["bob"], nil), 409, "request_not_pending")
	// the payer cannot cancel; the requester cannot decline
	q := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	expectErr(t, postNoKey(t, "/requests/"+q+"/cancel", e.tok["ada"], nil), 403, "forbidden")
	expectErr(t, postNoKey(t, "/requests/"+q+"/decline", e.tok["bob"], nil), 403, "forbidden")
}

func TestR30_R91_ListScopeOrderAndShape(t *testing.T) {
	e := setup(t)
	ids := []string{}
	for i := 0; i < 3; i++ {
		ids = append(ids, rid(t, mustRequest(t, e.tok["bob"], "ada", int64(10+i))))
		tick()
	}
	other := rid(t, mustRequest(t, e.tok["cy"], "dee", 5))
	for _, h := range []string{"ada", "bob"} {
		out, more := listReq(t, e.tok[h], "")
		if more || len(out) != 3 {
			t.Fatalf("%s sees %d more=%v", h, len(out), more)
		}
		// newest first
		for i, it := range out {
			if it["request_id"] != ids[2-i] {
				t.Fatalf("%s order: position %d is %v want %v", h, i, it["request_id"], ids[2-i])
			}
		}
	}
	// others are not visible to non-parties, including the operator
	for _, h := range []string{"op", "ada", "bob"} {
		out, _ := listReq(t, e.tok[h], "")
		if idSet(t, out, "request_id")[other] {
			t.Fatalf("%s sees a request they're not party to", h)
		}
	}
	out, _ := listReq(t, e.tok["cy"], "")
	if len(out) != 1 || out[0]["request_id"] != other {
		t.Fatalf("cy: %v", out)
	}
	out, _ = listReq(t, e.tok["op"], "")
	if len(out) != 0 {
		t.Fatalf("op sees %v", out)
	}
}

func TestR92_DirectionFilter(t *testing.T) {
	e := setup(t)
	in1 := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))  // ada incoming
	in2 := rid(t, mustRequest(t, e.tok["cy"], "ada", 10))   // ada incoming
	out1 := rid(t, mustRequest(t, e.tok["ada"], "bob", 10)) // ada outgoing
	inc, _ := listReq(t, e.tok["ada"], "?direction=incoming")
	if s := idSet(t, inc, "request_id"); len(s) != 2 || !s[in1] || !s[in2] {
		t.Fatalf("incoming: %v", s)
	}
	outg, _ := listReq(t, e.tok["ada"], "?direction=outgoing")
	if s := idSet(t, outg, "request_id"); len(s) != 1 || !s[out1] {
		t.Fatalf("outgoing: %v", s)
	}
	both, _ := listReq(t, e.tok["ada"], "")
	if len(both) != 3 {
		t.Fatalf("both: %d", len(both))
	}
	for _, bad := range []string{"sideways", "INCOMING", "both", ""} {
		expectErr(t, get(t, "/requests?direction="+bad, e.tok["ada"]), 422, "validation_failed")
	}
}

func TestR93_StatusFilter(t *testing.T) {
	e := setup(t)
	pend := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	paid := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	decl := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	canc := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	expect(t, postK(t, "/requests/"+paid+"/pay", e.tok["ada"], map[string]any{}), 201)
	expect(t, postNoKey(t, "/requests/"+decl+"/decline", e.tok["ada"], nil), 200)
	expect(t, postNoKey(t, "/requests/"+canc+"/cancel", e.tok["bob"], nil), 200)
	for st, id := range map[string]string{"pending": pend, "paid": paid, "declined": decl, "cancelled": canc} {
		out, _ := listReq(t, e.tok["ada"], "?status="+st)
		if len(out) != 1 || out[0]["request_id"] != id || out[0]["status"] != st {
			t.Fatalf("status=%s: %v", st, out)
		}
	}
	out, _ := listReq(t, e.tok["ada"], "?status=paid&direction=outgoing")
	if len(out) != 0 {
		t.Fatalf("combined filter: %v", out)
	}
	out, _ = listReq(t, e.tok["bob"], "?status=paid&direction=outgoing")
	if len(out) != 1 {
		t.Fatalf("combined filter bob: %v", out)
	}
	for _, bad := range []string{"open", "PAID", "done", ""} {
		expectErr(t, get(t, "/requests?status="+bad, e.tok["ada"]), 422, "validation_failed")
	}
}

func TestR94_R46_R49_RequestsPagination(t *testing.T) {
	e := setup(t)
	all := map[string]bool{}
	for i := 0; i < 5; i++ {
		all[rid(t, mustRequest(t, e.tok["bob"], "ada", int64(i+1)))] = true
	}
	page := func(q string) ([]map[string]any, bool) { return listReq(t, e.tok["ada"], q) }
	a, more := page("?limit=2&offset=0")
	if len(a) != 2 || !more {
		t.Fatalf("p1 %d %v", len(a), more)
	}
	b, more := page("?limit=2&offset=2")
	if len(b) != 2 || !more {
		t.Fatalf("p2 %d %v", len(b), more)
	}
	c, more := page("?limit=2&offset=4")
	if len(c) != 1 || more {
		t.Fatalf("p3 %d %v", len(c), more)
	}
	seen := map[string]bool{}
	for _, pg := range [][]map[string]any{a, b, c} {
		for _, it := range pg {
			id := str(t, it, "request_id")
			if seen[id] || !all[id] {
				t.Fatalf("pages overlap or alien id %s", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 5 {
		t.Fatal("pages do not cover all requests")
	}
	if out, more := page("?limit=5"); len(out) != 5 || more {
		t.Fatalf("exact fit: %d %v", len(out), more)
	}
	if out, more := page("?limit=4"); len(out) != 4 || !more {
		t.Fatalf("limit 4: %d %v", len(out), more)
	}
	if out, more := page("?offset=5"); len(out) != 0 || more {
		t.Fatalf("offset at end: %d %v", len(out), more)
	}
	if out, more := page("?offset=500"); len(out) != 0 || more {
		t.Fatalf("offset past end: %d %v", len(out), more)
	}
	if out, _ := page("?limit=1"); len(out) != 1 {
		t.Fatal("limit=1")
	}
	if out, _ := page("?limit=200"); len(out) != 5 {
		t.Fatal("limit=200")
	}
	if out, _ := page(""); len(out) != 5 {
		t.Fatal("default limit")
	}
	for _, q := range []string{"limit=0", "limit=201", "limit=-1", "limit=abc", "limit=", "limit=1e2", "limit=2.0", "limit=+4", "limit=4.0",
		"limit=1e9", "limit=0x10", "limit=%204", "offset=-1", "offset=abc", "offset=", "offset=1e1", "offset=+1", "offset=1.0", "offset=1e9"} {
		expectErr(t, get(t, "/requests?"+q, e.tok["ada"]), 422, "validation_failed")
	}
}

func TestR94_DefaultLimit50(t *testing.T) {
	e := setupUsers(t, nil, fu{"ada", 0}, fu{"bob", 0})
	for i := 0; i < 51; i++ {
		mustRequest(t, e.tok["bob"], "ada", 1)
	}
	out, more := listReq(t, e.tok["ada"], "")
	if len(out) != 50 || !more {
		t.Fatalf("default limit: %d %v", len(out), more)
	}
	out, more = listReq(t, e.tok["ada"], "?offset=50")
	if len(out) != 1 || more {
		t.Fatalf("tail: %d %v", len(out), more)
	}
}

func TestR31_SplitRequestsVisibleToTheirTwoPartiesOnly(t *testing.T) {
	e := setup(t)
	r := postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 300, "participant_handles": []string{"ada", "bob", "cy"}})
	expect(t, r, 201)
	for h, n := range map[string]int{"ada": 2, "bob": 1, "cy": 1, "dee": 0, "op": 0} {
		out, _ := listReq(t, e.tok[h], "")
		if len(out) != n {
			t.Fatalf("%s sees %d requests, want %d", h, len(out), n)
		}
	}
	for _, h := range []string{"ada", "bob", "cy", "dee"} {
		a, _ := activity(t, e.tok[h], "")
		if len(a) != 0 {
			t.Fatalf("%s sees splits/requests in feed: %v", h, a)
		}
	}
}
