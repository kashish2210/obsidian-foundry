package store

import "pocketful/internal/apierr"

// RequestView is the API representation of a payment request.
type RequestView struct {
	RequestID       string  `json:"request_id"`
	RequesterID     string  `json:"requester_id"`
	RequesterHandle string  `json:"requester_handle"`
	PayerID         string  `json:"payer_id"`
	PayerHandle     string  `json:"payer_handle"`
	Amount          int64   `json:"amount"`
	Currency        string  `json:"currency"`
	Note            string  `json:"note"`
	Status          string  `json:"status"`
	PaymentID       *string `json:"payment_id"`
	CreatedAt       string  `json:"created_at"`
}

// RequestInput is a validated new request.
type RequestInput struct {
	PayerHandle string
	Amount      int64
	Note        string
}

// RequestFilter narrows GET /requests. Empty fields match everything.
type RequestFilter struct {
	Direction string // "incoming", "outgoing" or ""
	Status    string
}

func (tx *Tx) requestView(r *Request) RequestView {
	requester, payer := tx.st.usersByID[r.RequesterID], tx.st.usersByID[r.PayerID]
	return RequestView{
		RequestID: r.ID, RequesterID: r.RequesterID, RequesterHandle: requester.Handle,
		PayerID: r.PayerID, PayerHandle: payer.Handle, Amount: r.Amount,
		Currency: tx.st.Currency, Note: r.Note, Status: r.Status,
		PaymentID: r.PaymentID, CreatedAt: r.CreatedAt,
	}
}

func (tx *Tx) addRequest(requester, payer *User, amount int64, note, createdAt string) *Request {
	r := &Request{
		ID: tx.st.newRequestID(), RequesterID: requester.ID, PayerID: payer.ID,
		Amount: amount, Note: note, Status: StatusPending, CreatedAt: createdAt,
	}
	tx.st.Requests = append(tx.st.Requests, r)
	tx.st.requestsByID[r.ID] = r
	return r
}

// CreateRequest asks another user for money. The payer's balance is not checked.
func (tx *Tx) CreateRequest(callerID string, in RequestInput) (RequestView, error) {
	caller, err := tx.user(callerID)
	if err != nil {
		return RequestView{}, err
	}
	if in.PayerHandle == caller.Handle {
		return RequestView{}, apierr.New(422, "self_request", "cannot request money from yourself")
	}
	payer := tx.st.usersByHandle[in.PayerHandle]
	if payer == nil {
		return RequestView{}, apierr.NotFound("no user has handle %q", in.PayerHandle)
	}
	return tx.requestView(tx.addRequest(caller, payer, in.Amount, in.Note, tx.stamp())), nil
}

// PayRequest pays a pending request from the payer's wallet.
func (tx *Tx) PayRequest(callerID, requestID, visibility string) (PaymentView, error) {
	payer, err := tx.user(callerID)
	if err != nil {
		return PaymentView{}, err
	}
	r := tx.st.requestsByID[requestID]
	if r == nil {
		return PaymentView{}, apierr.NotFound("no such request")
	}
	if r.PayerID != callerID {
		return PaymentView{}, apierr.Forbidden("only the payer may pay this request")
	}
	if r.Status != StatusPending {
		return PaymentView{}, apierr.Conflict("request_not_pending", "request is %s", r.Status)
	}
	if tx.available(payer) < r.Amount {
		return PaymentView{}, apierr.Conflict("insufficient_funds", "balance is below the amount")
	}
	requester := tx.st.usersByID[r.RequesterID]
	if err := checkCredit(requester, r.Amount); err != nil {
		return PaymentView{}, err
	}
	reqID := r.ID
	p := tx.transfer(payer, requester, r.Amount, r.Note, visibility, links{requestID: &reqID}, tx.stamp())
	r.Status = StatusPaid
	r.PaymentID = &p.ID
	return tx.paymentView(p), nil
}

// Decline lets the payer decline a pending request; repeating is a no-op.
func (tx *Tx) Decline(callerID, requestID string) (RequestView, error) {
	return tx.close(callerID, requestID, StatusDeclined)
}

// Cancel lets the requester cancel a pending request; repeating is a no-op.
func (tx *Tx) Cancel(callerID, requestID string) (RequestView, error) {
	return tx.close(callerID, requestID, StatusCancelled)
}

func (tx *Tx) close(callerID, requestID, status string) (RequestView, error) {
	if _, err := tx.user(callerID); err != nil {
		return RequestView{}, err
	}
	r := tx.st.requestsByID[requestID]
	if r == nil {
		return RequestView{}, apierr.NotFound("no such request")
	}
	owner, role := r.PayerID, "payer"
	if status == StatusCancelled {
		owner, role = r.RequesterID, "requester"
	}
	if owner != callerID {
		return RequestView{}, apierr.Forbidden("only the %s may do this", role)
	}
	if r.Status == status {
		return tx.requestView(r), nil
	}
	if r.Status != StatusPending {
		return RequestView{}, apierr.Conflict("request_not_pending", "request is %s", r.Status)
	}
	r.Status = status
	return tx.requestView(r), nil
}

// ListRequests returns the caller's requests, newest first.
func (tx *Tx) ListRequests(callerID string, f RequestFilter, pg Page) ([]RequestView, bool) {
	out := []RequestView{}
	skipped, more := 0, false
	for i := len(tx.st.Requests) - 1; i >= 0; i-- {
		r := tx.st.Requests[i]
		incoming, outgoing := r.PayerID == callerID, r.RequesterID == callerID
		if !incoming && !outgoing {
			continue
		}
		if f.Direction == "incoming" && !incoming || f.Direction == "outgoing" && !outgoing {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
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
		out = append(out, tx.requestView(r))
	}
	return out, more
}
