package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type platformPreview struct {
	accounts map[string]platformAccount

	mu       sync.Mutex
	window   time.Time
	attempts int
	sessions map[[32]byte]platformSession
}

type platformAccount struct {
	Hash string `json:"hash"`
	Role string `json:"role"`
}

type platformSession struct {
	User    string `json:"user"`
	Role    string `json:"role"`
	expires time.Time
}

func loadPlatformPreview() (*platformPreview, error) {
	accounts := make(map[string]platformAccount)
	user, hash := os.Getenv("FLOWXPAY_SANDBOX_OPS_USER"), os.Getenv("FLOWXPAY_SANDBOX_OPS_PASSWORD_HASH")
	if user != "" || hash != "" {
		accounts[user] = platformAccount{Hash: hash, Role: "viewer"}
	}
	if raw := os.Getenv("FLOWXPAY_LOCAL_PLATFORM_ACCOUNTS"); raw != "" {
		var local map[string]platformAccount
		if err := json.Unmarshal([]byte(raw), &local); err != nil || local == nil {
			return nil, errPlatformPreviewConfig
		}
		for name, account := range local {
			if _, exists := accounts[name]; exists {
				return nil, errPlatformPreviewConfig
			}
			accounts[name] = account
		}
	}
	if len(accounts) == 0 {
		return nil, nil
	}
	hashes := make(map[string]bool)
	for name, account := range accounts {
		cost, err := bcrypt.Cost([]byte(account.Hash))
		if err != nil || cost < 12 || name == "" || len(name) > 254 || strings.TrimSpace(name) != name ||
			(account.Role != "viewer" && account.Role != "operator") || hashes[account.Hash] {
			return nil, errPlatformPreviewConfig
		}
		hashes[account.Hash] = true
	}
	return &platformPreview{accounts: accounts, sessions: make(map[[32]byte]platformSession)}, nil
}

var errPlatformPreviewConfig = errors.New("invalid sandbox platform account configuration")

const platformSessionPrefix = "fxo_"

func (p *platformPreview) login(w http.ResponseWriter, r *http.Request) {
	var input struct{ User, Password string }
	if !parseJSON(w, r, &input) {
		return
	}
	now := time.Now()
	p.mu.Lock()
	if !now.Before(p.window.Add(time.Minute)) {
		p.window, p.attempts = now, 0
	}
	if p.attempts >= 10 {
		p.mu.Unlock()
		fail(w, 429, "ops_login_limited", "Too many attempts. Try again later.")
		return
	}
	p.attempts++
	p.mu.Unlock()
	account, known := p.accounts[input.User]
	if !known {
		// Preserve the password comparison for unknown accounts.
		for _, fallback := range p.accounts {
			account = fallback
			break
		}
	}
	validPassword := len(input.Password) >= 12 && len(input.Password) <= 72 &&
		bcrypt.CompareHashAndPassword([]byte(account.Hash), []byte(input.Password)) == nil
	if !known || !validPassword {
		fail(w, 401, "ops_login_invalid", "The platform account or password is incorrect.")
		return
	}
	session := platformSession{User: input.User, Role: account.Role, expires: now.Add(2 * time.Hour)}
	token, digest := newAuthToken(platformSessionPrefix)
	p.mu.Lock()
	for key, existing := range p.sessions {
		if !now.Before(existing.expires) {
			delete(p.sessions, key)
		}
	}
	p.sessions[digest] = session
	p.mu.Unlock()
	respond(w, 200, map[string]string{"token": token, "user": session.User, "role": session.Role})
}

func (p *platformPreview) authorize(next func(http.ResponseWriter, *http.Request, [32]byte, platformSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		digest, valid := authTokenDigest(token, platformSessionPrefix)
		p.mu.Lock()
		session := p.sessions[digest]
		p.mu.Unlock()
		if !bearer || !valid || len(r.Header.Values("Authorization")) != 1 || !time.Now().Before(session.expires) {
			fail(w, 401, "ops_session_required", "An active platform preview session is required.")
			return
		}
		next(w, r, digest, session)
	}
}

func (s *server) registerPlatformPreview(mux *http.ServeMux) {
	p := s.platform
	if p == nil {
		return
	}
	mux.HandleFunc("POST /sandbox/ops/login", p.login)
	mux.HandleFunc("POST /sandbox/ops/logout", p.authorize(func(w http.ResponseWriter, _ *http.Request, digest [32]byte, _ platformSession) {
		p.mu.Lock()
		delete(p.sessions, digest)
		p.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /sandbox/ops/dashboard", p.authorize(s.platformDashboard))
	s.registerPlatformOperations(mux)
}

type platformMerchant struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Sites    int    `json:"sites"`
	Accounts int    `json:"accounts"`
}

type platformVolume struct {
	Currency string `json:"currency"`
	Orders   int64  `json:"orders"`
	Paid     int64  `json:"paid"`
	Volume   string `json:"volume_minor"`
}

type platformPayment struct {
	ID        string     `json:"id"`
	Merchant  string     `json:"merchant"`
	Reference string     `json:"reference"`
	Currency  string     `json:"currency"`
	Amount    int64      `json:"amount_minor"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Market    string     `json:"market,omitempty"`
	Method    string     `json:"method,omitempty"`
	AccountID string     `json:"account_id,omitempty"`
}

type platformChannel struct {
	ID           string  `json:"id"`
	Merchant     string  `json:"merchant"`
	MerchantName string  `json:"merchant_name"`
	SiteName     string  `json:"site_name"`
	ChannelID    *string `json:"platform_channel_id,omitempty"`
	ChannelName  *string `json:"platform_channel_name,omitempty"`
	Provider     string  `json:"provider"`
	Enabled      bool    `json:"enabled"`
}

func readPlatformRows[T any](ctx context.Context, tx *sql.Tx, query string, scan func(*sql.Rows, *T) error, args ...any) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		var item T
		if err := scan(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *server) platformDashboard(w http.ResponseWriter, r *http.Request, _ [32]byte, session platformSession) {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		memberStorageError(w)
		return
	}
	defer tx.Rollback()
	query := r.URL.Query()
	merchant, reference, direction := query.Get("merchant"), query.Get("reference"), query.Get("direction")
	if direction != "" && direction != "collection" && direction != "payout" {
		fail(w, http.StatusBadRequest, "invalid_order_direction", "Select collection or payout.")
		return
	}
	if reference != "" && !clientID.MatchString(reference) {
		fail(w, http.StatusBadRequest, "invalid_payment_reference", "Supply a valid order ID or merchant reference.")
		return
	}
	if merchant != "" {
		if !clientID.MatchString(merchant) {
			fail(w, http.StatusBadRequest, "invalid_merchant", "Select a merchant ID.")
			return
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM merchants WHERE id=$1)", merchant).Scan(&exists); err != nil {
			memberStorageError(w)
			return
		}
		if !exists {
			fail(w, http.StatusNotFound, "ops_merchant_not_found", "Merchant not found.")
			return
		}
	}
	merchants, err := readPlatformRows(ctx, tx, `SELECT `+platformMerchantColumns+`
		FROM merchants m ORDER BY (m.id=$1) DESC,m.id LIMIT 100`, scanPlatformMerchant, merchant)
	if err != nil {
		memberStorageError(w)
		return
	}
	volumes, err := readPlatformRows(ctx, tx, `
		SELECT currency, count(*), count(*) FILTER (WHERE status='succeeded'),
		  COALESCE(sum(amount_minor) FILTER (WHERE status='succeeded'),0)::text
		FROM payments
		WHERE created_at >= (((now() AT TIME ZONE 'UTC')::date - 6)::timestamp AT TIME ZONE 'UTC')
		  AND ($1='' OR merchant_id=$1)
		GROUP BY currency ORDER BY currency`,
		func(rows *sql.Rows, v *platformVolume) error {
			return rows.Scan(&v.Currency, &v.Orders, &v.Paid, &v.Volume)
		}, merchant)
	if err != nil {
		memberStorageError(w)
		return
	}
	orderQuery := `
		SELECT id,merchant_id,merchant_reference,currency,amount_minor,
		  CASE WHEN status='requires_action' AND expires_at <= now() THEN 'expired' ELSE status END,created_at,
		  NULL::timestamptz,market,method,coalesce(route_snapshot->>'account_id','')
		FROM payments WHERE ($1='' OR merchant_id=$1) AND ($2='' OR id=$2 OR merchant_reference=$2)
		ORDER BY created_at DESC,id DESC LIMIT 50`
	if direction == "payout" {
		orderQuery = `
			SELECT id,merchant_id,merchant_reference,currency,amount_minor,status,created_at,updated_at,'','',account_id
			FROM sandbox_payouts WHERE ($1='' OR merchant_id=$1) AND ($2='' OR id=$2 OR merchant_reference=$2)
			ORDER BY created_at DESC,id DESC LIMIT 50`
	}
	payments, err := readPlatformRows(ctx, tx, orderQuery, func(rows *sql.Rows, p *platformPayment) error {
		return rows.Scan(&p.ID, &p.Merchant, &p.Reference, &p.Currency, &p.Amount, &p.Status, &p.CreatedAt,
			&p.UpdatedAt, &p.Market, &p.Method, &p.AccountID)
	}, merchant, reference)
	if err != nil {
		memberStorageError(w)
		return
	}
	channels, err := readPlatformRows(ctx, tx, `
		SELECT a.id,a.merchant_id,a.provider,a.enabled AND COALESCE(c.enabled,true),a.platform_channel_id,c.name,m.name,s.name
        FROM sandbox_channel_accounts a LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
        JOIN merchants m ON m.id=a.merchant_id
        JOIN merchant_sites s ON s.id=a.site_id AND s.merchant_id=a.merchant_id
        WHERE ($1='' OR a.merchant_id=$1) ORDER BY a.merchant_id,a.id LIMIT 100`,
		func(rows *sql.Rows, c *platformChannel) error {
			return rows.Scan(&c.ID, &c.Merchant, &c.Provider, &c.Enabled, &c.ChannelID, &c.ChannelName, &c.MerchantName, &c.SiteName)
		}, merchant)
	if err != nil {
		memberStorageError(w)
		return
	}
	result := map[string]any{"merchants": merchants, "volumes": volumes, "payments": payments, "channels": channels}
	if r.URL.Query().Get("tasks") == "1" {
		tasks, err := readPlatformTasks(ctx, tx, merchant, session)
		if err != nil {
			memberStorageError(w)
			return
		}
		result["tasks"] = tasks
		routes, err := readEffectiveCollectionRoutes(ctx, tx, merchant)
		if err == nil {
			result["sites"], err = readSiteCards(ctx, tx, merchant, routes)
		}
		if err != nil {
			memberStorageError(w)
			return
		}
	}
	respond(w, 200, result)
}
