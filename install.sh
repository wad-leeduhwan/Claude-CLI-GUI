#!/bin/bash
set -e

APP_NAME="Claude Code GUI"
APP_BUNDLE="${APP_NAME}.app"
INSTALL_DIR="/Applications"
REPO="wad-leeduhwan/Claude-CLI-GUI"

echo "=== ${APP_NAME} 설치 ==="

# ── 로컬 .app 번들 탐색 ──
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
SOURCE="${SCRIPT_DIR}/${APP_BUNDLE}"

if [ ! -d "${SOURCE}" ]; then
    SOURCE="${SCRIPT_DIR}/build/bin/${APP_BUNDLE}"
fi

INSTALL_MODE="local"
TMP_DIR=""

if [ ! -d "${SOURCE}" ]; then
    # ── 원격 모드: GitHub Releases에서 다운로드 ──
    INSTALL_MODE="remote"
    echo "  로컬 .app 없음 → GitHub Releases에서 다운로드합니다."
    echo ""

    echo "[1/5] 최신 릴리스 확인..."
    RELEASE_JSON=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest") || {
        echo "ERROR: GitHub API 호출 실패"
        echo "  네트워크 연결을 확인하거나, https://github.com/${REPO}/releases 에서 직접 다운로드하세요."
        exit 1
    }

    DOWNLOAD_URL=$(echo "${RELEASE_JSON}" | grep '"browser_download_url"' | grep '\.zip"' | head -1 | cut -d '"' -f 4)
    TAG_NAME=$(echo "${RELEASE_JSON}" | grep '"tag_name"' | head -1 | cut -d '"' -f 4)

    if [ -z "${DOWNLOAD_URL}" ]; then
        echo "ERROR: 릴리스에서 zip 파일을 찾을 수 없습니다."
        echo "  https://github.com/${REPO}/releases 에서 직접 다운로드하세요."
        exit 1
    fi

    echo "  버전: ${TAG_NAME}"
    echo "  URL:  ${DOWNLOAD_URL}"

    TMP_DIR=$(mktemp -d)

    echo "[2/5] 다운로드 중..."
    curl -fsSL -L -o "${TMP_DIR}/app.zip" "${DOWNLOAD_URL}" || {
        rm -rf "${TMP_DIR}"
        echo "ERROR: 다운로드 실패"
        exit 1
    }
    echo "  $(du -h "${TMP_DIR}/app.zip" | cut -f1) 다운로드 완료"

    echo "[3/5] 압축 해제..."
    ditto -x -k "${TMP_DIR}/app.zip" "${TMP_DIR}"

    # 압축 해제된 .app 찾기
    SOURCE=$(find "${TMP_DIR}" -maxdepth 2 -name "*.app" -type d | head -1)
    if [ -z "${SOURCE}" ] || [ ! -d "${SOURCE}" ]; then
        rm -rf "${TMP_DIR}"
        echo "ERROR: zip 안에서 .app 번들을 찾을 수 없습니다."
        exit 1
    fi
fi

# ── 설치 공통 로직 ──
if [ "${INSTALL_MODE}" = "local" ]; then
    STEP_PREFIX=""
    STEP_TOTAL=4
    STEP_OFFSET=0
else
    STEP_PREFIX=""
    STEP_TOTAL=5
    STEP_OFFSET=3
fi

step() {
    local n=$((STEP_OFFSET + $1))
    echo "[${n}/${STEP_TOTAL}] $2"
}

step 1 "기존 앱 제거..."
if [ -d "${INSTALL_DIR}/${APP_BUNDLE}" ]; then
    rm -rf "${INSTALL_DIR}/${APP_BUNDLE}"
    echo "  기존 버전 제거 완료"
else
    echo "  기존 버전 없음"
fi

step 2 "앱 복사..."
cp -R "${SOURCE}" "${INSTALL_DIR}/${APP_BUNDLE}"
echo "  ${INSTALL_DIR}/${APP_BUNDLE}"

step 3 "Gatekeeper 격리 속성 제거 + Ad-hoc 서명..."
xattr -cr "${INSTALL_DIR}/${APP_BUNDLE}"
codesign --force --deep --sign - "${INSTALL_DIR}/${APP_BUNDLE}"

step 4 "검증..."
BINARY="${INSTALL_DIR}/${APP_BUNDLE}/Contents/MacOS/awesomeProject1"
if [ -f "${BINARY}" ]; then
    ARCH=$(file "${BINARY}" | grep -o 'arm64\|x86_64')
    echo "  Arch: ${ARCH}"
else
    echo "  (바이너리 검증 스킵)"
fi

# ── 임시 파일 정리 ──
if [ -n "${TMP_DIR}" ] && [ -d "${TMP_DIR}" ]; then
    rm -rf "${TMP_DIR}"
fi

echo ""
echo "=== 설치 완료 ==="
echo "Launchpad 또는 아래 명령으로 실행하세요:"
echo "  open \"${INSTALL_DIR}/${APP_BUNDLE}\""
echo ""
