#!/bin/bash
# S3 spike: build embedpi with an embedded, pinned pi runtime (darwin-arm64).
#
#   build.sh fetch      one-time network step: npm ci from the committed lock,
#                       download Node, fd, rg and the upstream pi release
#                       archive, verify every download against a pinned SHA-256
#   build.sh node       DECIDED (a): official Node + pruned npm pi tree + fd/rg
#   build.sh node-full  (a) without pruning or fd/rg, for comparison
#   build.sh bun        (b1) our `bun build --compile` of the npm dist: dropped,
#                       not faithful (codemode worker path lacks src/)
#   build.sh upstream   (b2) upstream's Bun-compiled release: the only fallback
#
# Only `fetch` touches the network. Everything after it is offline.
# Downloads live under ~/.amq-squad/spike/s3/; build products under .work/ and
# bin/ (gitignored). Nothing is installed globally.
set -euo pipefail
cd "$(dirname "$0")"
here="$PWD"

pi_pkg='@earendil-works/pi-coding-agent'
pi_version='1.3.0'
node_version='v22.21.1'   # pi 1.3.0 engines: node >=22.19.0
node_tgz="node-$node_version-darwin-arm64.tar.gz"
node_sha='c170d6554fba83d41d25a76cdbad85487c077e51fa73519e41ac885aa429d8af'
up_tgz='pi-darwin-arm64.tar.gz'
up_sha='1f9ae61cdeae04dd68caf1a1e8ab8ec9b292062f6ccb3252e0a9cbe93b5b16f1'
# pi downloads the *latest* fd/rg at run time when missing; we embed pinned ones.
fd_ver='10.5.0'
fd_tgz="fd-v$fd_ver-aarch64-apple-darwin.tar.gz"
fd_sha='b67e1836c468e42e411984b56e52fa7abec08c2bd22c867398e7cc134aac5e12'
rg_ver='15.2.0'
rg_tgz="ripgrep-$rg_ver-aarch64-apple-darwin.tar.gz"
rg_sha='3750b2e93f37e0c692657da574d7019a101c0084da05a790c83fd335bad973e4'

dl="$HOME/.amq-squad/spike/s3/dl"
host_node="${HOST_NODE:-$HOME/.nvm/versions/node/$node_version/bin}"   # build-time only
bun="${BUN:-/opt/homebrew/bin/bun}"                                       # build-time only
export npm_config_cache="$HOME/.amq-squad/spike/s3/npm-cache"
export npm_config_audit=false npm_config_fund=false npm_config_update_notifier=false

[[ "$(uname -s)-$(uname -m)" == Darwin-arm64 ]] || { echo 'darwin-arm64 only' >&2; exit 1; }
sha_ok() { [[ "$(/usr/bin/shasum -a 256 "$1" | cut -d' ' -f1)" == "$2" ]] || { echo "SHA-256 mismatch: $1" >&2; exit 1; }; }

pkg="$here/.work/npm/node_modules/$pi_pkg"

case "${1:-}" in
fetch)
  mkdir -p "$dl/node" "$dl/pi-upstream" .work/npm
  cp package.json package-lock.json .work/npm/
  # Install scripts in this tree are no-ops or version warnings; skip them.
  PATH="$host_node:/usr/bin:/bin" npm ci --prefix .work/npm --omit=dev --ignore-scripts --engine-strict
  PATH="$host_node:/usr/bin:/bin" node -e '
    const p=require(process.argv[1]+"/package.json");
    if(p.name!==process.argv[2]||p.version!==process.argv[3]){console.error("pin mismatch",p.name,p.version);process.exit(1)}' \
    "$pkg" "$pi_pkg" "$pi_version"
  [[ -f "$dl/node/$node_tgz" ]] || curl -fsSL -o "$dl/node/$node_tgz" "https://nodejs.org/dist/$node_version/$node_tgz"
  sha_ok "$dl/node/$node_tgz" "$node_sha"
  [[ -f "$dl/pi-upstream/$up_tgz" ]] || curl -fsSL -o "$dl/pi-upstream/$up_tgz" \
    "https://github.com/earendil-works/pi/releases/download/v$pi_version/$up_tgz"
  sha_ok "$dl/pi-upstream/$up_tgz" "$up_sha"
  mkdir -p "$dl/tools"
  [[ -f "$dl/tools/$fd_tgz" ]] || curl -fsSL -o "$dl/tools/$fd_tgz" \
    "https://github.com/sharkdp/fd/releases/download/v$fd_ver/$fd_tgz"
  sha_ok "$dl/tools/$fd_tgz" "$fd_sha"
  [[ -f "$dl/tools/$rg_tgz" ]] || curl -fsSL -o "$dl/tools/$rg_tgz" \
    "https://github.com/BurntSushi/ripgrep/releases/download/$rg_ver/$rg_tgz"
  sha_ok "$dl/tools/$rg_tgz" "$rg_sha"
  echo "fetch ok: $pi_pkg@$pi_version, node $node_version, upstream $up_tgz, fd $fd_ver, rg $rg_ver"
  exit 0 ;;
node|node-full|bun|upstream) cand="$1" ;;
*) echo 'usage: build.sh fetch|node|node-full|bun|upstream' >&2; exit 2 ;;
esac

[[ -d "$pkg" ]] || { echo 'run build.sh fetch first' >&2; exit 1; }
stage="$here/.work/stage-$cand"
/bin/rm -rf "$stage"
mkdir -p "$stage" bin

add_tools() {  # pinned fd and rg into $stage/tools/, digest-checked again
  sha_ok "$dl/tools/$fd_tgz" "$fd_sha"; sha_ok "$dl/tools/$rg_tgz" "$rg_sha"
  local x="$here/.work/tools-x"; /bin/rm -rf "$x"; mkdir -p "$x" "$stage/tools"
  tar -xzf "$dl/tools/$fd_tgz" -C "$x"; tar -xzf "$dl/tools/$rg_tgz" -C "$x"
  cp "$x/fd-v$fd_ver-aarch64-apple-darwin/fd" "$stage/tools/fd"
  cp "$x/ripgrep-$rg_ver-aarch64-apple-darwin/rg" "$stage/tools/rg"
  cp "$x/fd-v$fd_ver-aarch64-apple-darwin/LICENSE-MIT" "$stage/tools/fd-LICENSE-MIT"
  cp "$x/ripgrep-$rg_ver-aarch64-apple-darwin/LICENSE-MIT" "$stage/tools/rg-LICENSE-MIT"
}

case "$cand" in
node|node-full)
  # Official Node.js build (Developer ID signed, notarized by the Node project).
  sha_ok "$dl/node/$node_tgz" "$node_sha"
  mkdir -p "$stage/node/bin" .work/node-x
  tar -xzf "$dl/node/$node_tgz" -C .work/node-x "node-$node_version-darwin-arm64/bin/node" "node-$node_version-darwin-arm64/LICENSE"
  cp ".work/node-x/node-$node_version-darwin-arm64/bin/node" "$stage/node/bin/node"
  cp ".work/node-x/node-$node_version-darwin-arm64/LICENSE" "$stage/node/LICENSE"
  # The complete npm-installed tree, exactly as the committed lock resolved it.
  mkdir -p "$stage/pi"
  cp -R .work/npm/node_modules "$stage/pi/node_modules"
  cp package.json package-lock.json "$stage/pi/"
  /bin/rm -rf "$stage/pi/node_modules/.bin" "$stage/pi/node_modules/.package-lock.json"   # symlinks / npm metadata
  suffix=''
  if [[ "$cand" == node ]]; then
    # Decided strip (S3 ruling 1): source maps, type declarations, @types and
    # native prebuilds for other platforms. None is read at run time.
    nm="$stage/pi/node_modules"
    /usr/bin/find "$nm" -type f \( -name '*.map' -o -name '*.d.ts' -o -name '*.d.mts' -o -name '*.d.cts' \) -delete
    /bin/rm -rf "$nm/@types"
    /usr/bin/find "$nm" -type f -name '*.node' ! -path '*darwin-arm64*' -delete
    /usr/bin/find "$nm" -type d -empty -delete
    add_tools
    suffix='-stripped'
  fi
  tool_args=()
  [[ -d "$stage/tools" ]] && tool_args=(--tool fd=tools/fd --tool rg=tools/rg)
  python3 pack.py ${tool_args[@]+"${tool_args[@]}"} "$stage" "pi-$pi_version-node-${node_version#v}-darwin-arm64$suffix" \
    node/bin/node "pi/node_modules/$pi_pkg/dist/bundle/cli.js"
  ;;
bun)
  # Mirrors upstream build:binary + copy-binary-assets, but from the npm dist
  # (upstream compiles from src/*.ts; see S3-report on the worker-path gap).
  "$bun" --version > "$stage/.bun-version"
  ( cd .work/npm && "$bun" build --compile --target=bun-darwin-arm64 \
      --no-compile-autoload-bunfig --no-compile-autoload-dotenv \
      "node_modules/$pi_pkg/dist/bun/cli.js" \
      "node_modules/$pi_pkg/dist/utils/image-resize-worker.js" \
      "node_modules/$pi_pkg/dist/extensions/codemode/worker.js" \
      --outfile "$stage/pi" )
  /bin/rm -f "$stage/.bun-version"
  cp "$pkg/package.json" "$pkg/README.md" "$pkg/CHANGELOG.md" "$stage/"
  mkdir -p "$stage/theme" "$stage/assets" "$stage/export-html/vendor" "$stage/native/darwin/prebuilds/darwin-arm64"
  cp "$pkg"/dist/modes/interactive/theme/*.json "$stage/theme/"
  cp "$pkg"/dist/modes/interactive/assets/*.png "$stage/assets/"
  cp "$pkg"/dist/core/export-html/template.{html,css,js} "$stage/export-html/"
  cp "$pkg"/dist/core/export-html/vendor/*.js "$stage/export-html/vendor/"
  cp -R "$pkg/docs" "$pkg/examples" "$stage/"
  cp .work/npm/node_modules/@silvia-odwyer/photon-node/photon_rs_bg.wasm "$stage/"
  cp .work/npm/node_modules/@earendil-works/pi-tui/native/darwin/prebuilds/darwin-arm64/darwin-platform.node \
     "$stage/native/darwin/prebuilds/darwin-arm64/"
  python3 pack.py "$stage" "pi-$pi_version-bun-$("$bun" --version)-darwin-arm64" pi
  ;;
upstream)
  sha_ok "$dl/pi-upstream/$up_tgz" "$up_sha"
  tar -xzf "$dl/pi-upstream/$up_tgz" -C "$stage" --strip-components=1
  python3 pack.py "$stage" "pi-$pi_version-upstream-bun-darwin-arm64" pi
  ;;
esac

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o "bin/embedpi-$cand" .
cp assets/manifest.json ".work/manifest-$cand.json"
cp assets/payload.tar.gz ".work/payload-$cand.tar.gz"
python3 - "$cand" <<'PY'
import json, pathlib, sys
c = sys.argv[1]
m = json.loads(pathlib.Path(f'.work/manifest-{c}.json').read_text())
r = dict(candidate=c, version=m['version'],
         binary_bytes=pathlib.Path(f'bin/embedpi-{c}').stat().st_size,
         payload_bytes=pathlib.Path(f'.work/payload-{c}.tar.gz').stat().st_size,
         unpacked_bytes=sum(f['size'] for f in m['files']), files=len(m['files']))
print(json.dumps(r))
if r['binary_bytes'] > 250_000_000:
    raise SystemExit('FAIL: bundle exceeds 250 MB (decimal)')
PY
