#!/usr/bin/env bash
# scripts/patch-wails.sh — Apply WebKit 2.52.x crash fix to Wails v3
#
# WebKit2GTK 2.52.x changed the ownership semantics of SoupMessageHeaders
# passed to webkit_uri_scheme_response_set_http_headers(). The Wails SDK
# calls soup_message_headers_unref() immediately after setting them on the
# response, but WebKit now keeps a reference and reads them asynchronously.
# This causes a use-after-free crash in soup_message_headers_iter_next().
#
# This script copies the Wails module and patches the affected file, then
# sets up a go.mod replace directive to use the patched copy.
#
# Usage: ./scripts/patch-wails.sh
#
set -euo pipefail

WAILS_VERSION="v3.0.0-beta.4"
WAILS_MOD="github.com/wailsapp/wails/v3"
PATCH_DIR=".patches/wails-v3"
TARGET_FILE="internal/assetserver/webview/webkit_linux_gtk3.go"
CACHED_MOD="$(go env GOMODCACHE)/${WAILS_MOD}@${WAILS_VERSION}"

if [ ! -d "$CACHED_MOD" ]; then
    echo "Fetching Wails module..."
    go mod download "${WAILS_MOD}@${WAILS_VERSION}"
fi

echo "Copying Wails SDK to ${PATCH_DIR}..."
rm -rf "$PATCH_DIR"
cp -r "$CACHED_MOD" "$PATCH_DIR"
chmod -R u+w "$PATCH_DIR"

echo "Applying WebKit SoupMessageHeaders crash fix..."
sed -i 's/\tdefer C\.soup_message_headers_unref(hdrs)/\t\/\/ PATCHED: Do NOT unref hdrs. WebKit 2.52.x takes ownership of the\n\t\/\/ SoupMessageHeaders passed to webkit_uri_scheme_response_set_http_headers.\n\t\/\/ Unreffing causes a use-after-free crash in soup_message_headers_iter_next./' \
    "${PATCH_DIR}/${TARGET_FILE}"

echo "Verifying go.mod replace directive..."
if ! grep -q "replace.*wailsapp/wails/v3.*\.patches/wails-v3" go.mod 2>/dev/null; then
    go mod edit -replace "${WAILS_MOD}=./${PATCH_DIR}"
    echo "Added go.mod replace directive."
else
    echo "go.mod replace directive already exists."
fi

echo "Done. Rebuild with: go build -tags production,gtk3 ..."
