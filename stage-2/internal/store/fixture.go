package store

import (
	"encoding/json"
	"sync"
	"time"

	"pocketful/internal/apierr"
	"pocketful/internal/money"
	"pocketful/internal/password"
)

// Fixture is the body of POST /_test/reset.
type Fixture struct {
	Currency              string           `json:"currency"`
	MinorUnits            *int             `json:"minor_units"`
	Users                 []FixtureUser    `json:"users"`
	Payments              []FixturePayment `json:"payments"`
	Requests              []FixtureRequest `json:"requests"`
	SettlementOperatorIDs []string         `json:"settlement_operator_ids"`
}

// FixtureUser is a seeded user with a plaintext password.
type FixtureUser struct {
	ID          string      `json:"id"`
	Email       string      `json:"email"`
	Password    string      `json:"password"`
	DisplayName string      `json:"display_name"`
	Handle      string      `json:"handle"`
	Balance     json.Number `json:"balance"`
}

// FixturePayment is a seeded payment; it never touches balances.
type FixturePayment struct {
	ID         string      `json:"id"`
	FromUserID string      `json:"from_user_id"`
	ToUserID   string      `json:"to_user_id"`
	Amount     json.Number `json:"amount"`
	Note       string      `json:"note"`
	Visibility string      `json:"visibility"`
	RequestID  *string     `json:"request_id"`
	CreatedAt  string      `json:"created_at"`
}

// FixtureRequest is a seeded request in any status.
type FixtureRequest struct {
	ID          string      `json:"id"`
	RequesterID string      `json:"requester_id"`
	PayerID     string      `json:"payer_id"`
	Amount      json.Number `json:"amount"`
	Note        string      `json:"note"`
	Status      string      `json:"status"`
	PaymentID   *string     `json:"payment_id"`
	CreatedAt   string      `json:"created_at"`
}

// Reset replaces all state with the fixture. The store is unchanged when
// the fixture is invalid.
func (s *Store) Reset(f Fixture) error {
	d, err := f.toData()
	if err != nil {
		return err
	}
	st, err := newState(d)
	if err != nil {
		return err
	}
	s.replace(st)
	return nil
}

func fixtureInt(n json.Number, what string) (int64, error) {
	if n == "" {
		return 0, nil
	}
	v, ok := money.ParseIntegral(n.String())
	if !ok || v < 0 {
		return 0, apierr.Invalid("%s must be a non-negative integer", what)
	}
	return v, nil
}

func (f Fixture) toData() (Data, error) {
	d := Data{Currency: f.Currency, MinorUnits: 2, Operators: append([]string{}, f.SettlementOperatorIDs...)}
	if d.Currency == "" {
		d.Currency = "EUR"
	}
	if f.MinorUnits != nil {
		d.MinorUnits = *f.MinorUnits
	}
	created := now()
	for _, fu := range f.Users {
		bal, err := fixtureInt(fu.Balance, "user balance")
		if err != nil {
			return Data{}, err
		}
		h := fu.Handle
		if h == "" {
			h = DerivedHandle(fu.Email)
		}
		// PasswordHash holds the plaintext until hashUsers runs.
		d.Users = append(d.Users, &User{ID: fu.ID, Email: fu.Email, DisplayName: fu.DisplayName, Handle: h, PasswordHash: fu.Password, Balance: bal})
	}
	hashUsers(d.Users)
	for _, fp := range f.Payments {
		amt, err := fixtureInt(fp.Amount, "payment amount")
		if err != nil {
			return Data{}, err
		}
		vis := fp.Visibility
		if vis == "" {
			vis = VisibilityPublic
		}
		d.Payments = append(d.Payments, &Payment{ID: fp.ID, FromID: fp.FromUserID, ToID: fp.ToUserID, Amount: amt, Note: fp.Note, Visibility: vis, RequestID: fp.RequestID, CreatedAt: orDefault(fp.CreatedAt, created)})
	}
	for _, fr := range f.Requests {
		amt, err := fixtureInt(fr.Amount, "request amount")
		if err != nil {
			return Data{}, err
		}
		status := fr.Status
		if status == "" {
			status = StatusPending
		}
		d.Requests = append(d.Requests, &Request{ID: fr.ID, RequesterID: fr.RequesterID, PayerID: fr.PayerID, Amount: amt, Note: fr.Note, Status: status, PaymentID: fr.PaymentID, CreatedAt: orDefault(fr.CreatedAt, created)})
	}
	return d, nil
}

// hashUsers replaces each user's plaintext password with its hash. A user
// without a password gets an empty hash and cannot log in.
func hashUsers(users []*User) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, u := range users {
		if u == nil || u.PasswordHash == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			h, err := password.Hash(u.PasswordHash)
			if err != nil {
				h = ""
			}
			u.PasswordHash = h
		}()
	}
	wg.Wait()
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05-07:00") }
