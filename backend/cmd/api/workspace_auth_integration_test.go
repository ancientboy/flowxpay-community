package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"flowxpay.local/platform/migrations"
	"golang.org/x/crypto/bcrypt"
)

func TestSandboxWorkspaceAuthentication(t *testing.T) {
	url := os.Getenv("FLOWXPAY_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("run scripts/local-sandbox.sh test to create an isolated PostgreSQL instance")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := t.Context()
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal("could not update isolated workspace fixture")
		}
	}
	admin, adminHash := newAuthToken("fxs_")
	viewer, viewerHash := newAuthToken("fxs_")
	exec(`INSERT INTO merchant_members (id, merchant_id, name, role) VALUES
		('auth-test-admin', 'M10028', 'Admin', 'admin'),
		('auth-test-viewer', 'M10028', 'Viewer', 'viewer')`)
	exec(`INSERT INTO merchant_sessions (token_hash, member_id, expires_at) VALUES
		($1, 'auth-test-admin', clock_timestamp() + interval '10 minutes'),
		($2, 'auth-test-viewer', clock_timestamp() + interval '10 minutes')`, adminHash[:], viewerHash[:])
	mfaTime := time.Unix(1_800_000_010, 0)
	app := &server{db: db, signing: []byte(strings.Repeat("s", 32)), mfaNow: func() time.Time { return mfaTime }}
	handler := app.routes()
	const base = "/sandbox/workspace"
	send := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	call := func(method, path, token, body string, want int) map[string]any {
		t.Helper()
		w := send(method, path, token, body)
		if w.Code != want {
			t.Fatalf("%s %s: status %d, want %d", method, path, w.Code, want)
		}
		var result map[string]any
		if want != http.StatusNoContent && json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("%s %s: response was not JSON", method, path)
		}
		return result
	}
	mint := func(email string) string {
		t.Helper()
		body := fmt.Sprintf(`{"email":%q,"name":"Invited member","role":"operator"}`, email)
		output := call("POST", "/invitations", admin, body, 201)
		path, _ := output["invite_path"].(string)
		token, ok := strings.CutPrefix(path, "/invite#")
		if !ok {
			t.Fatal("invitation token was missing from the fragment")
		}
		return token
	}
	const email = "sandbox-auth@example.test"
	const password = "sandbox synthetic passphrase"
	const acceptRoute = "/invitations/accept"
	accept := func(token string) string {
		return fmt.Sprintf(`{"token":%q,"password":%q}`, token, password)
	}
	call("POST", "/invitations", viewer, `{}`, 403)
	call("POST", "/invitations", admin, `{"email":"sandbox-auth@example.test","name":"A","role":"admin","merchant_id":"M20029"}`, 400)
	expiredInvite := mint("expired-auth@example.test")
	expiredHash, _ := authTokenDigest(expiredInvite, "fxi_")
	exec(`UPDATE merchant_invitations SET expires_at = clock_timestamp() - interval '1 second' WHERE token_hash = $1`, expiredHash[:])
	call("POST", acceptRoute, "", accept(expiredInvite), 400)
	call("POST", acceptRoute, "", accept("invalid"), 400)
	invite := mint("  SANDBOX-AUTH@example.test  ")
	inviteHash, _ := authTokenDigest(invite, "fxi_")
	var lifetimeOK bool
	if err := db.QueryRowContext(ctx, `SELECT expires_at BETWEEN clock_timestamp() + interval '23 hours'
		AND clock_timestamp() + interval '24 hours' FROM merchant_invitations WHERE token_hash = $1`,
		inviteHash[:]).Scan(&lifetimeOK); err != nil || !lifetimeOK {
		t.Fatal("invitation digest or validity window is incorrect")
	}
	call("POST", acceptRoute, "", fmt.Sprintf(`{"token":%q,"password":"short"}`, invite), 400)
	concurrent := func(path string, bodies ...string) map[int]int {
		t.Helper()
		results := make(chan int, len(bodies))
		for _, body := range bodies {
			go func() { results <- send("POST", path, "", body).Code }()
		}
		counts := make(map[int]int)
		for range bodies {
			counts[<-results]++
		}
		return counts
	}
	if counts := concurrent(acceptRoute, accept(invite), accept(invite)); counts[201] != 1 || counts[400] != 1 {
		t.Fatalf("concurrent invitation acceptance returned %v", counts)
	}
	call("POST", acceptRoute, "", accept(invite), 400)
	duplicateInvite := mint(email)
	call("POST", acceptRoute, "", accept(duplicateInvite), 400)
	var memberID, hash string
	if err := db.QueryRowContext(ctx, `SELECT member_id, password_hash FROM merchant_credentials
		WHERE merchant_id = 'M10028' AND email = $1`, email).Scan(&memberID, &hash); err != nil {
		t.Fatal("password credential missing")
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != passwordCost ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		t.Fatal("credential was not stored with the configured bcrypt hash")
	}

	loginBody := func(merchant, email, password string) string {
		return fmt.Sprintf(`{"merchant_id":%q,"email":%q,"password":%q}`, merchant, email, password)
	}
	login := func(want int) map[string]any {
		t.Helper()
		return call("POST", "/auth/login", "", loginBody("M10028", email, password), want)
	}
	wrong := send("POST", "/auth/login", "", loginBody("M10028", email, "incorrect password"))
	otherMerchant := send("POST", "/auth/login", "", loginBody("M20029", email, password))
	if wrong.Code != 401 || otherMerchant.Code != 401 || wrong.Body.String() != otherMerchant.Body.String() {
		t.Fatal("login failures disclose identity or tenant")
	}
	token, _ := login(200)["session_token"].(string)
	if m := call("GET", "/session?merchant_id=M20029", token, "", 200); m["merchant_id"] != "M10028" || m["role"] != "operator" {
		t.Fatal("invitation did not determine the member's tenant and role")
	}
	call("POST", "/security/mfa/setup", admin, fmt.Sprintf(`{"password":%q}`, password), 409)
	call("POST", "/security/mfa/setup", token, `{"password":"incorrect password"}`, 401)
	setup := fmt.Sprintf(`{"password":%q}`, password)
	secret := call("POST", "/security/mfa/setup", token, setup, 200)["secret"].(string)
	seed, err := mfaEncoding.DecodeString(secret)
	if err != nil || len(seed) != 20 {
		t.Fatal("authenticator setup did not return a valid seed")
	}
	var encrypted []byte
	if err := db.QueryRowContext(ctx, `SELECT enrollment_secret FROM merchant_credentials WHERE member_id = $1`,
		memberID).Scan(&encrypted); err != nil || len(encrypted) <= len(seed) {
		t.Fatal("authenticator seed was not encrypted")
	}
	call("POST", "/security/mfa/confirm", token, `{"code":"invalid"}`, 401)
	step := mfaTime.Unix() / 30
	enrollmentCode := mfaCode(seed, step)
	exec(`UPDATE merchant_credentials SET enrollment_expires_at = clock_timestamp() - interval '1 second' WHERE member_id = $1`, memberID)
	call("POST", "/security/mfa/confirm", token, fmt.Sprintf(`{"code":%q}`, enrollmentCode), 401)

	secret = call("POST", "/security/mfa/setup", token, setup, 200)["secret"].(string)
	seed, _ = mfaEncoding.DecodeString(secret)
	step = mfaTime.Unix() / 30
	// Use the previous accepted step for enrollment so the current step can
	// verify sign-in without waiting for the authenticator to roll over.
	enrollmentCode = mfaCode(seed, step-1)
	call("POST", "/security/mfa/confirm", token, fmt.Sprintf(`{"code":%q}`, enrollmentCode), 200)
	call("GET", "/session", token, "", 401)
	first := login(202)
	if _, ok := first["session_token"]; ok {
		t.Fatal("password login issued a full session before MFA")
	}
	challenge := first["challenge_token"].(string)
	call("GET", "/session", challenge, "", 401)
	challengeBody := func(token, code string) string {
		return fmt.Sprintf(`{"challenge_token":%q,"code":%q}`, token, code)
	}
	const mfaRoute = "/auth/mfa"
	call("POST", mfaRoute, "", challengeBody(challenge, enrollmentCode), 401)
	expiredChallenge := login(202)["challenge_token"].(string)
	challengeHash, _ := authTokenDigest(expiredChallenge, "fxc_")
	exec(`UPDATE merchant_login_challenges SET expires_at = clock_timestamp() - interval '1 second' WHERE token_hash = $1`, challengeHash[:])
	code := mfaCode(seed, step)
	call("POST", mfaRoute, "", challengeBody(expiredChallenge, code), 401)
	exhausted := login(202)["challenge_token"].(string)
	for range 5 {
		call("POST", mfaRoute, "", challengeBody(exhausted, "invalid"), 401)
	}
	call("POST", mfaRoute, "", challengeBody(exhausted, code), 401)
	winner := call("POST", mfaRoute, "", challengeBody(challenge, code), 200)["session_token"].(string)
	call("POST", mfaRoute, "", challengeBody(challenge, code), 401)
	call("GET", "/session", winner, "", 200)
	nextCode := mfaCode(seed, step+1)
	nextChallenge := login(202)["challenge_token"].(string)
	anotherChallenge := login(202)["challenge_token"].(string)
	disabledChallenge := login(202)["challenge_token"].(string)
	call("POST", mfaRoute, "", challengeBody(nextChallenge, code), 401)
	if counts := concurrent(mfaRoute, challengeBody(nextChallenge, nextCode), challengeBody(anotherChallenge, nextCode)); counts[200] != 1 || counts[401] != 1 {
		t.Fatalf("concurrent MFA replay checks returned %v", counts)
	}

	// The identity budget normalizes the tenant and email before counting.
	for range 8 {
		call("POST", "/auth/login", "", loginBody("M10028", "rate@example.test", "x"), 401)
	}
	call("POST", "/auth/login", "", loginBody("M10028", "RATE@example.test", "x"), 429)
	// Spoofed forwarded headers still share the rate limit for the socket peer.
	for i := range 31 {
		r := httptest.NewRequest("POST", base+"/auth/login", strings.NewReader(loginBody("M10028", fmt.Sprintf("peer-%d@example.test", i), "x")))
		r.RemoteAddr = "203.0.113.5:12345"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+10))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if i < 30 && w.Code != 401 || i == 30 && (w.Code != 429 || w.Header().Get("Retry-After") == "") {
			t.Fatalf("peer rate check %d returned %d", i+1, w.Code)
		}
	}
	call("POST", "/auth/logout", winner, "", 204)
	call("GET", "/session", winner, "", 401)
	call("POST", "/members/"+memberID+"/disable", admin, "", 200)
	call("POST", mfaRoute, "", challengeBody(disabledChallenge, nextCode), 401)
	bucket := sha256.Sum256([]byte("login_identity\x00M10028\x00" + email))
	exec(`UPDATE merchant_auth_limits SET expires_at = clock_timestamp() WHERE bucket = $1`, bucket[:])
	login(401)
}

func TestSandboxAuthenticatorRFC6238(t *testing.T) {
	const seed = "12345678901234567890"
	for _, test := range []struct {
		timestamp int64
		code      string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{20000000000, "353130"},
	} {
		if mfaCode([]byte(seed), test.timestamp/30) != test.code {
			t.Fatalf("incorrect RFC 6238 output at timestamp %d", test.timestamp)
		}
	}
}
