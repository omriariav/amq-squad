#!/usr/bin/env bash
# enroll.sh <owner-npub> <relay-url>
#
# The owner script that enrolls the one Buzz agent of amq-squad 3.0 (plan 9,
# R42, R33; brief 4-5 ruling 1: an owner script for 3.0, the S1 enroll.sh made
# the default). It is spike/gateverify/enroll.sh with the S1 lessons as
# defaults:
#
#   - profile and presence are on (amendment 19a): the share asks for the
#     buzz-dm and buzz-profile kinds, 8 grants, one owner signature, and the
#     body publishes a Buzz profile so the owner finds it by name in Buzz
#     (S1: a bare npub is "no matching agents" in the new-message picker);
#   - the owner key reaches amq-owner-sign ONLY through a password-manager
#     pipe (plan 9 secret rule). There is no typed or pasted path. The key
#     never passes through this script: no variable, no file, no log, no
#     argv, no clipboard, no terminal;
#   - the grant window is 30 days (ruling 2);
#   - the gate thread rule is on in the core (amendment 19b, c).
#
# Phases (each skips when its output exists; rerun to resume):
#   A  local   amq init, a manifest with the share's target, and
#              amq-remote share --enable buzz-dm --enable buzz-profile
#   B  OWNER   <password manager> | amq-owner-sign   (types the body prefix)
#   C  local   amq-remote share --bundle             (enroll the 8 grants)
#   D  local   bind presence (--presence NAME), start the endpoint so the
#              profile is published; OWNER opens the DM with the agent in
#              Buzz and passes the channel id (exit 11 until then)
#   E  local   amq-squad remote bind (the gate binding, issuer key, trust)
#   F  local   stop the enrollment endpoint; `amq-squad remote attach` runs
#              it from now on, only while the bound session is present
#
# THE PIPE (the only way the key enters). BUZZ_OWNER_KEY_CMD is a command
# whose stdout is the owner key on one line and nothing else; this script
# runs it with its stdout connected to amq-owner-sign's stdin and to nothing
# else:
#
#   BUZZ_OWNER_KEY_CMD='op read "op://<vault>/<item>/<field>"'
#
# Use the password manager's own read command. Do not wrap it in printf or
# echo: in S1 an unquoted printf %s\n inside sh -c appended a literal "n" to
# the key ("not a valid nsec"). Never run the key command on its own in a
# terminal, into a file or into a log: that is the forbidden form of the
# same command (plan 9). The command text itself names where the key is,
# not the key, so it may appear in your shell history.
#
# Environment:
#   BUZZ_HOME          state directory (default ~/.amq-squad/buzz)
#   BUZZ_SESSION       share session (default amq-squad)
#   BUZZ_NAME          the agent's name in Buzz (default "amq-squad")
#   BUZZ_DAYS          grant window in days (default 30)
#   BUZZ_OWNER_KEY_CMD the password-manager read command (phase B)
#   BUZZ_DM_CHANNEL    the DM channel id from phase D
#   BUZZ_RENEW=1       renew the window for the same agent (amq-remote share --renew)
#   BUZZ_SKIP_POLICY=1 do not retry an unpublished kind 30177 policy
#   BUZZ_NO_CLIPBOARD=1  do not put the body npub on the clipboard
#   BUZZ_UP_SETTLE     seconds to let the endpoint publish (default 8)
#   AMQ_SQUAD          amq-squad binary (default amq-squad on PATH)
set -euo pipefail
set +x # never trace: a trace would print the key command line

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <owner-npub> <relay-url>" >&2
  exit 2
fi
OWNER_NPUB=$1
RELAY=$2
BUZZ=${BUZZ_HOME:-$HOME/.amq-squad/buzz}
ROOT=$BUZZ/root
SESSION=${BUZZ_SESSION:-amq-squad}
NAME=${BUZZ_NAME:-amq-squad}
DAYS=${BUZZ_DAYS:-30}
TARGET=buzz-presence
AMQ_SQUAD=${AMQ_SQUAD:-amq-squad}
STATE=$ROOT/extensions/remote
umask 077
# No core dumps from this script or anything it starts: amq-owner-sign holds
# the owner key in memory while it signs and publishes, and a core file
# would be a copy of it (4-5 review item 1). RLIMIT_CORE 0 set inside
# amq-owner-sign itself is upstream (docs/v3-upstream-issues/16).
ulimit -c 0
# The share must live under ~/.amq-squad/buzz/, the tree guard protects
# from seat writes (the body key, the grants, the once-only DM marks).
case $BUZZ/ in *"/../"* | *"/./"* | *"//"*) echo "BUZZ_HOME must be a clean path" >&2; exit 2 ;; "$HOME/.amq-squad/buzz/"*) ;; *) echo "BUZZ_HOME must be ~/.amq-squad/buzz or a directory under it" >&2; exit 2 ;; esac
mkdir -p "$BUZZ" "$ROOT/attach"

case $OWNER_NPUB in npub1*) ;; *) echo "the owner key is given as its npub (npub1...), the public key only" >&2; exit 2 ;; esac
# The relay URL is parsed, not prefix-matched (4-5 re-pass): no userinfo,
# a host of plain host characters, wss:// required, and ws:// (no TLS) only
# for exactly localhost, 127.0.0.1 or ::1, the rule gateverify.CheckRelayURL
# applies in Go. ws://localhost.attacker.example/ and
# ws://127.0.0.1@relay.attacker.example/ are refused before amq-remote sees
# the URL.
relay_ok() {
  local u=$1 scheme rest hostport host tail re='^[][A-Za-z0-9.:-]+$' port='^:[0-9]+$'
  case $u in
    wss://*) scheme=wss rest=${u#wss://} ;;
    ws://*) scheme=ws rest=${u#ws://} ;;
    *) return 1 ;;
  esac
  hostport=${rest%%[/?#]*}
  [[ -n $hostport && $hostport != *@* && $hostport =~ $re ]] || return 1
  if [[ $hostport == \[* ]]; then
    host=${hostport#\[}
    [[ $host == *\]* ]] || return 1
    host=${host%%\]*}
    tail=${hostport#*\]}
  else
    host=${hostport%%:*}
    tail=${hostport#"$host"}
  fi
  [[ -n $host && ( -z $tail || $tail =~ $port ) ]] || return 1
  [[ $scheme == wss ]] && return 0
  case $host in localhost | 127.0.0.1 | ::1) return 0 ;; esac
  return 1
}
relay_ok "$RELAY" || { echo "relay url must be wss://HOST (ws:// only for localhost, 127.0.0.1 or ::1; no userinfo)" >&2; exit 2; }
read -r OWNER_HEX _ < <("$AMQ_SQUAD" remote npub "$OWNER_NPUB") || { echo "bad owner npub (or no $AMQ_SQUAD on PATH)" >&2; exit 2; }
echo "owner: $OWNER_NPUB ($OWNER_HEX)"
echo "relay: $RELAY"
echo "share: $ROOT session $SESSION, name \"$NAME\", $DAYS-day window"

# --- A: local -------------------------------------------------------------
if [[ ! -f $ROOT/meta/config.json ]]; then
  echo "== A: amq init (endpoint handle remote)"
  AMQ_NO_UPDATE_CHECK=1 amq init --root "$ROOT" --agents remote >/dev/null
fi
mkdir -p "$STATE"
if [[ ! -f $STATE/manifest.json ]]; then
  # The share needs one declared adapter target. amq-remote has no mailbox
  # adapter, so the target is a fake adapter that only carries presence: no
  # DM commands are bound to it (no --dm-channel), the attachment reads the
  # DMs itself (brief 4-5 log, deviation 1).
  cat >"$STATE/manifest.json" <<JSON
{"schema_version": 1, "layer": "remote", "adapters": [{"kind": "fake", "target": "$TARGET", "epoch": "amq-squad-buzz-1"}]}
JSON
fi
if [[ ${BUZZ_RENEW:-} == 1 && ! -f $BUZZ/renew.started ]]; then
  # A new window for the same body (same agent in Buzz): new preimages, one
  # owner signature, one enroll. The relay binding and presence stay.
  echo "== A: amq-remote share --renew (a new 30-day window for the same agent)"
  amq-remote share --root "$ROOT" --session "$SESSION" --renew --enable buzz-dm --enable buzz-profile >"$BUZZ/share.txt"
  rm -f "$BUZZ/bundle.json" "$BUZZ/policy.published"
  touch "$BUZZ/renew.started"
fi
if [[ ! -f $BUZZ/share.txt ]]; then
  echo "== A: amq-remote share --enable buzz-dm --enable buzz-profile --days $DAYS"
  amq-remote share --root "$ROOT" --session "$SESSION" --enable buzz-dm --enable buzz-profile --days "$DAYS" >"$BUZZ/share.txt"
fi
BODY=$(sed -n 's/^body-pubkey:[[:space:]]*\([0-9a-f]\{64\}\)$/\1/p' "$BUZZ/share.txt" | head -1)
[[ -n $BODY ]] || { echo "share.txt names no body" >&2; exit 1; }
echo "body:  $BODY (check this against what amq-owner-sign shows)"
echo "grants requested: $(grep -c '^kind [0-9]* preimage:' "$BUZZ/share.txt")"

# --- B: OWNER (the pipe) ----------------------------------------------------
# amq-owner-sign shows the session, the body and each kind, reads the body's
# first 8 characters from /dev/tty, then reads the key from stdin, which is
# the pipe (main.go: one line when stdin is not a terminal). It signs the
# grants, writes the bundle (public tags only, 0600), and publishes the
# owner's kind 30177 policy. Its stdout holds only public values; it is shown
# and checked for the policy line, never written to a file.
signer_output() {
  # Character by character, so the prompts (no newline) show as they come.
  # Before the key prompt everything passes (the key is not read yet); after
  # it only the signer's own lines pass, all public values.
  local c line="" after=0 dropped=0
  while IFS= read -r -n 1 -d '' c || [[ -n $c ]]; do
    [[ -z $c ]] && c=$'\n' # read -n 1 returns an empty c for a newline on some bash versions
    if [[ $after == 0 ]]; then
      printf '%s' "$c"
      line+=$c
      [[ $c == $'\n' ]] && line=""
      case $line in *"(hidden): ") after=1 line="" ;; esac
      continue
    fi
    line+=$c
    if [[ $c == $'\n' ]]; then
      case $dropped$line in
        "0published kind 30177 policy "*) printf '%s' "$line"; touch "$BUZZ/policy.published" ;;
        "0amq-owner-sign: "* | "0owner pubkey: "* | "0wrote "* | "0next: "*) printf '%s' "$line" ;;
        *) dropped=1 ;;
      esac
      line=""
    fi
  done
  [[ -n $line ]] && dropped=1
  if [[ $dropped == 1 ]]; then
    echo "   amq-owner-sign stopped abnormally; its crash trace is not shown (it may hold the owner key)."
  fi
  return 0
}
owner_sign() {
  if [[ -z ${BUZZ_OWNER_KEY_CMD:-} ]]; then
    echo "   OWNER STEP: set BUZZ_OWNER_KEY_CMD to your password manager's read command and rerun, e.g."
    echo "     BUZZ_OWNER_KEY_CMD='op read \"op://<vault>/<item>/<field>\"' $0 $OWNER_NPUB $RELAY"
    echo "   The key goes from that command straight into amq-owner-sign and nowhere else."
    exit 10
  fi
  echo "   OWNER: amq-owner-sign shows the body; check it is $BODY and type its first 8 characters."
  rm -f "$BUZZ/policy.published"
  # The key command's stdout is the signer's stdin and nothing else; its
  # stderr stays on the terminal (errors only). The signer's stdout (public
  # values: the summary, the owner pubkey, the bundle path, the policy id) is
  # shown line by line and checked for the policy line; it is not stored.
  #
  # The signer holds the key while it signs and publishes, so a crash must
  # leave no copy: ulimit -c 0 (above) and GOTRACEBACK=none mean no core
  # file even when the caller's environment asks for crash dumps. A Go
  # fatal signal still prints a stack and register trace on stderr whatever
  # GOTRACEBACK says, so the signer's stdout and stderr (one stream, in its
  # own order) go through signer_output: passed through as typed until the
  # key prompt, then only the signer's own lines (the owner pubkey, the
  # bundle, the policy id, its error lines); anything else (a crash trace)
  # is dropped and named.
  if ! bash -c "$BUZZ_OWNER_KEY_CMD" | GOTRACEBACK=none amq-owner-sign --share "$BUZZ/share.txt" --out "$BUZZ/bundle.json" --relay "$RELAY" --name "$NAME" 2>&1 | signer_output; then
    echo "   amq-owner-sign failed. If it said the key is not valid, check that BUZZ_OWNER_KEY_CMD prints the key and one newline only (no printf or echo wrapper)." >&2
    [[ -f $BUZZ/bundle.json ]] || exit 1
  fi
  if [[ ! -f $BUZZ/policy.published ]]; then
    echo "   (the kind 30177 policy was not published; the next run retries it, or set BUZZ_SKIP_POLICY=1)"
  fi
}
if [[ ! -f $BUZZ/bundle.json ]]; then
  echo "== B: the owner signs the grants (amq-owner-sign, key through the pipe)"
  owner_sign
elif [[ ! -f $BUZZ/policy.published && ${BUZZ_SKIP_POLICY:-} != 1 ]]; then
  echo "== B: retrying the kind 30177 policy (amq-owner-sign again; the pipe again)"
  owner_sign
fi
[[ -f $BUZZ/bundle.json ]] || { echo "no bundle written" >&2; exit 1; }
SIGNED=$(grep -o '"owner_pubkey": *"[0-9a-f]*"' "$BUZZ/bundle.json" | sed 's/.*"\([0-9a-f]*\)"$/\1/' | sort -u)
if [[ $(printf '%s\n' "$SIGNED" | wc -l | tr -d ' ') != 1 || $SIGNED != "$OWNER_HEX" ]]; then
  echo "the bundle is signed by $SIGNED, not the owner $OWNER_NPUB ($OWNER_HEX); nothing is enrolled" >&2
  exit 1
fi

# --- C: local enroll --------------------------------------------------------
if [[ ! -f $STATE/keys/$SESSION/share.json || $BUZZ/bundle.json -nt $STATE/keys/$SESSION/share.json ]]; then
  echo "== C: amq-remote share --bundle (enroll)"
  amq-remote share --root "$ROOT" --session "$SESSION" --bundle "$BUZZ/bundle.json"
fi
echo "grants enrolled: $(grep -o '"kind":[0-9]*' "$STATE/keys/$SESSION/share.json" | wc -l | tr -d ' ')"
rm -f "$BUZZ/renew.started"

# --- D: presence first, then the owner opens the DM ----------------------------
if ! grep -q '"presence"' "$STATE/manifest.json" 2>/dev/null; then
  echo "== D: amq-remote share --target --relay --presence \"$NAME\""
  amq-remote share --root "$ROOT" --session "$SESSION" --target "$TARGET" --relay "$RELAY" --presence "$NAME"
fi
up_running() { [[ -f $BUZZ/enroll-up.pid ]] && kill -0 "$(cat "$BUZZ/enroll-up.pid")" 2>/dev/null; }
CHANNEL=${BUZZ_DM_CHANNEL:-}
if [[ -z $CHANNEL && -f $BUZZ/dm-channel ]]; then CHANNEL=$(cat "$BUZZ/dm-channel"); fi
if [[ -z $CHANNEL ]]; then
  if ! up_running; then
    echo "== D: amq-remote up (background, publishes the profile and presence)"
    nohup amq-remote up --root "$ROOT" --registry "$ROOT/attach/keepalive-registry.json" </dev/null >>"$ROOT/attach/up.log" 2>&1 &
    echo $! >"$BUZZ/enroll-up.pid"
  fi
  sleep "${BUZZ_UP_SETTLE:-8}"
  amq-remote doctor --root "$ROOT" --json >"$BUZZ/doctor.json" 2>/dev/null || true
  grep -o '"presence": *"[a-z_]*"\|"discovery": *"[a-z_]*"\|"relay_auth": *"[a-z_]*"\|"state": *"[a-z_]*"' "$BUZZ/doctor.json" 2>/dev/null | sort -u | sed 's/^/     doctor: /' || true
  echo "== D: OWNER STEP (phone or Buzz Desktop): open a DM with the agent named \"$NAME\""
  echo "     and send the DM channel id back: rerun with BUZZ_DM_CHANNEL=<channel id>."
  BODY_NPUB=$("$AMQ_SQUAD" remote npub "$BODY" 2>/dev/null | awk '{print $2}' || true)
  if [[ -n $BODY_NPUB ]]; then
    echo "     agent npub: $BODY_NPUB"
    if command -v qrencode >/dev/null; then qrencode -t ANSIUTF8 "nostr:$BODY_NPUB"; fi
    if [[ ${BUZZ_NO_CLIPBOARD:-} != 1 ]] && command -v pbcopy >/dev/null; then printf '%s' "$BODY_NPUB" | pbcopy && echo "     (agent npub, a public key, copied to the clipboard)"; fi
  fi
  exit 11
fi
echo "$CHANNEL" >"$BUZZ/dm-channel"

# --- E: the gate binding ---------------------------------------------------
echo "== E: amq-squad remote bind"
"$AMQ_SQUAD" remote bind --relay "$RELAY" --channel "$CHANNEL" --share-root "$ROOT" --share-session "$SESSION"

# --- F: hand the endpoint to the attachment -------------------------------------
if up_running; then
  echo "== F: stopping the enrollment endpoint (amq-squad remote attach runs it from now on)"
  kill -TERM "$(cat "$BUZZ/enroll-up.pid")" 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do up_running || break; sleep 1; done
  date -u +%Y-%m-%dT%H:%M:%SZ >"$ROOT/attach/stopped-at" # the attachment's next start waits out the restart gap
fi
rm -f "$BUZZ/enroll-up.pid"
echo "enrolled. next: amq-squad remote attach --lead <lead handle> --project <dir> --session <session>"
