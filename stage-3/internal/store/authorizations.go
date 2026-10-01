package store

import (
	"time"

	"pocketful/internal/apierr"
)

// AuthorizationView is the API representation of an authorization.
type AuthorizationView struct {
	AuthorizationID string   `json:"authorization_id"`
	FromUserID      string   `json:"from_user_id"`
	FromHandle      string   `json:"from_handle"`
	ToUserID        string   `json:"to_user_id"`
	ToHandle        string   `json:"to_handle"`
	Amount          int64    `json:"amount"`
	CapturedAmount  int64    `json:"captured_amount"`
	RemainingAmount int64    `json:"remaining_amount"`
	Currency        string   `json:"currency"`
	Note            string   `json:"note"`
	Visibility      string   `json:"visibility"`
	Status          string   `json:"status"`
	ExpiresAt       string   `json:"expires_at"`
	PaymentID       *string  `json:"payment_id"`
	PaymentIDs      []string `json:"payment_ids"`
	CreatedAt       string   `json:"created_at"`
}

// CaptureInput is a validated capture. A nil Amount means the whole
// remainder; Final releases whatever is not captured.
type CaptureInput struct {
	Amount *int64
	Final  bool
}

// AuthorizationFilter narrows GET /authorizations. Empty fields match all.
type AuthorizationFilter struct {
	Direction string // "outgoing" (caller pays), "incoming" (caller receives) or ""
	Status    string
}

// held is the amount of the user's wallet reserved by open, unexpired
// authorizations.
func (tx *Tx) held(userID string) int64 {
	var sum int64
	for _, a := range tx.st.Authorizations {
		if a.FromID == userID && a.holding(tx.now) {
			sum += a.remaining()
		}
	}
	return sum
}

// available is the part of the wallet that can fund new spending.
func (tx *Tx) available(u *User) int64 { return u.Balance - tx.held(u.ID) }

func (tx *Tx) authorizationView(a *Authorization) AuthorizationView {
	from, to := tx.st.usersByID[a.FromID], tx.st.usersByID[a.ToID]
	v := AuthorizationView{
		AuthorizationID: a.ID, FromUserID: a.FromID, FromHandle: from.Handle,
		ToUserID: a.ToID, ToHandle: to.Handle, Amount: a.Amount,
		CapturedAmount: a.CapturedAmount, Currency: tx.st.Currency, Note: a.Note,
		Visibility: a.Visibility, Status: a.effectiveStatus(tx.now),
		ExpiresAt: a.ExpiresAt, PaymentIDs: append([]string{}, a.PaymentIDs...),
		CreatedAt: a.CreatedAt,
	}
	if a.holding(tx.now) {
		v.RemainingAmount = a.remaining()
	}
	if n := len(a.PaymentIDs); n > 0 {
		v.PaymentID = &a.PaymentIDs[n-1]
	}
	return v
}

// CreateAuthorization places a hold on the caller's available funds.
func (tx *Tx) CreateAuthorization(callerID string, in PaymentInput) (AuthorizationView, error) {
	caller, err := tx.user(callerID)
	if err != nil {
		return AuthorizationView{}, err
	}
	if in.ToHandle == caller.Handle {
		return AuthorizationView{}, apierr.New(422, "self_payment", "cannot authorize a payment to yourself")
	}
	to := tx.st.usersByHandle[in.ToHandle]
	if to == nil {
		return AuthorizationView{}, apierr.NotFound("no user has handle %q", in.ToHandle)
	}
	if tx.available(caller) < in.Amount {
		return AuthorizationView{}, apierr.Conflict("insufficient_funds", "available balance is below the amount")
	}
	created := tx.now.Truncate(time.Second)
	expires := created.Add(time.Duration(tx.st.AuthTTL) * time.Second)
	a := &Authorization{
		ID: tx.st.newAuthorizationID(), FromID: caller.ID, ToID: to.ID, Amount: in.Amount,
		Note: in.Note, Visibility: in.Visibility, Status: AuthOpen, PaymentIDs: []string{},
		CreatedAt: formatTime(created), ExpiresAt: formatTime(expires), expires: expires,
	}
	tx.st.Authorizations = append(tx.st.Authorizations, a)
	tx.st.authzByID[a.ID] = a
	return tx.authorizationView(a), nil
}

// Capture moves money for an open authorization from the payer to the
// receiver. Captures may spend the money held for them.
func (tx *Tx) Capture(callerID, authID string, in CaptureInput) (PaymentView, error) {
	if _, err := tx.user(callerID); err != nil {
		return PaymentView{}, err
	}
	a := tx.st.authzByID[authID]
	if a == nil {
		return PaymentView{}, apierr.NotFound("no such authorization")
	}
	if a.ToID != callerID {
		return PaymentView{}, apierr.Forbidden("only the receiver may capture this authorization")
	}
	if a.Status != AuthOpen {
		return PaymentView{}, apierr.Conflict("authorization_not_open", "authorization is %s", a.Status)
	}
	if !a.holding(tx.now) {
		return PaymentView{}, apierr.Conflict("authorization_expired", "authorization has expired")
	}
	amount := a.remaining()
	if in.Amount != nil {
		amount = *in.Amount
	}
	if amount > a.remaining() {
		return PaymentView{}, apierr.New(422, "capture_exceeds_authorization", "amount exceeds the remaining %d", a.remaining())
	}
	from, to := tx.st.usersByID[a.FromID], tx.st.usersByID[a.ToID]
	if err := checkCredit(to, amount); err != nil {
		return PaymentView{}, err
	}
	authRef := a.ID
	p := tx.transfer(from, to, amount, a.Note, a.Visibility, links{authorizationID: &authRef}, tx.stamp())
	a.CapturedAmount += amount
	a.PaymentIDs = append(a.PaymentIDs, p.ID)
	if in.Final || a.remaining() == 0 {
		a.Status = AuthCaptured
	}
	return tx.paymentView(p), nil
}

// Void lets the payer release an open authorization; repeating is a no-op.
func (tx *Tx) Void(callerID, authID string) (AuthorizationView, error) {
	if _, err := tx.user(callerID); err != nil {
		return AuthorizationView{}, err
	}
	a := tx.st.authzByID[authID]
	if a == nil {
		return AuthorizationView{}, apierr.NotFound("no such authorization")
	}
	if a.FromID != callerID {
		return AuthorizationView{}, apierr.Forbidden("only the payer may void this authorization")
	}
	switch status := a.effectiveStatus(tx.now); status {
	case AuthVoided:
	case AuthOpen:
		a.Status = AuthVoided
	default:
		return AuthorizationView{}, apierr.Conflict("authorization_not_open", "authorization is %s", status)
	}
	return tx.authorizationView(a), nil
}

// ListAuthorizations returns the caller's authorizations, newest first.
func (tx *Tx) ListAuthorizations(callerID string, f AuthorizationFilter, pg Page) ([]AuthorizationView, bool) {
	out := []AuthorizationView{}
	skipped, more := 0, false
	for i := len(tx.st.Authorizations) - 1; i >= 0; i-- {
		a := tx.st.Authorizations[i]
		outgoing, incoming := a.FromID == callerID, a.ToID == callerID
		if !outgoing && !incoming {
			continue
		}
		if f.Direction == "outgoing" && !outgoing || f.Direction == "incoming" && !incoming {
			continue
		}
		if f.Status != "" && a.effectiveStatus(tx.now) != f.Status {
			continue
		}
		if skipped < pg.Offset {
			skipped++
			continue
		}
		if len(out) == pg.Limit {
			more = true
			break
		}
		out = append(out, tx.authorizationView(a))
	}
	return out, more
}
