#!/bin/bash
# S3 Gatekeeper probe: simulate a browser download of the launcher by setting
# com.apple.quarantine on a copy, then run it from a shell in the stripped env.
# The unpacked runtime goes to a throwaway dir under ~/.amq-squad/runtime/.
# NOTE: if Gatekeeper blocks, macOS may show a dialog on the console.
set -u
cand="${1:-bun}"
here="$(cd "$(dirname "$0")" && pwd)"
q="$HOME/.amq-squad/spike/s3/qtest"
rt="$HOME/.amq-squad/runtime/qtest-$cand"
/bin/rm -rf "$q" "$rt"
mkdir -p "$q"
cp "$here/bin/embedpi-$cand" "$q/embedpi"
/usr/bin/xattr -w com.apple.quarantine "0081;$(printf %x "$(date +%s)");Safari;" "$q/embedpi"
echo "== launcher xattr"; /usr/bin/xattr -l "$q/embedpi"
echo "== spctl launcher"; /usr/sbin/spctl --assess --type execute -vvv "$q/embedpi" 2>&1; echo "spctl_exit=$?"
echo "== run quarantined launcher (60 s alarm)"
start=$(/bin/date +%s)
/usr/bin/perl -e 'alarm 60; exec @ARGV' /usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin TERM=xterm \
  EMBEDPI_TIMING=1 EMBEDPI_RUNTIME_DIR="$rt" PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0 \
  "$q/embedpi" --version 2>&1
echo "run_exit=$? seconds=$(( $(/bin/date +%s) - start ))"
exe="$(/usr/bin/find "$rt" -maxdepth 4 \( -name pi -o -name node \) -type f -perm -u+x 2>/dev/null | head -1)"
if [[ -n "$exe" ]]; then
  echo "== unpacked exe $exe"; /usr/bin/xattr -l "$exe"
  echo "== spctl unpacked"; /usr/sbin/spctl --assess --type execute -vvv "$exe" 2>&1; echo "spctl_exit=$?"
  echo "== direct exec of unpacked exe"; /usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin "$exe" --version 2>&1; echo "exit=$?"
else
  echo "no runtime unpacked"
fi
echo "== after: xattr -d com.apple.quarantine and rerun"
/usr/bin/xattr -d com.apple.quarantine "$q/embedpi"
/usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin TERM=xterm EMBEDPI_RUNTIME_DIR="$rt" PI_OFFLINE=1 "$q/embedpi" --version 2>&1; echo "exit=$?"
/bin/rm -rf "$rt"
