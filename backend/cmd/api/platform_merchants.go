package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const platformMerchantColumns = `m.id,m.name,
	(SELECT count(*) FROM merchant_sites WHERE merchant_id=m.id),
	(SELECT count(*) FROM sandbox_channel_accounts WHERE merchant_id=m.id)`

func scanPlatformMerchant(rows *sql.Rows, value *platformMerchant) error {
	return rows.Scan(&value.ID, &value.Name, &value.Sites, &value.Accounts)
}

func listPlatformMerchants(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _, _ string) (int, any, error) {
	query := r.URL.Query()
	search, after := strings.TrimSpace(query.Get("q")), query.Get("after")
	if utf8.RuneCountInString(search) > 100 || strings.ContainsRune(search, 0) ||
		(after != "" && !clientID.MatchString(after)) {
		fail(w, http.StatusBadRequest, "invalid_merchant_search", "Enter a merchant name or ID of up to 100 characters.")
		return 0, nil, nil
	}
	const pageSize = 100
	items, err := readPlatformRows(r.Context(), tx, `SELECT `+platformMerchantColumns+`
		FROM merchants m WHERE m.id > $2 AND
		($1='' OR strpos(lower(m.id), lower($1)) > 0 OR strpos(lower(m.name), lower($1)) > 0)
		ORDER BY m.id LIMIT $3`, scanPlatformMerchant, search, after, pageSize+1)
	if err != nil {
		return 0, nil, err
	}
	result := struct {
		Merchants []platformMerchant `json:"merchants"`
		Next      string             `json:"next_after,omitempty"`
	}{Merchants: items}
	if len(items) > pageSize {
		result.Merchants = items[:pageSize]
		result.Next = items[pageSize-1].ID
	}
	return http.StatusOK, result, nil
}

type onboardingInput struct {
	Name       string `json:"name"`
	SiteName   string `json:"site_name"`
	AdminName  string `json:"admin_name"`
	AdminEmail string `json:"admin_email"`
}

type onboardingStatus struct {
	MerchantID string    `json:"merchant_id"`
	SiteID     string    `json:"site_id"`
	Version    int       `json:"invite_version"`
	ExpiresAt  time.Time `json:"expires_at"`
	Status     string    `json:"invite_status"`
}

type issuedAdminInvitation struct {
	onboardingStatus
	InvitePath string `json:"invite_path"`
}

type issuedOnboarding struct {
	issuedAdminInvitation
	APIKey issuedAPIKey `json:"api_key"`
}

const onboardingSelect = `SELECT o.merchant_id, o.site_id, o.invite_version, i.expires_at,
	CASE WHEN i.consumed_at IS NOT NULL THEN 'accepted'
		WHEN i.expires_at <= clock_timestamp() THEN 'expired' ELSE 'pending' END
	FROM sandbox_merchant_onboardings o JOIN merchant_invitations i
	ON i.merchant_id=o.merchant_id AND i.token_hash=o.invite_hash`

func readOnboarding(row scanner) (onboardingStatus, error) {
	var value onboardingStatus
	err := row.Scan(&value.MerchantID, &value.SiteID, &value.Version, &value.ExpiresAt, &value.Status)
	return value, err
}

func onboardingRequestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key, ok := inventoryRequestKey(w, r)
	if ok && key == "" {
		fail(w, http.StatusBadRequest, "invalid_idempotency_key", "Supply one valid Idempotency-Key.")
	}
	return key, ok && key != ""
}

func onboardPlatformMerchant(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, _ string) (int, any, error) {
	key, ok := onboardingRequestKey(w, r)
	if !ok {
		return 0, nil, nil
	}
	var input onboardingInput
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	input.Name = strings.TrimSpace(input.Name)
	input.SiteName = strings.TrimSpace(input.SiteName)
	input.AdminName = strings.TrimSpace(input.AdminName)
	input.AdminEmail, ok = workspaceEmail(input.AdminEmail)
	if input.SiteName == "" {
		input.SiteName = input.Name
	}
	if input.AdminName == "" && ok {
		local := input.AdminEmail[:strings.LastIndexByte(input.AdminEmail, '@')]
		runes := []rune(local)
		input.AdminName = string(runes[:min(len(runes), 100)])
	}
	if !ok || !validPlatformLabel(input.Name) || !validPlatformLabel(input.SiteName) || !validPlatformLabel(input.AdminName) {
		fail(w, http.StatusBadRequest, "invalid_onboarding", "Enter a merchant name and a valid administrator email. Check any optional names.")
		return 0, nil, nil
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return 0, nil, err
	}
	hash := sha256.Sum256(payload)
	ctx := r.Context()

	// Serialize a request key across API processes before creating any merchant rows.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 620377312))", key); err != nil {
		return 0, nil, err
	}
	var merchant string
	var same bool
	err = tx.QueryRowContext(ctx, `
		SELECT merchant_id, request_hash=$2 FROM sandbox_merchant_onboardings WHERE request_key=$1`,
		key, hash[:]).Scan(&merchant, &same)
	if err == nil {
		if !same {
			return inventoryKeyConflict(w)
		}
		value, err := readOnboarding(tx.QueryRowContext(ctx, onboardingSelect+" WHERE o.merchant_id=$1", merchant))
		return http.StatusOK, value, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}

	for range idAttempts {
		id := newPlatformNumber("M", merchantLength)
		err = tx.QueryRowContext(ctx, `INSERT INTO merchants (id,name) VALUES ($1,$2)
			ON CONFLICT (id) DO NOTHING RETURNING id`, id, input.Name).Scan(&merchant)
		if !errors.Is(err, sql.ErrNoRows) {
			break
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, errIDAllocation
	}
	if err != nil {
		return 0, nil, err
	}
	siteID := newResourceID("site_")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO merchant_sites (id,merchant_id,name,is_default) VALUES ($1,$2,$3,true)`, siteID, merchant, input.SiteName); err != nil {
		return 0, nil, err
	}
	path, digest, expiry, err := issueInvitation(ctx, tx, merchant, input.AdminEmail, input.AdminName, "admin")
	if err != nil {
		return 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sandbox_merchant_onboardings (merchant_id,request_key,request_hash,site_id,invite_hash)
		VALUES ($1,$2,$3,$4,$5)`, merchant, key, hash[:], siteID, digest[:]); err != nil {
		return 0, nil, err
	}
	status, apiKey, err := insertPlatformAPIKey(w, r, tx, actor, merchant, "Initial sandbox key")
	if err != nil || status == 0 {
		return status, nil, err
	}
	value := onboardingStatus{MerchantID: merchant, SiteID: siteID, Version: 1, ExpiresAt: expiry, Status: "pending"}
	return http.StatusCreated, issuedOnboarding{issuedAdminInvitation{value, path}, apiKey},
		platformAudit(r, tx, actor, "merchant.onboard", merchant, merchant, value)
}

func reissuePlatformAdminInvitation(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	key, ok := onboardingRequestKey(w, r)
	if !ok {
		return 0, nil, nil
	}
	var input struct {
		Version int `json:"invite_version"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	if input.Version < 1 {
		fail(w, http.StatusBadRequest, "invalid_invite_version", "Supply the current invitation version.")
		return 0, nil, nil
	}
	ctx := r.Context()
	var oldHash []byte
	// Lock the merchant onboarding first, then read its current invitation in a fresh statement.
	if err := tx.QueryRowContext(ctx, `
		SELECT invite_hash FROM sandbox_merchant_onboardings WHERE merchant_id=$1 FOR UPDATE`, merchant).Scan(&oldHash); err != nil {
		return 0, nil, err
	}
	value, err := readOnboarding(tx.QueryRowContext(ctx, onboardingSelect+" WHERE o.merchant_id=$1 FOR UPDATE OF i", merchant))
	if err != nil {
		return 0, nil, err
	}
	var priorVersion int
	err = tx.QueryRowContext(ctx, `
		SELECT replaced_version FROM sandbox_merchant_invitation_reissues WHERE merchant_id=$1 AND request_key=$2`,
		merchant, key).Scan(&priorVersion)
	if err == nil {
		if priorVersion != input.Version {
			return inventoryKeyConflict(w)
		}
		return http.StatusOK, value, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	if value.Status == "accepted" {
		return routeConflict(w, "admin_invite_accepted", "The administrator has already accepted the invitation.")
	}
	if value.Version != input.Version {
		return routeConflict(w, "admin_invite_changed", "The invitation has changed. Refresh before trying again.")
	}

	var email, name string
	if err := tx.QueryRowContext(ctx, `
		UPDATE merchant_invitations SET expires_at=LEAST(expires_at,clock_timestamp())
		WHERE token_hash=$1 RETURNING email,name`, oldHash).Scan(&email, &name); err != nil {
		return 0, nil, err
	}
	path, digest, expiry, err := issueInvitation(ctx, tx, merchant, email, name, "admin")
	if err != nil {
		return 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sandbox_merchant_onboardings SET invite_hash=$2,invite_version=invite_version+1 WHERE merchant_id=$1`,
		merchant, digest[:]); err != nil {
		return 0, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sandbox_merchant_invitation_reissues (merchant_id,request_key,replaced_version) VALUES ($1,$2,$3)`,
		merchant, key, input.Version); err != nil {
		return 0, nil, err
	}
	value.Version++
	value.ExpiresAt, value.Status = expiry, "pending"
	return http.StatusCreated, issuedAdminInvitation{value, path}, platformAudit(r, tx, actor, "merchant.invite_reissue", merchant, merchant, value)
}
