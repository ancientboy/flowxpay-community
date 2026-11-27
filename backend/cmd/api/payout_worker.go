package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *server) dispatchPayout(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readPayout(tx.QueryRowContext(ctx, "SELECT "+payoutColumns+`
		FROM sandbox_payouts WHERE status='processing'
		ORDER BY updated_at,id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var enabled bool
	err = tx.QueryRowContext(ctx, `
		SELECT enabled FROM sandbox_channel_accounts
		WHERE id=$1 AND merchant_id=$2 FOR SHARE`, p.AccountID, p.merchantID).Scan(&enabled)
	if err != nil {
		return err
	}
	if enabled && p.PlatformChannelID != "" {
		err := lockEnabledPlatformChannel(ctx, tx, sql.NullString{String: p.PlatformChannelID, Valid: true})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		enabled = err == nil
	}
	failure := ""
	if !enabled {
		failure = "payout_account_disabled"
	}
	var simulation string
	var submittedAt time.Time
	if enabled {
		var maxPerSecond int
		var dailyMax int64
		// Serialize submissions and balance settlement for this payout account.
		// Read the database clock and committed usage in later statements, after
		// any concurrent transaction holding this row has committed.
		err = tx.QueryRowContext(ctx, `
			SELECT simulation,dispatches_per_second,daily_payout_minor FROM sandbox_payout_accounts
			WHERE account_id=$1 AND merchant_id=$2 FOR NO KEY UPDATE`, p.AccountID, p.merchantID).
			Scan(&simulation, &maxPerSecond, &dailyMax)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&submittedAt); err != nil {
			return err
		}
		failure, err = payoutDispatchFailure(ctx, tx, p, submittedAt, maxPerSecond, dailyMax)
		if err != nil {
			return err
		}
	}
	if failure != "" {
		if err := s.finishPayout(ctx, tx, p, "failed", "", failure); err != nil {
			return err
		}
		return tx.Commit()
	}

	// The mock receipt is durable before the platform observes a channel result.
	// Both the receipt and the move out of the dispatch queue commit together,
	// so another local worker cannot submit after a crash or lost acknowledgement.
	status, checkDelay := "succeeded", time.Second
	switch simulation {
	case "failure":
		status = "failed"
	case "disconnect":
		checkDelay = 5 * time.Second
	case "unresolved":
		status = "pending"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sandbox_channel_receipts (account_id,subject_id,reference,action)
		VALUES ($1,$2,$3,$4)`, p.AccountID, p.ID, p.ChannelReference, status); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sandbox_payouts SET status='pending_confirmation',submitted_at=$2,updated_at=$2,
			next_check_at=$2::timestamptz+$3 * interval '1 second' WHERE id=$1`, p.ID, submittedAt, int(checkDelay/time.Second)); err != nil {
		return err
	}
	return tx.Commit()
}

// The caller holds the payout account row lock. Each submitted order always
// counts as an attempt. Only pending and successful submissions use daily quota.
func payoutDispatchFailure(ctx context.Context, tx *sql.Tx, p payout, at time.Time, maxPerSecond int, dailyMax int64) (string, error) {
	var rateLimited, dailyLimited bool
	err := tx.QueryRowContext(ctx, `
		WITH limits AS (
			SELECT $3::timestamptz - interval '1 second' AS rate_start,
			       date_trunc('day', $3::timestamptz, 'UTC') AS day_start
		)
		SELECT count(*) FILTER (WHERE submitted_at>rate_start) >= $4,
		       COALESCE(sum(amount_minor) FILTER (
		           WHERE submitted_at>=day_start AND submitted_at<day_start+interval '24 hours'
		             AND status IN ('pending_confirmation','succeeded')
		       ),0) > $5::bigint - $6::bigint
		FROM sandbox_payouts CROSS JOIN limits
		WHERE merchant_id=$1 AND account_id=$2 AND submitted_at>=LEAST(rate_start,day_start)`,
		p.merchantID, p.AccountID, at, maxPerSecond, dailyMax, p.AmountMinor).Scan(&rateLimited, &dailyLimited)
	switch {
	case err != nil:
		return "", err
	case rateLimited:
		return "payout_rate_limited", nil
	case dailyLimited:
		return "payout_daily_limit_exceeded", nil
	default:
		return "", nil
	}
}

func (s *server) reconcilePayout(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readPayout(tx.QueryRowContext(ctx, "SELECT "+payoutColumns+`
		FROM sandbox_payouts WHERE status='pending_confirmation' AND next_check_at<=now()
		ORDER BY next_check_at,id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT action FROM sandbox_channel_receipts WHERE account_id=$1 AND subject_id=$2 AND reference=$3`,
		p.AccountID, p.ID, p.ChannelReference).Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if terminal(status) {
		var transfer string
		if status == "succeeded" {
			transfer = "utr_" + p.ID
		}
		err = s.finishPayout(ctx, tx, p, status, transfer, callbackFailure(status))
	} else {
		err = schedulePayoutCheck(ctx, tx, p)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func schedulePayoutCheck(ctx context.Context, tx *sql.Tx, p payout) error {
	attempt := p.checkAttempts + 1
	var next any
	if attempt < maxChannelChecks {
		next = time.Now().Add(time.Duration(5<<attempt) * time.Second)
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE sandbox_payouts SET check_attempts=$2, next_check_at=$3 WHERE id=$1`, p.ID, attempt, next)
	return err
}

// The caller holds the payout row lock. A terminal transition settles its one
// reservation and records the signed notification payload in the same transaction.
func (s *server) finishPayout(ctx context.Context, tx *sql.Tx, p payout, status, transfer, failure string) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE sandbox_payout_accounts SET reserved_minor=reserved_minor-$3,
			available_minor=available_minor+CASE WHEN $4='failed' THEN $3 ELSE 0 END
		WHERE merchant_id=$1 AND account_id=$2 AND reserved_minor >= $3`,
		p.merchantID, p.AccountID, p.AmountMinor, status)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return errors.New("payout reservation is inconsistent")
	}
	p, err = readPayout(tx.QueryRowContext(ctx, `
		UPDATE sandbox_payouts SET status=$2, transfer_reference=$3, failure_code=$4,
			updated_at=now(),next_check_at=NULL WHERE id=$1 RETURNING `+payoutColumns, p.ID, status, transfer, failure))
	if err != nil {
		return err
	}
	return s.enqueuePayoutEvent(ctx, tx, p)
}

func (s *server) enqueuePayoutEvent(ctx context.Context, tx *sql.Tx, p payout) error {
	return s.enqueueEvent(ctx, tx, p.merchantID, p.ID, businessEvent{
		Type: "payout." + p.Status, PayoutID: p.ID, AccountID: p.AccountID,
		TransferReference: p.TransferReference, Status: p.Status, AmountMinor: p.AmountMinor, Currency: p.Currency,
	})
}
