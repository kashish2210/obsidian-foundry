package store

import (
	"sort"
	"time"

	"pocketful/internal/apierr"
)

// Time travel is one pure computation. A payment contributes the delta of
// its selected revision: the latest revision recorded at or before the
// "known" instant (nil means everything recorded so far). A user's balance
// at time t is their opening balance plus the deltas of every selected
// revision whose effective time is at or before t. Holds follow the same
// split between when something happened and when it became known.

// ledgerItem is one payment's contribution to a user's history.
type ledgerItem struct {
	payment *Payment
	rev     *Revision
	delta   int64
}

func (it ledgerItem) effective() time.Time { return it.rev.EffectiveAt.T }

// ledger lists the user's selected revisions ordered by effective time,
// then payment id (plain string comparison of the ids).
func (tx *Tx) ledger(userID string, known *time.Time) []ledgerItem {
	var items []ledgerItem
	for _, p := range tx.st.Payments {
		if p.FromID != userID && p.ToID != userID {
			continue
		}
		rev := p.selected(known)
		if rev == nil {
			continue
		}
		delta := rev.Amount
		if p.FromID == userID {
			delta = -delta
		}
		items = append(items, ledgerItem{payment: p, rev: rev, delta: delta})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if ti, tj := items[i].effective(), items[j].effective(); !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return items[i].payment.ID < items[j].payment.ID
	})
	return items
}

// balanceAt is the opening balance plus every delta effective by t.
func balanceAt(opening int64, items []ledgerItem, t time.Time) int64 {
	total := opening
	for _, it := range items {
		if !it.effective().After(t) {
			total += it.delta
		}
	}
	return total
}

// heldAt is the amount the user's open authorizations reserved at time t,
// as far as was known at the known instant. A hold starts at creation; a
// capture reduces it when the capture happened; a final capture, void or
// expiry releases the remainder at that event. The expiry deadline is
// known as soon as the creation is.
func (tx *Tx) heldAt(userID string, t time.Time, known *time.Time) int64 {
	isKnown := func(at time.Time) bool { return known == nil || !at.After(*known) }
	var held int64
	for _, a := range tx.st.Authorizations {
		if a.FromID != userID || a.CreatedAt.T.After(t) || !isKnown(a.CreatedAt.T) {
			continue
		}
		released := a.ExpiresAt.T
		if a.ClosedAt != nil && isKnown(a.ClosedAt.T) && a.ClosedAt.T.Before(released) {
			released = a.ClosedAt.T
		}
		if !released.After(t) {
			continue
		}
		remaining := a.Amount
		for _, pid := range a.PaymentIDs {
			c := tx.st.paymentsByID[pid]
			if c != nil && !c.CreatedAt.T.After(t) && isKnown(c.CreatedAt.T) {
				remaining -= c.Revisions[0].Amount
			}
		}
		held += remaining
	}
	return held
}

// backfillClosedAt gives closed authorizations from an earlier stage a
// closing time: their last capture, or creation if nothing better is known.
func (st *state) backfillClosedAt() {
	for _, a := range st.Authorizations {
		if a.ClosedAt != nil || (a.Status != AuthCaptured && a.Status != AuthVoided) {
			continue
		}
		at := a.CreatedAt
		if n := len(a.PaymentIDs); n > 0 && a.Status == AuthCaptured {
			if p := st.paymentsByID[a.PaymentIDs[n-1]]; p != nil {
				at = p.CreatedAt
			}
		}
		a.ClosedAt = &at
	}
}

// TemporalQuery holds the optional instants of GET /me.
type TemporalQuery struct {
	AsOf    *Instant
	KnownAt *Instant
}

func (q TemporalQuery) set() bool { return q.AsOf != nil || q.KnownAt != nil }

func knownTime(i *Instant) *time.Time {
	if i == nil {
		return nil
	}
	t := i.T
	return &t
}

// MeAt returns the caller's wallet as of an instant and as known at another.
func (tx *Tx) MeAt(userID string, q TemporalQuery) (MeView, error) {
	if !q.set() {
		return tx.Me(userID)
	}
	u, err := tx.user(userID)
	if err != nil {
		return MeView{}, err
	}
	at := tx.now
	if q.AsOf != nil {
		at = q.AsOf.T
	}
	known := knownTime(q.KnownAt)
	total := balanceAt(*u.Opening, tx.ledger(userID, known), at)
	held := tx.heldAt(userID, at, known)
	return MeView{
		UserID: u.ID, DisplayName: u.DisplayName, Handle: u.Handle,
		Balance: total, Total: total, Available: total - held, Held: held,
		Currency: tx.st.Currency, MinorUnits: tx.st.MinorUnits,
		AsOf: q.AsOf, KnownAt: q.KnownAt,
	}, nil
}

// StatementEntry is one payment in a statement.
type StatementEntry struct {
	Payment      PaymentView `json:"payment"`
	Delta        int64       `json:"delta"`
	BalanceAfter int64       `json:"balance_after"`
	Revision     int         `json:"revision"`
	EffectiveAt  Instant     `json:"effective_at"`
	RecordedAt   Instant     `json:"recorded_at"`
}

// Snapshot is a frozen statement, paged by token.
type Snapshot struct {
	Token   string           `json:"token"`
	UserID  string           `json:"user_id"`
	Opening int64            `json:"opening_balance"`
	Closing int64            `json:"closing_balance"`
	Entries []StatementEntry `json:"entries"`
}

// StatementView is one page of a statement.
type StatementView struct {
	OpeningBalance int64            `json:"opening_balance"`
	Entries        []StatementEntry `json:"entries"`
	ClosingBalance int64            `json:"closing_balance"`
	HasMore        bool             `json:"has_more"`
	Snapshot       string           `json:"snapshot"`
}

// StatementQuery is the window and knowledge of a new statement.
type StatementQuery struct {
	From, To, KnownAt *Instant
}

func (s *Snapshot) page(pg Page) StatementView {
	entries := []StatementEntry{}
	if pg.Offset < len(s.Entries) {
		end := pg.Offset + pg.Limit
		if end > len(s.Entries) {
			end = len(s.Entries)
		}
		entries = s.Entries[pg.Offset:end]
	}
	return StatementView{
		OpeningBalance: s.Opening, Entries: entries, ClosingBalance: s.Closing,
		HasMore: pg.Offset+pg.Limit < len(s.Entries), Snapshot: s.Token,
	}
}

// Statement computes the caller's statement over [from, to), freezes it
// under a new snapshot token and returns the requested page.
func (tx *Tx) Statement(userID string, q StatementQuery, pg Page) (StatementView, error) {
	u, err := tx.user(userID)
	if err != nil {
		return StatementView{}, err
	}
	items := tx.ledger(userID, knownTime(q.KnownAt))
	// The default end is "now"; one microsecond more keeps a payment made
	// in this very instant inside the half-open window.
	to := tx.now.Add(time.Microsecond)
	if q.To != nil {
		to = q.To.T
	}
	snap := &Snapshot{UserID: userID, Opening: *u.Opening, Entries: []StatementEntry{}}
	for _, it := range items {
		if q.From != nil && it.effective().Before(q.From.T) {
			snap.Opening += it.delta
		}
	}
	running := snap.Opening
	empty := q.From != nil && !q.From.T.Before(to)
	for _, it := range items {
		if empty || (q.From != nil && it.effective().Before(q.From.T)) || !it.effective().Before(to) {
			continue
		}
		running += it.delta
		view := tx.paymentView(it.payment)
		view.Amount = it.rev.Amount
		snap.Entries = append(snap.Entries, StatementEntry{
			Payment: view, Delta: it.delta, BalanceAfter: running,
			Revision: it.rev.Revision, EffectiveAt: it.rev.EffectiveAt, RecordedAt: it.rev.RecordedAt,
		})
	}
	snap.Closing = running
	token, err := newToken()
	if err != nil {
		return StatementView{}, err
	}
	snap.Token = "snap_" + token
	tx.st.Snapshots = append(tx.st.Snapshots, snap)
	tx.st.snapshots[snap.Token] = snap
	return snap.page(pg), nil
}

// StatementSnapshot pages a frozen statement. Another user's token is
// indistinguishable from an unknown one.
func (tx *Tx) StatementSnapshot(userID, token string, pg Page) (StatementView, error) {
	if _, err := tx.user(userID); err != nil {
		return StatementView{}, err
	}
	snap := tx.st.snapshots[token]
	if snap == nil || snap.UserID != userID {
		return StatementView{}, errNoSuchSnapshot
	}
	return snap.page(pg), nil
}

var errNoSuchSnapshot = apierr.NotFound("no such statement snapshot")
