#!/bin/sh
# The squad's own lead onto the rc.2 tag build (the notes' step 2), with the
# owner's word of 2026-11-11 15:52 IST. SIGTERM only; never SIGKILL.
V=/Users/omri/.claude/jobs/6dd424c9/tmp/v320rc2
B=/Users/omri/scratch-v320rc2/build/pi/amq-squad
R=/Users/omri/code/amq-squad/.agent-mail/v31/extensions/amq-squad
LEAD=26009
SCHED=26011
cd /Users/omri/code/amq-squad || exit 1
{
date -u +"pre %Y-%m-%dT%H:%M:%SZ"
echo "build: $($B version); installed: $(/Users/omri/go/bin/amq-squad version)"
echo "--- processes with a cwd in a seat worktree (the standing lead check)"
pids=$(lsof -a -d cwd -Fpn | awk '/^p/{p=substr($0,2)} /^n\/Users\/omri\/code\/amq-squad\/.worktrees\//{print p}' | sort -u | tr '\n' ',' | sed 's/,$//')
[ -n "$pids" ] && ps -o pid,ppid,etime,command -p $pids | cut -c1-140
echo "--- status"
/Users/omri/go/bin/amq-squad status --session v31 | head -1
echo "--- ledgers before (names, sizes, times; no content)"
ls -la $R/commits $R/ledger $R/scheduler/exits.json
wc -l $R/scheduler/decisions.jsonl $R/routing/decisions.jsonl
echo "--- the lead and its scheduler"
ps -o pid,ppid,lstart,command -p $LEAD,$SCHED | cut -c1-150
} > $V/restart-pre.txt 2>&1
c=$(ps -o command= -p $LEAD)
case "$c" in
  "/Users/omri/go/bin/amq-squad lead --team"*) ;;
  *) echo "pid $LEAD is not the lead: $c" | tee -a $V/restart-pre.txt; exit 1 ;;
esac
{
date -u +"SIGTERM to $LEAD at %Y-%m-%dT%H:%M:%SZ"
kill -TERM $LEAD
i=0
while [ $i -lt 30 ] && { kill -0 $LEAD 2>/dev/null || kill -0 $SCHED 2>/dev/null; }; do sleep 1; i=$((i+1)); done
date -u +"waited ${i}s, now %Y-%m-%dT%H:%M:%SZ"
if kill -0 $LEAD 2>/dev/null || kill -0 $SCHED 2>/dev/null; then
  echo "still alive after 30 s; stopping here, no SIGKILL, no up"
  ps -o pid,ppid,etime,command -p $LEAD,$SCHED | cut -c1-150
  exit 1
fi
date -u +"up start %Y-%m-%dT%H:%M:%SZ"
} > $V/restart-run.txt 2>&1 || { cat $V/restart-run.txt; exit 1; }
$B up --binary $B --json > $V/up-320rc2.json 2> $V/up-320rc2.err &
UP=$!
sleep 12
{
date -u +"capture 1 %Y-%m-%dT%H:%M:%SZ"
tmux list-panes -a -F '#{pane_id} #{session_name} pid=#{pane_pid} cmd=#{pane_current_command} dead=#{pane_dead}' | grep squad-lead
} >> $V/restart-run.txt 2>&1
tmux capture-pane -p -t squad-lead-amq-squad-f42a4 > $V/lead-first-screen.txt 2>> $V/restart-run.txt
sleep 18
{
date -u +"capture 2 %Y-%m-%dT%H:%M:%SZ"
tmux list-panes -a -F '#{pane_id} #{session_name} pid=#{pane_pid} cmd=#{pane_current_command} dead=#{pane_dead}' | grep squad-lead
} >> $V/restart-run.txt 2>&1
tmux capture-pane -p -t squad-lead-amq-squad-f42a4 > $V/lead-screen-30s.txt 2>> $V/restart-run.txt
wait $UP
{
echo "up exit $?"
date -u +"up done %Y-%m-%dT%H:%M:%SZ"
echo "installed now: $(/Users/omri/go/bin/amq-squad version)"
ps -axo pid,ppid,lstart,command | grep -E 'amq-squad (lead --team|scheduler run)' | grep -v grep | cut -c1-150
} >> $V/restart-run.txt 2>&1
cat $V/restart-pre.txt $V/restart-run.txt
