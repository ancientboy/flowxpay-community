package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

type platformHandler func(http.ResponseWriter, *http.Request, *sql.Tx, string, string) (int, any, error)

func (s *server) registerPlatformOperations(mux *http.ServeMux) {
	s.registerSandboxRequests(mux)
	mux.HandleFunc("GET /sandbox/ops/merchants", s.platformOperation(false, false, listPlatformMerchants))
	mux.HandleFunc("POST /sandbox/ops/merchants", s.platformOperation(true, false, onboardPlatformMerchant))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/admin-invitation", s.platformOperation(true, true, reissuePlatformAdminInvitation))
	mux.HandleFunc("GET /sandbox/ops/operations", s.platformOperation(false, false, readPlatformOperations))
	mux.HandleFunc("GET /sandbox/ops/finance", s.platformOperation(false, false, readPlatformFinance))
	mux.HandleFunc("GET /sandbox/ops/prices", s.platformOperation(false, false, listChannelPrices))
	mux.HandleFunc("GET /sandbox/ops/merchants/{merchant}/prices", s.platformOperation(false, true, listChannelPrices))
	mux.HandleFunc("PUT /sandbox/ops/channels/{id}/prices", s.platformOperation(true, false, setChannelPrice))
	mux.HandleFunc("PUT /sandbox/ops/merchants/{merchant}/channels/{id}/prices", s.platformOperation(true, true, setChannelPrice))
	mux.HandleFunc("POST /sandbox/ops/channels", s.platformOperation(true, false, createPlatformChannel))
	mux.HandleFunc("PUT /sandbox/ops/channels/{id}/status", s.platformOperation(true, false, setPlatformChannelStatus))
	mux.HandleFunc("GET /sandbox/ops/merchants/{merchant}/sites/{site}/switch/{id}", s.platformOperation(false, true, previewCollectionSwitch))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/sites/{site}/switch/{id}", s.platformOperation(true, true, confirmCollectionSwitch))
	mux.HandleFunc("GET /sandbox/ops/merchants/{merchant}/order-fees", s.platformOperation(false, true, listPlatformOrderFees))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/assignments", s.platformOperation(true, true, assignPlatformChannel))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/routes", s.platformOperation(true, true, submitPlatformRoute))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/routes/{id}/approve", s.platformOperation(true, true, approvePlatformRoute))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/routes/{id}/cancel", s.platformOperation(true, true, cancelPlatformRoute))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/api-keys", s.platformOperation(true, true, issuePlatformKey))
	mux.HandleFunc("POST /sandbox/ops/merchants/{merchant}/api-keys/{id}/revoke", s.platformOperation(true, true, revokePlatformKey))
}

func (s *server) platformOperation(write, merchantRequired bool, next platformHandler) http.HandlerFunc {
	return s.platform.authorize(func(w http.ResponseWriter, r *http.Request, _ [32]byte, session platformSession) {
		if write && session.Role != "operator" {
			fail(w, http.StatusForbidden, "ops_forbidden", "A platform operator role is required.")
			return
		}
		merchant := r.PathValue("merchant")
		if !write && (r.URL.Path == "/sandbox/ops/operations" || r.URL.Path == "/sandbox/ops/finance") {
			merchant = r.URL.Query().Get("merchant")
		}
		if (merchantRequired || merchant != "") && !clientID.MatchString(merchant) {
			fail(w, http.StatusBadRequest, "invalid_merchant", "Select a merchant ID.")
			return
		}
		opts := &sql.TxOptions{ReadOnly: !write}
		if !write {
			opts.Isolation = sql.LevelRepeatableRead
		}
		tx, err := s.db.BeginTx(r.Context(), opts)
		if err != nil {
			memberStorageError(w)
			return
		}
		defer tx.Rollback()
		var status int
		var body any
		if merchant != "" {
			var existing string
			err = tx.QueryRowContext(r.Context(), "SELECT id FROM merchants WHERE id=$1", merchant).Scan(&existing)
		}
		if err == nil {
			status, body, err = next(w, r.WithContext(context.WithValue(r.Context(), platformRoleKey{}, session.Role)), tx, session.User, merchant)
		}
		switch {
		case errors.Is(err, sql.ErrNoRows):
			fail(w, http.StatusNotFound, "ops_resource_not_found", "The resource was not found for this merchant or site.")
		case err != nil:
			memberStorageError(w)
		case status == 0:
			// The operation already returned a validation error.
		case tx.Commit() != nil:
			memberStorageError(w)
		default:
			respond(w, status, body)
		}
	})
}

func platformAudit(r *http.Request, tx *sql.Tx, actor, action, merchant, id string, details any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO sandbox_platform_audit (actor,action,merchant_id,resource_id,details)
		VALUES ($1,$2,NULLIF($3,''),$4,$5::jsonb)`, actor, action, merchant, id, payload)
	return err
}

type platformChannelConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
	capability
}

func scanPlatformChannel(row scanner) (platformChannelConfig, error) {
	var value platformChannelConfig
	err := row.Scan(&value.ID, &value.Name, &value.Provider, &value.Enabled)
	profile, _ := profileFor(value.Provider)
	value.capability = profile.capability
	return value, err
}

func validPlatformLabel(value string) bool {
	return value != "" && utf8.RuneCountInString(value) <= 100 && !strings.ContainsRune(value, 0)
}

var platformChannelNames = map[string]string{
	"mock_pix": "Mock Pix", "mock_card": "Mock Card",
	"mock_in_redirect": "Mock India redirect", "mock_in_qr": "Mock India QR",
	"mock_in_unknown": "Mock India unsupported",
}

func createPlatformChannel(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, _ string) (int, any, error) {
	var input struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Enabled  *bool  `json:"enabled"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	input.Name = strings.TrimSpace(input.Name)
	if _, ok := profileFor(input.Provider); !ok || input.Enabled == nil {
		fail(w, 400, "invalid_channel", "Select a mock channel template and supply its status.")
		return 0, nil, nil
	}
	generated := input.ID == ""
	if generated && input.Name == "" {
		input.Name = platformChannelNames[input.Provider]
	}
	if !validPlatformLabel(input.Name) || (!generated && !clientID.MatchString(input.ID)) {
		fail(w, 400, "invalid_channel", "Check the channel name and any supplied channel ID.")
		return 0, nil, nil
	}
	ctx := r.Context()
	var key string
	var requestHash []byte
	if generated {
		var ok bool
		key, ok = onboardingRequestKey(w, r)
		if !ok {
			return 0, nil, nil
		}
		payload, err := json.Marshal(input)
		if err != nil {
			return 0, nil, err
		}
		digest := sha256.Sum256(payload)
		requestHash = digest[:]
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 620377313))", key); err != nil {
			return 0, nil, err
		}
		var id string
		var same bool
		err = tx.QueryRowContext(ctx, `SELECT id, creation_request_hash=$2 FROM sandbox_platform_channels
			WHERE creation_request_key=$1`, key, requestHash).Scan(&id, &same)
		if err == nil {
			if !same {
				return inventoryKeyConflict(w)
			}
			value, err := scanPlatformChannel(tx.QueryRowContext(ctx, `SELECT id,name,provider,enabled
				FROM sandbox_platform_channels WHERE id=$1`, id))
			return http.StatusOK, value, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, nil, err
		}
	}
	for range 3 {
		if generated {
			input.ID = newResourceID("channel_")
		}
		value, err := scanPlatformChannel(tx.QueryRowContext(ctx, `
			INSERT INTO sandbox_platform_channels (id,name,provider,enabled,creation_request_key,creation_request_hash)
			VALUES ($1,$2,$3,$4,NULLIF($5,''),$6)
			ON CONFLICT (id) DO NOTHING RETURNING id,name,provider,enabled`,
			input.ID, input.Name, input.Provider, *input.Enabled, key, requestHash))
		if errors.Is(err, sql.ErrNoRows) {
			if !generated {
				return routeConflict(w, "channel_exists", "This channel ID already exists.")
			}
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, value, platformAudit(r, tx, actor, "channel.create", "", value.ID, value)
	}
	return 0, nil, errors.New("could not allocate a platform channel ID")
}

func setPlatformChannelStatus(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, _ string) (int, any, error) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	if input.Enabled == nil {
		fail(w, 400, "invalid_channel_status", "Supply a channel status.")
		return 0, nil, nil
	}
	id := r.PathValue("id")
	old, err := scanPlatformChannel(tx.QueryRowContext(r.Context(), `SELECT id,name,provider,enabled
		FROM sandbox_platform_channels WHERE id=$1 FOR UPDATE`, id))
	if err != nil || old.Enabled == *input.Enabled {
		return 200, old, err
	}
	value, err := scanPlatformChannel(tx.QueryRowContext(r.Context(), `UPDATE sandbox_platform_channels SET enabled=$2
		WHERE id=$1 RETURNING id,name,provider,enabled`, id, *input.Enabled))
	if err != nil {
		return 0, nil, err
	}
	return 200, value, platformAudit(r, tx, actor, "channel.status", "", id, map[string]bool{"before": old.Enabled, "enabled": value.Enabled})
}

func assignPlatformChannel(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	var input struct {
		SiteID    string `json:"site_id"`
		ChannelID string `json:"channel_id"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	if !clientID.MatchString(input.SiteID) || !clientID.MatchString(input.ChannelID) {
		fail(w, 400, "invalid_assignment", "Select a site and a mock channel.")
		return 0, nil, nil
	}
	if err := lockRouteSite(r, tx, merchant, input.SiteID); err != nil {
		return 0, nil, err
	}
	channel, err := scanPlatformChannel(tx.QueryRowContext(r.Context(), `
		SELECT id,name,provider,enabled FROM sandbox_platform_channels WHERE id=$1 FOR SHARE`, input.ChannelID))
	if err != nil {
		return 0, nil, err
	}
	return assignChannelToSite(w, r, tx, actor, merchant, input.SiteID, channel)
}

func assignChannelToSite(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant, site string, channel platformChannelConfig) (int, any, error) {
	// Assignments stay on standby, including the first account for a method.
	account, err := scanChannelAccount(tx.QueryRowContext(r.Context(), `
		INSERT INTO sandbox_channel_accounts (id,merchant_id,site_id,provider,platform_channel_id,routing_standby)
		SELECT $1,$2,$3,$4,$5,true WHERE $6::boolean
		ON CONFLICT (merchant_id,site_id,platform_channel_id) WHERE platform_channel_id IS NOT NULL DO NOTHING
		RETURNING id,site_id,provider,environment,$6::boolean,platform_channel_id,true,$7::text`,
		newResourceID("acct_"), merchant, site, channel.Provider, channel.ID, channel.Enabled, channel.Name))
	if errors.Is(err, sql.ErrNoRows) {
		account, err = scanChannelAccount(tx.QueryRowContext(r.Context(), channelAccountSelect+`
		WHERE a.merchant_id=$1 AND a.site_id=$2 AND a.platform_channel_id=$3`, merchant, site, channel.ID))
		if errors.Is(err, sql.ErrNoRows) && !channel.Enabled {
			return routeConflict(w, "channel_disabled", "This channel is disabled.")
		}
		return 200, account, err
	}
	if err != nil {
		return 0, nil, err
	}
	return 201, account, platformAudit(r, tx, actor, "channel.assign", merchant, account.ID, account)
}
