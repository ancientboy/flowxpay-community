package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var mfaEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func securityStatus(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	var state struct {
		PasswordSet bool `json:"password_set"`
		MFAEnabled  bool `json:"mfa_enabled"`
	}
	err := tx.QueryRowContext(r.Context(), `
		SELECT COUNT(*) > 0, COUNT(mfa_secret) > 0 FROM merchant_credentials WHERE member_id = $1`,
		m.ID).Scan(&state.PasswordSet, &state.MFAEnabled)
	return http.StatusOK, state, err
}

func (s *server) mfaCipher() (cipher.AEAD, error) {
	if len(s.signing) < 32 {
		return nil, errors.New("missing local sandbox secret")
	}
	mac := hmac.New(sha256.New, s.signing)
	mac.Write([]byte("flowxpay.workspace.mfa.v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (s *server) startEnrollment(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	var input struct{ Password string }
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	ctx := r.Context()
	var hash string
	var enabled bool
	err := tx.QueryRowContext(ctx, `
		SELECT password_hash, mfa_secret IS NOT NULL FROM merchant_credentials WHERE member_id = $1`,
		m.ID).Scan(&hash, &enabled)
	if errors.Is(err, sql.ErrNoRows) || enabled {
		fail(w, http.StatusConflict, "mfa_setup_unavailable", "MFA setup requires a password account without MFA.")
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	if !validPassword(input.Password) || bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil {
		fail(w, http.StatusUnauthorized, "invalid_password", "The password is incorrect.")
		return 0, nil, nil
	}
	aead, err := s.mfaCipher()
	if err != nil {
		return 0, nil, err
	}
	var seed [20]byte
	rand.Read(seed[:])
	encrypted := aead.Seal(nil, nil, seed[:], []byte(m.ID))
	_, err = tx.ExecContext(ctx, `UPDATE merchant_credentials
		SET enrollment_secret = $2, enrollment_expires_at = clock_timestamp() + interval '10 minutes'
		WHERE member_id = $1`, m.ID, encrypted)
	return http.StatusOK, map[string]string{"secret": mfaEncoding.EncodeToString(seed[:])}, err
}

func (s *server) confirmEnrollment(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	var input struct{ Code string }
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	ctx := r.Context()
	var encrypted []byte
	err := tx.QueryRowContext(ctx, `SELECT enrollment_secret FROM merchant_credentials
		WHERE member_id = $1 AND mfa_secret IS NULL AND enrollment_expires_at > clock_timestamp()`,
		m.ID).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		invalidMFA(w)
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	step, valid, err := s.matchMFAStep(encrypted, m.ID, input.Code, -1)
	if err != nil {
		return 0, nil, err
	}
	if !valid {
		invalidMFA(w)
		return 0, nil, nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE merchant_credentials SET mfa_secret = enrollment_secret,
		last_mfa_step = $2, enrollment_secret = NULL, enrollment_expires_at = NULL WHERE member_id = $1`, m.ID, step)
	return http.StatusOK, map[string]bool{"mfa_enabled": true}, err
}

func invalidMFA(w http.ResponseWriter) {
	fail(w, http.StatusUnauthorized, "invalid_mfa", "The verification request or code is invalid, expired or already used.")
}

func (s *server) completeChallenge(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"challenge_token"`
		Code  string
	}
	if !parseJSON(w, r, &input) {
		return
	}
	digest, ok := authTokenDigest(input.Token, "fxc_")
	if !ok {
		invalidMFA(w)
		return
	}
	token, err := s.consumeChallenge(r.Context(), digest[:], input.Code)
	switch {
	case errors.Is(err, sql.ErrNoRows), err == nil && token == "":
		invalidMFA(w)
	case err != nil:
		memberStorageError(w)
	default:
		respond(w, http.StatusOK, map[string]string{"session_token": token})
	}
}

func (s *server) consumeChallenge(ctx context.Context, digest []byte, code string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT member_id FROM merchant_login_challenges WHERE token_hash = $1`,
		digest).Scan(&id)
	if err != nil {
		return "", err
	}
	// This lock serializes challenge use across every challenge for the member,
	// and orders session issuance with enrollment and member disable.
	if err := lockActiveMember(ctx, tx, id); err != nil {
		return "", err
	}
	err = tx.QueryRowContext(ctx, `UPDATE merchant_login_challenges SET attempts = attempts + 1
		WHERE token_hash = $1 AND expires_at > clock_timestamp() AND consumed_at IS NULL AND attempts < 5
		RETURNING member_id`, digest).Scan(&id)
	if err != nil {
		return "", err
	}
	var encrypted []byte
	var lastStep int64
	err = tx.QueryRowContext(ctx, `SELECT mfa_secret, last_mfa_step FROM merchant_credentials
		WHERE member_id = $1 AND mfa_secret IS NOT NULL`, id).Scan(&encrypted, &lastStep)
	if err != nil {
		return "", err
	}
	step, valid, err := s.matchMFAStep(encrypted, id, code, lastStep)
	if err != nil {
		return "", err
	}
	if !valid {
		return "", tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE merchant_credentials SET last_mfa_step = $2 WHERE member_id = $1`, id, step); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE merchant_login_challenges SET consumed_at = clock_timestamp()
		WHERE token_hash = $1`, digest); err != nil {
		return "", err
	}
	token, err := issueWorkspaceSession(ctx, tx, id, true)
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *server) matchMFAStep(encrypted []byte, memberID, code string, lastStep int64) (int64, bool, error) {
	if len(code) != 6 {
		return 0, false, nil
	}
	aead, err := s.mfaCipher()
	if err != nil {
		return 0, false, err
	}
	seed, err := aead.Open(nil, nil, encrypted, []byte(memberID))
	if err != nil {
		return 0, false, err
	}
	now := time.Now()
	if s.mfaNow != nil {
		now = s.mfaNow()
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		if step > lastStep && subtle.ConstantTimeCompare([]byte(code), []byte(mfaCode(seed, step))) == 1 {
			return step, true, nil
		}
	}
	return 0, false, nil
}

// mfaCode implements RFC 6238 with the standard six-digit authenticator format.
func mfaCode(seed []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, seed)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	value := binary.BigEndian.Uint32(sum[offset:]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}
