#!/bin/bash

set -euo pipefail

ENV_DIR="/etc/smalltalk"
ENV_FILE="$ENV_DIR/bbsService.env"

if [[ -z "${RESEND_API_KEY:-}" ]]; then
	printf 'RESEND_API_KEY is not set in this terminal.\n' >&2
	exit 1
fi

sudo install -d -o root -g root -m 700 "$ENV_DIR"
printf 'RESEND_API_KEY=%s\n' "$RESEND_API_KEY" | sudo tee "$ENV_FILE" >/dev/null
sudo chown root:root "$ENV_FILE"
sudo chmod 600 "$ENV_FILE"

if ! sudo grep -q '^RESEND_API_KEY=.' "$ENV_FILE"; then
	printf 'Failed to persist RESEND_API_KEY.\n' >&2
	exit 1
fi

printf 'RESEND_API_KEY persisted in %s (root-only).\n' "$ENV_FILE"
