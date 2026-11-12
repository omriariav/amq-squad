#!/bin/sh
# RELEASING.md step 6, the scratch-repository route: the tag build's lead in
# an empty git repository with a scratch HOME, its first screen at about 10 s.
V=/Users/omri/.claude/jobs/6dd424c9/tmp/v320rc2
B=/Users/omri/scratch-v320rc2/build/pi/amq-squad
S=$(mktemp -d /private/tmp/rc2lead.XXXXXX)
echo "$S" > $V/scratch-lead-root.txt
mkdir -p "$S/home" "$S/proj"
/usr/bin/git -C "$S/proj" init -q
printf 'set -g remain-on-exit on\n' > "$S/tmux.conf"
L=rc2lead$$
{
date -u +"start %Y-%m-%dT%H:%M:%SZ"
echo "binary: $B ($($B version))"
tmux -L $L -f "$S/tmux.conf" new-session -d -s l -x 100 -y 30 -c "$S/proj" \
  "env -i HOME=$S/home PATH=/usr/bin:/bin:/usr/sbin:/sbin TERM=xterm-256color GIT_CEILING_DIRECTORIES=$S $B lead --team -- --offline"
sleep 3
tmux -L $L list-panes -t l -F 'at 3 s: dead=#{pane_dead} status=#{pane_dead_status} cmd=#{pane_current_command}'
sleep 7
date -u +"capture %Y-%m-%dT%H:%M:%SZ"
tmux -L $L list-panes -t l -F 'at 10 s: dead=#{pane_dead} status=#{pane_dead_status} cmd=#{pane_current_command} pid=#{pane_pid}'
} > $V/scratch-lead-run.txt 2>&1
tmux -L $L capture-pane -p -t l > $V/lead-first-screen-scratch.txt 2>> $V/scratch-lead-run.txt
ps -axo pid,ppid,command | grep "$S" | grep -v grep >> $V/scratch-lead-run.txt
tmux -L $L kill-server >> $V/scratch-lead-run.txt 2>&1
sleep 2
echo "after kill-server, processes naming the scratch root:" >> $V/scratch-lead-run.txt
ps -axo pid,ppid,command | grep "$S" | grep -v grep >> $V/scratch-lead-run.txt
date -u +"end %Y-%m-%dT%H:%M:%SZ" >> $V/scratch-lead-run.txt
cat $V/scratch-lead-run.txt
