#!/usr/bin/env bash
# deadcode-check.sh -- gate new unreachable code against a committed baseline.
#
# golang.org/x/tools/cmd/deadcode reports lines like:
#   mware.go:266:6: unreachable func: RequireTrialOrPro
# Line:column drift on every edit, so we normalize to a STABLE identifier:
#   <file-basename>: <FuncOrMethodName>
# i.e. strip ":LINE:COL" and the "unreachable func:" prefix.
#
# Exit non-zero ONLY when deadcode reports an entry NOT in the baseline
# (genuinely new dead code). Entries in the baseline that are no longer dead
# are fine -- we warn but do not fail (baseline can be trimmed later).
#
# This fork's baseline (6 entries) covers known-unreachable funcs that are
# either chi interface-dispatch false positives (.Bind methods) or vendored
# helpers kept for parity with upstream. Do NOT delete those funcs; add a new
# baseline line only after confirming an entry is a deliberate false positive.

set -euo pipefail

cd "$(dirname "$0")/.."

BASELINE=".github/deadcode-baseline.txt"

if [[ ! -f "$BASELINE" ]]; then
	echo "deadcode-check: baseline file $BASELINE not found" >&2
	exit 2
fi

# Normalize deadcode output to "<file>: <Name>", sorted & deduped.
# NOTE: pass "." not "./..." -- the gitignored data/ dir is permission-denied.
normalize() {
	sed -E 's/^([^:]+):[0-9]+:[0-9]+: unreachable func: /\1: /' \
		| grep -E '^[^:]+: ' \
		| sort -u
}

# Pinned (not @latest) to match the repo's pin-everything ethos and keep the
# gate reproducible. Bump deliberately alongside x/tools upgrades.
current="$(go run golang.org/x/tools/cmd/deadcode@v0.45.0 . 2>/dev/null | normalize)"
baseline="$(sort -u "$BASELINE")"

# New = in current but not in baseline.
new_dead="$(comm -23 <(printf '%s\n' "$current") <(printf '%s\n' "$baseline") || true)"
# Stale = in baseline but no longer reported (informational only).
stale="$(comm -13 <(printf '%s\n' "$current") <(printf '%s\n' "$baseline") || true)"

if [[ -n "$stale" ]]; then
	echo "deadcode-check: NOTE -- baseline entries no longer dead (safe to trim):" >&2
	printf '%s\n' "$stale" | sed 's/^/  /' >&2
fi

if [[ -n "$new_dead" ]]; then
	echo "deadcode-check: FAIL -- new unreachable code not in baseline:" >&2
	printf '%s\n' "$new_dead" | sed 's/^/  /' >&2
	echo "" >&2
	echo "Either remove the dead code, or (if it is a deliberate false positive)" >&2
	echo "add the normalized line to $BASELINE." >&2
	exit 1
fi

echo "deadcode-check: OK -- no new dead code beyond the baseline ($(printf '%s\n' "$baseline" | grep -c .) entries)."
