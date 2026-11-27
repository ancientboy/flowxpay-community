#!/usr/bin/env bash
set -euo pipefail
umask 077

root="$(cd "$(dirname "$0")/.." && pwd)"
mode="${1:-run}"
if [[ "$mode" != run && "$mode" != test ]]; then
  echo "Usage: scripts/local-sandbox.sh [run|test]" >&2
  exit 2
fi
pg_bin="$(pg_config --bindir)"
sandbox="$(mktemp -d "${TMPDIR:-/tmp}/flowxpay.XXXXXX")"
api_pid=
web_pid=
pg_started=
cleanup() {
  local status=$? pg_status=0
  trap - EXIT
  if [[ -n "$web_pid" ]]; then kill "$web_pid" 2>/dev/null || true; fi
  if [[ -n "$api_pid" ]]; then kill "$api_pid" 2>/dev/null || true; fi
  if [[ -n "$pg_started" || -f "$sandbox/db/postmaster.pid" ]]; then
    if ! "$pg_bin/pg_ctl" -D "$sandbox/db" -w -t 20 stop -m fast >/dev/null 2>&1; then
      "$pg_bin/pg_ctl" -D "$sandbox/db" status >/dev/null 2>&1 || pg_status=$?
      if [[ "$pg_status" != 3 ]]; then
        printf 'Could not confirm PostgreSQL stopped. Kept %s.\n' "$sandbox" >&2
        if [[ "$status" == 0 ]]; then status=1; fi
        exit "$status"
      fi
    fi
  fi
  if [[ "$mode" == test && "${FLOWXPAY_KEEP_TEST_ARTIFACTS:-0}" != 1 ]]; then
    if ! rm -r -- "$sandbox"; then
      if [[ "$status" == 0 ]]; then status=1; fi
    fi
  elif [[ "$mode" == test ]]; then
    printf 'Test files retained in %s.\n' "$sandbox" >&2
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

"$pg_bin/initdb" -D "$sandbox/db" -A trust -U flowxpay --no-locale --encoding=UTF8 >"$sandbox/postgres-init.log"
"$pg_bin/pg_ctl" -D "$sandbox/db" -l "$sandbox/postgres.log" -w start \
  -o "-k $sandbox -p 5432 -c listen_addresses=''" >/dev/null
pg_started=1
"$pg_bin/createdb" -h "$sandbox" -U flowxpay flowxpay
export FLOWXPAY_DATABASE_URL="postgresql://flowxpay@/flowxpay?host=$sandbox&sslmode=disable"
if [[ "$mode" == test ]]; then
  export FLOWXPAY_TEST_DATABASE_URL="$FLOWXPAY_DATABASE_URL"
  (cd "$root/backend" && go test -race -count=1 ./...)
  exit
fi
export FLOWXPAY_SANDBOX_KEY="${FLOWXPAY_SANDBOX_KEY:-$(openssl rand -hex 32)}"
export FLOWXPAY_SECONDARY_KEY="${FLOWXPAY_SECONDARY_KEY:-$(openssl rand -hex 32)}"
export FLOWXPAY_CHECKOUT_SECRET="${FLOWXPAY_CHECKOUT_SECRET:-$(openssl rand -hex 32)}"
export FLOWXPAY_LISTEN="127.0.0.1:${FLOWXPAY_API_PORT:-8091}"
export FLOWXPAY_API_ORIGIN="http://$FLOWXPAY_LISTEN"
printf '%s\n' "$FLOWXPAY_SANDBOX_KEY" > "$sandbox/merchant-key"
if [[ "${FLOWXPAY_ENABLE_MOCK_CALLBACKS:-}" == 1 ]]; then
  export FLOWXPAY_SANDBOX_CALLBACK_ROOT="$(openssl rand -hex 32)"
  printf '%s\n' "$FLOWXPAY_SANDBOX_CALLBACK_ROOT" > "$sandbox/callback-root"
fi
(cd "$root/backend" && go build -o "$sandbox/flowxpay-api" ./cmd/api)
"$sandbox/flowxpay-api" >"$sandbox/api.log" 2>&1 & api_pid=$!
for attempt in {1..50}; do
  if curl -fsS "$FLOWXPAY_API_ORIGIN/healthz" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$api_pid" 2>/dev/null; then cat "$sandbox/api.log" >&2; exit 1; fi
  sleep 0.2
done
curl -fsS "$FLOWXPAY_API_ORIGIN/healthz" >/dev/null
workspace_token="fxs_$(openssl rand -hex 32)"
workspace_digest="$(printf '%s' "$workspace_token" | openssl dgst -sha256 -r | cut -d ' ' -f 1)"
"$pg_bin/psql" -X -h "$sandbox" -U flowxpay -d flowxpay -v ON_ERROR_STOP=1 \
  -v workspace_digest="$workspace_digest" >/dev/null <<'SQL'
BEGIN;
INSERT INTO merchant_members (id, merchant_id, name, role)
VALUES ('local-admin', 'M10028', 'Local Sandbox Admin', 'admin');
INSERT INTO merchant_sessions (token_hash, member_id, expires_at)
VALUES (decode(:'workspace_digest', 'hex'), 'local-admin', now() + interval '8 hours');
COMMIT;
SQL
printf '%s\n' "$workspace_token" > "$sandbox/member-session"
python3 "$root/scripts/seed-sandbox.py"
(cd "$root/web" && exec ./node_modules/.bin/vite --host 127.0.0.1 --port "${FLOWXPAY_WEB_PORT:-4173}" --strictPort) \
  >"$sandbox/web.log" 2>&1 & web_pid=$!
for attempt in {1..50}; do
  if curl -fsS "http://127.0.0.1:${FLOWXPAY_WEB_PORT:-4173}/healthz" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$web_pid" 2>/dev/null; then cat "$sandbox/web.log" >&2; exit 1; fi
  sleep 0.2
done
curl -fsS "http://127.0.0.1:${FLOWXPAY_WEB_PORT:-4173}/healthz" >/dev/null
printf 'Sandbox ready at http://127.0.0.1:%s/\nLocal merchant key file: %s/merchant-key\n' "${FLOWXPAY_WEB_PORT:-4173}" "$sandbox"
printf 'Local member session file: %s/member-session\n' "$sandbox"
wait "$web_pid"
