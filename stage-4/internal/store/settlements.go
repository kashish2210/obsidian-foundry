package store

import "pocketful/internal/apierr"

// SettlementView is the API representation of a committed settlement.
type SettlementView struct {
	SettlementID string        `json:"settlement_id"`
	CommittedAt  Instant       `json:"committed_at"`
	Payments     []PaymentView `json:"payments"`
}

// Transfer is one entry of a settlement. Invalid carries the field
// validation error of the entry, reported in input order with the lookup
// errors so the first failing entry always wins.
type Transfer struct {
	FromHandle string
	ToHandle   string
	Amount     int64
	Note       string
	Visibility string
	Invalid    error
}

type resolvedTransfer struct {
	from, to *User
	Transfer
}

// Settle commits all transfers together or none of them.
func (tx *Tx) Settle(transfers []Transfer) (SettlementView, error) {
	resolved := make([]resolvedTransfer, len(transfers))
	for i, t := range transfers {
		if t.Invalid != nil {
			return SettlementView{}, t.Invalid
		}
		from, to := tx.st.usersByHandle[t.FromHandle], tx.st.usersByHandle[t.ToHandle]
		if from == nil || to == nil {
			return SettlementView{}, apierr.NotFound("transfer %d names an unknown handle", i)
		}
		if from.ID == to.ID {
			return SettlementView{}, apierr.New(422, "self_payment", "transfer %d pays its own sender", i)
		}
		resolved[i] = resolvedTransfer{from: from, to: to, Transfer: t}
	}
	net := map[*User]int64{}
	for _, t := range resolved {
		net[t.from] -= t.Amount
		net[t.to] += t.Amount
	}
	for u, delta := range net {
		if tx.available(u)+delta < 0 {
			return SettlementView{}, apierr.Conflict("insufficient_funds", "settlement is not affordable")
		}
		if delta > 0 {
			if err := checkCredit(u, delta); err != nil {
				return SettlementView{}, err
			}
		}
	}
	committed := tx.stamp()
	id := tx.st.newSettlementID()
	rec := &Settlement{ID: id, CommittedAt: committed, PaymentIDs: []string{}}
	view := SettlementView{SettlementID: id, CommittedAt: committed, Payments: []PaymentView{}}
	for _, t := range resolved {
		p := tx.transfer(t.from, t.to, t.Amount, t.Note, t.Visibility, links{settlementID: &id}, committed)
		rec.PaymentIDs = append(rec.PaymentIDs, p.ID)
		view.Payments = append(view.Payments, tx.paymentView(p))
	}
	tx.st.Settlements = append(tx.st.Settlements, rec)
	tx.st.settlements[id] = rec
	return view, nil
}
