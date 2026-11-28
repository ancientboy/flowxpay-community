# FlowXPay Community Edition · Multi-country payment orchestration

**English** · [简体中文](README.md)

FlowXPay is an open-source, self-hosted platform for cross-border payment teams that manage their own provider integrations. One merchant API and an operations dashboard cover collections, payouts, routing, order lookup, refunds, and reconciliation.

[Live demo](https://ancientboy.github.io/flowxpay-community/?lang=en-US) · [Download the one-minute sandbox demo (MP4)](https://github.com/ancientboy/flowxpay-community/releases/download/v0.1.0/flowxpay-community-v0.1.0-demo.mp4) · [Quick start](#quick-start) · [Merchant API guide](docs/merchant-api.en.md) · [Interactive API reference](https://ancientboy.github.io/flowxpay-community/?workspace=merchant&page=api-docs&lang=en-US) · [OpenAPI specification](docs/openapi.yaml)

The live demo opens the community platform and merchant workspaces with read-only sample data. The self-hosted sandbox runs the Go API and PostgreSQL so you can perform workspace operations and simulate payments.

| Sandbox market | Currency | Simulated payment method |
| --- | --- | --- |
| Brazil | BRL | Pix QR code |
| United States | USD | Card checkout |
| India | INR | Payment gateway |

The video follows one Brazilian order from creation through QR checkout and payment confirmation. To explore the other markets locally, select a different payment method when creating a collection order. All three markets use simulated transactions.

## Quick start

Start Docker Engine 28 or later with Compose v2, then run:

```bash
git clone https://github.com/ancientboy/flowxpay-community.git
cd flowxpay-community
docker compose up -d --build --wait
```

1. Open the [local dashboard in English](http://127.0.0.1:4173/?lang=en-US).
2. Run the command below on your machine to read the local sandbox key. Choose **Use sandbox API key** and paste the key into the dashboard.
3. Create a collection order, open checkout, simulate a payment, and look up the order to verify its status.

```bash
docker compose exec -T api cat /run/flowxpay/api/merchant-key
```

- Stop with `docker compose down`. Orders and keys persist in volumes when you restart.
- If the port is busy, run `FLOWXPAY_WEB_PORT=4180 docker compose up -d --build --wait` and open port 4180.
- For a remote test host, keep the loopback binding and forward the dashboard with `ssh -N -L 4173:127.0.0.1:4173 user@your-server`.
- Check startup with `docker compose ps -a` and `docker compose logs init db api web`.

### Enable the platform and merchant workspaces

Install `htpasswd` and Python 3, then set a local operator password. Only the password hash is passed to Compose.

```bash
operator_account=$(htpasswd -nBC 12 operator) &&
  export FLOWXPAY_LOCAL_PLATFORM_ACCOUNTS="$(printf '%s' "$operator_account" |
    python3 -c 'import json,sys; name, hashed = sys.stdin.read().split(":", 1); print(json.dumps({name: {"hash": hashed.strip(), "role": "operator"}}))')" &&
  docker compose up -d --wait
unset operator_account
```

Sign in as `operator` at the [local platform workspace](http://127.0.0.1:4173/ops?lang=en-US). Open a merchant account from **Merchants**, then open the local administrator invitation link to set a password and sign in to the [merchant workspace](http://127.0.0.1:4173/?lang=en-US). Platform operators assign merchant channels.

## Run from source

The video uses the local development launcher. Install [the required Go version](backend/go.mod), Node.js 22, npm, Python 3, OpenSSL, and PostgreSQL tools including pg_config, initdb, and pg_ctl. Run as a regular user:

```bash
npm --prefix web ci
./scripts/local-sandbox.sh run
```

Open the URL printed in the terminal. Use the key from the file indicated by the launcher. Each run creates a temporary database, fresh credentials, and three demo orders. Press Ctrl+C to stop. Keep the database and dashboard on your machine or inside a controlled test environment.

## Integrate a provider

For API fields and responses, see the [OpenAPI specification](docs/openapi.yaml). Read the merchant API walkthrough in [English](docs/merchant-api.en.md) or [简体中文](docs/merchant-api.md). The [provider integration guide](docs/channel-integrations.md) is in Chinese.

Live integrations use your own provider account. Signatures, callbacks, and statement formats require provider-specific implementation and testing. For adapter or deployment support, [contact FlowXPay on Telegram](https://t.me/FlowXPay_nova) with the provider name, country, currency, and collection or payout requirements.

## Checks and feedback

```bash
./scripts/local-sandbox.sh test
npm --prefix web run typecheck
npm --prefix web run build
```

Report installation or sandbox issues through [GitHub Issues](https://github.com/ancientboy/flowxpay-community/issues). Include the version, environment, and steps to reproduce. Remove keys, account details, payment records, and customer information. Follow the private reporting process in [SECURITY.md](SECURITY.md) for security issues.

Released under the [Apache License 2.0](LICENSE).
