package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

type member struct {
	ID         string `json:"id"`
	MerchantID string `json:"merchant_id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Active     bool   `json:"active"`
}

type memberPermission int

const (
	readInventory memberPermission = iota
	writeInventory
	manageMembers
	draftPayouts
	reviewPayouts
	manageSettlement
	manageAPIKeys
)

func (m member) permits(permission memberPermission) bool {
	switch m.Role {
	case "admin":
		return permission == readInventory || permission == writeInventory || permission == manageMembers ||
			permission == draftPayouts || permission == reviewPayouts || permission == manageSettlement ||
			permission == manageAPIKeys
	case "operator":
		return permission == readInventory || permission == writeInventory || permission == draftPayouts ||
			permission == manageSettlement
	case "viewer":
		return permission == readInventory
	default:
		return false
	}
}

type memberHandler func(http.ResponseWriter, *http.Request, *sql.Tx, member) (int, any, error)

func (s *server) registerMemberRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sandbox/workspace/channel-quotes", s.authorizeMember(readInventory, listMerchantChannelQuotes))
	mux.HandleFunc("GET /sandbox/workspace/settlement-summary", s.settlementReport(s.authorizeMember(readInventory, readWorkspaceSettlementSummary)))
	mux.HandleFunc("GET /sandbox/workspace/setup", s.authorizeMember(readInventory, readWorkspaceSetup))
	mux.HandleFunc("GET /sandbox/workspace/overview", s.authorizeMember(readInventory, readWorkspaceOverview))
	mux.HandleFunc("GET /sandbox/workspace/payments", s.authorizeMember(readInventory, s.listWorkspacePayments))
	mux.HandleFunc("GET /sandbox/workspace/session", s.authorizeMember(readInventory,
		func(_ http.ResponseWriter, _ *http.Request, _ *sql.Tx, m member) (int, any, error) {
			return http.StatusOK, m, nil
		}))
	mux.HandleFunc("GET /sandbox/workspace/members", s.authorizeMember(manageMembers, listMembers))
	mux.HandleFunc("GET /sandbox/workspace/members/{id}", s.authorizeMember(manageMembers, getMember))
	mux.HandleFunc("POST /sandbox/workspace/members/{id}/disable", s.authorizeMember(manageMembers, disableMember))
	mux.HandleFunc("GET /sandbox/workspace/sites", s.authorizeMember(readInventory, listSites))
	mux.HandleFunc("POST /sandbox/workspace/sites", s.authorizeMember(writeInventory, createSite))
	mux.HandleFunc("GET /sandbox/workspace/sites/{id}", s.authorizeMember(readInventory, getSite))
	mux.HandleFunc("GET /sandbox/workspace/channel-accounts", s.authorizeMember(readInventory, listChannelAccounts))
	mux.HandleFunc("POST /sandbox/workspace/channel-accounts", s.authorizeMember(readInventory, platformManaged))
	mux.HandleFunc("GET /sandbox/workspace/channel-accounts/{id}", s.authorizeMember(readInventory, getChannelAccount))
}

func (s *server) authorizeMember(permission memberPermission, next memberHandler) http.HandlerFunc {
	return s.withMember(permission, false, next)
}

func (s *server) withMember(permission memberPermission, exclusive bool, next memberHandler) http.HandlerFunc {
	lock := "FOR SHARE OF m, sess"
	if exclusive {
		lock = "FOR UPDATE OF m FOR SHARE OF sess"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		digest, ok := workspaceSessionDigest(r)
		if !ok {
			memberUnauthorized(w)
			return
		}
		ctx := r.Context()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			memberStorageError(w)
			return
		}
		defer tx.Rollback()
		// Keep the authorizing rows locked through the workspace operation.
		// Disabling or revoking an identity waits for authorized work to finish.
		var m member
		var verified, enabled bool
		err = tx.QueryRowContext(ctx, `
			SELECT m.id, m.merchant_id, m.name, m.role, true, sess.mfa_verified
			FROM merchant_sessions AS sess JOIN merchant_members AS m ON m.id = sess.member_id
			WHERE sess.token_hash = $1 AND sess.expires_at > clock_timestamp()
			  AND sess.revoked_at IS NULL AND m.disabled_at IS NULL
			`+lock, digest[:]).Scan(&m.ID, &m.MerchantID, &m.Name, &m.Role, &m.Active, &verified)
		if errors.Is(err, sql.ErrNoRows) {
			memberUnauthorized(w)
			return
		}
		if err != nil {
			memberStorageError(w)
			return
		}
		// Read current MFA policy after acquiring the member lock, in a fresh
		// PostgreSQL statement snapshot. Enrollment takes an exclusive lock.
		err = tx.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM merchant_credentials WHERE member_id = $1 AND mfa_secret IS NOT NULL)`,
			m.ID).Scan(&enabled)
		if err != nil {
			memberStorageError(w)
			return
		}
		if enabled && !verified {
			memberUnauthorized(w)
			return
		}
		if !m.permits(permission) {
			fail(w, http.StatusForbidden, "forbidden", "The member role does not allow this action.")
			return
		}
		status, body, err := next(w, r, tx, m)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			fail(w, http.StatusNotFound, "not_found", "The resource was not found in this workspace.")
		case err != nil:
			memberStorageError(w)
		case status == 0:
			// The handler already sent a validation error.
		case tx.Commit() != nil:
			memberStorageError(w)
		default:
			if file, ok := body.(settlementCSV); ok {
				file.write(w)
				return
			}
			respond(w, status, body)
		}
	}
}

func workspaceSessionDigest(r *http.Request) ([32]byte, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(r.Header.Values("Authorization")) != 1 {
		return [32]byte{}, false
	}
	return authTokenDigest(token, "fxs_")
}

func memberUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	fail(w, http.StatusUnauthorized, "unauthorized", "An active sandbox member session is required.")
}

func memberStorageError(w http.ResponseWriter) {
	fail(w, http.StatusServiceUnavailable, "database_unavailable", "The sandbox workspace operation could not be completed.")
}

func scanMember(row scanner) (member, error) {
	var m member
	err := row.Scan(&m.ID, &m.MerchantID, &m.Name, &m.Role, &m.Active)
	return m, err
}

func listMembers(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	return listScopedRows(r, tx, m.MerchantID, `
		SELECT id, merchant_id, name, role, disabled_at IS NULL
		FROM merchant_members WHERE merchant_id = $1 ORDER BY lower(name), id`, scanMember)
}

func getMember(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	target, err := scanMember(tx.QueryRowContext(r.Context(), `
		SELECT id, merchant_id, name, role, disabled_at IS NULL
		FROM merchant_members WHERE merchant_id = $1 AND id = $2`, m.MerchantID, r.PathValue("id")))
	return http.StatusOK, target, err
}

func disableMember(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
	if r.PathValue("id") == m.ID {
		fail(w, http.StatusBadRequest, "self_disable", "A member cannot disable their own account.")
		return 0, nil, nil
	}
	target, err := scanMember(tx.QueryRowContext(r.Context(), `
		UPDATE merchant_members SET disabled_at = COALESCE(disabled_at, clock_timestamp())
		WHERE merchant_id = $1 AND id = $2
		RETURNING id, merchant_id, name, role, false`, m.MerchantID, r.PathValue("id")))
	return http.StatusOK, target, err
}
