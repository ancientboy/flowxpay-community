// Package migrations contains the schema for the local FlowXPay sandbox.
package migrations

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed 001_sandbox.sql
var Sandbox string

//go:embed 002_merchant_access.sql
var merchantAccess string

//go:embed 003_workspace_auth.sql
var workspaceAuth string

//go:embed 004_workspace_inventory.sql
var workspaceInventory string

//go:embed 005_collection_routing.sql
var collectionRouting string

//go:embed 006_collection_events.sql
var collectionEvents string

//go:embed 007_sandbox_payouts.sql
var sandboxPayouts string

//go:embed 008_sandbox_settlement.sql
var sandboxSettlement string

//go:embed 009_payout_dispatch_limits.sql
var payoutDispatchLimits string

//go:embed 010_workspace_controls.sql
var workspaceControls string

//go:embed 011_sandbox_chargebacks.sql
var sandboxChargebacks string

//go:embed 012_platform_operations.sql
var platformOperations string

//go:embed 013_platform_merchant_onboarding.sql
var platformMerchantOnboarding string

//go:embed 014_platform_channel_creation.sql
var platformChannelCreation string

//go:embed 015_site_business_domain.sql
var siteBusinessDomain string

//go:embed 016_channel_pricing.sql
var channelPricing string

//go:embed 017_upstream_integrations.sql
var upstreamIntegrations string

//go:embed 018_merchant_request_signatures.sql
var merchantRequestSignatures string

//go:embed 019_sandbox_requests.sql
var settlementRequests string

// Apply serializes schema initialization and rolls back all changes on failure.
func Apply(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var database string
	if err := tx.QueryRowContext(ctx, "SELECT pg_catalog.current_database()").Scan(&database); err != nil {
		return err
	}
	if database == "flowxpay_production" {
		return fmt.Errorf("sandbox migrations cannot run in the production database")
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(748654830891828731)"); err != nil {
		return err
	}
	for i, migration := range []string{Sandbox, merchantAccess, workspaceAuth, workspaceInventory, collectionRouting, collectionEvents, sandboxPayouts, sandboxSettlement, payoutDispatchLimits, workspaceControls, sandboxChargebacks, platformOperations, platformMerchantOnboarding, platformChannelCreation, siteBusinessDomain, channelPricing, upstreamIntegrations, merchantRequestSignatures, settlementRequests} {
		if _, err := tx.ExecContext(ctx, migration); err != nil {
			return fmt.Errorf("apply migration %03d: %w", i+1, err)
		}
	}
	return tx.Commit()
}
