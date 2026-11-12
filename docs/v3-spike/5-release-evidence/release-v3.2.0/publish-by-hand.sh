#!/bin/sh
# The hand steps of the 3.2.0 cut under the owner's answer A. Mirrors
# internal/releaseflow/gh.go:77-79 (tag) and :116-118 (release), as the rc.2 cut did.
# The release is NOT a prerelease and is marked latest by the create itself.
set -eu
cd <home>/code/amq-squad
V=<home>/amq-squad-lead/T/v320
TAG=v3.2.0
git fetch -q origin release/v3.2
C=$(git rev-parse origin/release/v3.2)
[ "$C" = e93ab08c378265e8b0a7d7a6123bc834feec5c67 ] || { echo "tip moved: $C"; exit 1; }
N=<home>/scratch-v320/docs/v3.2.0-release-notes.md
[ "$(shasum -a 256 "$N" | cut -c1-64)" = 8e6568fe96b986e9735c051280b057001790b559cb6d5150c3bc763526651df9 ] || { echo "notes digest differs from the prepared one"; exit 1; }
[ "$(git ls-remote origin refs/tags/$TAG | wc -l | tr -d ' ')" = 0 ] || { echo "the tag exists already"; exit 1; }
date -u +"tag start %Y-%m-%dT%H:%M:%SZ"
gh api -X POST repos/omriariav/amq-squad/git/refs -f ref=refs/tags/$TAG -f sha=$C > $V/tag-create.json
date -u +"tag done %Y-%m-%dT%H:%M:%SZ"
gh api repos/omriariav/amq-squad/git/ref/tags/$TAG --jq '.object.type + " " + .object.sha'
date -u +"release start %Y-%m-%dT%H:%M:%SZ"
gh release create $TAG --repo omriariav/amq-squad --verify-tag --target $C --title $TAG --notes-file "$N" --latest > $V/release-create.out
date -u +"release done %Y-%m-%dT%H:%M:%SZ"
cat $V/release-create.out
gh api repos/omriariav/amq-squad/releases/latest --jq '"latest: " + .tag_name'
