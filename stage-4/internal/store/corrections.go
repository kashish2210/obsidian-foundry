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
	PaymentID         string  `json:"payment_id"`
	Revision          int     `json:"revision"`
	Amount            int64   `json:"amount"`
	EffectiveAt       Instant `json:"effective_at"`
	RecordedAt        Instant `json:"recorded_at"`
	Reason            string  `json:"reason"`
	CorrectionBatchID *string `json:"correction_batch_id"`
}

func revisionView(p *Payment, r *Revision) RevisionView {
	return RevisionView{
		PaymentID: p.ID, Revision: r.Revision, Amount: r.Amount,
		EffectiveAt: r.EffectiveAt, RecordedAt: r.RecordedAt, Reason: r.Reason,
		CorrectionBatchID: r.CorrectionBatchID,
	}
}

// checkTarget runs the checks every correction makes against its payment,
// in precedence order: linked payments are immutable, the expected
// revision must be the latest, and the new amount cannot fall below what
// has already been refunded. Settlement members are corrected only in a
// batch.
func (tx *Tx) checkTarget(p *Payment, in CorrectionInput, inBatch bool) error {
	if p.AuthorizationID != nil || p.RefundOf != nil || (p.SettlementID != nil && !inBatch) {
		return apierr.New(422, "linked_payment_immutable", "this payment cannot be corrected on its own")
	}
	if prev := p.latest(); int64(prev.Revision) != in.ExpectedRevision {
		return apierr.Conflict("stale_revision", "payment is at revision %d", prev.Revision)
	}
	if refunded := tx.st.refunded[p.ID]; in.Amount < refunded {
		return apierr.New(422, "refund_exceeds_payment", "%d of this payment has already been refunded", refunded)
	}
	return nil
}

// shift moves diff from the receiver to the sender's side of a payment:
// the sender is debited diff and the receiver credited it (a negative diff
// does the opposite).
func (tx *Tx) shift(p *Payment, diff int64) {
	tx.st.usersByID[p.FromID].Balance -= diff
	tx.st.usersByID[p.ToID].Balance += diff
}

// recordedAfter returns the instant for a new revision: now, pushed past
// the previous recorded time of every payment it replaces.
func (tx *Tx) recordedAfter(payments ...*Payment) time.Time {
	recorded := tx.now
	for _, p := range payments {
		if prev := p.latest().RecordedAt.T; !recorded.After(prev) {
			recorded = prev.Add(time.Microsecond)
		}
	}
	return recorded
}

// Correct appends a revision to a payment sent by the caller. The change
// in amount moves between the same two wallets in the same step; it is
// refused, leaving everything untouched, if the debited wallet cannot
// afford it now or a wallet would have been overdrawn in the past.
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
	if err := tx.checkTarget(p, in, false); err != nil {
		return RevisionView{}, err
	}
	diff := in.Amount - p.latest().Amount
	// A larger amount debits the sender, a smaller one the receiver.
	debited, credited := tx.st.usersByID[p.FromID], tx.st.usersByID[p.ToID]
	if diff < 0 {
		debited, credited = credited, debited
	}
	if tx.available(debited) < absInt(diff) {
		return RevisionView{}, apierr.Conflict("insufficient_funds", "available balance is below the correction")
	}
	if err := checkCredit(credited, absInt(diff)); err != nil {
		return RevisionView{}, err
	}
	rev := &Revision{
		Revision: p.latest().Revision + 1, Amount: in.Amount, EffectiveAt: in.EffectiveAt,
		RecordedAt: NewInstant(tx.recordedAfter(p)), Reason: in.Reason,
	}
	p.Revisions = append(p.Revisions, rev)
	tx.shift(p, diff)
	if tx.overdrawnAtSomePoint(tx.st.usersByID[p.FromID]) || tx.overdrawnAtSomePoint(tx.st.usersByID[p.ToID]) {
		p.Revisions = p.Revisions[:len(p.Revisions)-1]
		tx.shift(p, -diff)
		return RevisionView{}, apierr.Conflict("historical_overdraft", "the correction would have overdrawn a wallet in the past")
	}
	return revisionView(p, rev), nil
}

// BatchItem is one entry of a correction batch. Invalid carries the field
// validation error of the entry so the first failing entry wins in input
// order.
type BatchItem struct {
	PaymentID string
	Input     CorrectionInput
	Invalid   error
}

// CorrectionBatch records a committed batch.
type CorrectionBatch struct {
	ID         string   `json:"id"`
	RecordedAt Instant  `json:"recorded_at"`
	PaymentIDs []string `json:"payment_ids"`
}

// BatchView is the API representation of a committed batch.
type BatchView struct {
	CorrectionBatchID string         `json:"correction_batch_id"`
	RecordedAt        Instant        `json:"recorded_at"`
	Revisions         []RevisionView `json:"revisions"`
}

// CorrectBatch corrects several payments together or none at all. Failures
// are reported in a fixed precedence: the first failing item, settlement
// completeness, current affordability of the combined effect, and finally
// the historical boundaries.
func (tx *Tx) CorrectBatch(items []BatchItem) (BatchView, error) {
	payments := make([]*Payment, len(items))
	inBatch := map[string]bool{}
	for i, it := range items {
		if it.Invalid != nil {
			return BatchView{}, it.Invalid
		}
		p := tx.st.paymentsByID[it.PaymentID]
		if p == nil {
			return BatchView{}, apierr.NotFound("no payment %q", it.PaymentID)
		}
		if err := tx.checkTarget(p, it.Input, true); err != nil {
			return BatchView{}, err
		}
		payments[i] = p
		inBatch[p.ID] = true
	}
	if err := tx.checkSettlements(items, payments, inBatch); err != nil {
		return BatchView{}, err
	}
	diffs := make([]int64, len(items))
	net := map[*User]int64{}
	for i, p := range payments {
		diffs[i] = items[i].Input.Amount - p.latest().Amount
		net[tx.st.usersByID[p.FromID]] -= diffs[i]
		net[tx.st.usersByID[p.ToID]] += diffs[i]
	}
	for u, delta := range net {
		if delta < 0 && tx.available(u)+delta < 0 {
			return BatchView{}, apierr.Conflict("insufficient_funds", "available balance is below the combined corrections")
		}
		if delta > 0 {
			if err := checkCredit(u, delta); err != nil {
				return BatchView{}, err
			}
		}
	}

	batchID := tx.st.newBatchID()
	recorded := NewInstant(tx.recordedAfter(payments...))
	view := BatchView{CorrectionBatchID: batchID, RecordedAt: recorded, Revisions: make([]RevisionView, len(items))}
	batch := &CorrectionBatch{ID: batchID, RecordedAt: recorded, PaymentIDs: make([]string, len(items))}
	for i, p := range payments {
		rev := &Revision{
			Revision: p.latest().Revision + 1, Amount: items[i].Input.Amount,
			EffectiveAt: items[i].Input.EffectiveAt, RecordedAt: recorded,
			Reason: items[i].Input.Reason, CorrectionBatchID: &batchID,
		}
		p.Revisions = append(p.Revisions, rev)
		tx.shift(p, diffs[i])
		view.Revisions[i] = revisionView(p, rev)
		batch.PaymentIDs[i] = p.ID
	}
	for u := range net {
		if tx.overdrawnAtSomePoint(u) {
			for i, p := range payments {
				p.Revisions = p.Revisions[:len(p.Revisions)-1]
				tx.shift(p, -diffs[i])
			}
			return BatchView{}, apierr.Conflict("historical_overdraft", "the corrections would have overdrawn a wallet in the past")
		}
	}
	tx.st.CorrectionBatches = append(tx.st.CorrectionBatches, batch)
	tx.st.batches[batchID] = batch
	return view, nil
}

// checkSettlements enforces that correcting a settlement member means
// correcting all of its members, to the same effective instant.
func (tx *Tx) checkSettlements(items []BatchItem, payments []*Payment, inBatch map[string]bool) error {
	instants := map[string]time.Time{}
	for i, p := range payments {
		if p.SettlementID == nil {
			continue
		}
		for _, member := range tx.st.settlements[*p.SettlementID].PaymentIDs {
			if !inBatch[member] {
				return apierr.New(422, "incomplete_settlement", "settlement %s needs every member corrected together", *p.SettlementID)
			}
		}
		at := items[i].Input.EffectiveAt.T
		if first, seen := instants[*p.SettlementID]; seen && !first.Equal(at) {
			return apierr.Invalid("members of settlement %s need the same effective instant", *p.SettlementID)
		}
		instants[*p.SettlementID] = at
	}
	return nil
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
