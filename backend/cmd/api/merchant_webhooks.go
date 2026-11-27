package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type eventConfig struct {
	callbackRoot []byte
	webhookRoot  []byte
	destinations map[string]string
}

func loadEventConfig(s *server) (eventConfig, error) {
	cfg := eventConfig{
		callbackRoot: []byte(os.Getenv("FLOWXPAY_SANDBOX_CALLBACK_ROOT")),
		webhookRoot:  []byte(os.Getenv("FLOWXPAY_SANDBOX_WEBHOOK_ROOT")),
		destinations: make(map[string]string),
	}
	for _, root := range [][]byte{cfg.callbackRoot, cfg.webhookRoot} {
		if len(root) != 0 && (len(root) < 32 || bytes.Equal(root, s.signing) ||
			string(root) == s.merchants[0].Key || string(root) == s.merchants[1].Key) {
			return eventConfig{}, errors.New("configure distinct sandbox event roots of at least 32 bytes")
		}
	}
	if len(cfg.callbackRoot) > 0 && bytes.Equal(cfg.callbackRoot, cfg.webhookRoot) {
		return eventConfig{}, errors.New("configure different callback and webhook roots")
	}
	for _, merchant := range s.merchants {
		destination := os.Getenv("FLOWXPAY_SANDBOX_WEBHOOK_" + merchant.ID)
		if destination == "" {
			continue
		}
		if len(cfg.webhookRoot) == 0 || !localWebhookURL(destination) {
			return eventConfig{}, errors.New("sandbox webhook requires a dedicated root and an explicit loopback HTTP endpoint")
		}
		cfg.destinations[merchant.ID] = destination
	}
	return cfg, nil
}

func localWebhookURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.Opaque != "" || u.Path == "" || u.RawPath != "" || strings.Contains(raw, "#") {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	number, portErr := strconv.Atoi(port)
	ip := net.ParseIP(host)
	return err == nil && portErr == nil && number >= 1 && number <= 65535 && strconv.Itoa(number) == port &&
		ip != nil && (host == "127.0.0.1" || host == "::1")
}

var webhookClient = &http.Client{
	Timeout: 2 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 8192,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil || (host != "127.0.0.1" && host != "::1") {
				return nil, errors.New("sandbox webhook dial requires a loopback address")
			}
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
		},
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

const maxWebhookAttempts = 5

func (s *server) deliverWebhook(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id, merchant, destination string
	var payload []byte
	var attempts int
	err = tx.QueryRowContext(ctx, `
		SELECT id,merchant_id,destination,payload,attempts FROM collection_outbox
		WHERE state='pending' AND next_attempt_at<=now()
		ORDER BY next_attempt_at, id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED`).Scan(&id, &merchant, &destination, &payload, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	state := "failed"
	if len(s.events.webhookRoot) > 0 && destination == s.events.destinations[merchant] && localWebhookURL(destination) {
		attempts++
		if s.sendWebhook(ctx, destination, merchant, id, payload) {
			state = "delivered"
		} else if attempts < maxWebhookAttempts {
			state = "pending"
		}
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE collection_outbox SET state=$2, attempts=$3, next_attempt_at=now()+interval '2 seconds'*
			(1 << GREATEST($3,0)) WHERE id=$1`, id, state, attempts)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *server) sendWebhook(ctx context.Context, destination, merchant, id string, payload []byte) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	key := deriveEventKey(s.events.webhookRoot, "webhook", merchant)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("FlowXPay-Event-ID", id)
	request.Header.Set("FlowXPay-Timestamp", stamp)
	request.Header.Set("FlowXPay-Signature", hex.EncodeToString(eventMAC(key, stamp, merchant, payload)))
	response, err := webhookClient.Do(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
}
