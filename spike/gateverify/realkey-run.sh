#!/usr/bin/env bash
# realkey-run.sh <owner-npub> <relay-url> <gate-id>
#
# The S1 real-key run, end to end, once the owner provides the Buzz npub and
# the relay URL. The last line is PASS, NOT PROVEN or FAIL.
#
#   1 enroll    enroll.sh. Owner steps: amq-owner-sign at a terminal, then
#               open the DM with the body in Buzz and give its channel id.
#   2 endpoint  wait until doctor shows relay_auth authenticated and the DM
#               surface subscription_active
#   3 render    the Go core renders the gate and writes the record, once,
#               into the gate store under the AMQ root, BEFORE the question
#               is sent. From here on the record is the input: nothing
#               downstream takes a digest or a record from a caller.
#   4 post      post the record's text into the DM through the share
#               (interim: signed by the share body key with its kind 9
#               grant); the posted question is stored once beside the record
#   5 answer    OWNER, phone: reply "approve <gate-id> merge <prefix>" in the
#               question's thread
#   6 resolve   event id from amq-remote serve's ingress ledger (primary);
#               candidates that do not verify are skipped and logged. A relay
#               watch (with reconnects) runs as fallback; an id found only by
#               the watch ends the run NOT PROVEN, because delivery by serve
#               is not proven.
#   7 fetch     REQ by id, signed in with the share body key (interim)
#   8 verify    against the stored record and question: BIP-340 under the
#               pinned npub, kind 9, DM channel, one-line tuple, window,
#               thread binding (on by default). Then the envelope is written
#               (fsynced), and only then is the gate marked consumed.
#   9 envelope  envelope-verify; real-event negative checks
#
# Environment: S1_ROOT S1_SESSION (see enroll.sh); S1_DM_CHANNEL;
#   S1_ANSWER_TIMEOUT (default 30m, the gate window); S1_REQUIRE_THREAD=0 to
#   accept an unthreaded answer (default 1); S1_GATE_SPEC to use a real batch
#   spec (default: a synthetic two-PR batch); S1_KEEP_UP=1 to leave the
#   endpoint running afterwards.
set -uo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <owner-npub> <relay-url> <gate-id>" >&2
  exit 2
fi
OWNER_NPUB=$1
RELAY=$2
GATE_ID=$3
HERE=$(cd "$(dirname "$0")" && pwd)
S1=${S1_HOME:-$HOME/.amq-squad/spike/s1}
GV=${GV:-$S1/gateverify}
RUN=$S1/realkey/$GATE_ID
mkdir -p "$RUN"
LOG=$RUN/run.log
exec > >(tee -a "$LOG") 2>&1

finish() { # verdict detail
  echo "RESULT $1 gate=$GATE_ID ${2:-}"
  if [[ ${S1_KEEP_UP:-} != 1 ]]; then "$HERE/teardown.sh" --keep-keys >/dev/null 2>&1 || true; fi
  echo "$1"
  case $1 in PASS) exit 0 ;; "NOT PROVEN") exit 4 ;; *) exit 1 ;; esac
}
now_ms() { python3 -I -c 'import time; print(int(time.time()*1000))'; }
json_field() { python3 -I -c 'import json,sys; print(json.loads(sys.stdin.read() or "{}").get(sys.argv[1],""))' "$1"; }

(cd "$HERE" && go build -o "$GV" .) || finish FAIL "build"

# 1 enroll ------------------------------------------------------------------
"$HERE/enroll.sh" "$OWNER_NPUB" "$RELAY"
rc=$?
if [[ $rc -eq 10 || $rc -eq 11 ]]; then
  echo "owner step pending (enroll exit $rc); finish it and rerun this script"
  exit $rc
fi
[[ $rc -eq 0 ]] || finish FAIL "enroll exit $rc"
# shellcheck disable=SC1091
source "$S1/s1.env"
CHANNEL=$S1_DM_CHANNEL
ROOT=$S1_ROOT
SHARE=(--share-root "$ROOT" --share-session "$S1_SESSION")
PIN=(--owner "$OWNER_NPUB" --channel "$CHANNEL" --body "$S1_BODY")
GATE=(--root "$ROOT" --gate-id "$GATE_ID")
THREAD=(--require-thread=true)
[[ ${S1_REQUIRE_THREAD:-1} == 0 ]] && THREAD=(--require-thread=false)

# 2 endpoint ----------------------------------------------------------------
echo "== 2: waiting for relay_auth authenticated and the DM surface"
ok=0
for _ in $(seq 1 30); do
  amq-remote doctor --root "$ROOT" --json >"$RUN/doctor.json" 2>/dev/null
  if python3 -I - "$RUN/doctor.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
bad=[f for f in (d.get("failing") or []) if (f.get("boundary") or f.get("subject")) in ("relay_auth","dm_surface","body_key","tag_expiry")]
sys.exit(1 if bad else 0)
PY
  then ok=1; break; fi
  sleep 3
done
[[ $ok -eq 1 ]] || finish FAIL "endpoint not authenticated or DM surface closed (see $RUN/doctor.json)"

# 3 render: the record is stored before the question is sent ------------------
SPEC=${S1_GATE_SPEC:-$RUN/spec.json}
if [[ -z ${S1_GATE_SPEC:-} ]]; then
  cat >"$SPEC" <<JSON
{"gate_id": "$GATE_ID", "class": "merge", "repo": "synthetic/s1-spike",
 "base_sha": "1111111111111111111111111111111111111111",
 "prs": [{"number": 101, "head_sha": "2222222222222222222222222222222222222222"},
         {"number": 102, "head_sha": "3333333333333333333333333333333333333333"}],
 "window_minutes": 30}
JSON
fi
"$GV" render --root "$ROOT" --spec "$SPEC" >"$RUN/gate.txt" || finish FAIL "render or store (a record for $GATE_ID may already exist: use a new gate id)"
echo "== 3: gate record stored under $ROOT/extensions/amq-squad/gates/; text:"
cat "$RUN/gate.txt"
if [[ ! -f $S1/e2e/issuer.pem ]]; then
  mkdir -p "$S1/e2e"
  "$GV" issuer-keygen --key "$S1/e2e/issuer.pem" --store "$S1/e2e/trust.json" >/dev/null || finish FAIL "issuer key"
fi

# 4 post --------------------------------------------------------------------
T_POST=$(now_ms)
QID=$("$GV" post-gate --relay "$RELAY" "${GATE[@]}" "${PIN[@]}" "${SHARE[@]}") || finish FAIL "post gate"
echo "== 4: gate question posted as event $QID"

# 5 answer (owner) ----------------------------------------------------------
echo "== 5: OWNER (phone): in the Buzz DM, reply in the gate's thread with exactly:"
grep '^approve ' "$RUN/gate.txt" | sed 's/^/       /'

# 6 resolve -----------------------------------------------------------------
STORE=$ROOT/extensions/amq-squad/gates
SINCE=$(python3 -I -c 'import json,sys,datetime; print(int(datetime.datetime.fromisoformat(json.load(open(sys.argv[1]))["not_before"].replace("Z","+00:00")).timestamp()))' "$STORE/$GATE_ID.json")
TIMEOUT=${S1_ANSWER_TIMEOUT:-30m}
# The watch reconnects by itself (WatchWithRetry); it only exits on success
# or at the deadline.
"$GV" watch --relay "$RELAY" "${GATE[@]}" "${PIN[@]}" "${SHARE[@]}" --timeout "$TIMEOUT" >"$RUN/watch.id" 2>"$RUN/watch.err" &
WATCH=$!
EID=""
VIA=""
WATCH_ID=""
grace=0
SEEN=" "
deadline=$(($(date +%s) + $(python3 -I -c 'import sys; s=sys.argv[1]; print(int(s[:-1])*{"s":1,"m":60,"h":3600}[s[-1]])' "$TIMEOUT")))
while [[ -z $EID && $(date +%s) -lt $deadline ]]; do
  ids=$("$GV" resolve --mode ingress --root "$ROOT" --session "$S1_SESSION" --owner "$OWNER_NPUB" --channel "$CHANNEL" --since "$SINCE" 2>>"$RUN/resolve.err")
  for id in $ids; do
    [[ $SEEN == *" $id "* ]] && continue
    if ! "$GV" fetch --relay "$RELAY" --id "$id" --owner "$OWNER_NPUB" "${SHARE[@]}" --out "$RUN/candidate.json" 2>>"$RUN/resolve.err"; then
      echo "   skip candidate $id: fetch failed (retried next round)"
      continue
    fi
    SEEN="$SEEN$id "
    out=$("$GV" verify --no-ledger --event "$RUN/candidate.json" "${GATE[@]}" "${PIN[@]}" "${THREAD[@]}")
    case $(json_field result <<<"$out") in
      approve | deny) EID=$id; VIA="serve ingress ledger"; break ;;
      *) echo "   skip candidate $id: $(json_field code <<<"$out")" ;;
    esac
  done
  [[ -n $EID ]] && break
  if [[ -z $WATCH_ID ]] && ! kill -0 $WATCH 2>/dev/null; then WATCH_ID=$(cat "$RUN/watch.id" 2>/dev/null); fi
  if [[ -n $WATCH_ID ]]; then
    grace=$((grace + 3))
    if [[ $grace -ge 15 ]]; then EID=$WATCH_ID; VIA="relay watch"; break; fi
  fi
  sleep 3
done
kill $WATCH 2>/dev/null
[[ -n $EID ]] || finish FAIL "no owner answer within $TIMEOUT ($(tail -1 "$RUN/watch.err" 2>/dev/null))"
echo "== 6: answer event $EID (resolved via $VIA)"

# 7 fetch -------------------------------------------------------------------
"$GV" fetch --relay "$RELAY" --id "$EID" --owner "$OWNER_NPUB" "${SHARE[@]}" --out "$RUN/event.json" || finish FAIL "fetch $EID"

# 8 verify: envelope written first, then the gate is consumed ---------------
"$GV" verify --event "$RUN/event.json" "${GATE[@]}" "${PIN[@]}" "${THREAD[@]}" \
  --issuer-key "$S1/e2e/issuer.pem" --relay "$RELAY" --project-dir "$ROOT" --session "$S1_SESSION" >"$RUN/verify.json"
vrc=$?
cat "$RUN/verify.json"
T_DONE=$(now_ms)
case $vrc in
  0) ;;
  3) finish FAIL "the owner answered deny (verified deny; rerun with a new gate id to test approve)" ;;
  *) finish FAIL "verify rejected the answer" ;;
esac
ANSWER=$ROOT/extensions/amq-squad/answers/$GATE_ID.json

# 9 envelope + real-event negatives ----------------------------------------
"$GV" envelope-verify --answer "$ANSWER" --store "$S1/e2e/trust.json" "${PIN[@]}" || finish FAIL "envelope-verify"
neg_ok=1
# Capture first: verify exits 1 on a rejection, which pipefail would report.
out=$("$GV" verify --event "$RUN/event.json" "${GATE[@]}" "${PIN[@]}" "${THREAD[@]}")
[[ $(json_field code <<<"$out") == already_consumed ]] && echo "   replay of the real event: already_consumed" || neg_ok=0
sed "s/\"gate_id\": \"$GATE_ID\"/\"gate_id\": \"$GATE_ID-x\"/" "$SPEC" >"$RUN/spec-other.json"
"$GV" render --root "$ROOT" --spec "$RUN/spec-other.json" >/dev/null
out=$("$GV" verify --no-ledger --event "$RUN/event.json" --root "$ROOT" --gate-id "$GATE_ID-x" "${PIN[@]}" --require-thread=false)
[[ $(json_field code <<<"$out") == gate_mismatch ]] && echo "   real event against another stored gate: gate_mismatch" || neg_ok=0
out=$("$GV" verify --no-ledger --event "$RUN/event.json" "${GATE[@]}" --owner "$S1_BODY" --channel "$CHANNEL" --require-thread=false)
[[ $(json_field code <<<"$out") == wrong_pubkey ]] && echo "   real event under the body key as pin: wrong_pubkey" || neg_ok=0
[[ $neg_ok -eq 1 ]] || finish FAIL "a real-event negative check did not reject"
python3 -I - "$RUN/event.json" "$QID" <<'PY'
import json,sys
e=json.load(open(sys.argv[1]))
threaded=any(t[:2]==["e",sys.argv[2]] for t in e["tags"])
print(f"   owner event kind {e['kind']} pubkey {e['pubkey'][:16]}… created_at {e['created_at']} threaded={threaded}")
PY
echo "== timing: gate posted -> envelope verified $((T_DONE - T_POST)) ms (includes the owner reading and typing)"
if [[ $VIA == "relay watch" ]]; then
  finish "NOT PROVEN" "delivery by serve not proven: the answer verified, but serve's ingress ledger never claimed it (event=$EID answer=$ANSWER)"
fi
finish PASS "event=$EID via=\"$VIA\" answer=$ANSWER"
