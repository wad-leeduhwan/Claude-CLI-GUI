#!/bin/bash
set -e

APP_NAME="Claude Code GUI"
BUILD_DIR="build/bin"
APP_BUNDLE="${BUILD_DIR}/${APP_NAME}.app"
BINARY="${APP_BUNDLE}/Contents/MacOS/claude-gui"
ENTITLEMENTS="entitlements.plist"
REPO="wad-leeduhwan/Claude-CLI-GUI"

# VERSION 인자 (예: ./build.sh v1.0.0)
VERSION="${1:-}"

# ── 서명 설정 (본인 값으로 교체) ──
CERT_ID=""  # security find-identity -v -p codesigning 결과값
NOTARY_PROFILE="notary-profile"  # xcrun notarytool store-credentials 때 설정한 이름

# 서명 모드 판별
if [ -z "$CERT_ID" ]; then
  SIGN_MODE="adhoc"
  echo "=== Building ${APP_NAME} (ad-hoc 서명) ==="
  echo "  정식 서명하려면 build.sh의 CERT_ID를 설정하세요."
else
  SIGN_MODE="release"
  echo "=== Building ${APP_NAME} (정식 서명 + 공증) ==="
fi

if [ -n "$VERSION" ]; then
  TOTAL_STEPS=5
  echo "  버전: ${VERSION} (빌드 + GitHub Release)"
else
  TOTAL_STEPS=3
  echo "  로컬 빌드 전용 (릴리스 생략)"
fi

# ── [1] Wails 빌드 ──
echo "[1/${TOTAL_STEPS}] Building app..."
BUILD_VERSION="${VERSION:-dev}"
BUILD_DATE="$(date +%Y-%m-%d)"
LDFLAGS="-X main.version=${BUILD_VERSION} -X main.buildDate=${BUILD_DATE}"
wails build -ldflags "${LDFLAGS}"

# ── [2] 서명 ──
echo "[2/${TOTAL_STEPS}] Signing..."
xattr -cr "${APP_BUNDLE}"

if [ "$SIGN_MODE" = "release" ]; then
  codesign --timestamp --force --deep \
    --sign "$CERT_ID" \
    --options runtime \
    --entitlements "$ENTITLEMENTS" \
    "${APP_BUNDLE}"
  codesign -v "${APP_BUNDLE}"

  echo "  Notarizing..."
  NOTARIZE_ZIP="${BUILD_DIR}/${APP_NAME}-notarize.zip"
  ditto -c -k --sequesterRsrc "${APP_BUNDLE}" "${NOTARIZE_ZIP}"
  xcrun notarytool submit "${NOTARIZE_ZIP}" \
    --keychain-profile "$NOTARY_PROFILE" \
    --wait
  rm -f "${NOTARIZE_ZIP}"

  echo "  Stapling ticket..."
  xcrun stapler staple "${APP_BUNDLE}"
else
  codesign --force --deep --sign - "${APP_BUNDLE}"
fi

# ── [3] 검증 ──
echo "[3/${TOTAL_STEPS}] Verifying..."
if [ -f "${BINARY}" ]; then
  ARCH=$(file "${BINARY}" | grep -o 'arm64\|x86_64')
  SIZE=$(du -sh "${APP_BUNDLE}" | cut -f1)
  echo "  App:  ${APP_BUNDLE}"
  echo "  Arch: ${ARCH}"
  echo "  Size: ${SIZE}"
  if [ "$SIGN_MODE" = "release" ]; then
    spctl -a -v "${APP_BUNDLE}" 2>&1 || true
  fi
else
  echo "ERROR: Binary not found!"
  exit 1
fi

# ── 릴리스 단계 (VERSION 인자가 있을 때만) ──
if [ -z "$VERSION" ]; then
  echo ""
  echo "=== 로컬 빌드 완료 ==="
  echo "  App: ${APP_BUNDLE}"
  echo "  릴리스 생성: ./build.sh v1.0.0"
  exit 0
fi

# gh CLI 확인
if ! command -v gh &> /dev/null; then
  echo "ERROR: gh CLI가 필요합니다."
  echo "  brew install gh && gh auth login"
  exit 1
fi

# ── [4] zip 생성 ──
ZIP_NAME="${APP_NAME}-${VERSION}.zip"
ZIP_PATH="${BUILD_DIR}/${ZIP_NAME}"
echo "[4/${TOTAL_STEPS}] Creating zip..."
rm -f "${ZIP_PATH}"
(cd "${BUILD_DIR}" && ditto -c -k --sequesterRsrc "${APP_NAME}.app" "${ZIP_NAME}")
echo "  ${ZIP_PATH} ($(du -h "${ZIP_PATH}" | cut -f1))"

# ── [5] GitHub Release 생성 ──
echo "[5/${TOTAL_STEPS}] Creating GitHub Release..."
gh release create "${VERSION}" \
  "${ZIP_PATH}" \
  install.sh \
  --repo "${REPO}" \
  --title "${APP_NAME} ${VERSION}" \
  --notes "## 설치

\`\`\`bash
curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash
\`\`\`

또는 아래 zip 파일을 직접 다운로드하여 \`/Applications\`에 복사하세요." \
  --latest

echo ""
echo "=== 릴리스 완료 ==="
echo "  버전:   ${VERSION}"
echo "  zip:    ${ZIP_PATH}"
echo "  릴리스: https://github.com/${REPO}/releases/tag/${VERSION}"
echo ""
echo "사용자 설치 명령:"
echo "  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash"
