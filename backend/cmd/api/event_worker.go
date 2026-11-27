package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

type channelLookup struct {
	SubjectID string
	Reference string
	Route     collectionRoute
	Kind      string
}

type channelObservation struct {
	Reference string
	Action    string
	Status    string
}

type channelRecovery interface {
	Refund(context.Context, sandboxRefund) error
	Lookup(context.Context, *sql.Tx, channelLookup) (channelObservation, error)
}

type sandboxChannelRecovery struct{ db *sql.DB }

func (s *server) channelRecovery() channelRecovery {
	if s.recovery != nil {
		return s.recovery
	}
	return sandboxChannelRecovery{s.db}
}

func storeMockReceipt(ctx context.Context, db *sql.DB, account, id, reference, action string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO sandbox_channel_receipts (account_id, subject_id, reference, action)
		VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, account, id, reference, action)
	return err
}

func (mock sandboxChannelRecovery) Refund(ctx context.Context, f sandboxRefund) error {
	var original string
	err := mock.db.QueryRowContext(ctx, `
		SELECT channel_reference FROM payments
		WHERE id=$1 AND status='succeeded' AND route_snapshot->>'account_id'=$2`,
		f.PaymentID, f.Route.AccountID).Scan(&original)
	if err != nil {
		return err
	}
	if original != f.Original {
		return errChannelRejected
	}
	return storeMockReceipt(ctx, mock.db, f.Route.AccountID, f.ID, f.Reference, "")
}

func (sandboxChannelRecovery) Lookup(ctx context.Context, tx *sql.Tx, lookup channelLookup) (channelObservation, error) {
	var result channelObservation
	err := tx.QueryRowContext(ctx, `
		SELECT reference, action FROM sandbox_channel_receipts WHERE account_id=$1 AND subject_id=$2`,
		lookup.Route.AccountID, lookup.SubjectID).Scan(&result.Reference, &result.Action)
	if err == nil && lookup.Kind == "refund" {
		result.Status = "succeeded"
	}
	return result, err
}

const maxChannelChecks = 8

func (s *server) runEventWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		for _, work := range []func(context.Context) error{s.reconcilePayment, s.reconcileRefund, s.dispatchPayout, s.reconcilePayout, s.deliverWebhook} {
			attempt, cancel := context.WithTimeout(ctx, 4*time.Second)
			err := work(attempt)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Print("sandbox event worker could not complete a database operation")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) reconcilePayment(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, err := readPayment(tx.QueryRowContext(ctx, "SELECT "+paymentColumns+`
		FROM payments WHERE status='pending_confirmation' AND next_check_at<=now()
		ORDER BY next_check_at, id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	route, valid := resolveOriginalRoute(p)
	if !valid {
		// Legacy or invalid routes cannot be queried.
		_, err := tx.ExecContext(ctx, "UPDATE payments SET next_check_at=NULL, check_attempts=$2 WHERE id=$1", p.ID, maxChannelChecks)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	ref := p.ChannelReference.String
	if ref == "" {
		ref = "sim_" + p.ID
	}
	result, limited, err := s.lookupOriginal(ctx, tx, channelLookup{p.ID, ref, route, "payment"})
	if err != nil {
		return err
	}
	switch {
	case terminal(result.Status):
		p, err = s.finishPayment(ctx, tx, p, result.Status, result.Reference, callbackFailure(result.Status))
	case result.Reference != "" && p.FailureCode.String == "channel_confirmation_pending":
		p, err = s.applyCollectionResult(ctx, tx, p, route, collectionResult{result.Reference, result.Action}, nil)
	}
	if err != nil {
		return err
	}
	if p.Status == "pending_confirmation" {
		_, err = tx.ExecContext(ctx, `
			UPDATE payments SET check_attempts=check_attempts+CASE WHEN $2 THEN 0 ELSE 1 END,
			next_check_at=CASE WHEN $2 THEN now()+interval '1 second'
				WHEN check_attempts+1 >= $3 THEN NULL
				ELSE now()+interval '5 seconds'*(check_attempts+1) END WHERE id=$1`,
			p.ID, limited, maxChannelChecks)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *server) reconcileRefund(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	f, err := readRefund(tx.QueryRowContext(ctx, "SELECT "+refundColumns+`
		FROM sandbox_refunds WHERE status='pending_confirmation' AND next_check_at<=now()
		ORDER BY next_check_at, id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED`))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	result, limited, err := s.lookupOriginal(ctx, tx, channelLookup{f.ID, f.Reference, f.Route, "refund"})
	if err != nil {
		return err
	}
	if terminal(result.Status) {
		_, err = s.finishRefund(ctx, tx, f, result.Status, callbackFailure(result.Status))
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE sandbox_refunds SET check_attempts=check_attempts+CASE WHEN $2 THEN 0 ELSE 1 END,
			next_check_at=CASE WHEN $2 THEN now()+interval '1 second'
				WHEN check_attempts+1 >= $3 THEN NULL
				ELSE now()+interval '5 seconds'*(check_attempts+1) END WHERE id=$1`,
			f.ID, limited, maxChannelChecks)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *server) lookupOriginal(ctx context.Context, tx *sql.Tx, lookup channelLookup) (channelObservation, bool, error) {
	if err := takeCollectionSlot(ctx, tx, lookup.Route); err != nil {
		if errors.Is(err, errChannelRateLimited) {
			return channelObservation{}, true, nil
		}
		return channelObservation{}, false, err
	}
	result, err := s.channelRecovery().Lookup(ctx, tx, lookup)
	if err != nil || result.Reference != lookup.Reference {
		return channelObservation{}, false, nil
	}
	return result, false, nil
}
