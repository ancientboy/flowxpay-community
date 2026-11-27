package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type settlementAction struct {
	Revision int             `json:"revision"`
	Actor    string          `json:"member_id"`
	Action   string          `json:"action"`
	Note     string          `json:"note"`
	Before   json.RawMessage `json:"before_check"`
	After    json.RawMessage `json:"after_check"`
	At       time.Time       `json:"created_at"`
}

func listSettlementActions(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _ member, scope settlementScope) (int, any, error) {
	after := 0
	if raw := r.URL.Query().Get("after"); raw != "" {
		var err error
		after, err = strconv.Atoi(raw)
		if err != nil || after < 0 || after > 2147483647 {
			return settlementError(w, http.StatusBadRequest, "invalid_cursor", "Use the cursor from the previous page.")
		}
	}
	ctx := r.Context()
	id := r.PathValue("entry")
	var found string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM sandbox_settlement_entries WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND id=$4`,
		scope.merchant, scope.account, scope.currency, id).Scan(&found)
	if err != nil {
		return 0, nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT revision,member_id,action,note,before_check,after_check,created_at FROM sandbox_settlement_actions
		WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND entry_id=$4 AND revision>$5
		ORDER BY revision LIMIT $6`, scope.merchant, scope.account, scope.currency, id, after, settlementPageSize+1)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	page := settlementPage[settlementAction]{Data: []settlementAction{}}
	for rows.Next() {
		var a settlementAction
		if err := rows.Scan(&a.Revision, &a.Actor, &a.Action, &a.Note, &a.Before, &a.After, &a.At); err != nil {
			return 0, nil, err
		}
		if len(page.Data) == settlementPageSize {
			page.NextAfter = strconv.Itoa(page.Data[len(page.Data)-1].Revision)
			break
		}
		page.Data = append(page.Data, a)
	}
	return http.StatusOK, page, rows.Err()
}

func actOnSettlement(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member, scope settlementScope) (int, any, error) {
	var input struct {
		Action           string `json:"action"`
		Note             string `json:"note"`
		ExpectedRevision *int   `json:"expected_revision"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	input.Note = strings.TrimSpace(input.Note)
	if input.Note == "" || strings.ContainsRune(input.Note, 0) || utf8.RuneCountInString(input.Note) > 300 ||
		input.ExpectedRevision == nil || *input.ExpectedRevision < 0 || *input.ExpectedRevision >= 2147483647 ||
		(input.Action != "acknowledge" && input.Action != "reopen" && input.Action != "recheck") {
		return settlementError(w, http.StatusBadRequest, "invalid_action", "Provide an action, a note of up to 300 characters, and the current revision.")
	}

	ctx := r.Context()
	id := r.PathValue("entry")
	entry, err := readSettlementEntry(tx.QueryRowContext(ctx, "SELECT "+settlementEntryColumns+`
		FROM sandbox_settlement_entries WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND id=$4 FOR UPDATE`,
		scope.merchant, scope.account, scope.currency, id))
	if err != nil {
		return 0, nil, err
	}
	if entry.Revision != *input.ExpectedRevision {
		return settlementError(w, http.StatusConflict, "revision_conflict", "This entry changed. Reload it before recording an action.")
	}
	before, err := json.Marshal(entry)
	if err != nil {
		return 0, nil, err
	}
	switch input.Action {
	case "acknowledge", "reopen":
		required := "open"
		if input.Action == "reopen" {
			required = "acknowledged"
		}
		if entry.Difference != required {
			return settlementError(w, http.StatusConflict, "difference_state_conflict", "The current difference status does not allow this action.")
		}
		entry.Acknowledged = input.Action == "acknowledge"
	case "recheck":
		var date string
		err := tx.QueryRowContext(ctx, `
			SELECT to_char(b.statement_date,'YYYY-MM-DD') FROM sandbox_settlement_batches b
			JOIN sandbox_settlement_entries e ON (b.merchant_id,b.account_id,b.currency,b.id)=(e.merchant_id,e.account_id,e.currency,e.batch_id)
			WHERE e.merchant_id=$1 AND e.account_id=$2 AND e.currency=$3 AND e.id=$4`,
			scope.merchant, scope.account, scope.currency, id).Scan(&date)
		if err != nil {
			return 0, nil, err
		}
		check, err := matchSettlement(ctx, tx, scope, entry.statementRow, date)
		if err != nil {
			return 0, nil, err
		}
		previous := entry.orderCheck
		amountChanged := (previous.Expected == nil) != (check.Expected == nil) ||
			(previous.Expected != nil && check.Expected != nil && *previous.Expected != *check.Expected)
		if previous.ID != check.ID || previous.Status != check.Status || previous.Match != check.Match || amountChanged {
			entry.Acknowledged = false
		}
		entry.orderCheck = check
	}
	entry.Revision++
	entry.updateDifference()
	after, err := json.Marshal(entry)
	if err != nil {
		return 0, nil, err
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE sandbox_settlement_entries SET order_id=NULLIF($5,''),order_status=NULLIF($6,''),
		    expected_gross_minor=$7,match_status=$8,acknowledged=$9,revision=$10
		WHERE merchant_id=$1 AND account_id=$2 AND currency=$3 AND id=$4`,
		scope.merchant, scope.account, scope.currency, id, entry.orderCheck.ID, entry.Status,
		entry.Expected, entry.Match, entry.Acknowledged, entry.Revision)
	if err != nil {
		return 0, nil, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sandbox_settlement_actions
		    (merchant_id,account_id,currency,entry_id,revision,member_id,action,note,before_check,after_check)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		scope.merchant, scope.account, scope.currency, id, entry.Revision, m.ID, input.Action, input.Note, string(before), string(after))
	return http.StatusOK, entry, err
}
