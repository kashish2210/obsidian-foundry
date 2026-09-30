package acceptance

import "testing"

// R26: a request is pending, then exactly one of paid/declined/cancelled; terminal states never change.
func TestR26_RequestLifecycleTerminalStates(t *testing.T) {
	e := setup(t)
	paid := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	decl := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	canc := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	expect(t, postK(t, "/requests/"+paid+"/pay", e.tok["ada"], map[string]any{}), 201)
	expect(t, postNoKey(t, "/requests/"+decl+"/decline", e.tok["ada"], nil), 200)
	expect(t, postNoKey(t, "/requests/"+canc+"/cancel", e.tok["bob"], nil), 200)
	for id, st := range map[string]string{paid: "paid", decl: "declined", canc: "cancelled"} {
		expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["ada"], map[string]any{}), 409, "request_not_pending")
		if st != "declined" {
			expectErr(t, postNoKey(t, "/requests/"+id+"/decline", e.tok["ada"], nil), 409, "request_not_pending")
		}
		if st != "cancelled" {
			expectErr(t, postNoKey(t, "/requests/"+id+"/cancel", e.tok["bob"], nil), 409, "request_not_pending")
		}
		out, _ := listReq(t, e.tok["ada"], "?status="+st+"&limit=200")
		if len(out) != 1 || out[0]["request_id"] != id {
			t.Fatalf("%s not in status %s", id, st)
		}
	}
	// only the payer pays/declines, only the requester cancels
	open := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	expectErr(t, postK(t, "/requests/"+open+"/pay", e.tok["bob"], map[string]any{}), 403, "forbidden")
	expectErr(t, postNoKey(t, "/requests/"+open+"/cancel", e.tok["ada"], nil), 403, "forbidden")
}

// R41: missing/empty Idempotency-Key is 400 missing_idempotency_key on all five write paths.
func TestR41_MissingIdempotencyKeyAllPaths(t *testing.T) { TestR61_MissingIdempotencyKey(t) }

// R42: missing, malformed or unknown bearer token is 401 unauthenticated.
func TestR42_Unauthenticated(t *testing.T) { TestR58_AuthRequired(t) }

// R43: the generic status/code pairs 403, 404, 409 (idempotency_key_reuse), 422.
func TestR43_GenericErrorCodes(t *testing.T) {
	e := setup(t)
	id := rid(t, mustRequest(t, e.tok["bob"], "ada", 10))
	expectErr(t, postK(t, "/requests/"+id+"/pay", e.tok["cy"], map[string]any{}), 403, "forbidden")
	expectErr(t, sendPay(t, e.tok["ada"], "ghost", 1, nil), 404, "not_found")
	expectErr(t, postK(t, "/requests/nope/pay", e.tok["ada"], map[string]any{}), 404, "not_found")
	k := newKey()
	expect(t, post(t, "/payments", e.tok["ada"], k, map[string]any{"to_handle": "bob", "amount": 1}), 201)
	expectErr(t, post(t, "/payments", e.tok["ada"], k, map[string]any{"to_handle": "bob", "amount": 2}), 409, "idempotency_key_reuse")
	expectErr(t, postK(t, "/payments", e.tok["ada"], map[string]any{"to_handle": "bob"}), 422, "validation_failed")
	expectErr(t, get(t, "/requests?limit=0", e.tok["ada"]), 422, "validation_failed")
}

// R44: correct JSON type with an invalid format or out-of-range value is 422, not 400.
func TestR44_InvalidValueOfCorrectTypeIs422(t *testing.T) {
	e := setup(t)
	expectErr(t, sendPay(t, e.tok["ada"], "bob", 1000000001, nil), 422, "validation_failed")
	expectErr(t, sendPay(t, e.tok["ada"], "bob", -1, nil), 422, "validation_failed")
	expectErr(t, sendPay(t, e.tok["ada"], "bob", 1, map[string]any{"note": strs(201)}), 422, "validation_failed")
	expectErr(t, post(t, "/payments", e.tok["ada"], strs(256), map[string]any{"to_handle": "bob", "amount": 1}), 422, "validation_failed")
	expectErr(t, get(t, "/activity?limit=201", e.tok["ada"]), 422, "validation_failed")
	expectErr(t, get(t, "/activity?offset=-1", e.tok["ada"]), 422, "validation_failed")
	expectErr(t, signup(t, "x@nodomain", "short"), 422, "validation_failed")
}
