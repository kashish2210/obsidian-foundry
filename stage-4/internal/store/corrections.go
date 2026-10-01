package store

import (
	"time"

	"pocketful/internal/apierr"
)

// CorrectionInput is a validated payment correction.
type CorrectionInput struct {
	ExpectedRevision int64
	Amount           int64
	EffectiveAt      Instant
	Reason           string
}

// RevisionView is the API representation of one payment revision.
type RevisionView struct {
	PaymentID   string  `json:"payment_id"`
	Revision    int     `json:"revision"`
	Amount      int64   `json:"amount"`
	EffectiveAt Instant `json:"effective_at"`
	RecordedAt  Instant `json:"recorded_at"`
	Reason      string  `json:"reason"`
}

func revisionView(p *Payment, r *Revision) RevisionView {
	return RevisionView{
		PaymentID: p.ID, Revision: r.Revision, Amount: r.Amount,
		EffectiveAt: r.EffectiveAt, RecordedAt: r.RecordedAt, Reason: r.Reason,
	}
}

// Correct appends a revision to a payment sent by the caller. The change
// in amount moves between the same two wallets in the same step; it is
// refused, leaving everything untouched, if the sender or receiver cannot
// afford it now or would have been overdrawn at any point in the past.
func (tx *Tx) Correct(callerID, paymentID string, in CorrectionInput) (RevisionView, error) {
	if _, err := tx.user(callerID); err != nil {
		return RevisionView{}, err
	}
	p := tx.st.paymentsByID[paymentID]
	if p == nil {
		return RevisionView{}, apierr.NotFound("no such payment")
	}
	if p.FromID != callerID {
		return RevisionView{}, apierr.Forbidden("only the sender may correct this payment")
	}
	if p.SettlementID != nil || p.AuthorizationID != nil {
		return RevisionView{}, apierr.New(422, "linked_payment_immutable", "settlement and capture payments cannot be corrected")
	}
	prev := p.latest()
	if int64(prev.Revision) != in.ExpectedRevision {
		return RevisionView{}, apierr.Conflict("stale_revision", "payment is at revision %d", prev.Revision)
	}
	from, to := tx.st.usersByID[p.FromID], tx.st.usersByID[p.ToID]
	diff := in.Amount - prev.Amount
	// A larger amount debits the sender, a smaller one the receiver.
	debited, credited := from, to
	if diff < 0 {
		debited, credited = to, from
	}
	if abs := absInt(diff); tx.available(debited) < abs {
		return RevisionView{}, apierr.Conflict("insufficient_funds", "available balance is below the correction")
	} else if err := checkCredit(credited, abs); err != nil {
		return RevisionView{}, err
	}

	recorded := tx.now
	if !recorded.After(prev.RecordedAt.T) {
		recorded = prev.RecordedAt.T.Add(time.Microsecond)
	}
	rev := &Revision{
		Revision: prev.Revision + 1, Amount: in.Amount, EffectiveAt: in.EffectiveAt,
		RecordedAt: NewInstant(recorded), Reason: in.Reason,
	}
	p.Revisions = append(p.Revisions, rev)
	debited.Balance -= absInt(diff)
	credited.Balance += absInt(diff)
	if tx.overdrawnAtSomePoint(from) || tx.overdrawnAtSomePoint(to) {
		p.Revisions = p.Revisions[:len(p.Revisions)-1]
		debited.Balance += absInt(diff)
		credited.Balance -= absInt(diff)
		return RevisionView{}, apierr.Conflict("historical_overdraft", "the correction would have overdrawn a wallet in the past")
	}
	return revisionView(p, rev), nil
}

func absInt(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// overdrawnAtSomePoint reports whether the user's total or available
// balance is negative at any boundary: an effective time of a payment, or
// an event in the life of one of their holds. Everything effective at a
// boundary counts together.
func (tx *Tx) overdrawnAtSomePoint(u *User) bool {
	items := tx.ledger(u.ID, nil)
	boundaries := []time.Time{tx.now}
	for _, it := range items {
		boundaries = append(boundaries, it.effective())
	}
	for _, a := range tx.st.Authorizations {
		if a.FromID != u.ID {
			continue
		}
		boundaries = append(boundaries, a.CreatedAt.T, a.ExpiresAt.T)
		if a.ClosedAt != nil {
			boundaries = append(boundaries, a.ClosedAt.T)
		}
		for _, pid := range a.PaymentIDs {
			if c := tx.st.paymentsByID[pid]; c != nil {
				boundaries = append(boundaries, c.CreatedAt.T)
			}
		}
	}
	for _, at := range boundaries {
		total := balanceAt(*u.Opening, items, at)
		if total < 0 || total-tx.heldAt(u.ID, at, nil) < 0 {
			return true
		}
	}
	return false
}

// Revisions lists a payment's history to either of its parties; anyone
// else is told the payment does not exist.
func (tx *Tx) Revisions(callerID, paymentID string) ([]RevisionView, error) {
	if _, err := tx.user(callerID); err != nil {
		return nil, err
	}
	p := tx.st.paymentsByID[paymentID]
	if p == nil || (p.FromID != callerID && p.ToID != callerID) {
		return nil, apierr.NotFound("no such payment")
	}
	out := make([]RevisionView, len(p.Revisions))
	for i, r := range p.Revisions {
		out[i] = revisionView(p, r)
	}
	return out, nil
}
