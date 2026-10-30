#!/bin/sh
# Passive notify observer: poll the process table every 20 ms and log any
# `amq __codex-notify` or SkyComputerUseClient process with its parent.
# It never invokes or alters the owner's notify command.
# Usage: notify-watch.sh LOGFILE   (stop with kill)
log="$1"
while :; do
  ps -axo pid=,ppid=,lstart=,command= | grep -E '__codex-notify|SkyComputerUseClient' | grep -v grep >> "$log"
  sleep 0.02
done
