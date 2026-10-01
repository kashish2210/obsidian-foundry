package store

import (
	"pocketful/internal/apierr"
	"pocketful/internal/money"
)

// PaymentView is the API representation of a payment.
type PaymentView struct {
	PaymentID       string  `json:"payment_id"`
	FromUserID      string  `json:"from_user_id"`
	FromHandle      string  `json:"from_handle"`
	ToUserID        string  `json:"to_user_id"`
	ToHandle        string  `json:"to_handle"`
	Amount          int64   `json:"amount"`
	Currency        string  `json:"currency"`
	Note            string  `json:"note"`
	Visibility      string  `json:"visibility"`
	RequestID       *string `json:"request_id"`
	SettlementID    *string `json:"settlement_id"`
	AuthorizationID *string `json:"authorization_id"`
	CreatedAt       Instant `json:"created_at"`
}

// PaymentInput is a validated direct payment.
type PaymentInput struct {
	ToHandle   string
	Amount     int64
	Note       string
	Visibility string
}

// Page selects a window of a newest-first listing.
type Page struct {
	Limit  int
	Offset int
}

func (tx *Tx) paymentView(p *Payment) PaymentView {
	from, to := tx.st.usersByID[p.FromID], tx.st.usersByID[p.ToID]
	return PaymentView{
		PaymentID: p.ID, FromUserID: p.FromID, FromHandle: from.Handle,
		ToUserID: p.ToID, ToHandle: to.Handle, Amount: p.Amount,
		Currency: tx.st.Currency, Note: p.Note, Visibility: p.Visibility,
		RequestID: p.RequestID, SettlementID: p.SettlementID, AuthorizationID: p.AuthorizationID, CreatedAt: p.CreatedAt,
	}
}

// links say what a payment was created for; all nil for a direct payment.
type links struct {
	requestID, settlementID, authorizationID *string
}

// transfer moves money and records the payment. Callers must have checked
// that from can afford amount.
func (tx *Tx) transfer(from, to *User, amount int64, note, visibility string, l links, createdAt Instant) *Payment {
	from.Balance -= amount
	to.Balance += amount
	p := &Payment{
		ID: tx.st.newPaymentID(), FromID: from.ID, ToID: to.ID, Amount: amount,
		Note: note, Visibility: visibility, RequestID: l.requestID,
		SettlementID: l.settlementID, AuthorizationID: l.authorizationID, CreatedAt: createdAt,
		Revisions: []*Revision{{Revision: 1, Amount: amount, EffectiveAt: createdAt, RecordedAt: createdAt}},
	}
	tx.st.Payments = append(tx.st.Payments, p)
	tx.st.paymentsByID[p.ID] = p
	return p
}

// checkCredit rejects a credit that would push a wallet past the safe range.
func checkCredit(to *User, amount int64) error {
	if to.Balance+amount > money.MaxSafe {
		return apierr.Invalid("recipient balance would exceed the supported range")
	}
	return nil
}

// SendPayment moves money from the caller to the recipient.
func (tx *Tx) SendPayment(callerID string, in PaymentInput) (PaymentView, error) {
	caller, err := tx.user(callerID)
	if err != nil {
		return PaymentView{}, err
	}
	if in.ToHandle == caller.Handle {
		return PaymentView{}, apierr.New(422, "self_payment", "cannot pay yourself")
	}
	to := tx.st.usersByHandle[in.ToHandle]
	if to == nil {
		return PaymentView{}, apierr.NotFound("no user has handle %q", in.ToHandle)
	}
	if tx.available(caller) < in.Amount {
		return PaymentView{}, apierr.Conflict("insufficient_funds", "balance is below the amount")
	}
	if err := checkCredit(to, in.Amount); err != nil {
		return PaymentView{}, err
	}
	p := tx.transfer(caller, to, in.Amount, in.Note, in.Visibility, links{}, tx.stamp())
	return tx.paymentView(p), nil
}

// Activity lists the payments visible to the caller, newest first.
func (tx *Tx) Activity(callerID string, pg Page) ([]PaymentView, bool) {
	out := []PaymentView{}
	skipped, more := 0, false
	for i := len(tx.st.Payments) - 1; i >= 0; i-- {
		p := tx.st.Payments[i]
		if p.Visibility != VisibilityPublic && p.FromID != callerID && p.ToID != callerID {
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
		out = append(out, tx.paymentView(p))
	}
	return out, more
}
