#!/usr/bin/env bash
set -euo pipefail
scriptDir=$(dirname -- "$(readlink -f -- "$BASH_SOURCE")")
cd "$scriptDir/../webapp"

# webpack 4 (CRA 4) uses md4 hashes which OpenSSL 3 dropped from defaults.
# Required for Node 17+.
export NODE_OPTIONS="--openssl-legacy-provider"

pnpm install --frozen-lockfile
pnpm run build
