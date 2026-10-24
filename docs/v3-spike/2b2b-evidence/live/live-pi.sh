#!/bin/bash
# R12 N=5 then R19 x2 through the scheduler process (brief 2b-2 milestone B live session).
B=$JOB
cd ~/code/amq-squad/.claude/worktrees/agent-a4c9efee2491e72e4
for i in 1 2 3 4 5; do
  d=$B/live/r12-n5-$i; rm -rf $d; mkdir -p $d
  $B/sched-live -mode r12 -dir $d -bin $B/amq-squad -home $B/home -label r12-n5-$i -rounds 40 > $d.out 2>&1
  echo "r12 run $i exit $?"
done
for i in 1 2; do
  d=$B/live/r19-$i; rm -rf $d; mkdir -p $d
  $B/sched-live -mode r19 -dir $d -bin $B/amq-squad -home $B/home -label r19-$i -rounds 60 > $d.out 2>&1
  echo "r19 run $i exit $?"
done
echo ALL-DONE
