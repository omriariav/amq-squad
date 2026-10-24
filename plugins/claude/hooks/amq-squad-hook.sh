#!/bin/sh
# amq-squad 3.0 Claude Code hook adapter (plan 4.3, 4.4; R35, R36).
#
# Usage: amq-squad-hook.sh session-start|user-prompt|pre-tool|post-tool
#
# Outside a seat (AM_ME unset or empty) every event exits 0 with no output,
# before anything else runs. In a seat the three verbs do the work:
#   session-start, user-prompt  amq-squad context --me $AM_ME --json
#                               injected as additionalContext, every turn (R35)
#   pre-tool                    amq-squad guard --me $AM_ME --stdin, verdict
#                               mapped to the PreToolUse decision (below)
#   post-tool                   amq-squad evidence ingest --me $AM_ME
#
# Verdict mapping (guard exits 0 for every verdict):
#   allow       -> permissionDecision allow
#   deny        -> permissionDecision deny, reason = the guard's reason
#   needs_gate  -> permissionDecision deny, reason "raise the gate for
#                  <action_class> <target>"; never ask, an unattended seat
#                  must not stop on a prompt (2a-3 notes, open question 9)
#   guard exit != 0, unparsable output, missing binary -> no decision, exit 0
#                  (fail open, as Claude Code does), one line in the hook log.
#
# Every failure path exits 0: exit 2 from UserPromptSubmit would erase the
# prompt and exit 2 from PostToolUse would feed stderr to the model. The log is
# $AM_ROOT/extensions/amq-squad/hooklog/$AM_ME.log. Enforcement is not here:
# it stays in the Go core (harness-side commit, core merge); this is UX.

[ -n "${AM_ME:-}" ] || exit 0

ev="${1:-}"
bin="${AMQ_SQUAD_BIN:-amq-squad}"
logf=/dev/null
if [ -n "${AM_ROOT:-}" ]; then
	logdir="$AM_ROOT/extensions/amq-squad/hooklog"
	[ -d "$logdir" ] || (umask 077; mkdir -p "$logdir" 2>/dev/null)
	[ -d "$logdir" ] && [ -w "$logdir" ] && logf="$logdir/$AM_ME.log"
fi

log() {
	printf '%s %s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$ev" "$*" >>"$logf" 2>/dev/null
	return 0
}

command -v "$bin" >/dev/null 2>&1 || { log "fail-open: $bin not found on PATH"; exit 0; }

# json_escape turns stdin into the body of a JSON string.
json_escape() {
	awk 'BEGIN{ORS=""} { gsub(/\\/,"\\\\"); gsub(/"/,"\\\""); gsub(/\t/,"\\t"); gsub(/\r/,"\\r"); if (NR>1) print "\\n"; print }' | tr -d '\000-\010\013\014\016-\037'
}

case "$ev" in
session-start | user-prompt)
	if [ "$ev" = session-start ]; then name=SessionStart; else name=UserPromptSubmit; fi
	out=$("$bin" context --me "$AM_ME" --json 2>>"$logf")
	rc=$?
	if [ "$rc" -ne 0 ] || [ -z "$out" ]; then
		log "fail-open: context exit $rc"
		exit 0
	fi
	body=$(printf 'amq-squad seat context (role, rules, current task, open gates):\n%s\n' "$out" | json_escape)
	printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}\n' "$name" "$body"
	;;
pre-tool)
	out=$("$bin" guard --me "$AM_ME" --stdin 2>>"$logf")
	rc=$?
	if [ "$rc" -ne 0 ]; then
		log "fail-open: guard exit $rc"
		exit 0
	fi
	verdict=$(printf '%s\n' "$out" | sed -n -E 's/^\{"verdict":"([a-z_]+)".*/\1/p')
	str='(([^"\\]|\\.)*)'
	case "$verdict" in
	allow)
		printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"amq-squad guard: allowed"}}\n'
		;;
	deny)
		reason=$(printf '%s\n' "$out" | sed -n -E "s/.*,\"reason\":\"$str\".*/\\1/p")
		printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"amq-squad guard: %s"}}\n' "$reason"
		;;
	needs_gate)
		class=$(printf '%s\n' "$out" | sed -n -E 's/.*,"action_class":"([^"]*)".*/\1/p')
		target=$(printf '%s\n' "$out" | sed -n -E "s/.*,\"target\":\"$str\".*/\\1/p")
		printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"raise the gate for %s %s"}}\n' "$class" "$target"
		;;
	*)
		log "fail-open: unparsable guard output"
		;;
	esac
	;;
post-tool)
	"$bin" evidence ingest --me "$AM_ME" >/dev/null 2>>"$logf"
	rc=$?
	[ "$rc" -eq 0 ] || log "fail-open: evidence ingest exit $rc"
	;;
*)
	log "unknown event '$ev'"
	;;
esac
exit 0
