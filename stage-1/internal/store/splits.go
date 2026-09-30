package store

import "pocketful/internal/apierr"

// SplitView is the API representation of a split.
type SplitView struct {
	SplitID   string        `json:"split_id"`
	Amount    int64         `json:"amount"`
	Currency  string        `json:"currency"`
	Note      string        `json:"note"`
	Shares    []Share       `json:"shares"`
	Requests  []RequestView `json:"requests"`
	CreatedAt string        `json:"created_at"`
}

// SplitInput is a validated split.
type SplitInput struct {
	Amount  int64
	Handles []string
	Note    string
}

// EqualShares divides amount among n people in whole minor units; the
// first amount%n people get one extra unit.
func EqualShares(amount int64, n int) []int64 {
	base, extra := amount/int64(n), int(amount%int64(n))
	shares := make([]int64, n)
	for i := range shares {
		shares[i] = base
		if i < extra {
			shares[i]++
		}
	}
	return shares
}

// CreateSplit asks every participant except the caller for their share.
func (tx *Tx) CreateSplit(callerID string, in SplitInput) (SplitView, error) {
	caller, err := tx.user(callerID)
	if err != nil {
		return SplitView{}, err
	}
	participants := make([]*User, len(in.Handles))
	for i, h := range in.Handles {
		if participants[i] = tx.st.usersByHandle[h]; participants[i] == nil {
			return SplitView{}, apierr.NotFound("no user has handle %q", h)
		}
	}
	created := now()
	amounts := EqualShares(in.Amount, len(participants))
	sp := &Split{ID: tx.st.newSplitID(), CallerID: callerID, Amount: in.Amount, Note: in.Note, CreatedAt: created, RequestIDs: []string{}}
	view := SplitView{Amount: in.Amount, Currency: tx.st.Currency, Note: in.Note, Requests: []RequestView{}, CreatedAt: created}
	for i, u := range participants {
		sp.Shares = append(sp.Shares, Share{Handle: u.Handle, Amount: amounts[i]})
		if u.ID == caller.ID {
			continue
		}
		r := tx.addRequest(caller, u, amounts[i], in.Note, created)
		sp.RequestIDs = append(sp.RequestIDs, r.ID)
		view.Requests = append(view.Requests, tx.requestView(r))
	}
	tx.st.Splits = append(tx.st.Splits, sp)
	tx.st.splitsByID[sp.ID] = sp
	view.SplitID = sp.ID
	view.Shares = sp.Shares
	return view, nil
}
