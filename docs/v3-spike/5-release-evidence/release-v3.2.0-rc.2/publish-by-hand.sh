#!/bin/sh
# The hand steps of the rc.2 cut under the owner's answer A. Mirrors
# internal/releaseflow/gh.go:77-79 (tag) and :116-118 (release).
set -eu
cd /Users/omri/code/amq-squad
V=/Users/omri/.claude/jobs/6dd424c9/tmp/v320rc2
TAG=v3.2.0-rc.2
C=$(git rev-parse origin/v3)
[ "$C" = da109b496787ffb7b8a716b8d2d75af19e98a045 ] || { echo "tip moved: $C"; exit 1; }
N=/Users/omri/scratch-v320rc2/docs/v3.2.0-release-notes.md
[ "$(shasum -a 256 "$N" | cut -c1-64)" = c6bfa2d2e508ab13edeb6dc71dbae9f3f456f5ca4344bd588e9ab2bb6e45525d ] || { echo "notes digest differs from the prepared one"; exit 1; }
date -u +"tag start %Y-%m-%dT%H:%M:%SZ"
gh api -X POST repos/omriariav/amq-squad/git/refs -f ref=refs/tags/$TAG -f sha=$C > $V/tag-create.json
date -u +"tag done %Y-%m-%dT%H:%M:%SZ"
gh api repos/omriariav/amq-squad/git/ref/tags/$TAG --jq '.object.type + " " + .object.sha'
date -u +"release start %Y-%m-%dT%H:%M:%SZ"
gh release create $TAG --repo omriariav/amq-squad --verify-tag --target $C --title $TAG --notes-file "$N" --prerelease --latest=false > $V/release-create.out
date -u +"release done %Y-%m-%dT%H:%M:%SZ"
cat $V/release-create.out
