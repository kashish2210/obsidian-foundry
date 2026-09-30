package store

import (
	"crypto/rand"
	"encoding/hex"

	"pocketful/internal/apierr"
)

// MeView is the body of GET /me.
type MeView struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Handle      string `json:"handle"`
	Balance     int64  `json:"balance"`
	Currency    string `json:"currency"`
	MinorUnits  int    `json:"minor_units"`
}

// Credentials is what a login needs to verify a password.
type Credentials struct {
	UserID      string
	DisplayName string
	Hash        string
}

// Session is the result of signup and login.
type Session struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Token       string `json:"token"`
}

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Authenticate resolves a bearer token to its user id.
func (tx *Tx) Authenticate(token string) (string, error) {
	uid, ok := tx.st.Tokens[token]
	if !ok {
		return "", apierr.Unauthenticated("unknown bearer token")
	}
	return uid, nil
}

func (tx *Tx) user(id string) (*User, error) {
	u := tx.st.usersByID[id]
	if u == nil {
		return nil, apierr.Unauthenticated("account no longer exists")
	}
	return u, nil
}

// Me returns the caller's profile.
func (tx *Tx) Me(userID string) (MeView, error) {
	u, err := tx.user(userID)
	if err != nil {
		return MeView{}, err
	}
	return MeView{UserID: u.ID, DisplayName: u.DisplayName, Handle: u.Handle, Balance: u.Balance, Currency: tx.st.Currency, MinorUnits: tx.st.MinorUnits}, nil
}

// Signup creates an account with the given password hash and a first token.
func (tx *Tx) Signup(email, displayName, hash string) (Session, error) {
	key := normalizeEmail(email)
	if tx.st.usersByEmail[key] != nil {
		return Session{}, apierr.Conflict("email_taken", "email is already registered")
	}
	handle := DerivedHandle(email)
	if tx.st.usersByHandle[handle] != nil {
		return Session{}, apierr.Conflict("handle_taken", "the handle derived from this email is taken")
	}
	token, err := newToken()
	if err != nil {
		return Session{}, err
	}
	u := &User{ID: tx.st.newUserID(), Email: email, DisplayName: displayName, Handle: handle, PasswordHash: hash}
	tx.st.Users = append(tx.st.Users, u)
	tx.st.usersByID[u.ID] = u
	tx.st.usersByHandle[handle] = u
	tx.st.usersByEmail[key] = u
	tx.st.Tokens[token] = u.ID
	return Session{UserID: u.ID, DisplayName: u.DisplayName, Token: token}, nil
}

// CredentialsFor looks up the login data for an email.
func (tx *Tx) CredentialsFor(email string) (Credentials, bool) {
	u := tx.st.usersByEmail[normalizeEmail(email)]
	if u == nil {
		return Credentials{}, false
	}
	return Credentials{UserID: u.ID, DisplayName: u.DisplayName, Hash: u.PasswordHash}, true
}

// IssueToken adds a new session token for an existing user.
func (tx *Tx) IssueToken(userID string) (string, error) {
	if _, err := tx.user(userID); err != nil {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	tx.st.Tokens[token] = userID
	return token, nil
}

// IsOperator reports whether the user may execute settlements.
func (tx *Tx) IsOperator(userID string) bool { return tx.st.operators[userID] }
