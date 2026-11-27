package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const passwordCost = 12

// The dummy hash balances work for unknown, disabled and passwordless identities.
var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte(rand.Text()), passwordCost)
	if err != nil {
		panic("could not initialize workspace password verification")
	}
	return hash
}()

func (s *server) registerWorkspaceAuthRoutes(mux *http.ServeMux) {
	const workspace = "/sandbox/workspace"
	mux.HandleFunc("POST "+workspace+"/invitations", s.authorizeMember(manageMembers, createInvitation))
	mux.HandleFunc("POST "+workspace+"/invitations/accept", s.limitAuth("invite", 24, s.acceptInvitation))
	mux.HandleFunc("POST "+workspace+"/auth/login", s.limitAuth("login", 30, s.passwordLogin))
	mux.HandleFunc("POST "+workspace+"/auth/mfa", s.limitAuth("mfa", 60, s.completeChallenge))
	mux.HandleFunc("POST "+workspace+"/auth/logout", s.logout)
	mux.HandleFunc("GET "+workspace+"/security", s.authorizeMember(readInventory, securityStatus))
	mux.HandleFunc("POST "+workspace+"/security/mfa/setup", s.limitAuth("setup", 12, s.withMember(readInventory, true, s.startEnrollment)))
	mux.HandleFunc("POST "+workspace+"/security/mfa/confirm", s.limitAuth("confirm", 24, s.withMember(readInventory, true, s.confirmEnrollment)))
}

func newAuthToken(prefix string) (string, [32]byte) {
	var secret [32]byte
	rand.Read(secret[:])
	token := prefix + hex.EncodeToString(secret[:])
	return token, sha256.Sum256([]byte(token))
}

func authTokenDigest(token, prefix string) ([32]byte, bool) {
	secret, ok := strings.CutPrefix(token, prefix)
	if !ok || len(secret) != 64 {
		return [32]byte{}, false
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(token)), true
}

func workspaceEmail(input string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(input))
	address, err := mail.ParseAddress(email)
	return email, len(email) <= 254 && err == nil && address.Name == "" && address.Address == email
}

func validPassword(password string) bool {
	return len(password) <= 72 && utf8.RuneCountInString(password) >= 12
}

func (s *server) takeAuthAttempt(w http.ResponseWriter, r *http.Request, scope, identity string, limit int) bool {
	bucket := sha256.Sum256([]byte(scope + "\x00" + identity))
	var attempts int
	var retry int
	err := s.db.QueryRowContext(r.Context(), `
		INSERT INTO merchant_auth_limits AS limits (bucket, expires_at, attempts)
		VALUES ($1, clock_timestamp() + interval '5 minutes', 1)
		ON CONFLICT (bucket) DO UPDATE SET
			attempts = CASE WHEN limits.expires_at <= clock_timestamp() THEN 1
				ELSE LEAST(limits.attempts + 1, $2 + 1) END,
			expires_at = CASE WHEN limits.expires_at <= clock_timestamp() THEN clock_timestamp() + interval '5 minutes'
				ELSE limits.expires_at END
		RETURNING attempts, GREATEST(1, CEIL(EXTRACT(EPOCH FROM (expires_at - clock_timestamp()))))::int`,
		bucket[:], limit).Scan(&attempts, &retry)
	if err != nil {
		memberStorageError(w)
		return false
	}
	if attempts > limit {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		fail(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.")
		return false
	}
	return true
}

func (s *server) limitAuth(scope string, limit int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Vite connects over loopback. Forwarded headers are caller-controlled.
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if s.takeAuthAttempt(w, r, scope, host, limit) {
			next(w, r)
		}
	}
}

func createInvitation(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	var input struct{ Email, Name, Role string }
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	email, valid := workspaceEmail(input.Email)
	name := strings.TrimSpace(input.Name)
	if !valid || name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsRune(name, 0) ||
		(input.Role != "admin" && input.Role != "operator" && input.Role != "viewer") {
		fail(w, http.StatusBadRequest, "invalid_invitation", "Supply a valid email, name and role.")
		return 0, nil, nil
	}
	path, _, expiry, err := issueInvitation(r.Context(), tx, m.MerchantID, email, name, input.Role)
	return http.StatusCreated, map[string]any{"invite_path": path, "expires_at": expiry}, err
}

func issueInvitation(ctx context.Context, tx *sql.Tx, merchant, email, name, role string) (string, [32]byte, time.Time, error) {
	token, digest := newAuthToken("fxi_")
	var expiry time.Time
	err := tx.QueryRowContext(ctx, `
		INSERT INTO merchant_invitations (token_hash, merchant_id, email, name, role, expires_at)
	VALUES ($1, $2, $3, $4, $5, clock_timestamp() + interval '24 hours') RETURNING expires_at`,
		digest[:], merchant, email, name, role).Scan(&expiry)
	return "/invite#" + token, digest, expiry, err
}

func invalidInvitation(w http.ResponseWriter) {
	fail(w, http.StatusBadRequest, "invalid_invitation", "This invitation is invalid, expired or already used.")
}

func (s *server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var input struct{ Token, Password string }
	if !parseJSON(w, r, &input) {
		return
	}
	digest, ok := authTokenDigest(input.Token, "fxi_")
	if !ok {
		invalidInvitation(w)
		return
	}
	if !validPassword(input.Password) {
		fail(w, http.StatusBadRequest, "invalid_password", "Use at least 12 characters and no more than 72 UTF-8 bytes.")
		return
	}
	ctx := r.Context()
	var available bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM merchant_invitations WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > clock_timestamp())`,
		digest[:]).Scan(&available)
	if err != nil {
		memberStorageError(w)
		return
	}
	if !available {
		invalidInvitation(w)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), passwordCost)
	if err != nil {
		memberStorageError(w)
		return
	}
	merchant, email, err := s.consumeInvitation(ctx, digest[:], string(hash))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		invalidInvitation(w)
	case err != nil:
		memberStorageError(w)
	default:
		respond(w, http.StatusCreated, map[string]string{"merchant_id": merchant, "email": email})
	}
}

func (s *server) consumeInvitation(ctx context.Context, digest []byte, hash string) (string, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var merchant, email, name, role string
	err = tx.QueryRowContext(ctx, `
		UPDATE merchant_invitations SET consumed_at = clock_timestamp()
		WHERE token_hash = $1 AND consumed_at IS NULL AND expires_at > clock_timestamp()
		RETURNING merchant_id, email, name, role`, digest).Scan(&merchant, &email, &name, &role)
	if err != nil {
		return "", "", err
	}
	id := newResourceID("mem_")
	_, err = tx.ExecContext(ctx, `INSERT INTO merchant_members (id, merchant_id, name, role) VALUES ($1, $2, $3, $4)`,
		id, merchant, name, role)
	if err != nil {
		return "", "", err
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO merchant_credentials (member_id, merchant_id, email, password_hash) VALUES ($1, $2, $3, $4)
		ON CONFLICT (merchant_id, email) DO NOTHING RETURNING member_id`, id, merchant, email, hash).Scan(&id)
	if err != nil {
		return "", "", err
	}
	return merchant, email, tx.Commit()
}

func invalidLogin(w http.ResponseWriter) {
	fail(w, http.StatusUnauthorized, "invalid_login", "The merchant, email or password is incorrect.")
}

func (s *server) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Merchant        string `json:"merchant_id"`
		Email, Password string
	}
	if !parseJSON(w, r, &input) {
		return
	}
	merchant := strings.ToUpper(strings.TrimSpace(input.Merchant))
	email, validEmail := workspaceEmail(input.Email)
	if !s.takeAuthAttempt(w, r, "login_identity", merchant+"\x00"+email, 8) {
		return
	}
	if !validEmail || !clientID.MatchString(merchant) || !validPassword(input.Password) {
		invalidLogin(w)
		return
	}
	ctx := r.Context()
	var id, hash string
	err := s.db.QueryRowContext(ctx, `
		SELECT c.member_id, c.password_hash FROM merchant_credentials AS c
		JOIN merchant_members AS m ON m.id = c.member_id
		WHERE c.merchant_id = $1 AND c.email = $2 AND m.disabled_at IS NULL`, merchant, email).Scan(&id, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		memberStorageError(w)
		return
	}
	candidate := dummyPasswordHash
	if err == nil {
		candidate = []byte(hash)
	}
	if bcrypt.CompareHashAndPassword(candidate, []byte(input.Password)) != nil || id == "" {
		invalidLogin(w)
		return
	}
	result, status, err := s.issuePasswordResult(ctx, id, hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		invalidLogin(w)
	case err != nil:
		memberStorageError(w)
	default:
		respond(w, status, result)
	}
}

func (s *server) issuePasswordResult(ctx context.Context, id, hash string) (map[string]string, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if err := lockActiveMember(ctx, tx, id); err != nil {
		return nil, 0, err
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT mfa_secret IS NOT NULL FROM merchant_credentials
		WHERE member_id = $1 AND password_hash = $2`, id, hash).Scan(&enabled); err != nil {
		return nil, 0, err
	}
	if enabled {
		token, digest := newAuthToken("fxc_")
		if _, err := tx.ExecContext(ctx, `INSERT INTO merchant_login_challenges (token_hash, member_id, expires_at)
			VALUES ($1, $2, clock_timestamp() + interval '5 minutes')`, digest[:], id); err != nil {
			return nil, 0, err
		}
		return map[string]string{"challenge_token": token}, http.StatusAccepted, tx.Commit()
	}
	token, err := issueWorkspaceSession(ctx, tx, id, false)
	if err != nil {
		return nil, 0, err
	}
	return map[string]string{"session_token": token}, http.StatusOK, tx.Commit()
}

func lockActiveMember(ctx context.Context, tx *sql.Tx, id string) error {
	return tx.QueryRowContext(ctx, `SELECT id FROM merchant_members WHERE id = $1 AND disabled_at IS NULL FOR UPDATE`,
		id).Scan(&id)
}

func issueWorkspaceSession(ctx context.Context, tx *sql.Tx, id string, verified bool) (string, error) {
	token, digest := newAuthToken("fxs_")
	_, err := tx.ExecContext(ctx, `INSERT INTO merchant_sessions (token_hash, member_id, expires_at, mfa_verified)
		VALUES ($1, $2, clock_timestamp() + interval '8 hours', $3)`, digest[:], id, verified)
	return token, err
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	digest, ok := workspaceSessionDigest(r)
	if !ok {
		memberUnauthorized(w)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE merchant_sessions SET revoked_at = clock_timestamp()
		WHERE token_hash = $1 AND revoked_at IS NULL`, digest[:]); err != nil {
		memberStorageError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
