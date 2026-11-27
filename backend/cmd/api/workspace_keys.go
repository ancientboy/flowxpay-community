package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

const apiKeyPrefix = "fxk_"

func (s *server) apiKeyMerchant(ctx context.Context, token string) (sandboxMerchant, error) {
	for _, legacy := range s.merchants {
		if legacy.Key != "" && subtle.ConstantTimeCompare([]byte(token), []byte(legacy.Key)) == 1 {
			var merchant sandboxMerchant
			err := s.db.QueryRowContext(ctx, `
				SELECT m.id, m.name FROM merchant_api_keys k JOIN merchants m ON m.id=k.merchant_id
				WHERE k.merchant_id=$1 AND k.id=$2 AND k.revoked_at IS NULL`,
				legacy.ID, "legacy_"+legacy.ID).Scan(&merchant.ID, &merchant.Name)
			return merchant, err
		}
	}
	var merchant sandboxMerchant
	digest, ok := authTokenDigest(token, apiKeyPrefix)
	if !ok {
		return merchant, sql.ErrNoRows
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT m.id, m.name FROM merchant_api_keys k JOIN merchants m ON m.id=k.merchant_id
		WHERE k.key_hash=$1 AND k.revoked_at IS NULL`, digest[:]).Scan(&merchant.ID, &merchant.Name)
	return merchant, err
}

type apiKeyMetadata struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Legacy     bool       `json:"legacy"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	CreatedBy  *string    `json:"created_by,omitempty"`
	ReplacesID *string    `json:"replaces_id,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	RevokedBy  *string    `json:"revoked_by,omitempty"`
	Platform   bool       `json:"platform"`
}

type issuedAPIKey struct {
	apiKeyMetadata
	Secret string `json:"secret"`
}

const apiKeyColumns = `id, label, key_hash IS NULL, created_at, COALESCE(platform_created_by,created_by), replaces_id, revoked_at, COALESCE(platform_revoked_by,revoked_by), platform_created_by IS NOT NULL`

func scanAPIKey(row scanner) (apiKeyMetadata, error) {
	var key apiKeyMetadata
	err := row.Scan(&key.ID, &key.Label, &key.Legacy, &key.CreatedAt, &key.CreatedBy, &key.ReplacesID, &key.RevokedAt, &key.RevokedBy, &key.Platform)
	return key, err
}

func (s *server) registerWorkspaceControlRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sandbox/workspace/api-keys", s.authorizeMember(manageAPIKeys, listAPIKeys))
	mux.HandleFunc("POST /sandbox/workspace/api-keys", s.authorizeMember(readInventory, platformManaged))
	mux.HandleFunc("POST /sandbox/workspace/api-keys/{id}/rotate", s.authorizeMember(manageAPIKeys, rotateWorkspaceKey))
	mux.HandleFunc("POST /sandbox/workspace/api-keys/{id}/revoke", s.authorizeMember(manageAPIKeys, revokeWorkspaceKey))
	mux.HandleFunc("GET /sandbox/workspace/route-changes", s.authorizeMember(readInventory, listRouteChanges))
	mux.HandleFunc("POST /sandbox/workspace/route-changes", s.authorizeMember(readInventory, platformManaged))
	mux.HandleFunc("POST /sandbox/workspace/route-changes/{id}/approve", s.authorizeMember(readInventory, platformManaged))
}

func listAPIKeys(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, "SELECT "+apiKeyColumns+`
		FROM merchant_api_keys WHERE merchant_id=$1 ORDER BY revoked_at IS NOT NULL, created_at DESC NULLS LAST, id`, scanAPIKey)
}

func rotateWorkspaceKey(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	var input struct {
		Label *string `json:"label"`
	}
	if r.ContentLength != 0 && !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	old, err := lockAPIKey(r, tx, m.MerchantID)
	if err != nil {
		return 0, nil, err
	}
	if old.Platform {
		return platformManaged(w, r, tx, m)
	}
	if old.RevokedAt != nil {
		return routeConflict(w, "key_retired", "This key has already been retired.")
	}
	label := old.Label
	if input.Label != nil {
		label = strings.TrimSpace(*input.Label)
	}
	if !validPlatformLabel(label) {
		fail(w, 400, "invalid_label", "Supply a label of 1 to 100 characters.")
		return 0, nil, nil
	}
	if _, err := retireAPIKey(r, tx, m.MerchantID, old.ID, &m.ID, nil); err != nil {
		return 0, nil, err
	}
	return insertAPIKey(w, r, tx, m.MerchantID, label, &m.ID, nil, &old.ID)
}

func revokeWorkspaceKey(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	old, err := lockAPIKey(r, tx, m.MerchantID)
	if err != nil {
		return 0, nil, err
	}
	if old.Platform {
		return platformManaged(w, r, tx, m)
	}
	if old.RevokedAt != nil {
		return http.StatusOK, old, nil
	}
	key, err := retireAPIKey(r, tx, m.MerchantID, old.ID, &m.ID, nil)
	return http.StatusOK, key, err
}

func issuePlatformKey(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	var input struct {
		Label string `json:"label"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	label := strings.TrimSpace(input.Label)
	if !validPlatformLabel(label) {
		fail(w, 400, "invalid_label", "Supply a label of 1 to 100 characters.")
		return 0, nil, nil
	}
	return insertPlatformAPIKey(w, r, tx, actor, merchant, label)
}

func insertPlatformAPIKey(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant, label string) (int, issuedAPIKey, error) {
	status, issued, err := insertAPIKey(w, r, tx, merchant, label, nil, &actor, nil)
	if err != nil || status == 0 {
		return status, issuedAPIKey{}, err
	}
	key := issued.apiKeyMetadata
	if err := platformAudit(r, tx, actor, "key.issue", merchant, key.ID, key); err != nil {
		return 0, issuedAPIKey{}, err
	}
	return status, issued, nil
}

func insertAPIKey(w http.ResponseWriter, r *http.Request, tx *sql.Tx, merchant, label string, memberActor, platformActor, replacesID *string) (int, issuedAPIKey, error) {
	secret, digest := newAuthToken(apiKeyPrefix)
	key, err := scanAPIKey(tx.QueryRowContext(r.Context(), `
		INSERT INTO merchant_api_keys (id,merchant_id,label,key_hash,created_at,created_by,platform_created_by,replaces_id)
		VALUES ($1,$2,$3,$4,clock_timestamp(),$5,$6,$7)
		ON CONFLICT (merchant_id, lower(label)) WHERE revoked_at IS NULL DO NOTHING
		RETURNING `+apiKeyColumns, newResourceID("key_"), merchant, label, digest[:], memberActor, platformActor, replacesID))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusConflict, "key_label_in_use", "An active key already uses this label.")
		return 0, issuedAPIKey{}, nil
	}
	if err != nil {
		return 0, issuedAPIKey{}, err
	}
	return http.StatusCreated, issuedAPIKey{apiKeyMetadata: key, Secret: secret}, nil
}

func revokePlatformKey(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	old, err := lockAPIKey(r, tx, merchant)
	if err != nil {
		return 0, nil, err
	}
	if !old.Platform {
		fail(w, 403, "legacy_key_protected", "This console can only revoke platform-issued keys.")
		return 0, nil, nil
	}
	if old.RevokedAt != nil {
		return http.StatusOK, old, nil
	}
	key, err := retireAPIKey(r, tx, merchant, old.ID, nil, &actor)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, key, platformAudit(r, tx, actor, "key.revoke", merchant, old.ID, key)
}

func lockAPIKey(r *http.Request, tx *sql.Tx, merchant string) (apiKeyMetadata, error) {
	return scanAPIKey(tx.QueryRowContext(r.Context(), "SELECT "+apiKeyColumns+`
		FROM merchant_api_keys WHERE merchant_id=$1 AND id=$2 FOR UPDATE`, merchant, r.PathValue("id")))
}

func retireAPIKey(r *http.Request, tx *sql.Tx, merchant, id string, memberActor, platformActor *string) (apiKeyMetadata, error) {
	return scanAPIKey(tx.QueryRowContext(r.Context(), `UPDATE merchant_api_keys
		SET revoked_at=clock_timestamp(),revoked_by=$3,platform_revoked_by=$4
		WHERE merchant_id=$1 AND id=$2 RETURNING `+apiKeyColumns, merchant, id, memberActor, platformActor))
}
