package store

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"pocketful/internal/apierr"
	"pocketful/internal/money"
)

// Request statuses.
const (
	StatusPending   = "pending"
	StatusPaid      = "paid"
	StatusDeclined  = "declined"
	StatusCancelled = "cancelled"
)

// Payment visibilities.
const (
	VisibilityPublic  = "public"
	VisibilityPrivate = "private"
)

var handlePattern = regexp.MustCompile(`^[a-z0-9_]{1,20}$`)

// User is an account and its wallet.
type User struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	DisplayName  string `json:"display_name"`
	Handle       string `json:"handle"`
	PasswordHash string `json:"password_hash"`
	Balance      int64  `json:"balance"`
}

// Payment is a completed money movement.
type Payment struct {
	ID           string  `json:"id"`
	FromID       string  `json:"from_user_id"`
	ToID         string  `json:"to_user_id"`
	Amount       int64   `json:"amount"`
	Note         string  `json:"note"`
	Visibility   string  `json:"visibility"`
	RequestID    *string `json:"request_id"`
	SettlementID *string `json:"settlement_id"`
	CreatedAt    string  `json:"created_at"`
}

// Request asks a payer for money.
type Request struct {
	ID          string  `json:"id"`
	RequesterID string  `json:"requester_id"`
	PayerID     string  `json:"payer_id"`
	Amount      int64   `json:"amount"`
	Note        string  `json:"note"`
	Status      string  `json:"status"`
	PaymentID   *string `json:"payment_id"`
	CreatedAt   string  `json:"created_at"`
}

// Share is one participant's part of a split.
type Share struct {
	Handle string `json:"handle"`
	Amount int64  `json:"amount"`
}

// Split records a bill split and the requests it created.
type Split struct {
	ID         string   `json:"id"`
	CallerID   string   `json:"caller_id"`
	Amount     int64    `json:"amount"`
	Note       string   `json:"note"`
	Shares     []Share  `json:"shares"`
	RequestIDs []string `json:"request_ids"`
	CreatedAt  string   `json:"created_at"`
}

// Settlement records the payments committed together as one batch.
type Settlement struct {
	ID          string   `json:"id"`
	CommittedAt string   `json:"committed_at"`
	PaymentIDs  []string `json:"payment_ids"`
}

// IdempotencyRecord remembers a successful idempotent write.
type IdempotencyRecord struct {
	UserID      string          `json:"user_id"`
	Path        string          `json:"path"`
	Key         string          `json:"key"`
	Fingerprint string          `json:"fingerprint"`
	Response    json.RawMessage `json:"response"`
}

// Data is the complete serialisable service state; it is the export format.
type Data struct {
	Currency    string               `json:"currency"`
	MinorUnits  int                  `json:"minor_units"`
	Users       []*User              `json:"users"`
	Tokens      map[string]string    `json:"tokens"`
	Payments    []*Payment           `json:"payments"`
	Requests    []*Request           `json:"requests"`
	Splits      []*Split             `json:"splits"`
	Settlements []*Settlement        `json:"settlements"`
	Idempotency []*IdempotencyRecord `json:"idempotency"`
	Operators   []string             `json:"operators"`
	Counters    map[string]int64     `json:"counters"`
}

// state is Data plus lookup indexes derived from it.
type state struct {
	Data
	usersByID     map[string]*User
	usersByHandle map[string]*User
	usersByEmail  map[string]*User
	paymentsByID  map[string]*Payment
	requestsByID  map[string]*Request
	splitsByID    map[string]*Split
	settlements   map[string]*Settlement
	idem          map[string]*IdempotencyRecord
	operators     map[string]bool
}

func validMinorUnits(n int) bool { return n == 0 || n == 2 || n == 3 }

func validVisibility(v string) bool { return v == VisibilityPublic || v == VisibilityPrivate }

func validStatus(s string) bool {
	switch s {
	case StatusPending, StatusPaid, StatusDeclined, StatusCancelled:
		return true
	}
	return false
}

// newState validates d and builds the indexes; d is owned by the result.
func newState(d Data) (*state, error) {
	if d.Currency == "" {
		return nil, apierr.Invalid("currency is required")
	}
	if !validMinorUnits(d.MinorUnits) {
		return nil, apierr.Invalid("minor_units must be 0, 2 or 3")
	}
	if d.Tokens == nil {
		d.Tokens = map[string]string{}
	}
	if d.Counters == nil {
		d.Counters = map[string]int64{}
	}
	st := &state{
		Data:          d,
		usersByID:     map[string]*User{},
		usersByHandle: map[string]*User{},
		usersByEmail:  map[string]*User{},
		paymentsByID:  map[string]*Payment{},
		requestsByID:  map[string]*Request{},
		splitsByID:    map[string]*Split{},
		settlements:   map[string]*Settlement{},
		idem:          map[string]*IdempotencyRecord{},
		operators:     map[string]bool{},
	}
	for _, u := range d.Users {
		if err := st.indexUser(u); err != nil {
			return nil, err
		}
	}
	for tok, uid := range d.Tokens {
		if tok == "" || st.usersByID[uid] == nil {
			return nil, apierr.Invalid("token is empty or refers to an unknown user")
		}
	}
	for _, p := range d.Payments {
		if p == nil || p.ID == "" || st.paymentsByID[p.ID] != nil {
			return nil, apierr.Invalid("payment ids must be non-empty and unique")
		}
		if st.usersByID[p.FromID] == nil || st.usersByID[p.ToID] == nil {
			return nil, apierr.Invalid("payment %s refers to an unknown user", p.ID)
		}
		if p.Amount < 0 || p.Amount > money.MaxSafe || !validVisibility(p.Visibility) {
			return nil, apierr.Invalid("payment %s has an invalid amount or visibility", p.ID)
		}
		st.paymentsByID[p.ID] = p
	}
	for _, r := range d.Requests {
		if r == nil || r.ID == "" || st.requestsByID[r.ID] != nil {
			return nil, apierr.Invalid("request ids must be non-empty and unique")
		}
		if st.usersByID[r.RequesterID] == nil || st.usersByID[r.PayerID] == nil {
			return nil, apierr.Invalid("request %s refers to an unknown user", r.ID)
		}
		if r.Amount < 0 || r.Amount > money.MaxSafe || !validStatus(r.Status) {
			return nil, apierr.Invalid("request %s has an invalid amount or status", r.ID)
		}
		st.requestsByID[r.ID] = r
	}
	for _, sp := range d.Splits {
		if sp == nil || sp.ID == "" || st.splitsByID[sp.ID] != nil {
			return nil, apierr.Invalid("split ids must be non-empty and unique")
		}
		st.splitsByID[sp.ID] = sp
	}
	for _, s := range d.Settlements {
		if s == nil || s.ID == "" || st.settlements[s.ID] != nil {
			return nil, apierr.Invalid("settlement ids must be non-empty and unique")
		}
		for _, pid := range s.PaymentIDs {
			if st.paymentsByID[pid] == nil {
				return nil, apierr.Invalid("settlement %s refers to an unknown payment", s.ID)
			}
		}
		st.settlements[s.ID] = s
	}
	for _, rec := range d.Idempotency {
		if rec == nil || rec.Key == "" || !json.Valid(rec.Response) {
			return nil, apierr.Invalid("invalid idempotency record")
		}
		k := idemKey(rec.UserID, rec.Path, rec.Key)
		if st.idem[k] != nil {
			return nil, apierr.Invalid("duplicate idempotency record")
		}
		st.idem[k] = rec
	}
	for _, id := range d.Operators {
		st.operators[id] = true
	}
	return st, nil
}

func (st *state) indexUser(u *User) error {
	if u == nil || u.ID == "" {
		return apierr.Invalid("user ids must be non-empty")
	}
	if !handlePattern.MatchString(u.Handle) {
		return apierr.Invalid("user %s has an invalid handle", u.ID)
	}
	if u.Email == "" {
		return apierr.Invalid("user %s has no email", u.ID)
	}
	if u.Balance < 0 || u.Balance > money.MaxSafe {
		return apierr.Invalid("user %s has an invalid balance", u.ID)
	}
	email := normalizeEmail(u.Email)
	if st.usersByID[u.ID] != nil || st.usersByHandle[u.Handle] != nil || st.usersByEmail[email] != nil {
		return apierr.Invalid("user %s duplicates another user's id, handle or email", u.ID)
	}
	st.usersByID[u.ID] = u
	st.usersByHandle[u.Handle] = u
	st.usersByEmail[email] = u
	return nil
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func idemKey(userID, path, key string) string { return userID + "\x00" + path + "\x00" + key }

// nextID returns an unused id of the form prefix_N.
func (st *state) nextID(prefix string, taken func(string) bool) string {
	for {
		st.Counters[prefix]++
		id := prefix + "_" + strconv.FormatInt(st.Counters[prefix], 10)
		if !taken(id) {
			return id
		}
	}
}

func (st *state) newUserID() string {
	return st.nextID("u", func(id string) bool { return st.usersByID[id] != nil })
}

func (st *state) newPaymentID() string {
	return st.nextID("p", func(id string) bool { return st.paymentsByID[id] != nil })
}

func (st *state) newRequestID() string {
	return st.nextID("rq", func(id string) bool { return st.requestsByID[id] != nil })
}

func (st *state) newSplitID() string {
	return st.nextID("sp", func(id string) bool { return st.splitsByID[id] != nil })
}

func (st *state) newSettlementID() string {
	return st.nextID("st", func(id string) bool { return st.settlements[id] != nil })
}

// DerivedHandle builds a handle from an email's local part.
func DerivedHandle(email string) string {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	var b strings.Builder
	for _, r := range local {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	h := b.String()
	if len(h) > 20 {
		h = h[:20]
	}
	return h
}
