package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type feeRate struct {
	BPS        int   `json:"bps"`
	FixedMinor int64 `json:"fixed_minor"`
}

type feeVersion struct {
	feeRate
	VersionID   string    `json:"version_id"`
	Source      string    `json:"source"`
	EffectiveAt time.Time `json:"effective_at"`
}

type orderFee struct {
	feeVersion
	FeeMinor int64 `json:"fee_minor"`
}

type orderPricing struct {
	ChannelID string    `json:"channel_id,omitempty"`
	Provider  string    `json:"provider"`
	Direction string    `json:"direction"`
	Currency  string    `json:"currency"`
	Cost      *orderFee `json:"cost"`
	Quote     *orderFee `json:"quote"`
}

type channelPricing struct {
	ChannelID string      `json:"channel_id"`
	Direction string      `json:"direction"`
	Currency  string      `json:"currency"`
	Cost      *feeVersion `json:"cost,omitempty"`
	Default   *feeVersion `json:"default"`
	Override  *feeVersion `json:"override"`
}

func (p channelPricing) quote() *feeVersion {
	if p.Override != nil {
		return p.Override
	}
	return p.Default
}

// All supported order amounts fit below 10^12 minor units. Round percentage
// half up to the nearest minor unit before adding the fixed amount.
func (v *feeVersion) forAmount(amount int64) *orderFee {
	if v == nil {
		return nil
	}
	return &orderFee{*v, (amount*int64(v.BPS)+5000)/10000 + v.FixedMinor}
}

const latestChannelPrices = `
	SELECT DISTINCT ON (v.channel_id,v.direction,v.currency,v.kind) v.channel_id,v.direction,v.currency,v.kind,
		v.id::text,v.bps,v.fixed_minor,v.effective_at
	FROM sandbox_channel_price_versions v
	WHERE (v.merchant_id IS NULL OR v.merchant_id=NULLIF($1,''))
	  AND ($2='' OR v.channel_id=$2) AND ($3='' OR v.direction=$3)
	  AND ($4='' OR v.currency=$4) AND ($5 OR v.kind<>'cost')
	ORDER BY v.channel_id,v.direction,v.currency,v.kind,v.id DESC
`

func readChannelPrices(ctx context.Context, tx *sql.Tx, merchant, channel, direction, currency string, costs bool) ([]channelPricing, error) {
	rows, err := tx.QueryContext(ctx, latestChannelPrices, merchant, channel, direction, currency, costs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prices := []channelPricing{}
	var scope channelPricing
	for rows.Next() {
		var id, dir, cur, kind string
		var rate feeVersion
		var bps, fixed sql.NullInt64
		if err := rows.Scan(&id, &dir, &cur, &kind, &rate.VersionID, &bps, &fixed, &rate.EffectiveAt); err != nil {
			return nil, err
		}
		if id != scope.ChannelID || dir != scope.Direction || cur != scope.Currency {
			if scope.ChannelID != "" {
				prices = append(prices, scope)
			}
			scope = channelPricing{ChannelID: id, Direction: dir, Currency: cur}
		}
		if !bps.Valid {
			continue
		}
		rate.feeRate, rate.Source = feeRate{int(bps.Int64), fixed.Int64}, kind
		switch kind {
		case "cost":
			scope.Cost = &rate
		case "default":
			scope.Default = &rate
		case "override":
			scope.Override = &rate
		}
	}
	if scope.ChannelID != "" {
		prices = append(prices, scope)
	}
	return prices, rows.Err()
}

func snapshotPricing(ctx context.Context, tx *sql.Tx, merchant, channel, provider, direction, currency string, amount int64) (orderPricing, error) {
	p := orderPricing{ChannelID: channel, Provider: provider, Direction: direction, Currency: currency}
	if channel == "" {
		return p, nil
	}
	prices, err := readChannelPrices(ctx, tx, merchant, channel, direction, currency, true)
	if err == nil && len(prices) != 0 {
		p.Cost, p.Quote = prices[0].Cost.forAmount(amount), prices[0].quote().forAmount(amount)
	}
	return p, err
}

func setChannelPrice(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	var input struct {
		Direction  string `json:"direction"`
		Currency   string `json:"currency"`
		Kind       string `json:"kind"`
		BPS        *int   `json:"bps"`
		FixedMinor *int64 `json:"fixed_minor"`
		Restore    bool   `json:"restore_default"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	channel := r.PathValue("id")
	kindOK := merchant == "" && (input.Kind == "cost" || input.Kind == "default") || merchant != "" && input.Kind == "override"
	rateOK := input.BPS != nil && *input.BPS >= 0 && *input.BPS <= 10000 &&
		input.FixedMinor != nil && *input.FixedMinor >= 0 && *input.FixedMinor <= 999999999999
	if input.Restore {
		rateOK = input.Kind == "override" && input.BPS == nil && input.FixedMinor == nil
	}
	if !clientID.MatchString(channel) || !kindOK || !rateOK ||
		(input.Direction != "collection" && input.Direction != "payout") {
		fail(w, 400, "invalid_price", "Supply a direction, currency, valid fee or merchant override reset.")
		return 0, nil, nil
	}
	ctx := r.Context()
	var provider string
	// Mutations take an exclusive channel lock. Order reservations and route
	// confirmations use shared locks through commit.
	if err := tx.QueryRowContext(ctx, "SELECT provider FROM sandbox_platform_channels WHERE id=$1 FOR UPDATE", channel).Scan(&provider); err != nil {
		return 0, nil, err
	}
	if profile, ok := profileFor(provider); !ok || profile.capability.Currency != input.Currency {
		fail(w, 400, "invalid_currency", "Use the currency supported by this channel.")
		return 0, nil, nil
	}
	var id int64
	var at time.Time
	var bps, fixed sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT id,effective_at,bps,fixed_minor FROM sandbox_channel_price_versions
		WHERE channel_id=$1 AND merchant_id IS NOT DISTINCT FROM NULLIF($2,'') AND direction=$3 AND currency=$4 AND kind=$5
		ORDER BY id DESC LIMIT 1`, channel, merchant, input.Direction, input.Currency, input.Kind).Scan(&id, &at, &bps, &fixed)
	if err == nil {
		if input.Restore && !bps.Valid || !input.Restore && bps.Valid && bps.Int64 == int64(*input.BPS) && fixed.Int64 == *input.FixedMinor {
			return http.StatusOK, publishedPrice(id, at, input.Direction, input.Currency, input.Kind, input.Restore), nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO sandbox_channel_price_versions (channel_id,merchant_id,direction,currency,kind,bps,fixed_minor,changed_by)
		VALUES ($1,NULLIF($2,''),$3,$4,$5,$6,$7,$8) RETURNING id,effective_at`,
		channel, merchant, input.Direction, input.Currency, input.Kind, input.BPS, input.FixedMinor, actor).Scan(&id, &at)
	if err != nil {
		return 0, nil, err
	}
	audit := publishedPrice(id, at, input.Direction, input.Currency, input.Kind, input.Restore)
	return http.StatusCreated, audit, platformAudit(r, tx, actor, "price.publish", merchant, channel, audit)
}

func publishedPrice(id int64, at time.Time, direction, currency, kind string, restored bool) map[string]any {
	return map[string]any{"direction": direction, "currency": currency, "kind": kind,
		"version_id": strconv.FormatInt(id, 10), "effective_at": at, "restore_default": restored}
}

type platformRoleKey struct{}

func listChannelPrices(_ http.ResponseWriter, r *http.Request, tx *sql.Tx, _, merchant string) (int, any, error) {
	prices, err := readChannelPrices(r.Context(), tx, merchant, "", "", "", r.Context().Value(platformRoleKey{}) == "operator")
	return http.StatusOK, prices, err
}
