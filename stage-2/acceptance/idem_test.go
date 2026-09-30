package acceptance

import (
	"strings"
	"sync"
	"testing"
)

// op describes one idempotent write path with a valid body for a fresh environment.
type op struct {
	name string
	tok  func(e *env) string
	path func(t testing.TB, e *env) string
	body func() any
}

func idemOps() []op {
	return []op{
		{"payments", func(e *env) string { return e.tok["ada"] }, func(t testing.TB, e *env) string { return "/payments" },
			func() any { return map[string]any{"to_handle": "bob", "amount": 100, "note": "x"} }},
		{"requests", func(e *env) string { return e.tok["ada"] }, func(t testing.TB, e *env) string { return "/requests" },
			func() any { return map[string]any{"payer_handle": "bob", "amount": 100, "note": "x"} }},
		{"pay", func(e *env) string { return e.tok["ada"] }, func(t testing.TB, e *env) string {
			return "/requests/" + rid(t, mustRequest(t, e.tok["bob"], "ada", 100)) + "/pay"
		}, func() any { return map[string]any{"visibility": "private"} }},
		{"splits", func(e *env) string { return e.tok["ada"] }, func(t testing.TB, e *env) string { return "/splits" },
			func() any {
				return map[string]any{"amount": 100, "participant_handles": []string{"ada", "bob", "cy"}}
			}},
		{"settlements", func(e *env) string { return e.tok["op"] }, func(t testing.TB, e *env) string { return "/settlements" },
			func() any {
				return map[string]any{"transfers": []any{map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 100}}}
			}},
	}
}

func TestR61_MissingIdempotencyKey(t *testing.T) {
	for _, o := range idemOps() {
		e := setup(t)
		p := o.path(t, e)
		expectErr(t, call(t, "POST", p, o.tok(e), nil, o.body()), 400, "missing_idempotency_key")
		expectErr(t, call(t, "POST", p, o.tok(e), map[string]string{"Idempotency-Key": ""}, o.body()), 400, "missing_idempotency_key")
		// nothing happened: same path works with a key
		expect(t, post(t, p, o.tok(e), newKey(), o.body()), 201)
		_ = o.name
	}
}

func TestR48_KeyLength(t *testing.T) {
	for _, o := range idemOps() {
		e := setup(t)
		p := o.path(t, e)
		expectErr(t, post(t, p, o.tok(e), strings.Repeat("k", 256), o.body()), 422, "validation_failed")
		expect(t, post(t, p, o.tok(e), strings.Repeat("k", 255), o.body()), 201)
		expect(t, post(t, p, o.tok(e), "k", o.body()), func() int {
			if o.name == "pay" {
				return 409 // request already paid by the 255-char key; different key is a new attempt
			}
			return 201
		}())
	}
}

func TestR62_R63_FirstUseThenReplay(t *testing.T) {
	for _, o := range idemOps() {
		e := setup(t)
		p, tk := o.path(t, e), o.tok(e)
		k := newKey()
		first := post(t, p, tk, k, o.body())
		expect(t, first, 201)
		for i := 0; i < 3; i++ {
			rep := post(t, p, tk, k, o.body())
			expect(t, rep, 200)
			requireSameJSON(t, first, rep)
		}
		if e.sum(t) != e.total {
			t.Fatalf("%s: sum changed", o.name)
		}
	}
}

func TestR63_ReplayHasOnlyOneEffect(t *testing.T) {
	e := setup(t)
	k := newKey()
	body := map[string]any{"to_handle": "bob", "amount": 1000}
	for i := 0; i < 4; i++ {
		post(t, "/payments", e.tok["ada"], k, body)
	}
	if bal(t, e.tok["ada"]) != 9000 || bal(t, e.tok["bob"]) != 3500 {
		t.Fatal("payment replays moved money more than once")
	}
	a, _ := activity(t, e.tok["ada"], "")
	if len(a) != 1 {
		t.Fatalf("feed has %d payments", len(a))
	}
	// request replay creates one request
	k2 := newKey()
	for i := 0; i < 3; i++ {
		post(t, "/requests", e.tok["ada"], k2, map[string]any{"payer_handle": "bob", "amount": 5})
	}
	out, _ := listReq(t, e.tok["ada"], "")
	if len(out) != 1 {
		t.Fatalf("request replays created %d requests", len(out))
	}
	// split replay creates one set of requests
	k3 := newKey()
	for i := 0; i < 3; i++ {
		post(t, "/splits", e.tok["ada"], k3, map[string]any{"amount": 90, "participant_handles": []string{"bob", "cy"}})
	}
	out, _ = listReq(t, e.tok["cy"], "")
	if len(out) != 1 {
		t.Fatalf("split replays created %d requests for cy", len(out))
	}
}

func TestR64_SameKeyDifferentBody(t *testing.T) {
	e := setup(t)
	k := newKey()
	expect(t, post(t, "/payments", e.tok["ada"], k, map[string]any{"to_handle": "bob", "amount": 100}), 201)
	for _, b := range []map[string]any{
		{"to_handle": "bob", "amount": 101},
		{"to_handle": "cy", "amount": 100},
		{"to_handle": "bob", "amount": 100, "note": "x"},
		{"to_handle": "bob", "amount": 100, "visibility": "public"},
		{"to_handle": "bob", "amount": 100, "visibility": "private"},
	} {
		expectErr(t, post(t, "/payments", e.tok["ada"], k, b), 409, "idempotency_key_reuse")
	}
	if bal(t, e.tok["ada"]) != 9900 {
		t.Fatal("conflicting reuse moved money")
	}
	// the same holds for the other paths
	kr := newKey()
	expect(t, post(t, "/requests", e.tok["ada"], kr, map[string]any{"payer_handle": "bob", "amount": 5}), 201)
	expectErr(t, post(t, "/requests", e.tok["ada"], kr, map[string]any{"payer_handle": "bob", "amount": 6}), 409, "idempotency_key_reuse")
	ks := newKey()
	expect(t, post(t, "/splits", e.tok["ada"], ks, map[string]any{"amount": 9, "participant_handles": []string{"bob"}}), 201)
	expectErr(t, post(t, "/splits", e.tok["ada"], ks, map[string]any{"amount": 9, "participant_handles": []string{"cy"}}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/splits", e.tok["ada"], ks, map[string]any{"amount": 9, "participant_handles": []string{"bob", "cy"}}), 409, "idempotency_key_reuse")
	// a different request id in the path, same key and body -> different path, see R67; the same path but different body -> 409
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	kp := newKey()
	expect(t, post(t, "/requests/"+rq+"/pay", e.tok["ada"], kp, map[string]any{}), 201)
	expectErr(t, post(t, "/requests/"+rq+"/pay", e.tok["ada"], kp, map[string]any{"visibility": "private"}), 409, "idempotency_key_reuse")
}

func TestR65_KeyReusableAfter4xx(t *testing.T) {
	e := setup(t)
	k := newKey()
	// 409 insufficient funds
	expectErr(t, post(t, "/payments", e.tok["bob"], k, map[string]any{"to_handle": "ada", "amount": 2501}), 409, "insufficient_funds")
	// the same key with a *different* (now valid) body is a first use
	r := post(t, "/payments", e.tok["bob"], k, map[string]any{"to_handle": "ada", "amount": 10})
	expect(t, r, 201)
	// and now it is claimed by that body
	expectErr(t, post(t, "/payments", e.tok["bob"], k, map[string]any{"to_handle": "ada", "amount": 11}), 409, "idempotency_key_reuse")
	rep := post(t, "/payments", e.tok["bob"], k, map[string]any{"to_handle": "ada", "amount": 10})
	expect(t, rep, 200)
	requireSameJSON(t, r, rep)

	// same body retried after the failure was cured: a first use, 201
	k2 := newKey()
	body := map[string]any{"to_handle": "ada", "amount": 2500}
	expectErr(t, post(t, "/payments", e.tok["bob"], k2, body), 409, "insufficient_funds") // bob has 2490
	mustPay(t, e.tok["ada"], "bob", 100, nil)
	expect(t, post(t, "/payments", e.tok["bob"], k2, body), 201)

	// 422, 404 and 403 likewise do not claim the key
	k3 := newKey()
	expectErr(t, post(t, "/payments", e.tok["ada"], k3, map[string]any{"to_handle": "bob", "amount": 0}), 422, "validation_failed")
	expect(t, post(t, "/payments", e.tok["ada"], k3, map[string]any{"to_handle": "bob", "amount": 1}), 201)
	k4 := newKey()
	expectErr(t, post(t, "/payments", e.tok["ada"], k4, map[string]any{"to_handle": "ghost", "amount": 5}), 404, "not_found")
	expect(t, post(t, "/payments", e.tok["ada"], k4, map[string]any{"to_handle": "bob", "amount": 5}), 201)
	k5 := newKey()
	rq := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	expectErr(t, post(t, "/requests/"+rq+"/pay", e.tok["cy"], k5, map[string]any{}), 403, "forbidden")
	expect(t, post(t, "/requests/"+rq+"/pay", e.tok["ada"], k5, map[string]any{}), 201)
	// requests / splits
	k6 := newKey()
	expectErr(t, post(t, "/requests", e.tok["ada"], k6, map[string]any{"payer_handle": "ada", "amount": 5}), 422, "self_request")
	expect(t, post(t, "/requests", e.tok["ada"], k6, map[string]any{"payer_handle": "bob", "amount": 5}), 201)
	k7 := newKey()
	expectErr(t, post(t, "/splits", e.tok["ada"], k7, map[string]any{"amount": 5, "participant_handles": []string{"ghost"}}), 404, "not_found")
	expect(t, post(t, "/splits", e.tok["ada"], k7, map[string]any{"amount": 5, "participant_handles": []string{"bob"}}), 201)
}

func TestR65_PayKeyReusableAfterInsufficientFunds(t *testing.T) {
	e := setup(t)
	rq := rid(t, mustRequest(t, e.tok["ada"], "cy", 1200))
	k := newKey()
	expectErr(t, post(t, "/requests/"+rq+"/pay", e.tok["cy"], k, map[string]any{}), 409, "insufficient_funds")
	mustPay(t, e.tok["ada"], "cy", 700, nil)
	r := post(t, "/requests/"+rq+"/pay", e.tok["cy"], k, map[string]any{})
	expect(t, r, 201)
	rep := post(t, "/requests/"+rq+"/pay", e.tok["cy"], k, map[string]any{})
	expect(t, rep, 200)
	requireSameJSON(t, r, rep)
	if bal(t, e.tok["cy"]) != 0 {
		t.Fatal("cy balance")
	}
}

func TestR66_KeysScopedPerUser(t *testing.T) {
	e := setup(t)
	k := "shared-key"
	a := post(t, "/payments", e.tok["ada"], k, map[string]any{"to_handle": "cy", "amount": 100})
	b := post(t, "/payments", e.tok["bob"], k, map[string]any{"to_handle": "cy", "amount": 100})
	c := post(t, "/payments", e.tok["dee"], k, map[string]any{"to_handle": "cy", "amount": 100})
	expect(t, a, 201)
	expect(t, b, 201)
	expectErr(t, c, 409, "insufficient_funds") // dee has nothing; proves dee is evaluated on their own
	if a.obj(t)["payment_id"] == b.obj(t)["payment_id"] {
		t.Fatal("two users got the same payment")
	}
	if a.obj(t)["from_handle"] != "ada" || b.obj(t)["from_handle"] != "bob" {
		t.Fatal("wrong senders")
	}
	// different body under the same key for another user is not a conflict
	d := post(t, "/payments", e.tok["cy"], k, map[string]any{"to_handle": "ada", "amount": 7})
	expect(t, d, 201)
	// each user replays their own
	ar := post(t, "/payments", e.tok["ada"], k, map[string]any{"to_handle": "cy", "amount": 100})
	expect(t, ar, 200)
	requireSameJSON(t, a, ar)
	br := post(t, "/payments", e.tok["bob"], k, map[string]any{"to_handle": "cy", "amount": 100})
	expect(t, br, 200)
	requireSameJSON(t, b, br)
	// the same scoping applies to requests and splits
	expect(t, post(t, "/requests", e.tok["ada"], k, map[string]any{"payer_handle": "cy", "amount": 1}), 201)
	expect(t, post(t, "/requests", e.tok["bob"], k, map[string]any{"payer_handle": "cy", "amount": 2}), 201)
}

func TestR67_SameKeyDifferentPathIsNotAReplay(t *testing.T) {
	e := setup(t)
	k := newKey()
	// Body accepted by both endpoints (unknown fields are ignored).
	body := map[string]any{"to_handle": "bob", "payer_handle": "bob", "amount": 100}
	p := post(t, "/payments", e.tok["ada"], k, body)
	expect(t, p, 201)
	q := post(t, "/requests", e.tok["ada"], k, body)
	expect(t, q, 201)
	if q.obj(t)["request_id"] == nil || q.obj(t)["status"] != "pending" {
		t.Fatalf("second call did not create a request: %s", q)
	}
	// each replays independently
	expect(t, post(t, "/payments", e.tok["ada"], k, body), 200)
	expect(t, post(t, "/requests", e.tok["ada"], k, body), 200)
	// two pay paths (different request ids) with the same key and body {}
	r1 := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	r2 := rid(t, mustRequest(t, e.tok["bob"], "ada", 20))
	k2 := newKey()
	a := post(t, "/requests/"+r1+"/pay", e.tok["ada"], k2, map[string]any{})
	b := post(t, "/requests/"+r2+"/pay", e.tok["ada"], k2, map[string]any{})
	expect(t, a, 201)
	expect(t, b, 201)
	if num(t, a.obj(t), "amount") != 10 || num(t, b.obj(t), "amount") != 20 {
		t.Fatal("wrong amounts")
	}
	if e.sum(t) != e.total {
		t.Fatal("sum")
	}
}

func TestR68_SameBodyMeansSameJSONValue(t *testing.T) {
	e := setup(t)
	k := newKey()
	first := post(t, "/payments", e.tok["ada"], k, `{"to_handle":"bob","amount":100,"note":"n"}`)
	expect(t, first, 201)
	for _, b := range []string{
		`{"note":"n","amount":100,"to_handle":"bob"}`,
		"{\n  \"amount\" : 100 ,\n\t\"note\":\"n\", \"to_handle\" : \"bob\"\n}",
	} {
		rep := post(t, "/payments", e.tok["ada"], k, b)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
	}
	// Reading chosen: numbers compare by parsed value, so 1e2 == 100 == 100.0.
	for _, b := range []string{`{"to_handle":"bob","amount":1e2,"note":"n"}`, `{"to_handle":"bob","amount":100.0,"note":"n"}`} {
		rep := post(t, "/payments", e.tok["ada"], k, b)
		expect(t, rep, 200)
		requireSameJSON(t, first, rep)
	}
	// An omitted field and the same field spelled out with its default are different JSON values.
	expectErr(t, post(t, "/payments", e.tok["ada"], k, `{"to_handle":"bob","amount":100,"note":"n","visibility":"public"}`), 409, "idempotency_key_reuse")
	// Extra unknown field changes the JSON value as well.
	expectErr(t, post(t, "/payments", e.tok["ada"], k, `{"to_handle":"bob","amount":100,"note":"n","zzz":1}`), 409, "idempotency_key_reuse")
	if bal(t, e.tok["ada"]) != 9900 {
		t.Fatal("balance")
	}
	// nested arrays keep their order
	ks := newKey()
	expect(t, post(t, "/splits", e.tok["ada"], ks, `{"amount":10,"participant_handles":["bob","cy"]}`), 201)
	expectErr(t, post(t, "/splits", e.tok["ada"], ks, `{"amount":10,"participant_handles":["cy","bob"]}`), 409, "idempotency_key_reuse")
	expect(t, post(t, "/splits", e.tok["ada"], ks, `{ "participant_handles" : ["bob","cy"], "amount" : 10 }`), 200)
}

func TestR87_PayBodyEmptyVsExplicitPublic(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	k := newKey()
	expect(t, post(t, "/requests/"+id+"/pay", e.tok["ada"], k, map[string]any{}), 201)
	expectErr(t, post(t, "/requests/"+id+"/pay", e.tok["ada"], k, map[string]any{"visibility": "public"}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/requests/"+id+"/pay", e.tok["ada"], k, map[string]any{"visibility": "private"}), 409, "idempotency_key_reuse")
	expect(t, post(t, "/requests/"+id+"/pay", e.tok["ada"], k, `{ }`), 200)
	// and the other way around
	id2 := rid(t, mustRequest(t, e.tok["bob"], "ada", 100))
	k2 := newKey()
	expect(t, post(t, "/requests/"+id2+"/pay", e.tok["ada"], k2, map[string]any{"visibility": "public"}), 201)
	expectErr(t, post(t, "/requests/"+id2+"/pay", e.tok["ada"], k2, map[string]any{}), 409, "idempotency_key_reuse")
	if bal(t, e.tok["ada"]) != 9800 {
		t.Fatal("balance")
	}
}

func TestR88_PayReplayAfterPaid(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 400))
	k := newKey()
	body := map[string]any{"visibility": "private"}
	first := post(t, "/requests/"+id+"/pay", e.tok["ada"], k, body)
	expect(t, first, 201)
	// the request is paid now; replay must not be request_not_pending
	rep := post(t, "/requests/"+id+"/pay", e.tok["ada"], k, body)
	expect(t, rep, 200)
	requireSameJSON(t, first, rep)
	// drain ada so that a fresh pay would be insufficient; replay still 200
	mustPay(t, e.tok["ada"], "dee", bal(t, e.tok["ada"]), nil)
	rep2 := post(t, "/requests/"+id+"/pay", e.tok["ada"], k, body)
	expect(t, rep2, 200)
	requireSameJSON(t, first, rep2)
	if bal(t, e.tok["ada"]) != 0 || bal(t, e.tok["bob"]) != 2900 {
		t.Fatalf("money moved again: ada=%d bob=%d", bal(t, e.tok["ada"]), bal(t, e.tok["bob"]))
	}
	// a fresh key on the paid request is request_not_pending
	expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["ada"], body), 409, "request_not_pending")
}

func TestR70_ReplayReturnsOriginalAfterChange(t *testing.T) {
	e := setup(t)
	// request replay after cancel: original body (status pending) is returned
	k := newKey()
	body := map[string]any{"payer_handle": "bob", "amount": 50}
	first := post(t, "/requests", e.tok["ada"], k, body)
	expect(t, first, 201)
	id := rid(t, first.obj(t))
	expect(t, postNoKey(t, "/requests/"+id+"/cancel", e.tok["ada"], nil), 200)
	rep := post(t, "/requests", e.tok["ada"], k, body)
	expect(t, rep, 200)
	requireSameJSON(t, first, rep)
	if rep.obj(t)["status"] != "pending" {
		t.Fatalf("replay must show the original response, got %s", rep)
	}
	// the cancelled request stays cancelled and there is exactly one
	out, _ := listReq(t, e.tok["ada"], "")
	if len(out) != 1 || out[0]["status"] != "cancelled" {
		t.Fatalf("list %v", out)
	}
	// payment replay after the sender's balance is gone
	k2 := newKey()
	pb := map[string]any{"to_handle": "bob", "amount": 10000}
	p := post(t, "/payments", e.tok["ada"], k2, pb)
	expect(t, p, 201)
	rp := post(t, "/payments", e.tok["ada"], k2, pb)
	expect(t, rp, 200)
	requireSameJSON(t, p, rp)
	if bal(t, e.tok["ada"]) != 0 || bal(t, e.tok["bob"]) != 12500 {
		t.Fatal("replay changed balances")
	}
	// split replay after the requests were paid/declined
	k3 := newKey()
	sb := map[string]any{"amount": 30, "participant_handles": []string{"bob", "cy"}}
	s := post(t, "/splits", e.tok["bob"], k3, sb)
	expect(t, s, 201)
	for _, q := range s.obj(t)["requests"].([]any) {
		qid := rid(t, q.(map[string]any))
		if q.(map[string]any)["payer_handle"] == "cy" {
			expect(t, postNoKey(t, "/requests/"+qid+"/decline", e.tok["cy"], nil), 200)
		}
	}
	sr := post(t, "/splits", e.tok["bob"], k3, sb)
	expect(t, sr, 200)
	requireSameJSON(t, s, sr)
}

func TestR71_ClaimedKeyResolvedBeforeValidation(t *testing.T) {
	e := setup(t)
	k := newKey()
	expect(t, post(t, "/payments", e.tok["ada"], k, map[string]any{"to_handle": "bob", "amount": 100}), 201)
	for _, b := range []any{
		map[string]any{"to_handle": "bob", "amount": -5},
		map[string]any{"to_handle": "bob", "amount": "x"},
		map[string]any{"to_handle": "ghost", "amount": 100},
		map[string]any{"to_handle": "ada", "amount": 100},
		map[string]any{"to_handle": "bob", "amount": 999999999},
		map[string]any{"to_handle": "bob", "amount": 100, "visibility": "bogus"},
		map[string]any{"to_handle": 5, "amount": 100},
		map[string]any{},
	} {
		expectErr(t, post(t, "/payments", e.tok["ada"], k, b), 409, "idempotency_key_reuse")
	}
	// an unparseable body is still 400, and without auth still 401
	expectErr(t, post(t, "/payments", e.tok["ada"], k, `{"to_handle":`), 400, "malformed_request")
	expectErr(t, post(t, "/payments", e.tok["ada"], k, `[]`), 400, "malformed_request")
	expectErr(t, post(t, "/payments", "", k, map[string]any{"to_handle": "bob", "amount": -5}), 401, "unauthenticated")
	// pay: visibility bogus after success is 409, not 422
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	kp := newKey()
	expect(t, post(t, "/requests/"+id+"/pay", e.tok["ada"], kp, map[string]any{}), 201)
	expectErr(t, post(t, "/requests/"+id+"/pay", e.tok["ada"], kp, map[string]any{"visibility": "bogus"}), 409, "idempotency_key_reuse")
	// requests / splits
	kr := newKey()
	expect(t, post(t, "/requests", e.tok["ada"], kr, map[string]any{"payer_handle": "bob", "amount": 5}), 201)
	expectErr(t, post(t, "/requests", e.tok["ada"], kr, map[string]any{"payer_handle": "bob", "amount": 0}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/requests", e.tok["ada"], kr, map[string]any{}), 409, "idempotency_key_reuse")
	ks := newKey()
	expect(t, post(t, "/splits", e.tok["ada"], ks, map[string]any{"amount": 5, "participant_handles": []string{"bob"}}), 201)
	expectErr(t, post(t, "/splits", e.tok["ada"], ks, map[string]any{"amount": 5, "participant_handles": []string{}}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/splits", e.tok["ada"], ks, map[string]any{"amount": 0, "participant_handles": []string{"ghost"}}), 409, "idempotency_key_reuse")
	// settlements
	kt := newKey()
	good := map[string]any{"transfers": []any{map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 1}}}
	expect(t, post(t, "/settlements", e.tok["op"], kt, good), 201)
	expectErr(t, post(t, "/settlements", e.tok["op"], kt, map[string]any{"transfers": []any{}}), 409, "idempotency_key_reuse")
	expectErr(t, post(t, "/settlements", e.tok["op"], kt, map[string]any{}), 409, "idempotency_key_reuse")
}

// ---- R69: concurrent identical requests ----

type result struct {
	r   resp
	err error
}

func fanout(n int, f func(i int) (resp, error)) []result {
	out := make([]result, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := f(i)
			out[i] = result{r, err}
		}(i)
	}
	close(start)
	wg.Wait()
	return out
}

// onlyOneCreated asserts one 201 and n-1 identical 200s; returns the 201 response.
func onlyOneCreated(t *testing.T, rs []result) resp {
	t.Helper()
	var created *resp
	n201 := 0
	for _, x := range rs {
		if x.err != nil {
			t.Fatalf("transport error: %v", x.err)
		}
		switch x.r.Status {
		case 201:
			n201++
			rr := x.r
			created = &rr
		case 200:
		default:
			t.Fatalf("unexpected status %s", x.r)
		}
	}
	if n201 != 1 {
		t.Fatalf("want exactly one 201, got %d", n201)
	}
	for _, x := range rs {
		if !sameJSON(x.r.Body, created.Body) {
			t.Fatalf("concurrent responses differ: %s vs %s", x.r, *created)
		}
	}
	return *created
}

func TestR69_ConcurrentIdenticalPayments(t *testing.T) {
	e := setup(t)
	k := newKey()
	body := map[string]any{"to_handle": "bob", "amount": 1000}
	rs := fanout(50, func(int) (resp, error) {
		return send("POST", "/payments", e.tok["ada"], map[string]string{"Idempotency-Key": k}, body)
	})
	onlyOneCreated(t, rs)
	if bal(t, e.tok["ada"]) != 9000 || bal(t, e.tok["bob"]) != 3500 {
		t.Fatalf("effect not once: ada=%d bob=%d", bal(t, e.tok["ada"]), bal(t, e.tok["bob"]))
	}
	a, _ := activity(t, e.tok["ada"], "")
	if len(a) != 1 {
		t.Fatalf("%d payments in feed", len(a))
	}
}

func TestR69_ConcurrentIdenticalRequestsAndSplits(t *testing.T) {
	e := setup(t)
	k := newKey()
	rs := fanout(40, func(int) (resp, error) {
		return send("POST", "/requests", e.tok["ada"], map[string]string{"Idempotency-Key": k}, map[string]any{"payer_handle": "bob", "amount": 3})
	})
	onlyOneCreated(t, rs)
	out, _ := listReq(t, e.tok["bob"], "")
	if len(out) != 1 {
		t.Fatalf("%d requests", len(out))
	}
	ks := newKey()
	rs = fanout(40, func(int) (resp, error) {
		return send("POST", "/splits", e.tok["ada"], map[string]string{"Idempotency-Key": ks}, map[string]any{"amount": 30, "participant_handles": []string{"bob", "cy", "dee"}})
	})
	onlyOneCreated(t, rs)
	out, _ = listReq(t, e.tok["cy"], "")
	if len(out) != 1 {
		t.Fatalf("cy has %d requests", len(out))
	}
	out, _ = listReq(t, e.tok["bob"], "")
	if len(out) != 2 {
		t.Fatalf("bob has %d requests", len(out))
	}
}

func TestR69_ConcurrentIdenticalPay(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 700))
	k := newKey()
	rs := fanout(50, func(int) (resp, error) {
		return send("POST", "/requests/"+id+"/pay", e.tok["ada"], map[string]string{"Idempotency-Key": k}, map[string]any{})
	})
	onlyOneCreated(t, rs)
	if bal(t, e.tok["ada"]) != 9300 || bal(t, e.tok["bob"]) != 3200 {
		t.Fatal("pay effect not once")
	}
}

func TestR69_ConcurrentIdenticalSettlement(t *testing.T) {
	e := setup(t)
	k := newKey()
	body := map[string]any{"transfers": []any{
		map[string]any{"from_handle": "ada", "to_handle": "bob", "amount": 100},
		map[string]any{"from_handle": "bob", "to_handle": "cy", "amount": 50},
	}}
	rs := fanout(40, func(int) (resp, error) {
		return send("POST", "/settlements", e.tok["op"], map[string]string{"Idempotency-Key": k}, body)
	})
	onlyOneCreated(t, rs)
	if bal(t, e.tok["ada"]) != 9900 || bal(t, e.tok["bob"]) != 2550 || bal(t, e.tok["cy"]) != 550 {
		t.Fatal("settlement effect not once")
	}
}
