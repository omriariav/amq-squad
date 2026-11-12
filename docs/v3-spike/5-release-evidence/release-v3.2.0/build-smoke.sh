#!/bin/sh
V=<home>/amq-squad-lead/T/v320
cd <home>/scratch-v320 || exit 1
git describe --tags --exact-match > $V/describe.txt 2>&1
date -u +"build start %Y-%m-%dT%H:%M:%SZ" > $V/build-times.txt
scripts/build-pi-runtime.sh all > $V/build.log 2>&1; echo "build exit $?" >> $V/build-times.txt
date -u +"build done %Y-%m-%dT%H:%M:%SZ" >> $V/build-times.txt
build/pi/amq-squad version >> $V/build-times.txt 2>&1
make release-smoke VERSION=v3.2.0 > $V/release-smoke.log 2>&1; echo "smoke exit $?" >> $V/build-times.txt
date -u +"smoke done %Y-%m-%dT%H:%M:%SZ" >> $V/build-times.txt
git status --porcelain | wc -l | tr -d ' ' >> $V/build-times.txt
cat $V/build-times.txt
