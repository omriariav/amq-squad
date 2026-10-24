#!/bin/bash
# Live part 2: R19 x2 and R12 N=5 (probe role) on the commit-6 binary.
B=$JOB
cd ~/code/amq-squad/.claude/worktrees/agent-a4c9efee2491e72e4
date -u +%FT%TZ
for i in 1 2; do
  d=$B/live2/r19-$i; rm -rf $d; mkdir -p $d
  $B/sched-live2 -mode r19 -dir $d -bin $B/amq-squad-2 -home $B/home -label p2-r19-$i -rounds 40 > $d.out 2>&1
  echo "r19 run $i exit $?"
done
for i in 1 2 3 4 5; do
  d=$B/live2/r12-$i; rm -rf $d; mkdir -p $d
  $B/sched-live2 -mode r12 -probe-role -dir $d -bin $B/amq-squad-2 -home $B/home -label p2-r12-$i -rounds 40 > $d.out 2>&1
  echo "r12 run $i exit $?"
done
date -u +%FT%TZ
echo ALL-DONE
