#!/usr/bin/env bash
#
# Download the latest chaaga-cli release and install it on this computer.
#
# Usage:
#   ./install.sh [flags]
#   curl -fsSL https://raw.githubusercontent.com/chaaga-world/chaaga-cli/main/install.sh | bash
#
# Flags:
#   --version X     install a specific version (e.g. 1.4.0) instead of latest
#   --pre           allow the newest prerelease if it's ahead of the latest stable
#   --bin-dir DIR   install into DIR (default: /usr/local/bin, else ~/.local/bin)
#   -h, --help      show this help

set -euo pipefail

REPO="chaaga-world/chaaga-cli"
BIN_NAME="chaaga-cli"

want_version=""
want_pre=0
bin_dir=""

while [ $# -gt 0 ]; do
  case "$1" in
    --version) want_version="${2#v}"; shift ;;
    --pre) want_pre=1 ;;
    --bin-dir) bin_dir="${2:-}"; shift ;;
    -h|--help) sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
  shift
done

die() { echo "install.sh: $*" >&2; exit 1; }

# --- platform -------------------------------------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
  darwin|linux) ;;
  *) die "unsupported OS '$os' — on Windows, download ${BIN_NAME}-windows-amd64.exe from the Releases page by hand" ;;
esac
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) die "unsupported CPU architecture '$arch'" ;;
esac
asset="${BIN_NAME}-${os}-${arch}"

# --- tools --------------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }
have curl || have wget || die "need curl or wget"
fetch() { if have curl; then curl -fsSL "$1"; else wget -qO- "$1"; fi; }

# --- resolve tag ------------------------------------------------------
api="https://api.github.com/repos/$REPO"
if [ -n "$want_version" ]; then
  tag="v${want_version}"
elif [ "$want_pre" -eq 1 ]; then
  tag="$(fetch "$api/releases?per_page=1" \
        | grep '"tag_name"' | head -n1 \
        | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
else
  tag="$(fetch "$api/releases/latest" \
        | grep '"tag_name"' | head -n1 \
        | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
fi
[ -n "${tag:-}" ] || die "could not determine a release to install from $REPO"
version="${tag#v}"
echo ">> release: $tag"

# --- download -------------------------------------------------------
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
base="https://github.com/$REPO/releases/download/$tag"
echo ">> downloading $asset"
fetch "$base/$asset" > "$tmp/$asset" || die "download failed: $base/$asset"
[ -s "$tmp/$asset" ] || die "downloaded file is empty (no build for $os/$arch in $tag?)"
fetch "$base/SHA256SUMS" > "$tmp/SHA256SUMS" 2>/dev/null || true

# --- verify checksum ----------------------------------------------
if [ -s "$tmp/SHA256SUMS" ]; then
  want_sum="$(awk -v a="$asset" '$2==a || $2=="*"a {print $1}' "$tmp/SHA256SUMS" | head -n1)"
  if [ -n "$want_sum" ]; then
    if have sha256sum; then got_sum="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
    else got_sum="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"; fi
    [ "$want_sum" = "$got_sum" ] || die "checksum mismatch for $asset"
    echo ">> checksum ok"
  fi
else
  echo ">> warning: no SHA256SUMS in the release — skipping checksum" >&2
fi

# --- install --------------------------------------------------------
if [ -z "$bin_dir" ]; then
  if mkdir -p /usr/local/bin 2>/dev/null && [ -w /usr/local/bin ]; then
    bin_dir=/usr/local/bin
  else
    bin_dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$bin_dir" || die "cannot create $bin_dir"
dest="$bin_dir/$BIN_NAME"

chmod +x "$tmp/$asset"
if mv "$tmp/$asset" "$dest" 2>/dev/null; then :
elif have sudo; then
  echo ">> writing to $bin_dir needs sudo"
  sudo mv "$tmp/$asset" "$dest"
else
  die "cannot write to $bin_dir — re-run with: --bin-dir \"\$HOME/.local/bin\""
fi

[ "$os" = "darwin" ] && xattr -d com.apple.quarantine "$dest" 2>/dev/null || true

echo ">> installed $BIN_NAME $version -> $dest"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo ">> note: $bin_dir is not on your PATH — add it or run $dest directly" ;;
esac
"$dest" version 2>/dev/null || true
