#!/bin/sh
set -eu
umask 077

create_secret() {
    file=$1
    if [ ! -e "$file" ]; then
        temp=$(mktemp "${file}.XXXXXX")
        secret=$(od -An -v -N 32 -tx1 /dev/urandom | tr -d ' \n')
        printf '%s\n' "$secret" > "$temp"
        chown 10001:10001 "$temp"
        chmod 0400 "$temp"
        # An atomic hard link keeps the existing secret if initialization races.
        if ! ln "$temp" "$file" 2>/dev/null && [ ! -e "$file" ]; then
            rm -f "$temp"
            exit 1
        fi
        rm -f "$temp"
    fi
    if ! grep -Eq '^[0-9a-f]{64}$' "$file"; then
        printf '%s\n' "Invalid sandbox secret file: $file" >&2
        exit 1
    fi
}

create_secret /run/flowxpay/db/password
create_secret /run/flowxpay/api/merchant-key
create_secret /run/flowxpay/api/secondary-key
create_secret /run/flowxpay/api/checkout-secret
