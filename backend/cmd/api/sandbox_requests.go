package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type requestInput struct {
	Action       string `json:"action"`
	Merchant     string `json:"merchant_id"`
	Account      string `json:"account_id"`
	Currency     string `json:"currency"`
	Dest         string `json:"destination_currency"`
	Key          string `json:"request_id"`
	Minor        string `json:"source_minor"`
	Network      string `json:"network"`
	Recipient    string `json:"recipient"`
	Batch        string `json:"batch_id"`
	Rate         string `json:"fx_rate"`
	Expected     string `json:"expected_amount"`
	Reason       string `json:"reason"`
	Proof        string `json:"transfer_reference"`
	Day          string `json:"paid_on"`
	Paid         string `json:"paid_amount"`
	PaidCurrency string `json:"paid_currency"`
}

var srID = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
var srRate = regexp.MustCompile("^(0|[1-9][0-9]{0,10})([.][0-9]{1,12})?$")

func (s *server) registerSandboxRequests(mux *http.ServeMux) {
	for _, method := range []string{"GET", "POST"} {
		write := method == "POST"
		perm := readInventory
		if write {
			perm = manageSettlement
		}
		mux.HandleFunc(method+" /sandbox/workspace/settlement-requests", s.authorizeMember(perm, func(w http.ResponseWriter, r *http.Request, tx *sql.Tx, m member) (int, any, error) {
			return sandboxRequests(w, r, tx, m.ID, m.MerchantID, true)
		}))
		mux.HandleFunc(method+" /sandbox/ops/settlement-requests", s.platformOperation(write, false, func(w http.ResponseWriter, r *http.Request, tx *sql.Tx, a, _ string) (int, any, error) {
			return sandboxRequests(w, r, tx, a, r.URL.Query().Get("merchant_id"), false)
		}))
	}
}
func sandboxRequests(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, m string, member bool) (int, any, error) {
	ctx := r.Context()
	reject := func(n int, code string) (int, any, error) {
		fail(w, n, code, strings.ReplaceAll(code, "_", " "))
		return 0, nil, nil
	}
	if r.Method == "GET" {
		var raw json.RawMessage
		var err error
		if r.URL.Query().Get("part") == "accounts" {
			err = tx.QueryRowContext(ctx, `select coalesce(jsonb_agg(jsonb_build_object('id',id,'currency',currency,'scale',scale,'settle_available_minor',settle_available_minor::text,'settlement_held_minor',settlement_held_minor::text) order by id),'[]') from sandbox_request_balances where merchant_id=$1`, m).Scan(&raw)
		} else {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page < 0 || page > 100000 {
				return reject(400, "invalid_page")
			}
			err = tx.QueryRowContext(ctx, `select coalesce(jsonb_agg(to_jsonb(x) order by created_at desc,id),'[]') from(select id,merchant_id,account_id,currency,2 source_scale,source_minor::text,requested_currency,network,recipient,phase,trim_scale(rate)::text rate,trim_scale(amount)::text amount,cancel_reason,transfer_reference,paid_on,created_at from sandbox_settlement_requests where ($1='' or merchant_id=$1) order by created_at desc,id limit 100 offset $2)x`, m, page*100).Scan(&raw)
		}
		return 200, raw, err
	}
	var q requestInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if dec.Decode(&q) != nil {
		return reject(400, "invalid_request")
	}
	if !member && q.Merchant == "" {
		return reject(400, "invalid_merchant")
	}
	if member && q.Merchant != "" && q.Merchant != m {
		return reject(403, "merchant_mismatch")
	}
	if !member {
		m = q.Merchant
	}
	if member != (q.Action == "submit" || q.Action == "approve") {
		return reject(403, "access_denied")
	}
	var id string
	if q.Action == "submit" {
		n, e := strconv.ParseInt(q.Minor, 10, 64)
		if e != nil || n <= 0 || !srID.MatchString(q.Key) || (q.Dest != q.Currency && q.Dest != "USDT") || len(strings.TrimSpace(q.Network)) == 0 || len(q.Network) > 48 || len(strings.TrimSpace(q.Recipient)) < 3 || len(q.Recipient) > 240 {
			return reject(400, "invalid_request")
		}
		if _, e = tx.ExecContext(ctx, "select pg_advisory_xact_lock(hashtextextended($1,35))", m+"/"+q.Key); e != nil {
			return 0, nil, e
		}
		var same bool
		e = tx.QueryRowContext(ctx, `select id,(account_id,currency,source_minor,requested_currency,network,recipient)=($3,$4,$5,$6,$7,$8) from sandbox_settlement_requests where merchant_id=$1 and request_key=$2`, m, q.Key, q.Account, q.Currency, n, q.Dest, q.Network, q.Recipient).Scan(&id, &same)
		if e == nil {
			if !same {
				return reject(409, "request_conflict")
			}
			return 200, map[string]string{"result": id}, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return 0, nil, e
		}
		var accountID string
		e = tx.QueryRowContext(ctx, "select id from sandbox_channel_accounts where merchant_id=$1 and id=$2 for update", m, q.Account).Scan(&accountID)
		if e != nil {
			return 0, nil, e
		}
		var available int64
		e = tx.QueryRowContext(ctx, "select available_minor from sandbox_payout_accounts where merchant_id=$1 and account_id=$2 and currency=$3 for update", m, q.Account, q.Currency).Scan(&available)
		poolReserved := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return 0, nil, e
		}
		e = tx.QueryRowContext(ctx, "select settle_available_minor from sandbox_request_balances where merchant_id=$1 and id=$2", m, q.Account).Scan(&available)
		if e != nil {
			return 0, nil, e
		}
		if available < n {
			return reject(409, "insufficient_eligible_balance")
		}
		_, e = tx.ExecContext(ctx, "update sandbox_payout_accounts set available_minor=available_minor-$3 where merchant_id=$1 and account_id=$2", m, q.Account, n)
		if e != nil {
			return 0, nil, e
		}
		e = tx.QueryRowContext(ctx, `insert into sandbox_settlement_requests(merchant_id,account_id,currency,request_key,source_minor,requested_currency,network,recipient,payout_pool_reserved,phase)values($1,$2,$3,$4,$5,$6,$7,$8,$9,'requested')returning id`, m, q.Account, q.Currency, q.Key, n, q.Dest, q.Network, q.Recipient, poolReserved).Scan(&id)
		if e != nil {
			return 0, nil, e
		}
	} else {
		if !srID.MatchString(q.Batch) {
			return reject(400, "invalid_batch")
		}
		var phase, c, dest, a string
		var n int64
		var poolReserved bool
		e := tx.QueryRowContext(ctx, "select phase,currency,requested_currency,account_id,source_minor,payout_pool_reserved from sandbox_settlement_requests where merchant_id=$1 and id=$2 for update", m, q.Batch).Scan(&phase, &c, &dest, &a, &n, &poolReserved)
		if e != nil {
			return 0, nil, e
		}
		id = q.Batch
		var sqlQuery string
		args := []any{id}
		switch q.Action {
		case "quote":
			if (phase != "requested" && phase != "quoted") || !srRate.MatchString(q.Rate) {
				return reject(409, "invalid_quote")
			}
			precision := 2
			if dest == "USDT" {
				precision = 6
			}
			sqlQuery = `update sandbox_settlement_requests set phase='quoted',rate=$2::numeric,amount=round(source_minor*$2::numeric/100,$3) where id=$1 and $2::numeric>0 and (currency<>requested_currency or $2::numeric=1) and round(source_minor*$2::numeric/100,$3)>0`
			args = append(args, q.Rate, precision)
		case "approve":
			if (phase != "quoted" && phase != "approved") || !srRate.MatchString(q.Rate) || !srRate.MatchString(q.Expected) {
				return reject(409, "stale_quote")
			}
			sqlQuery = "update sandbox_settlement_requests set phase='approved' where id=$1 and rate=$2::numeric and amount=$3::numeric"
			args = append(args, q.Rate, q.Expected)
		case "paying":
			if phase != "approved" && phase != "paying" {
				return reject(409, "settlement_state_conflict")
			}
			sqlQuery = "update sandbox_settlement_requests set phase='paying' where id=$1"
		case "cancel":
			if phase == "cancelled" {
				var same bool
				e = tx.QueryRowContext(ctx, "select cancel_reason=$2 from sandbox_settlement_requests where id=$1", id, q.Reason).Scan(&same)
				if e != nil {
					return 0, nil, e
				}
				if same {
					return 200, map[string]string{"result": id}, nil
				}
			}
			if phase == "paying" || phase == "confirmed" || phase == "cancelled" || len(strings.TrimSpace(q.Reason)) == 0 || len(q.Reason) > 300 {
				return reject(409, "settlement_state_conflict")
			}
			_, e = tx.ExecContext(ctx, "update sandbox_payout_accounts set available_minor=available_minor+$3 where merchant_id=$1 and account_id=$2 and $4", m, a, n, poolReserved)
			if e != nil {
				return 0, nil, e
			}
			sqlQuery = "update sandbox_settlement_requests set phase='cancelled',cancel_reason=$2 where id=$1"
			args = append(args, q.Reason)
		case "confirm":
			_, dateErr := time.Parse(time.DateOnly, q.Day)
			if (phase != "paying" && phase != "confirmed") || q.PaidCurrency != dest || !srRate.MatchString(q.Paid) || dateErr != nil || q.Day > time.Now().UTC().Format(time.DateOnly) || len(strings.TrimSpace(q.Proof)) == 0 || len(q.Proof) > 120 {
				return reject(409, "invalid_payment_proof")
			}
			if phase == "confirmed" {
				var same bool
				e = tx.QueryRowContext(ctx, "select amount=$2::numeric and transfer_reference=$3 and paid_on=$4::date from sandbox_settlement_requests where id=$1", id, q.Paid, q.Proof, q.Day).Scan(&same)
				if e != nil {
					return 0, nil, e
				}
				if same {
					return 200, map[string]string{"result": id}, nil
				}
				return reject(409, "payment_proof_conflict")
			}
			if _, e = tx.ExecContext(ctx, "select pg_advisory_xact_lock(hashtextextended($1,36))", dest+"/"+q.Proof); e != nil {
				return 0, nil, e
			}
			var used bool
			e = tx.QueryRowContext(ctx, "select exists(select from sandbox_settlement_requests where requested_currency=$1 and transfer_reference=$2 and phase='confirmed')", dest, q.Proof).Scan(&used)
			if e != nil {
				return 0, nil, e
			}
			if used {
				return reject(409, "payment_proof_conflict")
			}
			sqlQuery = "update sandbox_settlement_requests set phase='confirmed' ,paid_on=$2,transfer_reference=$3 where id=$1 and amount=$4::numeric and $2::date >= (created_at at time zone 'UTC')::date"
			args = append(args, q.Day, q.Proof, q.Paid)
		default:
			return reject(400, "invalid_action")
		}
		res, e := tx.ExecContext(ctx, sqlQuery, args...)
		if e != nil {
			return 0, nil, e
		}
		changed, e := res.RowsAffected()
		if e != nil {
			return 0, nil, e
		}
		if changed != 1 {
			return reject(409, "settlement_state_conflict")
		}
	}
	if e := platformAudit(r, tx, actor, "settlement."+q.Action, m, id, map[string]string{"phase": q.Action}); e != nil {
		return 0, nil, e
	}
	return 200, map[string]string{"result": id}, nil
}
