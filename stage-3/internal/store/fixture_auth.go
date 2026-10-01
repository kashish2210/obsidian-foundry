package store

import (
	"pocketful/internal/apierr"
	"pocketful/internal/money"
)

// FixtureAuthorization is a seeded authorization in any status.
type FixtureAuthorization struct {
	ID             string   `json:"id"`
	FromUserID     string   `json:"from_user_id"`
	ToUserID       string   `json:"to_user_id"`
	Amount         Number   `json:"amount"`
	CapturedAmount Number   `json:"captured_amount"`
	Note           string   `json:"note"`
	Visibility     string   `json:"visibility"`
	Status         string   `json:"status"`
	ExpiresAt      string   `json:"expires_at"`
	PaymentID      *string  `json:"payment_id"`
	PaymentIDs     []string `json:"payment_ids"`
	CreatedAt      *string  `json:"created_at"`
}

func (fa FixtureAuthorization) toAuthorization(reset Instant) (*Authorization, error) {
	amount, err := fixtureInt(fa.Amount, "authorization amount")
	if err != nil {
		return nil, err
	}
	captured, err := fixtureInt(fa.CapturedAmount, "authorization captured_amount")
	if err != nil {
		return nil, err
	}
	vis := fa.Visibility
	if vis == "" {
		vis = VisibilityPublic
	}
	status := fa.Status
	if status == "" {
		status = AuthOpen
	}
	ids := append([]string{}, fa.PaymentIDs...)
	if len(ids) == 0 && fa.PaymentID != nil {
		ids = append(ids, *fa.PaymentID)
	}
	expires, err := ParseInstant(fa.ExpiresAt)
	if err != nil {
		return nil, apierr.Invalid("authorization expires_at must be an RFC 3339 instant with an offset")
	}
	created, err := seededInstant(fa.CreatedAt, reset, "authorization created_at")
	if err != nil {
		return nil, err
	}
	a := &Authorization{
		ID: fa.ID, FromID: fa.FromUserID, ToID: fa.ToUserID, Amount: amount,
		CapturedAmount: captured, Note: fa.Note, Visibility: vis, Status: status,
		ExpiresAt: expires, PaymentIDs: ids, CreatedAt: created,
	}
	if status != AuthOpen {
		// A seeded closed hold has no lifecycle to reconstruct: it closed
		// at reset, or at its deadline if that already passed.
		closed := reset
		if status == AuthExpired && !expires.T.After(reset.T) {
			closed = expires
		}
		a.ClosedAt = &closed
	}
	return a, nil
}

// applyAuthorizations copies the fixture's ttl and seeded authorizations
// into d.
func (f Fixture) applyAuthorizations(d *Data, reset Instant) error {
	if f.AuthorizationTTL != nil {
		ttl, ok := money.ParseIntegral(f.AuthorizationTTL.String())
		if !ok || ttl < 1 || ttl > MaxAuthorizationTTL {
			return apierr.Invalid("authorization_ttl_seconds must be a positive integer")
		}
		d.AuthTTL = int(ttl)
	}
	for _, fa := range f.Authorizations {
		a, err := fa.toAuthorization(reset)
		if err != nil {
			return err
		}
		d.Authorizations = append(d.Authorizations, a)
	}
	return nil
}
