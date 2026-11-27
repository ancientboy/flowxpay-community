package main

import (
	"fmt"
	"testing"
	"time"
)

func TestSandboxSettlementRequestLifecycle(t *testing.T) {
	f := newControlsFixture(t)
	const url = "/sandbox/workspace/settlement-requests"
	const ops = "/sandbox/ops/settlement-requests"
	const body = `{"action":"submit","request_id":"88888888-0000-4000-8000-000000000101","account_id":"default_M10028_mock_card","currency":"USD","source_minor":"100","destination_currency":"USDT","network":"testnet","recipient":"test-receiver"}`
	f.call(0, "POST", url, f.admin, "", body).expect(t, 409)
	f.exec(t, `insert into payments(id,merchant_id,merchant_reference,idempotency_key,request_hash,market,currency,method,amount_minor,status,route) values('req-credit','M10028','request-credit','request-credit','fixture','US','USD','card',101,'succeeded','mock_card')`)
	f.exec(t, `insert into sandbox_settlement_batches(merchant_id,account_id,currency,id,statement_date,settlement_status,withdrawal_status,withdrawal_reference,body_hash,imported_by,row_count,gross_minor,fee_minor,net_minor) values('M10028','default_M10028_mock_card','USD','request-day',current_date,'settled','not_reported','','x','control-admin',1,101,1,100)`)
	f.exec(t, `insert into sandbox_settlement_entries(merchant_id,account_id,currency,batch_id,id,kind,channel_reference,gross_minor,fee_minor,net_minor,order_id,order_status,expected_gross_minor,match_status)values('M10028','default_M10028_mock_card','USD','request-day','request-line','collection','reference',101,1,100,'req-credit','succeeded',101,'matched')`)
	f.call(0, "POST", url, f.viewer, "", body).expect(t, 403)
	id := f.call(0, "POST", url, f.admin, "", body).object(t, 200)["result"]
	f.call(0, "POST", url, f.admin, "", body).expect(t, 200)
	step := func(path, token, action, extra string, want int) {
		f.call(0, "POST", path, token, "", fmt.Sprintf(`{"action":%q,"merchant_id":"M10028","batch_id":%q%s}`, action, id, extra)).expect(t, want)
	}
	step(ops, f.ops, "paying", "", 409)
	step(ops, f.ops, "quote", `,"fx_rate":"0.2"`, 200)
	step(url, f.admin, "approve", `,"fx_rate":"0.3","expected_amount":"0.2"`, 409)
	step(url, f.admin, "approve", `,"fx_rate":"0.2","expected_amount":"0.2"`, 200)
	step(ops, f.ops, "paying", "", 200)
	step(ops, f.ops, "cancel", `,"reason":"unknown"`, 409)
	step(ops, f.ops, "confirm", `,"paid_amount":"0.2","paid_currency":"USDT","transfer_reference":"sandbox-proof","paid_on":"`+time.Now().UTC().Format(time.DateOnly)+`"`, 200)
}
