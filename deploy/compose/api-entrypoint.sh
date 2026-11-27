#!/bin/sh
set -eu

FLOWXPAY_SANDBOX_KEY=$(cat /run/flowxpay/api/merchant-key)
FLOWXPAY_SECONDARY_KEY=$(cat /run/flowxpay/api/secondary-key)
FLOWXPAY_CHECKOUT_SECRET=$(cat /run/flowxpay/api/checkout-secret)
password=$(cat /run/flowxpay/db/password)
FLOWXPAY_DATABASE_URL="host=/var/run/postgresql user=flowxpay dbname=flowxpay password=$password sslmode=disable"
export FLOWXPAY_SANDBOX_KEY FLOWXPAY_SECONDARY_KEY FLOWXPAY_CHECKOUT_SECRET FLOWXPAY_DATABASE_URL
exec /usr/local/bin/flowxpay-api
