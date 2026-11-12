#!/bin/sh
# RELEASING.md step 6, the scratch-repository route, as corrected in 3.2.0: the tag
# build's lead in an empty git repository after `amq coop init`, with a scratch HOME
# and its own tmux socket; its first screen at about 10 s. rc.2's scratch-lead.sh with
# the init step, the host amq on PATH (it is run, never written), and the decision log kept.
V=<home>/amq-squad-lead/T/v320
B=<home>/scratch-v320/build/pi/amq-squad
S=$(mktemp -d /private/tmp/v320lead.XXXXXX)
echo "$S" > $V/scratch-lead-root.txt
mkdir -p "$S/home" "$S/proj"
/usr/bin/git -C "$S/proj" init -q
printf 'set -g remain-on-exit on\n' > "$S/tmux.conf"
L=v320lead$$
E="HOME=$S/home PATH=<home>/go/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color GIT_CEILING_DIRECTORIES=$S AMQ_NO_UPDATE_CHECK=1"
{
date -u +"start %Y-%m-%dT%H:%M:%SZ"
echo "binary: $B ($($B version))"
echo "amq: $(env -i $E amq version 2>&1 | head -1) at $(env -i $E /usr/bin/which amq)"
(cd "$S/proj" && env -i $E perl -e 'alarm 60; exec @ARGV' amq coop init < /dev/null > $V/scratch-coop-init.txt 2>&1; x=$?; if [ $x -eq 142 ]; then echo "amq coop init killed at 60 s"; else echo "amq coop init exit=$x"; fi)
echo ".amqrc present: $([ -f "$S/proj/.amqrc" ] && echo yes || echo no); .agent-mail present: $([ -d "$S/proj/.agent-mail" ] && echo yes || echo no)"
tmux -L $L -f "$S/tmux.conf" new-session -d -s l -x 100 -y 30 -c "$S/proj" \
  "env -i $E $B lead --team -- --offline"
sleep 3
tmux -L $L list-panes -t l -F 'at 3 s: dead=#{pane_dead} status=#{pane_dead_status} cmd=#{pane_current_command}'
sleep 7
date -u +"capture %Y-%m-%dT%H:%M:%SZ"
tmux -L $L list-panes -t l -F 'at 10 s: dead=#{pane_dead} status=#{pane_dead_status} cmd=#{pane_current_command} pid=#{pane_pid}'
} > $V/scratch-lead-run.txt 2>&1
tmux -L $L capture-pane -p -t l > $V/lead-first-screen-scratch.txt 2>> $V/scratch-lead-run.txt
find "$S/proj/.agent-mail" -name decisions.jsonl -path '*scheduler*' -exec cp {} $V/scratch-decisions.jsonl \; 2>> $V/scratch-lead-run.txt
ps -axo pid,ppid,command | grep "$S" | grep -v grep >> $V/scratch-lead-run.txt
tmux -L $L kill-server >> $V/scratch-lead-run.txt 2>&1
sleep 2
echo "after kill-server, processes naming the scratch root:" >> $V/scratch-lead-run.txt
ps -axo pid,ppid,command | grep "$S" | grep -v grep >> $V/scratch-lead-run.txt
date -u +"end %Y-%m-%dT%H:%M:%SZ" >> $V/scratch-lead-run.txt
cat $V/scratch-lead-run.txt
