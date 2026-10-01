package acceptance

import (
	"testing"
	"time"
)

// ---- refunds ----

func refund(t testing.TB, tok, pid string, amount any) resp {
	t.Helper()
	return postK(t, "/payments/"+pid+"/refunds", tok, map[string]any{"amount": amount})
}

func mustRefund(t testing.TB, tok, pid string, amount int64) map[string]any {
	t.Helper()
	r := refund(t, tok, pid, amount)
	expect(t, r, 201)
	return r.obj(t)
}

// ---- batches ----

func item(pid string, rev int64, amount any, eff any, reason any) map[string]any {
	if tm, ok := eff.(time.Time); ok {
		eff = tsMicro(tm)
	}
	return map[string]any{"payment_id": pid, "expected_revision": rev, "amount": amount, "effective_at": eff, "reason": reason}
}

func cbody(items ...any) map[string]any { return map[string]any{"corrections": items} }

func cbatch(t testing.TB, tok string, body any) resp {
	t.Helper()
	return postK(t, "/correction-batches", tok, body)
}

func mustBatch(t testing.TB, tok string, body any) map[string]any {
	t.Helper()
	r := cbatch(t, tok, body)
	expect(t, r, 201)
	return r.obj(t)
}

// ---- scenario F: a plain refund playground ----

func setupF(t testing.TB) *env {
	t.Helper()
	return setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
}

// refundsOf returns the refund payments (refund_of == pid) visible in the caller's statement.
func refundsOf(t testing.TB, tok, pid string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, en := range entriesOf(t, fullStmt(t, tok, "")) {
		p := en["payment"].(map[string]any)
		if p["refund_of"] == pid {
			out = append(out, p)
		}
	}
	return out
}

// ---- scenario S: seeded history plus a settlement ----
//
//	b_1 ada->bob 500 at D-3d, b_2 bob->cy 300 at D-2d, b_3 cy->dee 100 at D-1d
//	finals ada 5000, bob 3000, cy 2000, dee 100, op 0 (operator)
//	openings: ada 5500, bob 2800, cy 1800, dee 0
//	then a settlement (op): ada->bob 100, bob->cy 50, cy->dee 20  (members)
type scenS struct {
	e              *env
	t1, t2, t3     time.Time
	sid            string
	m              []string // member payment ids in input order
	commit         time.Time
	settlementResp resp
	settlementKey  string
	settlementBody map[string]any
}

func setupS(t testing.TB) *scenS {
	t.Helper()
	s := &scenS{t1: ago(3 * day), t2: ago(2 * day), t3: ago(1 * day)}
	s.e = setupP(t, []any{
		seedP("b_1", "ada", "bob", 500, "public", s.t1),
		seedP("b_2", "bob", "cy", 300, "public", s.t2),
		seedP("b_3", "cy", "dee", 100, "private", s.t3),
	}, fu{"ada", 5000}, fu{"bob", 3000}, fu{"cy", 2000}, fu{"dee", 100}, fu{"op", 0})
	s.settlementKey = newKey()
	s.settlementBody = batch(tr("ada", "bob", 100), tr("bob", "cy", 50), tr("cy", "dee", 20))
	s.settlementResp = post(t, "/settlements", s.e.tok["op"], s.settlementKey, s.settlementBody)
	expect(t, s.settlementResp, 201)
	m := s.settlementResp.obj(t)
	s.sid = str(t, m, "settlement_id")
	s.commit = parseTS(t, str(t, m, "committed_at"))
	for _, p := range m["payments"].([]any) {
		s.m = append(s.m, str(t, p.(map[string]any), "payment_id"))
	}
	return s
}

func stateOfS(t testing.TB, s *scenS, pids ...string) map[string]any {
	t.Helper()
	out := map[string]any{}
	for h, tk := range s.e.tok {
		st := fullStmt(t, tk, "")
		delete(st, "snapshot")
		out["stmt-"+h] = st
		out["me-"+h] = meAt(t, tk, "")
		out["past-"+h] = balAt(t, tk, "as_of="+qe(tsStr(ago(60*hour))))
	}
	for _, pid := range pids {
		var revs []map[string]any
		for _, h := range []string{"ada", "bob", "cy", "dee"} {
			r := get(t, "/payments/"+pid+"/revisions", s.e.tok[h])
			if r.Status == 200 {
				out["rev-"+pid] = r.obj(t)["revisions"]
				break
			}
		}
		_ = revs
	}
	return out
}
