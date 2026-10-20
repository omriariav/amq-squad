#!/usr/bin/env bash
# enroll.sh <owner-npub> <relay-url>
#
# Enroll a strict amq-remote Buzz DM share for the S1 gate prototype in a
# scratch AMQ root with an operator handle, following amq-remote v0.102.0
# (cmd/amq-remote/README.md "Strict share in one owner step"):
#
#   A  local   amq init (operator handle), manifest with a fake target,
#              amq-remote share --enable buzz-dm        -> share.txt
#   B  OWNER   amq-owner-sign --share share.txt         -> bundle.json
#              (owner's terminal: types the body prefix and the owner nsec)
#   C  local   amq-remote share --bundle                (enroll, no target yet)
#   D  OWNER   open a 1:1 DM with the body in Buzz (phone); give its channel id
#   E  local   amq-remote share --target --relay --dm-channel --native-session
#   F  local   amq-remote up (background), doctor
#
# The script is resumable: each phase skips when its output exists. It stops
# after C when no DM channel is known, and prints what the owner must do.
#
# Environment:
#   S1_ROOT          scratch AMQ root (default ~/.amq-squad/spike/s1/root)
#   S1_SESSION       share session name (default s1gate)
#   S1_OPERATOR      operator handle (default operator)
#   S1_DM_CHANNEL    the DM channel id from phase D
#   S1_NO_UP=1       skip phase F
#   S1_DRYRUN_NSEC_FILE  DRY RUN ONLY: a THROWAWAY owner secret file; phase B
#                    is then driven through a pty instead of the owner. Never
#                    point this at a real owner key.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <owner-npub> <relay-url>" >&2
  exit 2
fi
OWNER_NPUB=$1
RELAY=$2
HERE=$(cd "$(dirname "$0")" && pwd)
S1=${S1_HOME:-$HOME/.amq-squad/spike/s1}
ROOT=${S1_ROOT:-$S1/root}
SESSION=${S1_SESSION:-s1gate}
OPERATOR=${S1_OPERATOR:-operator}
TARGET=gate-fake
GV=${GV:-$S1/gateverify}
STATE=$ROOT/extensions/remote
mkdir -p "$S1"

if [[ ! -x $GV ]]; then
  (cd "$HERE" && go build -o "$GV" .)
fi
read -r OWNER_HEX _ < <("$GV" npub "$OWNER_NPUB") || { echo "bad owner npub" >&2; exit 2; }
echo "owner: $OWNER_NPUB ($OWNER_HEX)"
echo "relay: $RELAY"
echo "root:  $ROOT  session: $SESSION  operator handle: $OPERATOR"

# --- A: local -------------------------------------------------------------
if [[ ! -f $ROOT/meta/config.json ]]; then
  echo "== A: amq init with the operator handle"
  amq init --root "$ROOT" --agents "$OPERATOR,remote" >/dev/null
fi
mkdir -p "$STATE"
if [[ ! -f $STATE/manifest.json ]]; then
  # A fake target: the share needs a declared adapter target. Owner DMs
  # become submits to it (harmless); the gate answer is read from the relay.
  cat >"$STATE/manifest.json" <<JSON
{"schema_version": 1, "layer": "remote", "adapters": [{"kind": "fake", "target": "$TARGET", "epoch": "s1-epoch-1"}]}
JSON
fi
if [[ ! -f $S1/share.txt ]]; then
  echo "== A: amq-remote share --enable buzz-dm"
  amq-remote share --root "$ROOT" --session "$SESSION" --enable buzz-dm >"$S1/share.txt"
fi
BODY=$(sed -n 's/^body-pubkey:[[:space:]]*\([0-9a-f]\{64\}\)$/\1/p' "$S1/share.txt" | head -1)
echo "body:  $BODY"

# --- B: OWNER (terminal) --------------------------------------------------
if [[ ! -f $S1/bundle.json ]]; then
  echo "== B: owner signs the grants (amq-owner-sign)"
  if [[ -n ${S1_DRYRUN_NSEC_FILE:-} ]]; then
    echo "   DRY RUN: driving amq-owner-sign through a pty with a THROWAWAY key"
    # Paced so each read sees its own line and stdin stays open while the
    # signer runs (an early EOF on the pty reads as "not confirmed").
    { sleep 2; echo "${BODY:0:8}"; sleep 2; cat "$S1_DRYRUN_NSEC_FILE"; sleep 45; } |
      script -q /dev/null amq-owner-sign --share "$S1/share.txt" --out "$S1/bundle.json" --relay "$RELAY" --name "amq-squad S1 gate" ||
      echo "   (amq-owner-sign exited non-zero; the bundle is written before the policy publish)"
  elif [[ -t 0 ]]; then
    echo "   OWNER: check the body pubkey ($BODY), type its first 8 characters, then your nsec."
    amq-owner-sign --share "$S1/share.txt" --out "$S1/bundle.json" --relay "$RELAY" --name "amq-squad S1 gate"
  else
    echo "   OWNER STEP: in your own terminal run:"
    echo "     amq-owner-sign --share $S1/share.txt --out $S1/bundle.json --relay $RELAY"
    echo "   then run this script again."
    exit 10
  fi
fi
[[ -f $S1/bundle.json ]] || { echo "no bundle written" >&2; exit 1; }
SIGNED_OWNER=$(python3 -I -c 'import json,sys; t=json.load(open(sys.argv[1])); print({x["owner_pubkey"] for x in t}.pop() if len({x["owner_pubkey"] for x in t})==1 else "MIXED")' "$S1/bundle.json")
if [[ $SIGNED_OWNER != "$OWNER_HEX" ]]; then
  echo "bundle is signed by $SIGNED_OWNER, not the pinned owner $OWNER_HEX" >&2
  exit 1
fi

# --- C: local enroll -------------------------------------------------------
if [[ ! -f $STATE/keys/$SESSION/share.json ]]; then
  echo "== C: amq-remote share --bundle (enroll)"
  amq-remote share --root "$ROOT" --session "$SESSION" --bundle "$S1/bundle.json"
fi

# --- D: OWNER (phone) ------------------------------------------------------
CHANNEL=${S1_DM_CHANNEL:-}
if [[ -z $CHANNEL && -f $S1/dm-channel ]]; then CHANNEL=$(cat "$S1/dm-channel"); fi
if [[ -z $CHANNEL ]]; then
  read -r _ BODY_NPUB < <("$GV" npub "$BODY")
  echo "== D: OWNER STEP (phone): in Buzz, open a 1:1 DM with the body"
  echo "     body npub: $BODY_NPUB"
  echo "   then rerun with S1_DM_CHANNEL=<channel id> (or write it to $S1/dm-channel)."
  exit 11
fi
echo "$CHANNEL" >"$S1/dm-channel"

# --- E: bind the share -----------------------------------------------------
if ! grep -q '"dm_channel_id"' "$STATE/manifest.json" 2>/dev/null; then
  echo "== E: amq-remote share --target --relay --dm-channel --native-session"
  amq-remote share --root "$ROOT" --session "$SESSION" --target "$TARGET" --relay "$RELAY" \
    --dm-channel "$CHANNEL" --native-session "$TARGET"
fi

if [[ -n ${S1_RELAY_SELF:-} ]]; then
  # TEST ONLY, for a loopback relay with no NIP-11 document: pin the relay's
  # self key (the manifest's optional relay_self, README "Relay").
  python3 -I - "$STATE/manifest.json" "$S1_RELAY_SELF" <<'PY'
import json,sys
p,selfkey=sys.argv[1],sys.argv[2]
m=json.load(open(p)); m["relay"]["relay_self"]=selfkey
open(p,"w").write(json.dumps(m,indent=2)+"\n")
PY
fi

cat >"$S1/s1.env" <<ENV
S1_ROOT=$ROOT
S1_SESSION=$SESSION
S1_OPERATOR=$OPERATOR
S1_RELAY=$RELAY
S1_OWNER_NPUB=$OWNER_NPUB
S1_OWNER_HEX=$OWNER_HEX
S1_BODY=$BODY
S1_DM_CHANNEL=$CHANNEL
ENV
echo "wrote $S1/s1.env"

# --- F: endpoint ------------------------------------------------------------
if [[ ${S1_NO_UP:-} != 1 ]]; then
  if [[ -f $S1/up.pid ]] && kill -0 "$(cat "$S1/up.pid")" 2>/dev/null; then
    echo "== F: amq-remote up already running (pid $(cat "$S1/up.pid"))"
  else
    echo "== F: amq-remote up (background; stop with teardown.sh)"
    nohup amq-remote up --root "$ROOT" --registry "$S1/keepalive-registry.json" >"$S1/up.log" 2>&1 &
    echo $! >"$S1/up.pid"
  fi
  sleep "${S1_UP_SETTLE:-8}"
  amq-remote doctor --root "$ROOT" --json >"$S1/doctor.json" 2>"$S1/doctor.err" || true
  python3 -I - "$S1/doctor.json" <<'PY' || true
import json,sys
d=json.load(open(sys.argv[1]))
for f in d.get("failing",[]) or []:
    print("   doctor failing:", f.get("boundary") or f.get("subject"), "-", f.get("detail","")[:160])
print("   doctor ok" if not d.get("failing") else "")
PY
fi
