package acceptance

import (
	"strings"
	"testing"
)

func signup(t testing.TB, email, password string) resp {
	t.Helper()
	return postNoKey(t, "/auth/signup", "", map[string]any{"email": email, "password": password, "display_name": "Zed"})
}

func TestR51_Signup(t *testing.T) {
	setup(t)
	r := signup(t, "zed@example.com", "longenough")
	expect(t, r, 201)
	m := r.obj(t)
	if str(t, m, "user_id") == "" || m["display_name"] != "Zed" || str(t, m, "token") == "" {
		t.Fatalf("bad signup body %s", r)
	}
	tok := str(t, m, "token")
	me := get(t, "/me", tok).obj(t)
	if me["user_id"] != m["user_id"] || num(t, me, "balance") != 0 || me["handle"] != "zed" || me["display_name"] != "Zed" {
		t.Fatalf("/me after signup: %v", me)
	}
}

func TestR52_Login(t *testing.T) {
	setup(t)
	r := postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": pw})
	expect(t, r, 200)
	m := r.obj(t)
	if m["user_id"] != "u_ada" || m["display_name"] != "Ada" || str(t, m, "token") == "" {
		t.Fatalf("bad login body %s", r)
	}
	// signup then login
	expect(t, signup(t, "zed@example.com", "longenough"), 201)
	expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "zed@example.com", "password": "longenough"}), 200)
}

func TestR53_EmailTaken(t *testing.T) {
	setup(t)
	expectErr(t, signup(t, "ada@example.com", "longenough"), 409, "email_taken")
	expect(t, signup(t, "zed@example.com", "longenough"), 201)
	expectErr(t, signup(t, "zed@example.com", "longenough"), 409, "email_taken")
	// the original account is unharmed (password not overwritten)
	expect(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "zed@example.com", "password": "longenough"}), 200)
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": "longenough"}), 401, "unauthenticated")
}

func TestR54_ShortPassword(t *testing.T) {
	setup(t)
	expectErr(t, signup(t, "a1@example.com", "1234567"), 422, "validation_failed")
	expectErr(t, signup(t, "a1@example.com", ""), 422, "validation_failed")
	expect(t, signup(t, "a1@example.com", "12345678"), 201) // not created by the failures above
	// 8 characters counted as characters: 8 emoji are valid
	expect(t, signup(t, "a2@example.com", strings.Repeat("😀", 8)), 201)
}

func TestR55_BadEmail(t *testing.T) {
	setup(t)
	for _, em := range []string{"", "plain", "a@", "@b.com", "two@@b.com", "no at.com"} {
		expectErr(t, signup(t, em, "longenough"), 422, "validation_failed")
	}
	// missing fields
	expectErr(t, postNoKey(t, "/auth/signup", "", map[string]any{"password": "longenough", "display_name": "x"}), 422, "validation_failed")
	expectErr(t, postNoKey(t, "/auth/signup", "", map[string]any{"email": "q@example.com", "display_name": "x"}), 422, "validation_failed")
}

func TestR40_SignupWrongTypes(t *testing.T) {
	setup(t)
	expectErr(t, postNoKey(t, "/auth/signup", "", map[string]any{"email": 5, "password": "longenough", "display_name": "x"}), 400, "malformed_request")
	expectErr(t, postNoKey(t, "/auth/signup", "", map[string]any{"email": "q@example.com", "password": 12345678, "display_name": "x"}), 400, "malformed_request")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": []string{"a"}, "password": "x"}), 400, "malformed_request")
}

func TestR47_UnparseableBodies(t *testing.T) {
	e := setup(t)
	bad := []string{`{`, `{"to_handle":`, `not json`, `[]`, `[{"to_handle":"bob"}]`, `"str"`, `42`, `null`}
	for _, b := range bad {
		expectErr(t, postNoKey(t, "/auth/signup", "", b), 400, "malformed_request")
		expectErr(t, postNoKey(t, "/auth/login", "", b), 400, "malformed_request")
		for _, p := range []string{"/payments", "/requests", "/splits", "/settlements"} {
			tok := e.tok["ada"]
			if p == "/settlements" {
				tok = e.tok["op"]
			}
			expectErr(t, postK(t, p, tok, b), 400, "malformed_request")
		}
	}
	// Reading chosen: an empty body is an unparseable body.
	expectErr(t, call(t, "POST", "/payments", e.tok["ada"], map[string]string{"Idempotency-Key": newKey()}, nil), 400, "malformed_request")
	expectErr(t, postNoKey(t, "/auth/login", "", nil), 400, "malformed_request")
}

func TestR56_LoginFailures(t *testing.T) {
	setup(t)
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": "wrong password"}), 401, "unauthenticated")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ghost@example.com", "password": pw}), 401, "unauthenticated")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": pw + " "}), 401, "unauthenticated")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@example.com", "password": strings.ToUpper(pw)}), 401, "unauthenticated")
}

func TestR23_DerivedHandle(t *testing.T) {
	setup(t)
	cases := []struct{ email, handle string }{
		{"Grace.Hopper+cobol@example.com", "grace_hopper_cobol"},
		{"UPPER_case9@example.com", "upper_case9"},
		{"abcdefghijklmnopqrstuvwxyz@example.com", "abcdefghijklmnopqrst"},
		{"a-b.c@example.com", "a_b_c"},
		{"x@example.com", "x"},
		{"très@example.com", "tr__s"}, // é is one char outside [a-z0-9_] -> one underscore (see comment below)
	}
	for _, c := range cases {
		r := signup(t, c.email, "longenough")
		expect(t, r, 201)
		me := get(t, "/me", str(t, r.obj(t), "token")).obj(t)
		got := me["handle"].(string)
		if c.email == "très@example.com" {
			// Ambiguity: "every character" is a Unicode char (->"tr_s") or byte (->"tr__s").
			// Reading chosen: per character, but accept the per-byte reading to avoid over-asserting.
			if got != "tr_s" && got != "tr__s" {
				t.Fatalf("handle for %q = %q", c.email, got)
			}
			continue
		}
		if got != c.handle {
			t.Fatalf("handle for %q = %q want %q", c.email, got, c.handle)
		}
	}
	// derived handle is usable by others
	e := setup(t)
	r := signup(t, "Zed.Z@example.com", "longenough")
	expect(t, r, 201)
	p := mustPay(t, e.tok["ada"], "zed_z", 10, nil)
	if p["to_handle"] != "zed_z" {
		t.Fatalf("payment to derived handle: %v", p)
	}
}

func TestR24_NewUserStartsAtZeroAndReceives(t *testing.T) {
	e := setup(t)
	r := signup(t, "newbie@example.com", "longenough")
	expect(t, r, 201)
	tok := str(t, r.obj(t), "token")
	if bal(t, tok) != 0 {
		t.Fatal("new user balance not 0")
	}
	mustPay(t, e.tok["ada"], "newbie", 250, nil)
	if bal(t, tok) != 250 {
		t.Fatal("did not receive")
	}
	rq := mustRequest(t, e.tok["ada"], "newbie", 100)
	if rq["status"] != "pending" {
		t.Fatal("request on new user not pending")
	}
	// new user can request from others immediately, and a request made to them appears
	in, _ := listReq(t, tok, "?direction=incoming")
	if len(in) != 1 {
		t.Fatalf("incoming %d", len(in))
	}
	// paying with 0 balance is insufficient
	expectErr(t, sendPay(t, signupTok(t, "broke@example.com"), "ada", 1, nil), 409, "insufficient_funds")
}

func signupTok(t testing.TB, email string) string {
	t.Helper()
	r := signup(t, email, "longenough")
	expect(t, r, 201)
	return str(t, r.obj(t), "token")
}

func TestR57_HandleTaken(t *testing.T) {
	setup(t)
	// ada's handle is taken by the fixture
	expectErr(t, signup(t, "ada@other.org", "longenough"), 409, "handle_taken")
	expectErr(t, signup(t, "ADA@other.org", "longenough"), 409, "handle_taken")
	expectErr(t, signup(t, "Ada@third.org", "longenough"), 409, "handle_taken")
	// no account created: login with that email fails and the email stays free
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "ada@other.org", "password": "longenough"}), 401, "unauthenticated")
	// two emails deriving the same handle
	expect(t, signup(t, "x.y@a.com", "longenough"), 201)
	expectErr(t, signup(t, "x_y@b.com", "longenough"), 409, "handle_taken")
	expectErr(t, postNoKey(t, "/auth/login", "", map[string]any{"email": "x_y@b.com", "password": "longenough"}), 401, "unauthenticated")
	// truncation collisions count too
	expect(t, signup(t, "abcdefghijklmnopqrstuvw@a.com", "longenough"), 201)
	expectErr(t, signup(t, "abcdefghijklmnopqrstXYZ@a.com", "longenough"), 409, "handle_taken")
}

func TestR58_AuthRequired(t *testing.T) {
	e := setup(t)
	rq := mustRequest(t, e.tok["bob"], "ada", 5)
	rid := str(t, rq, "request_id")
	hdrs := []map[string]string{
		nil,
		{"Authorization": "Bearer"},
		{"Authorization": "Bearer "},
		{"Authorization": "Bearer not-a-real-token"},
		{"Authorization": "Basic " + e.tok["ada"]},
		{"Authorization": e.tok["ada"]},
	}
	paths := []struct{ m, p string }{
		{"GET", "/me"}, {"GET", "/activity"}, {"GET", "/requests"},
		{"POST", "/payments"}, {"POST", "/requests"}, {"POST", "/splits"}, {"POST", "/settlements"},
		{"POST", "/requests/" + rid + "/pay"}, {"POST", "/requests/" + rid + "/decline"}, {"POST", "/requests/" + rid + "/cancel"},
	}
	for _, h := range hdrs {
		for _, p := range paths {
			hh := map[string]string{"Idempotency-Key": newKey()}
			for k, v := range h {
				hh[k] = v
			}
			var body any
			if p.m == "POST" {
				body = map[string]any{}
			}
			expectErr(t, call(t, p.m, p.p, "", hh, body), 401, "unauthenticated")
		}
	}
	// no money moved, request untouched
	if bal(t, e.tok["ada"]) != 10000 {
		t.Fatal("unauthenticated call moved money")
	}
	// public endpoints need no auth
	expect(t, get(t, "/health", ""), 200)
}

func TestR59_MultipleTokens(t *testing.T) {
	setup(t)
	var toks []string
	for i := 0; i < 3; i++ {
		toks = append(toks, login(t, "ada@example.com"))
	}
	su := signup(t, "zed@example.com", "longenough")
	expect(t, su, 201)
	zt := str(t, su.obj(t), "token")
	lr := postNoKey(t, "/auth/login", "", map[string]any{"email": "zed@example.com", "password": "longenough"})
	expect(t, lr, 200)
	zt2 := str(t, lr.obj(t), "token")
	for _, tk := range append(toks, zt, zt2) {
		expect(t, get(t, "/me", tk), 200)
	}
	// both sessions act on the same wallet
	mustPay(t, toks[0], "bob", 100, nil)
	mustPay(t, toks[1], "bob", 100, nil)
	if bal(t, toks[2]) != 9800 {
		t.Fatal("sessions see different wallets")
	}
	// logging in again did not invalidate earlier tokens
	expect(t, get(t, "/me", toks[0]), 200)
}
