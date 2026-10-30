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
#   S1_DRYRUN_HOLD   dry run: seconds the pty input stays open (default 45)
#   S1_OWNER_KEY_CMD option A: command printing the nsec, piped to the signer
#   S1_SKIP_POLICY=1 do not retry an unpublished kind 30177 policy
#   S1_NO_CLIPBOARD=1  phase D does not put the body npub on the clipboard
#                    (tests). Only public values ever go on the clipboard.
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
# S1_ADD_PROFILE=1: renew the window with the buzz-profile kind (0) added, so
# serve can publish the body's Buzz profile and the owner can find the body in
# Buzz's new-message picker (it lists profiles and agents only; a bare npub is
# "no matching agents", owner run 2026-10-22). The renewal needs a new owner
# signature and a new enroll, so the bundle and the policy marker are reset.
if [[ ${S1_ADD_PROFILE:-} == 1 && ! -f $S1/share.profile-renewed ]]; then
  echo "== A: amq-remote share --renew --enable buzz-dm --enable buzz-profile"
  amq-remote share --root "$ROOT" --session "$SESSION" --renew --enable buzz-dm --enable buzz-profile >"$S1/share.txt"
  rm -f "$S1/bundle.json" "$S1/policy.published"
  touch "$S1/share.profile-renewed"
fi
if [[ ! -f $S1/share.txt ]]; then
  echo "== A: amq-remote share --enable buzz-dm${S1_PRESENCE:+ --enable buzz-profile}"
  amq-remote share --root "$ROOT" --session "$SESSION" --enable buzz-dm ${S1_PRESENCE:+--enable buzz-profile} >"$S1/share.txt"
fi
BODY=$(sed -n 's/^body-pubkey:[[:space:]]*\([0-9a-f]\{64\}\)$/\1/p' "$S1/share.txt" | head -1)
echo "body:  $BODY"

# --- B: OWNER (terminal) --------------------------------------------------
# amq-owner-sign signs the grants, writes the bundle, then publishes the
# owner's kind 30177 policy. The bundle is written before the publish, so a
# failed publish leaves a bundle and no policy (owner-sign main.go:168-176).
# On resume, a bundle without a recorded publish is retried. A retry is a new
# amq-owner-sign run, so it needs the owner key again. S1_SKIP_POLICY=1 skips
# it: the policy only lists the body in Buzz Desktop, and S1 does not read it.
#
# The owner key reaches amq-owner-sign one of two ways, and both are a
# one-time export to this Mac:
#   A  S1_OWNER_KEY_CMD: a command that prints the nsec, for example a
#      password manager CLI. It is piped to amq-owner-sign's stdin, which
#      reads one line when stdin is not a terminal (main.go:327-331).
#   B  otherwise, the hidden prompt (term.ReadPassword, main.go:324-326).
# The confirmation (the first 8 body characters) is always typed on
# /dev/tty (main.go:206-216). The nsec never passes through tee or the log.
owner_sign() {
  local log=$S1/owner-sign.log
  local args=(--share "$S1/share.txt" --out "$S1/bundle.json" --relay "$RELAY" --name "amq-squad S1 gate")
  if [[ -n ${S1_DRYRUN_NSEC_FILE:-} ]]; then
    echo "   DRY RUN: driving amq-owner-sign through a pty with a THROWAWAY key"
    # Paced so each read sees its own line and stdin stays open while the
    # signer runs (an early EOF on the pty reads as "not confirmed").
    { sleep 2; echo "${BODY:0:8}"; sleep 2; cat "$S1_DRYRUN_NSEC_FILE"; sleep "${S1_DRYRUN_HOLD:-45}"; } |
      script -q /dev/null amq-owner-sign "${args[@]}" 2>&1 | tee "$log" || true
  elif [[ -n ${S1_OWNER_KEY_CMD:-} ]]; then
    echo "   OWNER (option A): check the body pubkey ($BODY) and type its first 8 characters; the key comes from S1_OWNER_KEY_CMD."
    bash -c "$S1_OWNER_KEY_CMD" | amq-owner-sign "${args[@]}" 2>&1 | tee "$log" || true
  elif [[ -t 0 ]]; then
    echo "   OWNER (option B): check the body pubkey ($BODY), type its first 8 characters, then TYPE your nsec at the hidden prompt (never paste it)."
    amq-owner-sign "${args[@]}" 2>&1 | tee "$log" || true
  else
    echo "   OWNER STEP: in your own terminal run:"
    echo "     amq-owner-sign ${args[*]}"
    echo "   then run this script again."
    exit 10
  fi
  if grep -q '^published kind 30177 policy ' "$log" 2>/dev/null; then
    touch "$S1/policy.published"
  else
    echo "   (the kind 30177 policy was not published; the next run retries it, or set S1_SKIP_POLICY=1)"
  fi
}
if [[ ! -f $S1/bundle.json ]]; then
  echo "== B: owner signs the grants (amq-owner-sign)"
  owner_sign
elif [[ ! -f $S1/policy.published && ${S1_SKIP_POLICY:-} != 1 ]]; then
  echo "== B: retrying the kind 30177 policy publication (amq-owner-sign again; needs the owner key)"
  owner_sign
fi
[[ -f $S1/bundle.json ]] || { echo "no bundle written" >&2; exit 1; }
SIGNED_OWNER=$(python3 -I -c 'import json,sys; t=json.load(open(sys.argv[1])); print({x["owner_pubkey"] for x in t}.pop() if len({x["owner_pubkey"] for x in t})==1 else "MIXED")' "$S1/bundle.json")
if [[ $SIGNED_OWNER != "$OWNER_HEX" ]]; then
  echo "bundle is signed by $SIGNED_OWNER, not the pinned owner $OWNER_HEX" >&2
  exit 1
fi

# --- C: local enroll -------------------------------------------------------
if [[ ! -f $STATE/keys/$SESSION/share.json || $S1/bundle.json -nt $STATE/keys/$SESSION/share.json ]]; then
  echo "== C: amq-remote share --bundle (enroll)"
  amq-remote share --root "$ROOT" --session "$SESSION" --bundle "$S1/bundle.json"
fi

# --- D: OWNER (phone) ------------------------------------------------------
CHANNEL=${S1_DM_CHANNEL:-}
if [[ -z $CHANNEL && -f $S1/dm-channel ]]; then CHANNEL=$(cat "$S1/dm-channel"); fi
if [[ -z $CHANNEL && -n ${S1_PRESENCE:-} ]]; then
  # Presence path: bind the target with --presence (no DM yet) and start the
  # endpoint, so serve publishes the body's kind 0 profile and status. The
  # owner then finds the body by name in Buzz and opens the DM from there.
  if ! grep -q '"presence"' "$STATE/manifest.json" 2>/dev/null; then
    echo "== D0: amq-remote share --target --relay --presence (profile first, DM later)"
    amq-remote share --root "$ROOT" --session "$SESSION" --target "$TARGET" --relay "$RELAY" --presence "$S1_PRESENCE"
  fi
  if [[ -f $S1/up.pid ]] && kill -0 "$(cat "$S1/up.pid")" 2>/dev/null; then
    echo "== D0: amq-remote up already running (pid $(cat "$S1/up.pid"))"
  else
    echo "== D0: amq-remote up (background; publishes the profile; stop with teardown.sh)"
    nohup amq-remote up --root "$ROOT" --registry "$S1/keepalive-registry.json" >"$S1/up.log" 2>&1 &
    echo $! >"$S1/up.pid"
  fi
  sleep "${S1_UP_SETTLE:-8}"
  amq-remote doctor --root "$ROOT" --json >"$S1/doctor.json" 2>"$S1/doctor.err" || true
  grep -o '"policy_[a-z]*"\|"profile[a-z_]*": *"[a-z_]*"\|"relay_auth[a-z_]*": *"[a-z_]*"' "$S1/doctor.json" 2>/dev/null | sort -u | sed 's/^/     doctor: /'
fi
if [[ -z $CHANNEL ]]; then
  read -r _ BODY_NPUB < <("$GV" npub "$BODY")
  echo "== D: OWNER STEP (phone): in Buzz, open a 1:1 DM with the body${S1_PRESENCE:+ (search for the agent named \"$S1_PRESENCE\")}"
  echo "     body npub: $BODY_NPUB"
  # Mac -> phone only: a QR to scan, and the Mac clipboard (Universal
  # Clipboard). Nothing is copied back from the phone except the channel id.
  if command -v qrencode >/dev/null; then qrencode -t ANSIUTF8 "nostr:$BODY_NPUB"; fi
  if [[ ${S1_NO_CLIPBOARD:-} != 1 ]] && command -v pbcopy >/dev/null; then printf '%s' "$BODY_NPUB" | pbcopy && echo "     (body npub copied to the Mac clipboard)"; fi
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
