#!/usr/bin/env bash
# install-amq-release-binaries.sh VERSION DEST
#
# Installs amq-remote and amq-owner-sign from AMQ's GitHub release assets into
# DEST, for the real-AMQ CI lane (amq itself is go-installed there). VERSION is
# a release tag (v0.140.0) or "latest". Each asset is checked against the
# release's checksums.txt (sha256); on macOS each binary is also checked for a
# valid code signature by the AMQ release's Developer ID team. Any mismatch
# stops the script before anything is installed for that binary.
#
# Needs gh (GH_TOKEN in CI), shasum, tar and, on macOS, codesign.
set -euo pipefail

version="${1:?usage: install-amq-release-binaries.sh VERSION DEST}"
dest="${2:?usage: install-amq-release-binaries.sh VERSION DEST}"
repo="avivsinai/agent-message-queue"
team="93ZUZ2PTM9" # Developer ID Application: Aviv Sinai, the AMQ release signer

if [ "$version" = latest ]; then
  version="$(gh release view --repo "$repo" --json tagName -q .tagName)"
fi
v="${version#v}"
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "install-amq-release-binaries: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) echo "install-amq-release-binaries: unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

work="$(mktemp -d)"
mkdir -p "$dest"
gh release download "$version" --repo "$repo" --dir "$work" \
  -p checksums.txt \
  -p "amq-remote_${v}_${os}_${arch}.tar.gz" \
  -p "amq-owner-sign_${v}_${os}_${arch}.tar.gz"

for name in amq-remote amq-owner-sign; do
  asset="${name}_${v}_${os}_${arch}.tar.gz"
  line="$(awk -v a="$asset" '$2 == a' "$work/checksums.txt")"
  if [ -z "$line" ]; then
    echo "install-amq-release-binaries: no checksum for $asset in $version's checksums.txt" >&2
    exit 1
  fi
  (cd "$work" && printf '%s\n' "$line" | shasum -a 256 -c -)
  tar -xzf "$work/$asset" -C "$work" "$name"
  if [ "$os" = darwin ]; then
    codesign --verify --strict "$work/$name"
    signer="$(codesign -dv "$work/$name" 2>&1)"
    if ! printf '%s\n' "$signer" | grep -x "TeamIdentifier=$team" >/dev/null; then
      echo "install-amq-release-binaries: $name is not signed by team $team" >&2
      exit 1
    fi
  fi
  install -m 0755 "$work/$name" "$dest/$name"
  rm -f "$work/$asset" "$work/$name"
done
rm -f "$work/checksums.txt"
rmdir "$work"

"$dest/amq-remote" version
"$dest/amq-owner-sign" --version
