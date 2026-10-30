#!/bin/sh
# Independent, uncached verification of a lead-loop result.
# Usage: verify-go-test.sh <module dir> <label> <log file>
dir=$1 label=$2 log=$3
cd "$dir" || exit 2
{
	echo "# $label; $(date -u +%FT%TZ)"
	echo '$ go test -count=1 -v ./...'
	GOCACHE="$HOME/.amq-squad/spike/s5/gocache" go test -count=1 -v ./... 2>&1
	echo "exit=$?"
} > "$log"
grep -E '^(--- |ok|FAIL|exit)' "$log"
