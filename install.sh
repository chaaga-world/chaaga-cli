#!/usr/bin/env sh
# install.sh — install the chaaga CLI on macOS or Linux
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.sh | sh
#   # or pin a version:
#   curl -fsSL https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.sh | sh -s v1.2.3
set -e

REPO="chaaga-world/chaaga-cli"
BIN="chaaga"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"

# ── Detect OS ─────────────────────────────────────────────────────────────────
OS="$(uname -s)"
case "$OS" in
  Linux)  OS="linux" ;;
  Darwin) OS="darwin" ;;
  *)      echo "Unsupported OS: $OS" >&2; exit 1 ;;
esac

# ── Detect arch ───────────────────────────────────────────────────────────────
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported arch: $ARCH" >&2; exit 1 ;;
esac

# ── Resolve 'latest' version via GitHub API ───────────────────────────────────
if [ "$VERSION" = "latest" ]; then
  API_RESPONSE="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>&1)" || true
  VERSION="$(printf '%s' "$API_RESPONSE" \
    | grep '"tag_name"' \
    | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')"
  if [ -z "$VERSION" ]; then
    echo "" >&2
    echo "Could not find a published release for ${REPO}." >&2
    echo "" >&2
    echo "If you are the maintainer, create a release with GoReleaser first:" >&2
    echo "  goreleaser release --clean" >&2
    echo "" >&2
    echo "Or build from source (requires Go 1.22+):" >&2
    echo "  git clone https://github.com/${REPO} && cd chaaga-cli && go build -o chaaga ." >&2
    echo "" >&2
    exit 1
  fi
fi

# GoReleaser strips the leading 'v' in archive filenames (tag v0.2 → file 0.2)
FILE_VERSION="${VERSION#v}"
ARCHIVE="chaaga_${FILE_VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
# ── Download ──────────────────────────────────────────────────────────────────
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading chaaga ${VERSION} (${OS}/${ARCH})..."
curl -fsSL "$URL" -o "$TMP/$ARCHIVE"

# ── Verify checksum ───────────────────────────────────────────────────────────
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"
if curl -fsSL "$CHECKSUM_URL" -o "$TMP/checksums.txt" 2>/dev/null; then
  EXPECTED="$(grep "$ARCHIVE" "$TMP/checksums.txt" | awk '{print $1}')"
  if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')"
  fi
  if [ -n "$EXPECTED" ] && [ -n "$ACTUAL" ]; then
    if [ "$EXPECTED" != "$ACTUAL" ]; then
      echo "Checksum mismatch! Expected $EXPECTED, got $ACTUAL" >&2
      exit 1
    fi
    echo "Checksum verified."
  fi
fi

# ── Extract ───────────────────────────────────────────────────────────────────
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"

# ── Install ───────────────────────────────────────────────────────────────────
# Prefer /usr/local/bin if writable, otherwise fall back to ~/.local/bin (no sudo)
if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP/$BIN" "$INSTALL_DIR/$BIN"
  chmod +x "$INSTALL_DIR/$BIN"
  echo "Installed to $INSTALL_DIR/$BIN"
else
  INSTALL_DIR="$HOME/.local/bin"
  mkdir -p "$INSTALL_DIR"
  mv "$TMP/$BIN" "$INSTALL_DIR/$BIN"
  chmod +x "$INSTALL_DIR/$BIN"
  echo "Installed to $INSTALL_DIR/$BIN"
  echo ""
  echo "NOTE: Make sure $INSTALL_DIR is in your PATH."
  echo "  Add this to your shell profile:"
  echo "    export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

echo ""
echo "chaaga $("$INSTALL_DIR/$BIN" --version 2>/dev/null || echo "$VERSION") installed successfully."
