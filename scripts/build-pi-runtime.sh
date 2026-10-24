#!/bin/bash
# build-pi-runtime.sh: build amq-squad with the embedded pi runtime (plan 4.1,
# R1, brief 4-1; ported from spike/embedpi/build.sh, packaging option (a)).
#
#   scripts/build-pi-runtime.sh fetch   the one networked step: download Node,
#                                       fd and rg (S3 pins) when absent and
#                                       check every SHA-256; npm ci from
#                                       pi/package-lock.json (integrity-pinned)
#   scripts/build-pi-runtime.sh build   offline: stage Node + the pruned npm
#                                       tree + the @amq-squad/pi package +
#                                       fd/rg, pack the payload, go build with
#                                       -tags amqpi
#   scripts/build-pi-runtime.sh all     fetch, then build
#
# Environment:
#   AMQ_SQUAD_PI_DL     download cache (default ~/.amq-squad/spike/s3/dl, the S3 cache)
#   AMQ_SQUAD_NPM_CACHE npm cache (default ~/.amq-squad/spike/s3/npm-cache)
#   NPM_OFFLINE=1       npm ci --offline (only the cache; no registry request)
#   HOST_NODE           build-time Node bin dir for npm (default ~/.nvm/versions/node/v22.21.1/bin)
#   OUT                 output binary (default build/pi/amq-squad)
#
# The payload (internal/piruntime/payload/) and build/ are gitignored. darwin
# arm64 only: the payload carries the darwin-arm64 Node build. Release CI with
# signing is phase 5 (16.4).
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

pi_pkg='@earendil-works/pi-coding-agent'
pi_version='1.3.0'        # brief 4-1 ruling 1: keep 1.3.0 (upstream floor 1.2.0)
node_version='v22.21.1'   # pi 1.3.0 engines: node >=22.19.0
node_tgz="node-$node_version-darwin-arm64.tar.gz"
node_sha='c170d6554fba83d41d25a76cdbad85487c077e51fa73519e41ac885aa429d8af'
fd_ver='10.5.0'
fd_tgz="fd-v$fd_ver-aarch64-apple-darwin.tar.gz"
fd_sha='b67e1836c468e42e411984b56e52fa7abec08c2bd22c867398e7cc134aac5e12'
rg_ver='15.2.0'
rg_tgz="ripgrep-$rg_ver-aarch64-apple-darwin.tar.gz"
rg_sha='3750b2e93f37e0c692657da574d7019a101c0084da05a790c83fd335bad973e4'

dl="${AMQ_SQUAD_PI_DL:-$HOME/.amq-squad/spike/s3/dl}"
host_node="${HOST_NODE:-$HOME/.nvm/versions/node/$node_version/bin}"
export npm_config_cache="${AMQ_SQUAD_NPM_CACHE:-$HOME/.amq-squad/spike/s3/npm-cache}"
export npm_config_audit=false npm_config_fund=false npm_config_update_notifier=false
work="$root/build/pi"
out="${OUT:-$work/amq-squad}"
payload="$root/internal/piruntime/payload"

[[ "$(uname -s)-$(uname -m)" == Darwin-arm64 ]] || { echo 'darwin-arm64 only' >&2; exit 1; }
sha_ok() { [[ "$(/usr/bin/shasum -a 256 "$1" | cut -d' ' -f1)" == "$2" ]] || { echo "SHA-256 mismatch: $1" >&2; exit 1; }; }

fetch() {
  mkdir -p "$dl/node" "$dl/tools" "$work/npm"
  [[ -f "$dl/node/$node_tgz" ]] || curl -fsSL -o "$dl/node/$node_tgz" "https://nodejs.org/dist/$node_version/$node_tgz"
  sha_ok "$dl/node/$node_tgz" "$node_sha"
  [[ -f "$dl/tools/$fd_tgz" ]] || curl -fsSL -o "$dl/tools/$fd_tgz" "https://github.com/sharkdp/fd/releases/download/v$fd_ver/$fd_tgz"
  sha_ok "$dl/tools/$fd_tgz" "$fd_sha"
  [[ -f "$dl/tools/$rg_tgz" ]] || curl -fsSL -o "$dl/tools/$rg_tgz" "https://github.com/BurntSushi/ripgrep/releases/download/$rg_ver/$rg_tgz"
  sha_ok "$dl/tools/$rg_tgz" "$rg_sha"
  cp pi/package.json pi/package-lock.json "$work/npm/"
  offline=()
  [[ "${NPM_OFFLINE:-}" == 1 ]] && offline=(--offline)
  # The tree's install scripts are no-ops or version warnings (S3); skip them.
  PATH="$host_node:/usr/bin:/bin" npm ci --prefix "$work/npm" --ignore-scripts --engine-strict ${offline[@]+"${offline[@]}"}
  PATH="$host_node:/usr/bin:/bin" node -e '
    const p=require(process.argv[1]+"/package.json");
    if(p.name!==process.argv[2]||p.version!==process.argv[3]){console.error("pin mismatch",p.name,p.version);process.exit(1)}' \
    "$work/npm/node_modules/$pi_pkg" "$pi_pkg" "$pi_version"
  echo "fetch ok: $pi_pkg@$pi_version, node $node_version, fd $fd_ver, rg $rg_ver"
}

build() {
  # Review item 4: never trust a node_modules left from an earlier fetch.
  # Every build rebuilds the tree from the npm cache with `npm ci --offline`,
  # which checks every tarball against the lockfile's integrity hashes, and
  # only then hashes it into the manifest. An edited file in a previous tree
  # cannot reach the payload.
  /bin/rm -rf "$work/npm"
  mkdir -p "$work/npm"
  cp pi/package.json pi/package-lock.json "$work/npm/"
  PATH="$host_node:/usr/bin:/bin" npm ci --prefix "$work/npm" --offline --ignore-scripts --engine-strict
  [[ -d "$work/npm/node_modules/$pi_pkg" ]] || { echo 'npm ci --offline did not produce the pi tree; run scripts/build-pi-runtime.sh fetch first' >&2; exit 1; }
  stage="$work/stage"
  /bin/rm -rf "$stage" "$work/x"
  mkdir -p "$stage/node/bin" "$stage/pi" "$stage/tools" "$work/x"
  sha_ok "$dl/node/$node_tgz" "$node_sha"
  tar -xzf "$dl/node/$node_tgz" -C "$work/x" "node-$node_version-darwin-arm64/bin/node" "node-$node_version-darwin-arm64/LICENSE"
  cp "$work/x/node-$node_version-darwin-arm64/bin/node" "$stage/node/bin/node"
  cp "$work/x/node-$node_version-darwin-arm64/LICENSE" "$stage/node/LICENSE"
  # The @amq-squad/pi package with the npm tree its lock resolved.
  cp pi/package.json pi/package-lock.json "$stage/pi/"
  cp -R pi/extensions "$stage/pi/extensions"
  cp -R "$work/npm/node_modules" "$stage/pi/node_modules"
  /bin/rm -rf "$stage/pi/node_modules/.bin" "$stage/pi/node_modules/.package-lock.json"
  # S3 strip: source maps, type declarations, @types, foreign native addons.
  nm="$stage/pi/node_modules"
  /usr/bin/find "$nm" -type f \( -name '*.map' -o -name '*.d.ts' -o -name '*.d.mts' -o -name '*.d.cts' \) -delete
  /bin/rm -rf "$nm/@types"
  /usr/bin/find "$nm" -type f -name '*.node' ! -path '*darwin-arm64*' -delete
  /usr/bin/find "$nm" -type d -empty -delete
  sha_ok "$dl/tools/$fd_tgz" "$fd_sha"; sha_ok "$dl/tools/$rg_tgz" "$rg_sha"
  tar -xzf "$dl/tools/$fd_tgz" -C "$work/x"; tar -xzf "$dl/tools/$rg_tgz" -C "$work/x"
  cp "$work/x/fd-v$fd_ver-aarch64-apple-darwin/fd" "$stage/tools/fd"
  cp "$work/x/ripgrep-$rg_ver-aarch64-apple-darwin/rg" "$stage/tools/rg"
  cp "$work/x/fd-v$fd_ver-aarch64-apple-darwin/LICENSE-MIT" "$stage/tools/fd-LICENSE-MIT"
  cp "$work/x/ripgrep-$rg_ver-aarch64-apple-darwin/LICENSE-MIT" "$stage/tools/rg-LICENSE-MIT"
  go run ./scripts/pipack -tree "$stage" -out "$payload" \
    -version "pi-$pi_version-node-${node_version#v}-darwin-arm64" -platform darwin-arm64 \
    -pi "$pi_version" -node "${node_version#v}" -exe node/bin/node \
    -entry "pi/node_modules/$pi_pkg/dist/bundle/cli.js" -package pi \
    -tool fd=tools/fd -tool rg=tools/rg | tee "$work/pack.json"
  version="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags amqpi -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$out" ./cmd/amq-squad
  bytes=$(/usr/bin/stat -f %z "$out")
  echo "{\"binary\":\"$out\",\"binary_bytes\":$bytes}"
  (( bytes <= 250000000 )) || { echo 'FAIL: binary exceeds 250 MB (decimal)' >&2; exit 1; }
}

case "${1:-}" in
fetch) fetch ;;
build) build ;;
all) fetch; build ;;
*) echo 'usage: scripts/build-pi-runtime.sh fetch|build|all' >&2; exit 2 ;;
esac
