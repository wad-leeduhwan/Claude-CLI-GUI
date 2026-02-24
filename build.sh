#!/bin/bash
set -e

APP_NAME="claude-gui"
BUILD_DIR="build/bin"
APP_BUNDLE="${BUILD_DIR}/${APP_NAME}.app"

echo "=== Building ${APP_NAME} for macOS (Apple Silicon) ==="

# Build with Wails
echo "[1/3] Building app..."
wails build

# Remove quarantine attribute
echo "[2/3] Removing quarantine attribute..."
xattr -cr "${APP_BUNDLE}"

# Ad-hoc code sign
echo "[3/3] Signing app..."
codesign --force --deep --sign - "${APP_BUNDLE}"

# Verify
BINARY="${APP_BUNDLE}/Contents/MacOS/awesomeProject1"
if [ -f "${BINARY}" ]; then
    ARCH=$(file "${BINARY}" | grep -o 'arm64\|x86_64')
    SIZE=$(du -sh "${APP_BUNDLE}" | cut -f1)
    echo ""
    echo "=== Build complete ==="
    echo "  App:  ${APP_BUNDLE}"
    echo "  Arch: ${ARCH}"
    echo "  Size: ${SIZE}"
    echo ""
    echo "Run:  open ${APP_BUNDLE}"
    echo "Install: cp -r ${APP_BUNDLE} /Applications/"
else
    echo "ERROR: Binary not found!"
    exit 1
fi
