package store

import (
	"encoding/json"
	"testing"
	"time"
)

func fixture(t *testing.T, raw string) *Store {
	t.Helper()
	var f Fixture
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	s := New()
	if err := s.Reset(f); err != nil {
		t.Fatal(err)
	}
	return s
}

const twoUsers = `"users":[
 {"id":"u_a","email":"a@x.io","password":"correct horse","display_name":"A","handle":"a","balance":1000},
 {"id":"u_b","email":"b@x.io","password":"correct horse","display_name":"B","handle":"b","balance":0}]`

func TestHoldsAndPartialCapture(t *testing.T) {
	s := fixture(t, `{`+twoUsers+`,"authorization_ttl_seconds":600}`)
	err := s.Update(func(tx *Tx) error {
		a, err := tx.CreateAuthorization("u_a", PaymentInput{ToHandle: "b", Amount: 600, Visibility: "public"})
		if err != nil {
			return err
		}
		me, _ := tx.Me("u_a")
		if me.Available != 400 || me.Held != 600 || me.Total != 1000 {
			t.Errorf("after hold: %+v", me)
		}
		if _, err := tx.SendPayment("u_a", PaymentInput{ToHandle: "b", Amount: 500, Visibility: "public"}); err == nil {
			t.Error("held funds funded a payment")
		}
		amount := int64(200)
		if _, err := tx.Capture("u_b", a.AuthorizationID, CaptureInput{Amount: &amount, Final: true}); err != nil {
			return err
		}
		me, _ = tx.Me("u_a")
		if me.Available != 800 || me.Held != 0 || me.Total != 800 {
			t.Errorf("after final capture of 200: %+v", me)
		}
		if _, err := tx.Capture("u_b", a.AuthorizationID, CaptureInput{Final: true}); err == nil {
			t.Error("second capture succeeded")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSeededExpiryAndOverHold(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	s := fixture(t, `{`+twoUsers+`,"authorizations":[{"id":"a_1","from_user_id":"u_a","to_user_id":"u_b","amount":900,"status":"open","expires_at":"`+past+`"}]}`)
	_ = s.View(func(tx *Tx) error {
		me, _ := tx.Me("u_a")
		if me.Available != 1000 || me.Held != 0 {
			t.Errorf("expired hold still held: %+v", me)
		}
		list, _ := tx.ListAuthorizations("u_a", AuthorizationFilter{Status: "expired"}, Page{Limit: 10})
		if len(list) != 1 {
			t.Errorf("expired listing: %d", len(list))
		}
		return nil
	})
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	var f Fixture
	raw := `{` + twoUsers + `,"authorizations":[{"id":"a_1","from_user_id":"u_a","to_user_id":"u_b","amount":1001,"status":"open","expires_at":"` + future + `"}]}`
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(f); err == nil {
		t.Error("reset accepted holds above the balance")
	}
}
