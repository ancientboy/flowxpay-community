package main

import (
	"net/http"
	"time"
)

type collectionException struct {
	Kind      string    `json:"kind"`
	PaymentID string    `json:"payment_id"`
	SubjectID string    `json:"subject_id"`
	EventID   string    `json:"event_id,omitempty"`
	At        time.Time `json:"at"`
}

func (s *server) listCollectionExceptions(w http.ResponseWriter, r *http.Request, merchant sandboxMerchant) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT kind, payment_id, subject_id, event_id, at FROM (
			SELECT outcome AS kind, payment_id, subject_id, event_id, received_at AS at
			  FROM collection_inbox WHERE merchant_id=$1 AND payment_id IS NOT NULL AND outcome='terminal_conflict'
			UNION ALL
			SELECT 'replay_conflict', payment_id, subject_id, event_id, replay_conflict_at
			  FROM collection_inbox WHERE merchant_id=$1 AND payment_id IS NOT NULL AND replay_conflict_at IS NOT NULL
			UNION ALL
			SELECT 'webhook_failed', payment_id, subject_id, id, created_at
			  FROM collection_outbox WHERE merchant_id=$1 AND payment_id IS NOT NULL AND state='failed'
			UNION ALL
			SELECT 'payment_confirmation_pending', id, id, '', created_at
			  FROM payments WHERE merchant_id=$1 AND status='pending_confirmation'
			UNION ALL
			SELECT 'refund_confirmation_pending', payment_id, id, '', updated_at
			  FROM sandbox_refunds WHERE merchant_id=$1 AND status='pending_confirmation'
		) AS exceptions ORDER BY at DESC, kind, subject_id, event_id LIMIT 100`, merchant.ID)
	if err != nil {
		respondEventStoreError(w)
		return
	}
	defer rows.Close()
	items := []collectionException{}
	for rows.Next() {
		var item collectionException
		if err := rows.Scan(&item.Kind, &item.PaymentID, &item.SubjectID, &item.EventID, &item.At); err != nil {
			respondEventStoreError(w)
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		respondEventStoreError(w)
		return
	}
	respond(w, http.StatusOK, map[string]any{"data": items})
}
