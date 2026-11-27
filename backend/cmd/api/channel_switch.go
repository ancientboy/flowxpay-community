package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

type switchChannel struct {
	ID        string      `json:"channel_id,omitempty"`
	AccountID string      `json:"account_id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Provider  string      `json:"provider"`
	Available bool        `json:"available"`
	Cost      *feeVersion `json:"cost,omitempty"`
	Quote     *feeVersion `json:"quote"`
}

type collectionSwitch struct {
	Merchant string         `json:"merchant_id"`
	Site     string         `json:"site_id"`
	ChangeID string         `json:"current_route_id,omitempty"`
	Current  *switchChannel `json:"current"`
	Target   switchChannel  `json:"target"`
	capability
	Preview string `json:"preview"`
}

var errSwitchUnavailable = errors.New("target channel or account unavailable")

func readCollectionSwitch(r *http.Request, tx *sql.Tx, merchant, site, id string, confirm bool) (collectionSwitch, platformChannelConfig, error) {
	ctx := r.Context()
	p := collectionSwitch{Merchant: merchant, Site: site}
	var existing string
	if err := tx.QueryRowContext(ctx, "SELECT id FROM merchant_sites WHERE merchant_id=$1 AND id=$2", merchant, site).Scan(&existing); err != nil {
		return p, platformChannelConfig{}, err
	}

	if confirm {
		// Hold channel state and pricing stable through audit and route activation.
		// Include the site's current and fallback accounts as well as the target.
		rows, err := tx.QueryContext(ctx, `
			SELECT id FROM sandbox_platform_channels WHERE id=$3 OR id IN (
				SELECT platform_channel_id FROM sandbox_channel_accounts WHERE merchant_id=$1 AND site_id=$2
			) ORDER BY id FOR SHARE`, merchant, site, id)
		if err != nil {
			return p, platformChannelConfig{}, err
		}
		defer rows.Close()
		for rows.Next() {
			if err := rows.Scan(&existing); err != nil {
				return p, platformChannelConfig{}, err
			}
		}
		if err := rows.Err(); err != nil {
			return p, platformChannelConfig{}, err
		}
	}
	target, err := scanPlatformChannel(tx.QueryRowContext(ctx, "SELECT id,name,provider,enabled FROM sandbox_platform_channels WHERE id=$1", id))
	if err != nil {
		return p, target, err
	}

	p.capability = target.capability
	p.Target = switchChannel{ID: id, Provider: target.Provider, Name: target.Name, Available: target.Enabled}
	accountQuery := `SELECT id,enabled FROM sandbox_channel_accounts WHERE merchant_id=$1 AND site_id=$2 AND platform_channel_id=$3`
	if confirm {
		accountQuery += " FOR SHARE"
	}
	var accountEnabled bool
	err = tx.QueryRowContext(ctx, accountQuery, merchant, site, id).Scan(&p.Target.AccountID, &accountEnabled)
	if err == nil {
		p.Target.Available = p.Target.Available && accountEnabled
	} else if !errors.Is(err, sql.ErrNoRows) {
		return p, target, err
	}
	if !p.Target.Available {
		return p, target, errSwitchUnavailable
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT id,account_id FROM collection_route_changes
		WHERE merchant_id=$1 AND site_id=$2 AND (market,currency,method)=($3,$4,$5) AND `+currentCollectionRoute,
		merchant, site, p.Market, p.Currency, p.Method).Scan(&p.ChangeID, &current)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id FROM sandbox_channel_accounts WHERE merchant_id=$1 AND site_id=$2 AND `+
			collectionAccountFilter("$3::text[]", "''")+` ORDER BY array_position($3::text[],provider), id LIMIT 1`,
			merchant, site, collectionProviders(p.capability)).Scan(&current)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, target, err
	}
	if current != "" {
		p.Current = &switchChannel{AccountID: current}
		query := `SELECT a.provider,COALESCE(c.id,''),COALESCE(c.name,''),a.enabled AND COALESCE(c.enabled,true)
			FROM sandbox_channel_accounts a LEFT JOIN sandbox_platform_channels c ON c.id=a.platform_channel_id
			WHERE a.merchant_id=$1 AND a.site_id=$2 AND a.id=$3`
		if confirm {
			query += " FOR SHARE OF a"
		}
		if err := tx.QueryRowContext(ctx, query, merchant, site, current).
			Scan(&p.Current.Provider, &p.Current.ID, &p.Current.Name, &p.Current.Available); err != nil {
			return p, target, err
		}
	}
	prices, err := readChannelPrices(ctx, tx, merchant, "", "collection", p.Currency, true)
	if err != nil {
		return p, target, err
	}
	for _, price := range prices {
		if price.ChannelID == p.Target.ID {
			p.Target.Cost, p.Target.Quote = price.Cost, price.quote()
		}
		if p.Current != nil && price.ChannelID == p.Current.ID {
			p.Current.Cost, p.Current.Quote = price.Cost, price.quote()
		}
	}
	data, err := json.Marshal(p)
	sum := sha256.Sum256(data)
	p.Preview = hex.EncodeToString(sum[:])
	return p, target, err
}

func previewCollectionSwitch(w http.ResponseWriter, r *http.Request, tx *sql.Tx, _, merchant string) (int, any, error) {
	value, _, err := readCollectionSwitch(r, tx, merchant, r.PathValue("site"), r.PathValue("id"), false)
	if errors.Is(err, errSwitchUnavailable) {
		return routeConflict(w, "route_account_unavailable", "The target channel is no longer available.")
	}
	if r.Context().Value(platformRoleKey{}) != "operator" {
		value.Target.Cost, value.Preview = nil, ""
		if value.Current != nil {
			value.Current.Cost = nil
		}
	}
	return http.StatusOK, value, err
}

func confirmCollectionSwitch(w http.ResponseWriter, r *http.Request, tx *sql.Tx, actor, merchant string) (int, any, error) {
	var input struct {
		Preview string `json:"preview"`
	}
	if !parseJSON(w, r, &input) {
		return 0, nil, nil
	}
	decoded, err := hex.DecodeString(input.Preview)
	if err != nil || len(decoded) != sha256.Size {
		fail(w, 400, "invalid_switch_preview", "Supply the preview digest for this channel switch.")
		return 0, nil, nil
	}
	site := r.PathValue("site")
	if err := lockRouteSite(r, tx, merchant, site); err != nil {
		return 0, nil, err
	}
	value, target, err := readCollectionSwitch(r, tx, merchant, site, r.PathValue("id"), true)
	if errors.Is(err, errSwitchUnavailable) {
		return routeConflict(w, "route_account_unavailable", "The target channel is no longer available.")
	}
	if err != nil {
		return 0, nil, err
	}
	if input.Preview != value.Preview {
		return routeConflict(w, "route_preview_changed", "The route or pricing changed. Review the updated preview before confirming.")
	}
	account := value.Target.AccountID
	if account == "" {
		status, result, err := assignChannelToSite(w, r, tx, actor, merchant, site, target)
		if err != nil || status == 0 {
			return status, result, err
		}
		account = result.(channelAccount).ID
	}
	status, route, err := savePlatformRoute(w, r, tx, actor, merchant, site, account, target.capability, true)
	if err != nil || status != http.StatusCreated {
		return status, route, err
	}
	details := map[string]any{
		"account_id": account, "site_id": site, "channel_id": target.ID, "previous_route_id": value.ChangeID,
		"preview": value.Preview,
	}
	for side, channel := range map[string]*switchChannel{"target": &value.Target, "current": value.Current} {
		if channel == nil {
			continue
		}
		details[side+"_account_id"] = channel.AccountID
		details[side+"_channel_id"] = channel.ID
		if channel.Cost != nil {
			details[side+"_cost_version_id"] = channel.Cost.VersionID
		}
		if channel.Quote != nil {
			details[side+"_quote_version_id"] = channel.Quote.VersionID
		}
	}
	return status, route, platformAudit(r, tx, actor, "route.switch", merchant, route.(routeChange).ID, details)
}
