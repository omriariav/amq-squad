#!/usr/bin/env bash
# realkey-run.sh <owner-npub> <relay-url> <gate-id>
#
# The S1 real-key run, end to end, once the owner provides the Buzz npub and
# the relay URL. Prints PASS or FAIL on the last line.
#
#   1 enroll    enroll.sh (owner steps: amq-owner-sign in a terminal, then
#               open the DM with the body in Buzz and give its channel id)
#   2 endpoint  wait until doctor shows relay_auth authenticated and the DM
#               surface subscription_active
#   3 render    the Go core renders the gate (ids, class, digest, PR numbers)
#   4 post      post the gate text into the DM through the share
#               (interim: signed by the share body key with its kind 9 grant)
#   5 answer    OWNER, phone: reply "approve <gate-id> merge <prefix>" in Buzz
#   6 resolve   event id from amq-remote serve's ingress ledger (the journal
#               of owner DM events), falling back to a relay watch
#   7 fetch     REQ by id, signed in with the share body key (interim)
#   8 verify    BIP-340 under the pinned npub, kind 9, DM channel, tuple,
#               window; single-use ledger
#   9 envelope  operatorauth envelope (core Ed25519 issuer) + bip340 evidence,
#               then envelope-verify; real-event negative checks
#
# Environment: S1_ROOT S1_SESSION (see enroll.sh); S1_DM_CHANNEL;
#   S1_ANSWER_TIMEOUT (default 15m); S1_REQUIRE_THREAD=1 to demand a thread
#   reply; S1_GATE_SPEC to use a real batch spec (default: a synthetic
#   two-PR batch); S1_KEEP_UP=1 to leave the endpoint running afterwards.
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

result() {
  echo "RESULT $1 gate=$GATE_ID ${2:-}"
  if [[ ${S1_KEEP_UP:-} != 1 ]]; then "$HERE/teardown.sh" --keep-keys >/dev/null 2>&1 || true; fi
  [[ $1 == PASS ]] && echo PASS || echo FAIL
  [[ $1 == PASS ]]
  exit $?
}
now_ms() { python3 -I -c 'import time; print(int(time.time()*1000))'; }

(cd "$HERE" && go build -o "$GV" .) || result FAIL "build"

# 1 enroll ------------------------------------------------------------------
"$HERE/enroll.sh" "$OWNER_NPUB" "$RELAY"
rc=$?
if [[ $rc -eq 10 || $rc -eq 11 ]]; then
  echo "owner step pending (enroll exit $rc); finish it and rerun this script"
  exit $rc
fi
[[ $rc -eq 0 ]] || result FAIL "enroll exit $rc"
# shellcheck disable=SC1091
source "$S1/s1.env"
CHANNEL=$S1_DM_CHANNEL
SHARE=(--share-root "$S1_ROOT" --share-session "$S1_SESSION")
PIN=(--owner "$OWNER_NPUB" --channel "$CHANNEL")

# 2 endpoint ----------------------------------------------------------------
echo "== 2: waiting for relay_auth authenticated and the DM surface"
ok=0
for _ in $(seq 1 30); do
  amq-remote doctor --root "$S1_ROOT" --json >"$RUN/doctor.json" 2>/dev/null
  if python3 -I - "$RUN/doctor.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
bad=[f for f in (d.get("failing") or []) if (f.get("boundary") or f.get("subject")) in ("relay_auth","dm_surface","body_key","tag_expiry")]
sys.exit(1 if bad else 0)
PY
  then ok=1; break; fi
  sleep 3
done
[[ $ok -eq 1 ]] || result FAIL "endpoint not authenticated or DM surface closed (see $RUN/doctor.json)"

# 3 render --------------------------------------------------------------------
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
"$GV" render --spec "$SPEC" --out "$RUN/gate.json" >"$RUN/gate.txt" || result FAIL "render"
echo "== 3: gate text"
cat "$RUN/gate.txt"
if [[ ! -f $S1/e2e/issuer.pem ]]; then
  mkdir -p "$S1/e2e"
  "$GV" issuer-keygen --key "$S1/e2e/issuer.pem" --store "$S1/e2e/trust.json" >/dev/null || result FAIL "issuer key"
fi

# 4 post --------------------------------------------------------------------
T_POST=$(now_ms)
QID=$("$GV" post-gate --relay "$RELAY" --gate "$RUN/gate.json" "${PIN[@]}" "${SHARE[@]}") || result FAIL "post gate"
echo "== 4: gate question posted as event $QID"

# 5 answer (owner) ----------------------------------------------------------
PREFIX=$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["digest"][7:23])' "$RUN/gate.json")
echo "== 5: OWNER (phone): in the Buzz DM, reply in the gate's thread with exactly:"
echo "       approve $GATE_ID merge $PREFIX"

# 6 resolve -----------------------------------------------------------------
SINCE=$(python3 -I -c 'import json,sys,datetime; print(int(datetime.datetime.fromisoformat(json.load(open(sys.argv[1]))["not_before"].replace("Z","+00:00")).timestamp()))' "$RUN/gate.json")
TIMEOUT=${S1_ANSWER_TIMEOUT:-15m}
"$GV" watch --relay "$RELAY" --gate "$RUN/gate.json" "${PIN[@]}" "${SHARE[@]}" --timeout "$TIMEOUT" >"$RUN/watch.id" 2>"$RUN/watch.err" &
WATCH=$!
EID=""
VIA=""
deadline=$(( $(date +%s) + $(python3 -I -c 'import sys; s=sys.argv[1]; print(int(s[:-1])*{"s":1,"m":60,"h":3600}[s[-1]])' "$TIMEOUT") ))
WATCH_ID=""
grace=0
while [[ -z $EID && $(date +%s) -lt $deadline ]]; do
  # Primary: the journal amq-remote serve keeps of owner DM events.
  for id in $("$GV" resolve --mode ingress --root "$S1_ROOT" --session "$S1_SESSION" --owner "$OWNER_NPUB" --channel "$CHANNEL" --since "$SINCE" 2>/dev/null); do
    "$GV" fetch --relay "$RELAY" --id "$id" --owner "$OWNER_NPUB" "${SHARE[@]}" --out "$RUN/candidate.json" 2>/dev/null || continue
    if grep -q "$GATE_ID" "$RUN/candidate.json"; then EID=$id; VIA="serve ingress ledger"; break; fi
  done
  [[ -n $EID ]] && break
  # Fallback: the relay watch; give serve 15 s more to claim the same event.
  if [[ -z $WATCH_ID ]] && ! kill -0 $WATCH 2>/dev/null; then WATCH_ID=$(cat "$RUN/watch.id" 2>/dev/null); fi
  if [[ -n $WATCH_ID ]]; then
    grace=$((grace + 3))
    if [[ $grace -ge 15 ]]; then EID=$WATCH_ID; VIA="relay watch (serve claimed no ingress)"; break; fi
  fi
  sleep 3
done
if [[ -n $WATCH_ID && $VIA == "serve ingress ledger" ]]; then
  [[ $WATCH_ID == "$EID" ]] && VIA="$VIA; relay watch agrees" || VIA="$VIA; relay watch saw $WATCH_ID"
fi
kill $WATCH 2>/dev/null
[[ -n $EID ]] || result FAIL "no owner answer within $TIMEOUT ($(cat "$RUN/watch.err" 2>/dev/null))"
echo "== 6: answer event $EID (resolved via $VIA)"

# 7 fetch -------------------------------------------------------------------
"$GV" fetch --relay "$RELAY" --id "$EID" --owner "$OWNER_NPUB" "${SHARE[@]}" --out "$RUN/event.json" || result FAIL "fetch $EID"

# 8 verify ------------------------------------------------------------------
THREAD=()
[[ ${S1_REQUIRE_THREAD:-} == 1 ]] && THREAD=(--require-thread)
"$GV" verify --event "$RUN/event.json" --gate "$RUN/gate.json" "${PIN[@]}" "${THREAD[@]}" \
  --ledger "$RUN/ledger" --issuer-key "$S1/e2e/issuer.pem" --out "$RUN/answer.json" --relay "$RELAY" \
  --project-dir "$S1_ROOT" --session "$S1_SESSION" >"$RUN/verify.json"
vrc=$?
cat "$RUN/verify.json"
T_DONE=$(now_ms)
case $vrc in
  0) ;;
  3) result FAIL "the owner answered deny (verified deny; rerun with a new gate id to test approve)" ;;
  *) result FAIL "verify rejected the answer" ;;
esac

# 9 envelope + real-event negatives ----------------------------------------
"$GV" envelope-verify --answer "$RUN/answer.json" --store "$S1/e2e/trust.json" "${PIN[@]}" || result FAIL "envelope-verify"
neg_ok=1
# Capture first: verify exits 1 on a rejection, which pipefail would report.
out=$("$GV" verify --event "$RUN/event.json" --gate "$RUN/gate.json" "${PIN[@]}" --ledger "$RUN/ledger")
grep -q '"code":"already_consumed"' <<<"$out" && echo "   replay of the real event: already_consumed" || neg_ok=0
sed "s/\"gate_id\": \"$GATE_ID\"/\"gate_id\": \"$GATE_ID-x\"/" "$SPEC" >"$RUN/spec-other.json"
"$GV" render --spec "$RUN/spec-other.json" --out "$RUN/gate-other.json" >/dev/null
out=$("$GV" verify --event "$RUN/event.json" --gate "$RUN/gate-other.json" "${PIN[@]}")
grep -q '"code":"gate_mismatch"' <<<"$out" && echo "   real event against another gate id: gate_mismatch" || neg_ok=0
out=$("$GV" verify --event "$RUN/event.json" --gate "$RUN/gate.json" --owner "$S1_BODY" --channel "$CHANNEL")
grep -q '"code":"wrong_pubkey"' <<<"$out" && echo "   real event under the body key as pin: wrong_pubkey" || neg_ok=0
[[ $neg_ok -eq 1 ]] || result FAIL "a real-event negative check did not reject"
python3 -I - "$RUN/event.json" "$QID" <<'PY'
import json,sys
e=json.load(open(sys.argv[1]))
threaded=any(t[:2]==["e",sys.argv[2]] for t in e["tags"])
print(f"   owner event kind {e['kind']} pubkey {e['pubkey'][:16]}… created_at {e['created_at']} threaded={threaded}")
PY
echo "== timing: gate posted -> envelope verified $(( T_DONE - T_POST )) ms (includes the owner reading and typing)"
result PASS "event=$EID via=\"$VIA\" answer=$RUN/answer.json"
