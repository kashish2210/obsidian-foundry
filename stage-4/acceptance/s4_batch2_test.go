package acceptance

import (
	"testing"
	"time"
)

// R257 step 2: completeness of EVERY settlement in the batch outranks the identical-instant rule,
// regardless of the order the items are listed in.
func TestR257_CompletenessOfAllSettlementsBeforeInstantRule(t *testing.T) {
	e := setupUsers(t, []string{"u_op"}, fu{"ada", 10000}, fu{"bob", 2500}, fu{"cy", 500}, fu{"dee", 0}, fu{"op", 0})
	op := e.tok["op"]
	s1 := settle(t, op, batch(tr("ada", "bob", 100), tr("bob", "cy", 50)))
	expect(t, s1, 201)
	s2 := settle(t, op, batch(tr("ada", "cy", 30), tr("cy", "dee", 10)))
	expect(t, s2, 201)
	id := func(r resp, i int) string {
		return str(t, r.obj(t)["payments"].([]any)[i].(map[string]any), "payment_id")
	}
	a0, a1, b0, b1 := id(s1, 0), id(s1, 1), id(s2, 0), id(s2, 1)
	x := parseTS(t, str(t, s2.obj(t), "committed_at")).Add(-time.Hour)
	// S1 complete but with mismatched instants; S2 incomplete (one member only)
	mism := []any{item(a0, 1, 100, x, "s1"), item(a1, 1, 50, x.Add(time.Second), "s1")}
	partial := item(b0, 1, 30, x, "s2")
	before := stateOfF(t, e)
	expectErr(t, cbatch(t, op, cbody(append(append([]any{}, mism...), partial)...)), 422, "incomplete_settlement")
	expectErr(t, cbatch(t, op, cbody(partial, mism[0], mism[1])), 422, "incomplete_settlement")
	expectErr(t, cbatch(t, op, cbody(mism[0], partial, mism[1])), 422, "incomplete_settlement")
	// once every settlement is complete, the mismatched instants are a validation_failed, in any order
	full2 := []any{partial, item(b1, 1, 10, x, "s2")}
	expectErr(t, cbatch(t, op, cbody(append(append([]any{}, mism...), full2...)...)), 422, "validation_failed")
	expectErr(t, cbatch(t, op, cbody(append(append([]any{}, full2...), mism...)...)), 422, "validation_failed")
	// and a second settlement that is the mismatched one
	mism2 := []any{item(b0, 1, 30, x, "s2"), item(b1, 1, 10, x.Add(-time.Microsecond), "s2")}
	okS1 := []any{item(a0, 1, 100, x, "s1"), item(a1, 1, 50, x, "s1")}
	expectErr(t, cbatch(t, op, cbody(append(append([]any{}, okS1...), mism2...)...)), 422, "validation_failed")
	if len(revisionsOf(t, e.tok["ada"], a0)) != 1 {
		t.Fatal("rejected batches changed history")
	}
	_ = before
	// both settlements complete and consistent (different instants BETWEEN settlements are fine)
	mustBatch(t, op, cbody(append(append([]any{}, okS1...), item(b0, 1, 30, x.Add(time.Minute), "s2"), item(b1, 1, 10, x.Add(time.Minute), "s2"))...))
}
