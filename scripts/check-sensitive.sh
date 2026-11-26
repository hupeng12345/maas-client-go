#!/usr/bin/env bash
#
# Fails when public sources contain content that should not be published:
#   - absolute URLs pointing at a real host (placeholders are allowed)
#   - private network addresses
#   - e-mail addresses
#   - key-shaped strings in non-test sources
#
# Documentation placeholders are allowed (example.com, *.test, *.invalid).
# The SDK embeds no endpoint, so a real domain in the sources is a mistake.

set -euo pipefail

cd "$(dirname "$0")/.."

INCLUDE=(--include='*.go' --include='*.md' --include='*.yml' --include='*.yaml' --include='Makefile')
EXCLUDES=(--exclude-dir=.git --exclude-dir=.gocache --exclude-dir=.gomodcache
          --exclude-dir=.gopath --exclude-dir=.gotmp --exclude-dir=.idea)

fail=0
report() { echo "sensitive: $1"; echo "$2"; fail=1; }

# 1. Absolute URLs with a concrete host.
urls=$(grep -rnE 'https?://[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z]{2,}' "${INCLUDE[@]}" "${EXCLUDES[@]}" . 2>/dev/null \
  | grep -vE 'https?://([a-z0-9.-]+\.(test|invalid|example)|example\.com|localhost|127\.0\.0\.1|your-[a-z-]+|www\.w3\.org|go\.dev|pkg\.go\.dev)' || true)
[ -n "$urls" ] && report "absolute URL with a real host" "$urls"

# 2. Private network addresses.
private=$(grep -rnE '(^|[^0-9.])(10\.[0-9]+\.[0-9]+\.[0-9]+|192\.168\.[0-9]+\.[0-9]+|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]+\.[0-9]+)' "${INCLUDE[@]}" "${EXCLUDES[@]}" . 2>/dev/null || true)
[ -n "$private" ] && report "private network address" "$private"

# 3. E-mail addresses.
emails=$(grep -rnE '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' "${INCLUDE[@]}" "${EXCLUDES[@]}" . 2>/dev/null \
  | grep -vE '@([a-z0-9.-]+\.(test|invalid|example)|example\.com|your-[a-z-]+)' || true)
[ -n "$emails" ] && report "e-mail address" "$emails"

# 4. Key-shaped strings outside tests (tests use synthetic keys on purpose).
keys=$(grep -rnE '\bsk_[A-Za-z0-9]{24,}' --include='*.go' --include='*.md' --include='*.yml' \
  "${EXCLUDES[@]}" . 2>/dev/null | grep -v '_test.go' || true)
[ -n "$keys" ] && report "key-shaped string" "$keys"

# 5. Cluster-internal markers.
internal=$(grep -rnE '\.svc\.cluster\.local|\.internal/|\.local:[0-9]+' "${INCLUDE[@]}" "${EXCLUDES[@]}" . 2>/dev/null || true)
[ -n "$internal" ] && report "cluster-internal marker" "$internal"

if [ "$fail" -eq 0 ]; then
  echo "check-sensitive: OK"
fi
exit "$fail"
