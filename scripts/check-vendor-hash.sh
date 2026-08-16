#!/usr/bin/env bash
#
# Detect go.sum/go.mod ↔ flake.nix vendorHash drift before nix does.
#
# Why: buildGoModule's vendorHash must be re-derived whenever the Go module
# set changes. When it rots, "nix build" fails with a cryptic hash-mismatch
# error. This guard gives a fast, actionable message instead — and catches
# the drift even when no nix job runs.
#
# Usage:
#   scripts/check-vendor-hash.sh [BASE_REV]
#
#   No argument  → compare the working tree against HEAD (local pre-commit use).
#   BASE_REV     → compare BASE_REV..working tree, or BASE_REV..HEAD when the
#                  working tree is clean (CI use, e.g. "HEAD~1").
#
# Rules:
#   FAIL  : go.mod or go.sum changed but the vendorHash in flake.nix did not.
#   WARN  : vendorHash changed with no go.mod/go.sum change (possible nixpkgs
#           rehash via flake.lock bump — legal, so only a warning).
#   PASS  : everything else, including no changes at all.

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

base_rev=${1:-HEAD}

if ! git rev-parse --verify --quiet "$base_rev" >/dev/null; then
	echo "check-vendor-hash: base revision '$base_rev' not found" >&2
	echo "  hint: on CI, checkout with fetch-depth: 2 so HEAD~1 exists" >&2
	exit 2
fi

go_files_changed=0
if ! git diff --quiet "$base_rev" -- go.mod go.sum; then
	go_files_changed=1
fi

base_vendor_hash=$(git show "$base_rev:flake.nix" 2>/dev/null | grep -o 'vendorHash = "[^"]*"' || true)
head_vendor_hash=$(grep -o 'vendorHash = "[^"]*"' flake.nix || true)

if [[ $go_files_changed -eq 1 && "$base_vendor_hash" == "$head_vendor_hash" ]]; then
	cat >&2 <<EOF
check-vendor-hash: DRIFT DETECTED

go.mod/go.sum changed since $base_rev but the vendorHash in flake.nix
did not ($head_vendor_hash).

Fix:
  nix build .#default       # fails with the correct hash in the error
  # copy the "got:" sha256 into flake.nix's vendorHash
  nix build .#default       # now passes
EOF
	exit 1
fi

if [[ $go_files_changed -eq 0 && "$base_vendor_hash" != "$head_vendor_hash" ]]; then
	echo "check-vendor-hash: note — vendorHash changed without go.mod/go.sum changes"
	echo "  (expected after a nixpkgs/flake.lock bump rehashes the vendor derivation)"
fi

echo "check-vendor-hash: OK (base=$base_rev)"
