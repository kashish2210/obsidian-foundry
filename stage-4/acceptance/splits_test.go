package acceptance

import (
	"encoding/json"
	"testing"
)

func doSplit(t testing.TB, tok string, amount any, handles []string, extra map[string]any) resp {
	t.Helper()
	b := map[string]any{"amount": amount, "participant_handles": handles}
	for k, v := range extra {
		b[k] = v
	}
	return postK(t, "/splits", tok, b)
}

func shareList(t testing.TB, m map[string]any) ([]string, []int64) {
	t.Helper()
	arr, ok := m["shares"].([]any)
	if !ok {
		t.Fatalf("shares missing: %v", m)
	}
	var hs []string
	var as []int64
	for _, a := range arr {
		s := a.(map[string]any)
		hs = append(hs, str(t, s, "handle"))
		n, _ := s["amount"].(json.Number).Int64()
		as = append(as, n)
	}
	return hs, as
}

func eqInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestR95_R96_R97_SplitShapeAndRequests(t *testing.T) {
	e := setup(t)
	r := doSplit(t, e.tok["ada"], 3000, []string{"ada", "bob", "cy"}, map[string]any{"note": "dinner"})
	expect(t, r, 201)
	m := r.obj(t)
	if str(t, m, "split_id") == "" || num(t, m, "amount") != 3000 || m["currency"] != "EUR" || m["note"] != "dinner" {
		t.Fatalf("split: %v", m)
	}
	checkTS(t, str(t, m, "created_at"))
	hs, as := shareList(t, m)
	if len(hs) != 3 || hs[0] != "ada" || hs[1] != "bob" || hs[2] != "cy" || !eqInts(as, []int64{1000, 1000, 1000}) {
		t.Fatalf("shares %v %v", hs, as)
	}
	reqs := m["requests"].([]any)
	if len(reqs) != 2 {
		t.Fatalf("requests: %v", reqs)
	}
	for i, h := range []string{"bob", "cy"} {
		q := reqs[i].(map[string]any)
		if q["payer_handle"] != h || q["requester_handle"] != "ada" || q["requester_id"] != "u_ada" || q["status"] != "pending" ||
			num(t, q, "amount") != 1000 || q["note"] != "dinner" || !isNull(q, "payment_id") || q["currency"] != "EUR" {
			t.Fatalf("request %d: %v", i, q)
		}
		// the same request is visible to its parties with the same id
		out, _ := listReq(t, e.tok[h], "?direction=incoming")
		if len(out) != 1 || out[0]["request_id"] != q["request_id"] {
			t.Fatalf("%s incoming: %v", h, out)
		}
	}
	out, _ := listReq(t, e.tok["ada"], "?direction=outgoing")
	if len(out) != 2 {
		t.Fatalf("ada outgoing %d", len(out))
	}
	// splits do not move money and do not check balances
	if e.sum(t) != e.total || bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("split moved money")
	}
	// the default note is empty
	r2 := doSplit(t, e.tok["ada"], 10, []string{"bob"}, nil)
	expect(t, r2, 201)
	if r2.obj(t)["note"] != "" {
		t.Fatalf("default note: %s", r2)
	}
	if str(t, r2.obj(t), "split_id") == str(t, m, "split_id") {
		t.Fatal("split ids not unique")
	}
}

func TestR96_CallerOmitted(t *testing.T) {
	e := setup(t)
	r := doSplit(t, e.tok["op"], 1000, []string{"ada", "bob", "cy"}, nil)
	expect(t, r, 201)
	m := r.obj(t)
	hs, as := shareList(t, m)
	if len(hs) != 3 || !eqInts(as, []int64{334, 333, 333}) {
		t.Fatalf("shares %v %v", hs, as)
	}
	reqs := m["requests"].([]any)
	if len(reqs) != 3 {
		t.Fatalf("requests %d", len(reqs))
	}
	for i, h := range []string{"ada", "bob", "cy"} {
		q := reqs[i].(map[string]any)
		if q["payer_handle"] != h || q["requester_handle"] != "op" || num(t, q, "amount") != as[i] {
			t.Fatalf("request %d: %v", i, q)
		}
	}
}

func TestR96_CallerInMiddleAndLast(t *testing.T) {
	e := setup(t)
	for _, order := range [][]string{{"bob", "ada", "cy"}, {"bob", "cy", "ada"}} {
		r := doSplit(t, e.tok["ada"], 1000, order, nil)
		expect(t, r, 201)
		m := r.obj(t)
		hs, as := shareList(t, m)
		for i := range order {
			if hs[i] != order[i] {
				t.Fatalf("share order %v", hs)
			}
		}
		if !eqInts(as, []int64{334, 333, 333}) {
			t.Fatalf("shares %v", as)
		}
		reqs := m["requests"].([]any)
		if len(reqs) != 2 {
			t.Fatalf("requests %d", len(reqs))
		}
		var want []string
		for _, h := range order {
			if h != "ada" {
				want = append(want, h)
			}
		}
		for i := range want {
			q := reqs[i].(map[string]any)
			if q["payer_handle"] != want[i] {
				t.Fatalf("request order: %v", q)
			}
		}
		// requests for non-caller participants carry those participants' shares
		for i, h := range order {
			if h == "ada" {
				continue
			}
			for _, q := range reqs {
				qq := q.(map[string]any)
				if qq["payer_handle"] == h && num(t, qq, "amount") != as[i] {
					t.Fatalf("request amount for %s", h)
				}
			}
		}
	}
}

func TestR101_SplitTable(t *testing.T) {
	e := setup(t)
	all := []string{"ada", "bob", "cy", "dee", "op"}
	cases := []struct {
		amount int64
		n      int
		want   []int64
	}{
		{1000, 3, []int64{334, 333, 333}},
		{1, 3, []int64{1, 0, 0}},
		{10, 3, []int64{4, 3, 3}},
		{999, 3, []int64{333, 333, 333}},
		{5, 5, []int64{1, 1, 1, 1, 1}},
		{1, 1, []int64{1}},
		{7, 2, []int64{4, 3}},
		{1000000000, 3, []int64{333333334, 333333333, 333333333}},
		{11, 5, []int64{3, 2, 2, 2, 2}},
		{2, 5, []int64{1, 1, 0, 0, 0}},
	}
	for _, c := range cases {
		r := doSplit(t, e.tok["ada"], c.amount, all[:c.n], nil)
		expect(t, r, 201)
		_, as := shareList(t, r.obj(t))
		if !eqInts(as, c.want) {
			t.Fatalf("split %d/%d = %v want %v", c.amount, c.n, as, c.want)
		}
	}
}

func TestR102_OrderDecidesWhoGetsExtraUnit(t *testing.T) {
	e := setup(t)
	r1 := doSplit(t, e.tok["ada"], 10, []string{"ada", "bob", "cy"}, nil)
	r2 := doSplit(t, e.tok["ada"], 10, []string{"cy", "bob", "ada"}, nil)
	r3 := doSplit(t, e.tok["ada"], 10, []string{"bob", "cy", "ada"}, nil)
	for _, r := range []resp{r1, r2, r3} {
		expect(t, r, 201)
	}
	get3 := func(r resp) map[string]int64 {
		hs, as := shareList(t, r.obj(t))
		m := map[string]int64{}
		for i := range hs {
			m[hs[i]] = as[i]
		}
		return m
	}
	a, b, c := get3(r1), get3(r2), get3(r3)
	if a["ada"] != 4 || a["bob"] != 3 || a["cy"] != 3 {
		t.Fatalf("%v", a)
	}
	if b["cy"] != 4 || b["bob"] != 3 || b["ada"] != 3 {
		t.Fatalf("%v", b)
	}
	if c["bob"] != 4 || c["cy"] != 3 || c["ada"] != 3 {
		t.Fatalf("%v", c)
	}
}

func TestR102_ZeroShareStillMakesRequestAndIsPayable(t *testing.T) {
	e := setup(t)
	r := doSplit(t, e.tok["ada"], 1, []string{"ada", "bob", "cy"}, nil)
	expect(t, r, 201)
	m := r.obj(t)
	reqs := m["requests"].([]any)
	if len(reqs) != 2 {
		t.Fatalf("zero-share participants must still get requests: %v", reqs)
	}
	for _, q := range reqs {
		if num(t, q.(map[string]any), "amount") != 0 {
			t.Fatalf("expected 0 amount: %v", q)
		}
	}
	// Reading chosen (ledger R102): paying a zero request is legal and moves 0.
	id := str(t, reqs[0].(map[string]any), "request_id")
	p := postK(t, "/requests/"+id+"/pay", e.tok["bob"], map[string]any{})
	expect(t, p, 201)
	if num(t, p.obj(t), "amount") != 0 {
		t.Fatalf("pay of zero request: %s", p)
	}
	if e.sum(t) != e.total || bal(t, e.tok["bob"]) != 2500 {
		t.Fatal("zero payment moved money")
	}
	// caller's own participant with a share 0
	r2 := doSplit(t, e.tok["ada"], 1, []string{"bob", "cy", "ada"}, nil)
	expect(t, r2, 201)
	if len(r2.obj(t)["requests"].([]any)) != 2 {
		t.Fatal("requests count")
	}
}

func TestR99_OnlyCallerParticipant(t *testing.T) {
	e := setup(t)
	r := doSplit(t, e.tok["ada"], 1000, []string{"ada"}, nil)
	expect(t, r, 201)
	m := r.obj(t)
	arr, ok := m["requests"].([]any)
	if !ok || len(arr) != 0 {
		t.Fatalf("requests must be an empty array: %s", r)
	}
	hs, as := shareList(t, m)
	if len(hs) != 1 || hs[0] != "ada" || as[0] != 1000 {
		t.Fatalf("shares %v %v", hs, as)
	}
	// no balance check: poor caller may split a huge amount
	r2 := doSplit(t, e.tok["dee"], 1000000000, []string{"dee", "ada"}, nil)
	expect(t, r2, 201)
	if e.sum(t) != e.total || bal(t, e.tok["dee"]) != 0 {
		t.Fatal("split touched balances")
	}
}

func TestR98_SplitValidation(t *testing.T) {
	e := setup(t)
	tk := e.tok["ada"]
	for _, a := range []any{0, -1, 1000000001, 1.5, "100", true, nil} {
		expectErr(t, doSplit(t, tk, a, []string{"bob"}, nil), 422, "validation_failed")
	}
	expectErr(t, postK(t, "/splits", tk, map[string]any{"participant_handles": []string{"bob"}}), 422, "validation_failed")
	expectErr(t, postK(t, "/splits", tk, map[string]any{"amount": 10}), 422, "validation_failed")
	expectErr(t, doSplit(t, tk, 10, []string{}, nil), 422, "validation_failed")
	expectErr(t, doSplit(t, tk, 10, []string{"bob", "bob"}, nil), 422, "validation_failed")
	expectErr(t, doSplit(t, tk, 10, []string{"ada", "bob", "ada"}, nil), 422, "validation_failed")
	expectErr(t, doSplit(t, tk, 10, []string{"bob", "ghost"}, nil), 404, "not_found")
	expectErr(t, doSplit(t, tk, 10, []string{"ghost"}, nil), 404, "not_found")
	expectErr(t, doSplit(t, tk, 10, []string{"bob"}, map[string]any{"note": strs201()}), 422, "validation_failed")
	// a rejected split creates no requests at all
	for _, h := range []string{"ada", "bob", "cy"} {
		out, _ := listReq(t, e.tok[h], "")
		if len(out) != 0 {
			t.Fatalf("%s has requests after rejected splits: %v", h, out)
		}
	}
}

func strs201() string {
	b := make([]byte, 201)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func TestR103_SplitsIndependentAndSumPreserved(t *testing.T) {
	e := setup(t)
	handles := []string{"ada", "bob", "cy"}
	ids := []string{}
	for i := 0; i < 6; i++ {
		order := []string{handles[i%3], handles[(i+1)%3], handles[(i+2)%3]}
		r := doSplit(t, e.tok["dee"], int64(1000+i), order, nil)
		expect(t, r, 201)
		_, as := shareList(t, r.obj(t))
		var sum int64
		for _, a := range as {
			sum += a
		}
		if sum != int64(1000+i) {
			t.Fatalf("shares %v do not sum to %d", as, 1000+i)
		}
		for _, q := range r.obj(t)["requests"].([]any) {
			ids = append(ids, str(t, q.(map[string]any), "request_id"))
		}
	}
	// pay everything off, in every payer's wallet; dee (requester) collects
	for _, h := range handles {
		tok := e.tok[h]
		in, _ := listReq(t, tok, "?direction=incoming&limit=200")
		for _, q := range in {
			if num(t, q, "amount") > bal(t, tok) {
				// top the payer up from ada so it can pay; total is conserved by design
				mustPay(t, e.tok["ada"], h, num(t, q, "amount"), nil)
			}
			expect(t, postK(t, "/requests/"+str(t, q, "request_id")+"/pay", tok, map[string]any{}), 201)
		}
	}
	if e.sum(t) != e.total {
		t.Fatalf("sum %d != seeded %d", e.sum(t), e.total)
	}
	if len(ids) != 18 {
		t.Fatalf("expected 18 requests, got %d", len(ids))
	}
	for _, h := range []string{"ada", "bob", "cy", "dee", "op"} {
		if bal(t, e.tok[h]) < 0 {
			t.Fatal("negative balance")
		}
	}
}
