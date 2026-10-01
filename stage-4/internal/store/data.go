package store

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	// Opening is the balance before any payment moved. Nil only in an
	// export from an earlier stage, where newState derives it.
	Opening *int64 `json:"opening_balance"`
}

// Payment is a completed money movement.
type Payment struct {
	ID              string  `json:"id"`
	FromID          string  `json:"from_user_id"`
	ToID            string  `json:"to_user_id"`
	Amount          int64   `json:"amount"`
	Note            string  `json:"note"`
	Visibility      string  `json:"visibility"`
	RequestID       *string `json:"request_id"`
	SettlementID    *string `json:"settlement_id"`
	AuthorizationID *string `json:"authorization_id"`
	// RefundOf names the payment this one refunds; nil for any other.
	RefundOf *string `json:"refund_of"`
	// Revisions is the append-only history; revision 1 is the original
	// payment. Amount above always stays the original amount.
	Revisions []*Revision `json:"revisions"`
	CreatedAt Instant     `json:"created_at"`
}

// Revision is one version of a payment's amount. EffectiveAt is when the
// money took effect, RecordedAt when the service learned of it.
type Revision struct {
	Revision    int     `json:"revision"`
	Amount      int64   `json:"amount"`
	EffectiveAt Instant `json:"effective_at"`
	RecordedAt  Instant `json:"recorded_at"`
	Reason      string  `json:"reason"`
	// CorrectionBatchID is set on revisions made by a correction batch.
	CorrectionBatchID *string `json:"correction_batch_id"`
}

// latest is the newest revision.
func (p *Payment) latest() *Revision { return p.Revisions[len(p.Revisions)-1] }

// selected is the newest revision recorded at or before known; a nil known
// means everything recorded so far. It is nil if none was recorded yet.
func (p *Payment) selected(known *time.Time) *Revision {
	for i := len(p.Revisions) - 1; i >= 0; i-- {
		if known == nil || !p.Revisions[i].RecordedAt.T.After(*known) {
			return p.Revisions[i]
		}
	}
	return nil
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
	CreatedAt   Instant `json:"created_at"`
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
	CreatedAt  Instant  `json:"created_at"`
}

// Settlement records the payments committed together as one batch.
type Settlement struct {
	ID          string   `json:"id"`
	CommittedAt Instant  `json:"committed_at"`
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

// Authorization statuses. "expired" is also derived: an open authorization
// whose expiry has passed is reported as expired without being rewritten.
const (
	AuthOpen     = "open"
	AuthCaptured = "captured"
	AuthVoided   = "voided"
	AuthExpired  = "expired"
)

// DefaultAuthorizationTTL is the lifetime in seconds of a new authorization
// when the fixture does not say otherwise.
const DefaultAuthorizationTTL = 600

// Authorization reserves part of the payer's wallet for the receiver to
// capture later. While open and unexpired it holds Amount-CapturedAmount.
type Authorization struct {
	ID             string   `json:"id"`
	FromID         string   `json:"from_user_id"`
	ToID           string   `json:"to_user_id"`
	Amount         int64    `json:"amount"`
	CapturedAmount int64    `json:"captured_amount"`
	Note           string   `json:"note"`
	Visibility     string   `json:"visibility"`
	Status         string   `json:"status"`
	ExpiresAt      Instant  `json:"expires_at"`
	PaymentIDs     []string `json:"payment_ids"`
	CreatedAt      Instant  `json:"created_at"`

	// ClosedAt is when a final capture or void released the hold; nil
	// while open. A clock expiry is derived from ExpiresAt instead.
	ClosedAt *Instant `json:"closed_at"`
}

// remaining is the part of the authorization not yet captured.
func (a *Authorization) remaining() int64 { return a.Amount - a.CapturedAmount }

// holding reports whether the authorization currently reserves funds.
func (a *Authorization) holding(now time.Time) bool {
	return a.Status == AuthOpen && a.ExpiresAt.T.After(now)
}

// effectiveStatus is the status as of now, with lazy expiry applied.
func (a *Authorization) effectiveStatus(now time.Time) string {
	if a.Status == AuthOpen && !a.ExpiresAt.T.After(now) {
		return AuthExpired
	}
	return a.Status
}

// Data is the complete serialisable service state; it is the export format.
type Data struct {
	Currency          string               `json:"currency"`
	MinorUnits        int                  `json:"minor_units"`
	AuthTTL           int                  `json:"authorization_ttl_seconds"`
	Users             []*User              `json:"users"`
	Tokens            map[string]string    `json:"tokens"`
	Payments          []*Payment           `json:"payments"`
	Requests          []*Request           `json:"requests"`
	Splits            []*Split             `json:"splits"`
	Settlements       []*Settlement        `json:"settlements"`
	Authorizations    []*Authorization     `json:"authorizations"`
	Idempotency       []*IdempotencyRecord `json:"idempotency"`
	Operators         []string             `json:"operators"`
	Counters          map[string]int64     `json:"counters"`
	Snapshots         []*Snapshot          `json:"snapshots"`
	CorrectionBatches []*CorrectionBatch   `json:"correction_batches"`
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
	authzByID     map[string]*Authorization
	snapshots     map[string]*Snapshot
	batches       map[string]*CorrectionBatch
	refunded      map[string]int64
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
	if d.AuthTTL == 0 {
		d.AuthTTL = DefaultAuthorizationTTL
	}
	if d.AuthTTL < 0 || d.AuthTTL > MaxAuthorizationTTL {
		return nil, apierr.Invalid("authorization_ttl_seconds must be a positive integer")
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
		authzByID:     map[string]*Authorization{},
		snapshots:     map[string]*Snapshot{},
		batches:       map[string]*CorrectionBatch{},
		refunded:      map[string]int64{},
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
		if err := normalizeRevisions(p); err != nil {
			return nil, err
		}
		st.paymentsByID[p.ID] = p
	}
	for _, p := range d.Payments {
		if p.RefundOf == nil {
			continue
		}
		if st.paymentsByID[*p.RefundOf] == nil {
			return nil, apierr.Invalid("refund %s refers to an unknown payment", p.ID)
		}
		st.refunded[*p.RefundOf] += p.Amount
	}
	for _, b := range d.CorrectionBatches {
		if b == nil || b.ID == "" || st.batches[b.ID] != nil {
			return nil, apierr.Invalid("correction batch ids must be non-empty and unique")
		}
		st.batches[b.ID] = b
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
	for _, a := range d.Authorizations {
		if err := st.indexAuthorization(a); err != nil {
			return nil, err
		}
	}
	st.deriveOpenings()
	st.backfillClosedAt()
	for _, sn := range d.Snapshots {
		if sn == nil || sn.Token == "" || st.usersByID[sn.UserID] == nil || st.snapshots[sn.Token] != nil {
			return nil, apierr.Invalid("invalid statement snapshot")
		}
		st.snapshots[sn.Token] = sn
	}
	if err := st.checkHolds(time.Now()); err != nil {
		return nil, err
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

// normalizeRevisions gives a payment from an earlier stage its revision 1
// and checks the history of a stage-3 payment.
func normalizeRevisions(p *Payment) error {
	if p.CreatedAt.IsZero() {
		return apierr.Invalid("payment %s has no created_at", p.ID)
	}
	if len(p.Revisions) == 0 {
		p.Revisions = []*Revision{{Revision: 1, Amount: p.Amount, EffectiveAt: p.CreatedAt, RecordedAt: p.CreatedAt}}
		return nil
	}
	for i, r := range p.Revisions {
		bad := r == nil || r.Revision != i+1 || r.Amount < 0 || r.Amount > money.MaxSafe ||
			r.EffectiveAt.IsZero() || r.RecordedAt.IsZero() ||
			(i > 0 && !r.RecordedAt.T.After(p.Revisions[i-1].RecordedAt.T))
		if bad {
			return apierr.Invalid("payment %s has an invalid revision history", p.ID)
		}
	}
	if p.Revisions[0].Amount != p.Amount {
		return apierr.Invalid("payment %s revision 1 differs from its original amount", p.ID)
	}
	return nil
}

// deriveOpenings fills the opening balance of users that lack one: the
// current balance minus the net effect of every payment's original amount.
func (st *state) deriveOpenings() {
	net := map[string]int64{}
	for _, p := range st.Payments {
		net[p.ToID] += p.Amount
		net[p.FromID] -= p.Amount
	}
	for _, u := range st.Users {
		if u.Opening == nil {
			opening := u.Balance - net[u.ID]
			u.Opening = &opening
		}
	}
}

func (st *state) newBatchID() string {
	return st.nextID("cb", func(id string) bool { return st.batches[id] != nil })
}

func (st *state) newAuthorizationID() string {
	return st.nextID("a", func(id string) bool { return st.authzByID[id] != nil })
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

// MaxAuthorizationTTL bounds the fixture lifetime (ten years) so expiry
// arithmetic cannot overflow.
const MaxAuthorizationTTL = 10 * 365 * 24 * 3600

func (st *state) indexAuthorization(a *Authorization) error {
	if a == nil || a.ID == "" || st.authzByID[a.ID] != nil {
		return apierr.Invalid("authorization ids must be non-empty and unique")
	}
	if st.usersByID[a.FromID] == nil || st.usersByID[a.ToID] == nil || a.FromID == a.ToID {
		return apierr.Invalid("authorization %s has invalid parties", a.ID)
	}
	if a.Amount < 1 || a.Amount > money.MaxSafe || a.CapturedAmount < 0 || a.CapturedAmount > a.Amount || !validVisibility(a.Visibility) {
		return apierr.Invalid("authorization %s has an invalid amount or visibility", a.ID)
	}
	switch a.Status {
	case AuthOpen, AuthCaptured, AuthVoided, AuthExpired:
	default:
		return apierr.Invalid("authorization %s has an invalid status", a.ID)
	}
	if a.ExpiresAt.IsZero() || a.CreatedAt.IsZero() {
		return apierr.Invalid("authorization %s needs created_at and expires_at", a.ID)
	}
	if a.PaymentIDs == nil {
		a.PaymentIDs = []string{}
	}
	st.authzByID[a.ID] = a
	return nil
}

// checkHolds rejects a state in which some wallet has more reserved than
// it owns.
func (st *state) checkHolds(now time.Time) error {
	held := map[string]int64{}
	for _, a := range st.Authorizations {
		if a.holding(now) {
			held[a.FromID] += a.remaining()
		}
	}
	for id, h := range held {
		if h > st.usersByID[id].Balance {
			return apierr.Invalid("open authorizations of %s exceed the balance", id)
		}
	}
	return nil
}
