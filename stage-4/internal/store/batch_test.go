package store

import (
	"errors"
	"testing"
	"time"

	"pocketful/internal/apierr"
)

// Two complete-or-not settlements in one batch: completeness of every
// settlement is reported before any instant mismatch, whatever the order.
func TestBatchChecksCompletenessBeforeInstants(t *testing.T) {
	s := fixture(t, `{"settlement_operator_ids":["u_a"],"users":[
 {"id":"u_a","email":"a@x.io","password":"correct horse","display_name":"A","handle":"a","balance":1000},
 {"id":"u_b","email":"b@x.io","password":"correct horse","display_name":"B","handle":"b","balance":1000}]}`)
	settle := func(tx *Tx) []string {
		v, err := tx.Settle([]Transfer{
			{FromHandle: "a", ToHandle: "b", Amount: 10, Visibility: VisibilityPublic},
			{FromHandle: "b", ToHandle: "a", Amount: 5, Visibility: VisibilityPublic},
		})
		if err != nil {
			t.Fatal(err)
		}
		return []string{v.Payments[0].PaymentID, v.Payments[1].PaymentID}
	}
	err := s.Update(func(tx *Tx) error {
		s1, s2 := settle(tx), settle(tx)
		at := NewInstant(time.Now().Add(-time.Hour))
		later := NewInstant(time.Now().Add(-time.Hour + time.Second))
		item := func(id string, when Instant) BatchItem {
			return BatchItem{PaymentID: id, Input: CorrectionInput{ExpectedRevision: 1, Amount: 1, EffectiveAt: when, Reason: "r"}}
		}
		mismatched := []BatchItem{item(s1[0], at), item(s1[1], later)}
		incomplete := item(s2[0], at)
		for name, items := range map[string][]BatchItem{
			"mismatched first": append(append([]BatchItem{}, mismatched...), incomplete),
			"incomplete first": append([]BatchItem{incomplete}, mismatched...),
		} {
			_, err := tx.CorrectBatch(items)
			var ae *apierr.Error
			if !errors.As(err, &ae) || ae.Code != "incomplete_settlement" {
				t.Errorf("%s: got %v, want incomplete_settlement", name, err)
			}
		}
		_, err := tx.CorrectBatch(append(append([]BatchItem{}, mismatched...), item(s2[0], at), item(s2[1], at)))
		var ae *apierr.Error
		if !errors.As(err, &ae) || ae.Code != "validation_failed" {
			t.Errorf("complete but mismatched: got %v, want validation_failed", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
