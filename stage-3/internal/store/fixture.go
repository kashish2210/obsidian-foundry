package store

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"pocketful/internal/apierr"
	"pocketful/internal/money"
	"pocketful/internal/password"
)

// Number is a JSON number literal. Unlike json.Number it refuses strings,
// so a fixture cannot smuggle in "600" where a number is required.
type Number string

func (n Number) String() string { return string(n) }

// UnmarshalJSON accepts only number literals; null leaves the field empty.
func (n *Number) UnmarshalJSON(b []byte) error {
	switch {
	case string(b) == "null":
		*n = ""
	case len(b) > 0 && (b[0] == '-' || b[0] >= '0' && b[0] <= '9'):
		*n = Number(b)
	default:
		return fmt.Errorf("expected a number, got %s", b)
	}
	return nil
}

// Fixture is the body of POST /_test/reset.
type Fixture struct {
	Currency              string                 `json:"currency"`
	MinorUnits            *int                   `json:"minor_units"`
	Users                 []FixtureUser          `json:"users"`
	Payments              []FixturePayment       `json:"payments"`
	Requests              []FixtureRequest       `json:"requests"`
	SettlementOperatorIDs []string               `json:"settlement_operator_ids"`
	AuthorizationTTL      *Number                `json:"authorization_ttl_seconds"`
	Authorizations        []FixtureAuthorization `json:"authorizations"`
}

// FixtureUser is a seeded user with a plaintext password.
type FixtureUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Handle      string `json:"handle"`
	Balance     Number `json:"balance"`
}

// FixturePayment is a seeded payment; it never touches balances.
type FixturePayment struct {
	ID         string  `json:"id"`
	FromUserID string  `json:"from_user_id"`
	ToUserID   string  `json:"to_user_id"`
	Amount     Number  `json:"amount"`
	Note       string  `json:"note"`
	Visibility string  `json:"visibility"`
	RequestID  *string `json:"request_id"`
	CreatedAt  string  `json:"created_at"`
}

// FixtureRequest is a seeded request in any status.
type FixtureRequest struct {
	ID          string  `json:"id"`
	RequesterID string  `json:"requester_id"`
	PayerID     string  `json:"payer_id"`
	Amount      Number  `json:"amount"`
	Note        string  `json:"note"`
	Status      string  `json:"status"`
	PaymentID   *string `json:"payment_id"`
	CreatedAt   string  `json:"created_at"`
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

func fixtureInt(n Number, what string) (int64, error) {
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
	reset := NewInstant(time.Now())
	if err := f.applyAuthorizations(&d, reset); err != nil {
		return Data{}, err
	}
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
	if err := hashUsers(d.Users); err != nil {
		return Data{}, err
	}
	for _, fp := range f.Payments {
		amt, err := fixtureInt(fp.Amount, "payment amount")
		if err != nil {
			return Data{}, err
		}
		vis := fp.Visibility
		if vis == "" {
			vis = VisibilityPublic
		}
		at, err := seededInstant(fp.CreatedAt, reset, "payment created_at")
		if err != nil {
			return Data{}, err
		}
		d.Payments = append(d.Payments, &Payment{ID: fp.ID, FromID: fp.FromUserID, ToID: fp.ToUserID, Amount: amt, Note: fp.Note, Visibility: vis, RequestID: fp.RequestID, CreatedAt: at})
	}
	// Oldest first, so later API payments always follow the seeded ones.
	sort.SliceStable(d.Payments, func(i, j int) bool { return d.Payments[i].CreatedAt.T.Before(d.Payments[j].CreatedAt.T) })
	for _, fr := range f.Requests {
		amt, err := fixtureInt(fr.Amount, "request amount")
		if err != nil {
			return Data{}, err
		}
		status := fr.Status
		if status == "" {
			status = StatusPending
		}
		at, err := seededInstant(fr.CreatedAt, reset, "request created_at")
		if err != nil {
			return Data{}, err
		}
		d.Requests = append(d.Requests, &Request{ID: fr.ID, RequesterID: fr.RequesterID, PayerID: fr.PayerID, Amount: amt, Note: fr.Note, Status: status, PaymentID: fr.PaymentID, CreatedAt: at})
	}
	return d, nil
}

// hashUsers replaces each user's plaintext password with its hash. A user
// without a password keeps an empty hash and cannot log in.
func hashUsers(users []*User) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
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
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			u.PasswordHash = h
		}()
	}
	wg.Wait()
	return firstErr
}

// seededInstant reads an optional seeded timestamp: omission means the
// reset time, and a time after the reset is rejected.
func seededInstant(text string, reset Instant, what string) (Instant, error) {
	if text == "" {
		return reset, nil
	}
	at, err := ParseInstant(text)
	if err != nil {
		return Instant{}, apierr.Invalid("%s must be an RFC 3339 instant with an offset", what)
	}
	if at.T.After(reset.T) {
		return Instant{}, apierr.Invalid("%s must not be in the future", what)
	}
	return at, nil
}

// stamp is the current time as an API timestamp, to the microsecond.
func (tx *Tx) stamp() Instant { return NewInstant(tx.now) }
