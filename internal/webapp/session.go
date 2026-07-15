package webapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// sessionTTL is how long a browser login session stays valid before the
// user has to log in again.
const sessionTTL = 30 * 24 * time.Hour

// SessionCookieName is the cookie the browser login flow uses, distinct
// from Telegram's initData header so both auth paths coexist untouched.
const SessionCookieName = "fahriddin_session"

// ErrInvalidCredentials is returned by Login for a wrong username/password
// (kept generic so callers don't leak which part was wrong).
var ErrInvalidCredentials = errors.New("invalid username or password")

// User is an authenticated browser-login account.
type User struct {
	ID          string
	Username    string
	DisplayName string
	Role        string
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// Login verifies a username/password against the users table and, on
// success, creates a new session row. Returns ErrInvalidCredentials for any
// bad-credential case (unknown username or wrong password) — never reveals
// which.
func Login(ctx context.Context, db *sql.DB, username, password string) (token string, user *User, err error) {
	var u User
	var hash string
	err = db.QueryRowContext(ctx,
		`SELECT id, username, display_name, role, password_hash FROM users WHERE username = $1`,
		username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role, &hash)
	if err == sql.ErrNoRows {
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, fmt.Errorf("login query: %w", err)
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", nil, ErrInvalidCredentials
	}

	token, err = CreateSession(ctx, db, u.ID)
	if err != nil {
		return "", nil, err
	}
	return token, &u, nil
}

// CreateSession issues a new random session token for the given user ID.
func CreateSession(ctx context.Context, db *sql.DB, userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)

	_, err := db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3)`,
		token, userID, time.Now().Add(sessionTTL),
	)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// ValidateSession looks up a session token and returns the user it belongs
// to, if the session exists and hasn't expired.
func ValidateSession(ctx context.Context, db *sql.DB, token string) (*User, error) {
	if token == "" {
		return nil, sql.ErrNoRows
	}
	var u User
	err := db.QueryRowContext(ctx,
		`SELECT u.id, u.username, u.display_name, u.role
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token = $1 AND s.expires_at > now()`,
		token,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Role)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteSession logs a session out (idempotent — no error if already gone).
func DeleteSession(ctx context.Context, db *sql.DB, token string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}

// UserCount returns how many accounts exist — used at startup to decide
// whether to bootstrap the first admin user.
func UserCount(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a new account with a bcrypt-hashed password.
func CreateUser(ctx context.Context, db *sql.DB, username, password, displayName, role string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return fmt.Errorf("generate user id: %w", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, display_name, role) VALUES ($1, $2, $3, $4, $5)`,
		hex.EncodeToString(id), username, hash, displayName, role,
	)
	return err
}
