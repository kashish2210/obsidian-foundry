package store

import "pocketful/internal/apierr"

// Refund returns part or all of a payment to its sender. Only the
// receiver may refund, and all refunds of a payment together may not
// exceed its current corrected amount. The refund is an ordinary payment
// in the opposite direction, funded from the receiver's available money.
func (tx *Tx) Refund(callerID, paymentID string, amount int64) (PaymentView, error) {
	if _, err := tx.user(callerID); err != nil {
		return PaymentView{}, err
	}
	p := tx.st.paymentsByID[paymentID]
	if p == nil {
		return PaymentView{}, apierr.NotFound("no such payment")
	}
	if p.ToID != callerID {
		return PaymentView{}, apierr.Forbidden("only the receiver may refund this payment")
	}
	if p.RefundOf != nil {
		return PaymentView{}, apierr.New(422, "invalid_refund_target", "a refund cannot be refunded")
	}
	if tx.st.refunded[p.ID]+amount > p.latest().Amount {
		return PaymentView{}, apierr.New(422, "refund_exceeds_payment", "refunds would exceed the payment's amount of %d", p.latest().Amount)
	}
	receiver, sender := tx.st.usersByID[p.ToID], tx.st.usersByID[p.FromID]
	if tx.available(receiver) < amount {
		return PaymentView{}, apierr.Conflict("insufficient_funds", "available balance is below the refund")
	}
	if err := checkCredit(sender, amount); err != nil {
		return PaymentView{}, err
	}
	target := p.ID
	refund := tx.transfer(receiver, sender, amount, p.Note, p.Visibility, links{refundOf: &target}, tx.stamp())
	tx.st.refunded[p.ID] += amount
	return tx.paymentView(refund), nil
}
