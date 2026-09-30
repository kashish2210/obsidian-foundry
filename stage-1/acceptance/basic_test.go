package acceptance

import (
	"strings"
	"testing"
)

func TestR11_Health(t *testing.T) {
	r := get(t, "/health", "")
	expect(t, r, 200)
	m := r.obj(t)
	if m["status"] != "ok" {
		t.Fatalf("want status ok, got %s", r)
	}
}

func TestR12_ResetReplacesAllState(t *testing.T) {
	e := setup(t)
	k := "reset-key-1"
	body := map[string]any{"to_handle": "bob", "amount": 100}
	expect(t, post(t, "/payments", e.tok["ada"], k, body), 201)
	mustRequest(t, e.tok["ada"], "bob", 50)

	reset(t, fixtureCur("EUR", 2, nil, fu{"eve", 700}, fu{"fay", 300}))
	// old users and tokens are gone
	expectErr(t, get(t, "/me", e.tok["ada"]), 401, "unauthenticated")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": pw}), 401, "unauthenticated")
	tok := login(t, "eve@example.com")
	if bal(t, tok) != 700 {
		t.Fatal("eve balance not 700")
	}
	// old handles are unknown
	expectErr(t, sendPay(t, tok, "ada", 1, nil), 404, "not_found")
	// feed and requests are empty
	a, _ := activity(t, tok, "")
	rq, _ := listReq(t, tok, "")
	if len(a) != 0 || len(rq) != 0 {
		t.Fatalf("state leaked across reset: %v %v", a, rq)
	}
	// idempotency records are cleared: same key is a first use (201), not a replay (200)
	reset(t, stdFixture())
	tokA := login(t, "ada@example.com")
	expect(t, post(t, "/payments", tokA, k, body), 201)
}

func TestR13_ResetRepeatedAndOldTokensInvalid(t *testing.T) {
	for i := 0; i < 3; i++ {
		e := setup(t)
		if bal(t, e.tok["ada"]) != 10000 {
			t.Fatalf("round %d: ada balance not fixture value", i)
		}
		mustPay(t, e.tok["ada"], "bob", 1000, nil)
		old := e.tok["ada"]
		e2 := setup(t)
		expect(t, get(t, "/me", e2.tok["ada"]), 200)
		if bal(t, e2.tok["ada"]) != 10000 || bal(t, e2.tok["bob"]) != 2500 {
			t.Fatalf("round %d: state not reset to fixture", i)
		}
		// same user id after reset: token from earlier session must not be honoured
		expectErr(t, get(t, "/me", old), 401, "unauthenticated")
	}
}

func TestR13_ResetClearsSignedUpUsers(t *testing.T) {
	e := setup(t)
	r := postNoKey(t, "/auth/signup", "", map[string]any{"email": "newbie@example.com", "password": "longenough", "display_name": "N"})
	expect(t, r, 201)
	tok := str(t, r.obj(t), "token")
	reset(t, stdFixture())
	expectErr(t, get(t, "/me", tok), 401, "unauthenticated")
	// email is free again
	expect(t, postNoKey(t, "/auth/signup", "", map[string]any{"email": "newbie@example.com", "password": "longenough", "display_name": "N"}), 201)
	_ = e
}

func TestR14_JSONContentType(t *testing.T) {
	e := setup(t)
	for _, r := range []resp{
		get(t, "/health", ""),
		get(t, "/me", e.tok["ada"]),
		get(t, "/me", ""), // error body
		sendPay(t, e.tok["ada"], "bob", 1, nil),
	} {
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		if !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type %q for %s", ct, r)
		}
		if !strings.Contains(strings.ReplaceAll(ct, " ", ""), "charset=utf-8") {
			t.Fatalf("Content-Type lacks charset=utf-8: %q", ct)
		}
	}
}

func TestR15_TimestampsRFC3339WithOffset(t *testing.T) {
	e := setup(t)
	p := mustPay(t, e.tok["ada"], "bob", 10, nil)
	checkTS(t, str(t, p, "created_at"))
	rq := mustRequest(t, e.tok["bob"], "ada", 10)
	checkTS(t, str(t, rq, "created_at"))
	r := postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 10, "participant_handles": []string{"ada", "bob"}})
	expect(t, r, 201)
	checkTS(t, str(t, r.obj(t), "created_at"))
	items, _ := activity(t, e.tok["ada"], "")
	for _, it := range items {
		checkTS(t, str(t, it, "created_at"))
	}
}

func TestR16_UnknownBodyFieldsIgnored(t *testing.T) {
	e := setup(t)
	expect(t, postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": "bob", "amount": 10, "bogus": []int{1}, "x": nil}), 201)
	expect(t, postK(t, "/requests", e.tok["ada"], map[string]any{"payer_handle": "bob", "amount": 10, "bogus": 1}), 201)
	expect(t, postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 10, "participant_handles": []string{"ada"}, "bogus": true}), 201)
	rq := mustRequest(t, e.tok["bob"], "ada", 5)
	expect(t, postK(t, "/requests/"+str(t, rq, "request_id")+"/pay", e.tok["ada"], map[string]any{"visibility": "public", "bogus": 1}), 201)
	expect(t, postNoKey(t, "/auth/signup", "", map[string]any{"email": "q@example.com", "password": "longenough", "display_name": "Q", "handle": "zzz", "bogus": 1}), 201)
	expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "bob@example.com", "password": pw, "bogus": 1}), 200)
	f := stdFixture()
	f["bogus"] = "x"
	reset(t, f)
}

func TestR17_UnknownQueryParamsIgnored(t *testing.T) {
	e := setup(t)
	expect(t, get(t, "/activity?foo=bar&limit=5", e.tok["ada"]), 200)
	expect(t, get(t, "/requests?zzz=1&direction=incoming", e.tok["ada"]), 200)
	expect(t, get(t, "/me?x=y", e.tok["ada"]), 200)
	expect(t, get(t, "/health?x=y", ""), 200)
}

func TestR18_IdsAreStringsUpTo64(t *testing.T) {
	e := setup(t)
	chk := func(m map[string]any, k string) {
		s := str(t, m, k)
		if s == "" || len(s) > 64 {
			t.Fatalf("id %q=%q has bad length", k, s)
		}
	}
	p := mustPay(t, e.tok["ada"], "bob", 10, nil)
	chk(p, "payment_id")
	chk(p, "from_user_id")
	rq := mustRequest(t, e.tok["bob"], "ada", 10)
	chk(rq, "request_id")
	r := postK(t, "/splits", e.tok["ada"], map[string]any{"amount": 10, "participant_handles": []string{"bob"}})
	expect(t, r, 201)
	chk(r.obj(t), "split_id")
	s := postNoKey(t, "/auth/signup", "", map[string]any{"email": "idtest@example.com", "password": "longenough", "display_name": "I"})
	expect(t, s, 201)
	chk(s.obj(t), "user_id")
	me := get(t, "/me", e.tok["ada"]).obj(t)
	chk(me, "user_id")
}

func TestR19_CurrencyAndMinorUnits(t *testing.T) {
	for _, c := range []struct {
		cur string
		mu  int
	}{{"EUR", 2}, {"JPY", 0}, {"BHD", 3}} {
		reset(t, fixtureCur(c.cur, c.mu, nil, fu{"ada", 5000}, fu{"bob", 0}))
		ta := login(t, "ada@example.com")
		me := get(t, "/me", ta).obj(t)
		if me["currency"] != c.cur || num(t, me, "minor_units") != int64(c.mu) || num(t, me, "balance") != 5000 {
			t.Fatalf("%s: bad /me %v", c.cur, me)
		}
		p := mustPay(t, ta, "bob", 1234, nil)
		if p["currency"] != c.cur {
			t.Fatalf("payment currency %v want %s", p["currency"], c.cur)
		}
		rq := mustRequest(t, ta, "bob", 1)
		if rq["currency"] != c.cur {
			t.Fatalf("request currency %v", rq["currency"])
		}
		r := postK(t, "/splits", ta, map[string]any{"amount": 100, "participant_handles": []string{"bob"}})
		expect(t, r, 201)
		if r.obj(t)["currency"] != c.cur {
			t.Fatalf("split currency")
		}
		if bal(t, ta) != 5000-1234 {
			t.Fatalf("balance wrong in %s", c.cur)
		}
	}
}

func TestR4_MoneyOnlyBetweenExistingWallets(t *testing.T) {
	e := setup(t)
	expectErr(t, sendPay(t, e.tok["ada"], "nobody", 10, nil), 404, "not_found")
	if e.sum(t) != e.total {
		t.Fatal("sum changed")
	}
}

func TestR20_AmountParsing(t *testing.T) {
	e := setup(t)
	// valid integral spellings
	for _, raw := range []string{`1000`, `1000.0`, `1e3`, `1E3`, `1.0e3`, `10e2`, `1000.00`, `0.1e4`} {
		e = setup(t)
		r := postK(t, "/payments", e.tok["ada"], `{"to_handle":"bob","amount":`+raw+`}`)
		expect(t, r, 201)
		if num(t, r.obj(t), "amount") != 1000 {
			t.Fatalf("amount %s not read as 1000: %s", raw, r)
		}
		if bal(t, e.tok["ada"]) != 9000 || bal(t, e.tok["bob"]) != 3500 {
			t.Fatalf("amount %s moved the wrong money", raw)
		}
		rr := postK(t, "/requests", e.tok["ada"], `{"payer_handle":"bob","amount":`+raw+`}`)
		expect(t, rr, 201)
		if num(t, rr.obj(t), "amount") != 1000 {
			t.Fatalf("request amount %s: %s", raw, rr)
		}
		sp := postK(t, "/splits", e.tok["ada"], `{"participant_handles":["bob"],"amount":`+raw+`}`)
		expect(t, sp, 201)
	}
	e = setup(t)
	for _, raw := range []string{`"1000"`, `true`, `false`, `null`, `10.5`, `1e-1`, `0`, `-1`, `-1000`, `0.0`, `1000000001`, `1e10`, `1e3.5`, `"1e3"`} {
		r := postK(t, "/payments", e.tok["ada"], `{"to_handle":"bob","amount":`+raw+`}`)
		if raw == `1e3.5` {
			expectErr(t, r, 400, "malformed_request") // not even valid JSON
			continue
		}
		expectErr(t, r, 422, "validation_failed")
		expectErr(t, postK(t, "/requests", e.tok["ada"], `{"payer_handle":"bob","amount":`+raw+`}`), 422, "validation_failed")
		expectErr(t, postK(t, "/splits", e.tok["ada"], `{"participant_handles":["bob"],"amount":`+raw+`}`), 422, "validation_failed")
	}
	// missing amount
	expectErr(t, postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": "bob"}), 422, "validation_failed")
	if e.sum(t) != e.total || bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("rejected payments changed balances")
	}
}

func TestR20_AmountBoundaries(t *testing.T) {
	reset(t, fixtureCur("EUR", 2, nil, fu{"ada", 5000000000}, fu{"bob", 0}))
	ta := login(t, "ada@example.com")
	expect(t, postK(t, "/payments", ta, `{"to_handle":"bob","amount":1000000000}`), 201)
	expect(t, postK(t, "/payments", ta, `{"to_handle":"bob","amount":1}`), 201)
	expectErr(t, postK(t, "/payments", ta, `{"to_handle":"bob","amount":1000000001}`), 422, "validation_failed")
	expect(t, postK(t, "/requests", ta, `{"payer_handle":"bob","amount":1000000000}`), 201)
	expectErr(t, postK(t, "/requests", ta, `{"payer_handle":"bob","amount":1000000001}`), 422, "validation_failed")
	expect(t, postK(t, "/splits", ta, `{"participant_handles":["bob","ada"],"amount":1000000000}`), 201)
	expectErr(t, postK(t, "/splits", ta, `{"participant_handles":["bob","ada"],"amount":1000000001}`), 422, "validation_failed")
	if bal(t, ta) != 5000000000-1000000001 {
		t.Fatalf("balance %d", bal(t, ta))
	}
}

func TestR33_LargeBalancesExact(t *testing.T) {
	const big = int64(4000000000000001) // < 2^53, not representable after lossy float math at ±1
	reset(t, fixtureCur("EUR", 2, nil, fu{"ada", big}, fu{"bob", big}))
	ta := login(t, "ada@example.com")
	tb := login(t, "bob@example.com")
	for i := 0; i < 3; i++ {
		mustPay(t, ta, "bob", 999999999, nil)
	}
	mustPay(t, tb, "ada", 1, nil)
	if got := bal(t, ta); got != big-3*999999999+1 {
		t.Fatalf("ada %d", got)
	}
	if got := bal(t, tb); got != big+3*999999999-1 {
		t.Fatalf("bob %d", got)
	}
}

func TestR22_R34_R35_R36_SeededFixture(t *testing.T) {
	fx := fixtureCur("EUR", 2, nil, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 77})
	fx["payments"] = []any{
		map[string]any{"id": "p_1", "from_user_id": "u_ada", "to_user_id": "u_bob", "amount": 500, "note": "coffee", "visibility": "public"},
		map[string]any{"id": "p_2", "from_user_id": "u_bob", "to_user_id": "u_cy", "amount": 40, "note": "secret", "visibility": "private"},
	}
	fx["requests"] = []any{
		map[string]any{"id": "rq_1", "requester_id": "u_bob", "payer_id": "u_ada", "amount": 1200, "note": "taxi", "status": "pending"},
		map[string]any{"id": "rq_2", "requester_id": "u_cy", "payer_id": "u_ada", "amount": 10, "note": "", "status": "declined"},
		map[string]any{"id": "rq_3", "requester_id": "u_ada", "payer_id": "u_bob", "amount": 11, "note": "", "status": "cancelled"},
		map[string]any{"id": "rq_4", "requester_id": "u_ada", "payer_id": "u_cy", "amount": 12, "note": "", "status": "paid"},
	}
	reset(t, fx)
	ta, tb, tc := login(t, "ada@example.com"), login(t, "bob@example.com"), login(t, "cy@example.com")
	// R36: balances are final, not replayed
	if bal(t, ta) != 10000 || bal(t, tb) != 2500 || bal(t, tc) != 77 {
		t.Fatal("seeded balances were altered by replaying seeded payments")
	}
	// payments visible with fixture ids
	fa, _ := activity(t, ta, "")
	ids := idSet(t, fa, "payment_id")
	if !ids["p_1"] || ids["p_2"] || len(fa) != 1 {
		t.Fatalf("ada feed: %v", ids)
	}
	p1 := fa[0]
	if p1["note"] != "coffee" || num(t, p1, "amount") != 500 || p1["visibility"] != "public" || p1["from_handle"] != "ada" || p1["to_handle"] != "bob" {
		t.Fatalf("seeded payment wrong: %v", p1)
	}
	fb, _ := activity(t, tb, "")
	ids = idSet(t, fb, "payment_id")
	if !ids["p_1"] || !ids["p_2"] {
		t.Fatalf("bob feed: %v", ids)
	}
	fc, _ := activity(t, tc, "")
	if ids = idSet(t, fc, "payment_id"); !ids["p_1"] || !ids["p_2"] {
		t.Fatalf("cy feed (receiver of private): %v", ids)
	}
	// requests with every status honoured
	ra, _ := listReq(t, ta, "")
	if len(ra) != 4 {
		t.Fatalf("ada requests: %d", len(ra))
	}
	byID := map[string]map[string]any{}
	for _, x := range ra {
		byID[str(t, x, "request_id")] = x
	}
	for id, st := range map[string]string{"rq_1": "pending", "rq_2": "declined", "rq_3": "cancelled", "rq_4": "paid"} {
		if byID[id] == nil || byID[id]["status"] != st {
			t.Fatalf("request %s: %v", id, byID[id])
		}
	}
	// seeded pending request can be paid; a declined one cannot
	pay := postK(t, "/requests/rq_1/pay", ta, map[string]any{})
	expect(t, pay, 201)
	if bal(t, ta) != 8800 || bal(t, tb) != 3700 {
		t.Fatal("paying seeded request moved wrong money")
	}
	expectErr(t, postK(t, "/requests/rq_2/pay", ta, map[string]any{}), 409, "request_not_pending")
	// R35 login
	expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "cy@example.com", "password": pw}), 200)
	me := get(t, "/me", ta).obj(t)
	if me["handle"] != "ada" || me["display_name"] != "Ada" || me["user_id"] != "u_ada" {
		t.Fatalf("me: %v", me)
	}
}

func TestR37_NegativeFixtureBalanceRejected(t *testing.T) {
	e := setup(t)
	bad := fixtureCur("EUR", 2, nil, fu{"eve", 100}, fu{"fay", -1})
	r, err := sendWith(ctlClient, "POST", "/_test/reset", "", nil, bad)
	if err != nil {
		t.Fatal(err)
	}
	expectErr(t, r, 422, "validation_failed")
	// previous state intact
	expect(t, get(t, "/me", e.tok["ada"]), 200)
	if bal(t, e.tok["ada"]) != 10000 || bal(t, e.tok["bob"]) != 2500 {
		t.Fatal("state changed by rejected reset")
	}
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "eve@example.com", "password": pw}), 401, "unauthenticated")
	// zero balance is fine
	reset(t, fixtureCur("EUR", 2, nil, fu{"eve", 0}))
}

func TestR39_ErrorBodyShape(t *testing.T) {
	e := setup(t)
	for _, r := range []resp{
		get(t, "/me", ""),
		get(t, "/activity?limit=0", e.tok["ada"]),
		sendPay(t, e.tok["ada"], "ghost", 1, nil),
		call(t, "POST", "/payments", e.tok["ada"], nil, `{"to_handle":"bob","amount":1}`),
		postNoKey(t, "/requests/nope/cancel", e.tok["ada"], nil),
	} {
		if r.Status < 400 || r.Status > 499 {
			t.Fatalf("want 4xx got %s", r)
		}
		m := r.obj(t)
		er, ok := m["error"].(map[string]any)
		if !ok {
			t.Fatalf("no error object: %s", r)
		}
		if c, ok := er["code"].(string); !ok || c == "" {
			t.Fatalf("no code: %s", r)
		}
		if c, ok := er["message"].(string); !ok || c == "" {
			t.Fatalf("no message: %s", r)
		}
	}
}
