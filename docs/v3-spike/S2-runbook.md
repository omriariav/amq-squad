# S2 runbook: seat PR merges under CI-only protection with the typed gate as the review (criterion 2)

Prepared 2026-10-22 07:1x while S6 runs. Target: amq-noc PR #78 (`loadtest/N1`, head
`f1f5a84`, base `main` at `9bedf33`), opened by the S6 fullstack seat at 06:42 with the
owner's GitHub identity. Its own workflow ran on the PR and the check `ci` is `SUCCESS`
(the workflow file lives only on the PR branch; GitHub ran it from the branch). amq-noc has
no ruleset and no branch protection today (checked 07:05). The brief forbids every seat to
merge; this merge is the operator's, under the owner's identity, after the gate.

Files: `spike/loadtest/s2/ruleset.json` (CI-only protection), `merge-queue-rule.json`
(the probe), `gate-spec.json` (the R34 batch spec: ordered (PR, head SHA) plus base SHA).

## Owner decisions needed before step 1

1. Creating a ruleset on `omriariav/amq-noc` is a change to the owner's repository. The
   operator applies it only on the owner's go. It is removed in step 7.
2. Which key answers the gate:
   - **Real key (S1 real-key run):** needs the owner's nsec decision (A or B) and the Buzz
     npub plus relay URL. Then the S1 real-key sequence (`docs/v3-spike/S1-owner-run.md`)
     posts the gate text to the DM and the owner answers from the phone.
   - **Throwaway key (synthetic):** `gateverify keygen` mints a key in scratch, the owner
     types the `approve` line in this session and the operator signs it with that key.
     Criterion 2 is then "passed with a synthetic signer"; the report says so, and the
     real-key signature stays a criterion 1 item.
3. Ten minutes of the owner's presence for steps 3 to 6.

## Sequence (operator unless marked owner)

    export PATH=$HOME/go/bin:$PATH; S2=~/.amq-squad/spike/s2; mkdir -p $S2; cd /Users/omri/code/amq-squad/spike/loadtest/s2

1. Ruleset (owner go first). Record the response id.

       gh api -X POST repos/omriariav/amq-noc/rulesets --input ruleset.json | tee $S2/ruleset-created.json
       gh api repos/omriariav/amq-noc/rules/branches/main | tee $S2/rules-main.json

2. Direct-push block test, from a scratch clone outside the repo (expected: refused by the
   ruleset; the test commit never exists on the server):

       git clone -q --depth 1 https://github.com/omriariav/amq-noc $S2/push-probe && cd $S2/push-probe
       git commit -q --allow-empty -m "s2 push probe (must be refused)" && git push origin HEAD:main 2>&1 | tee $S2/push-probe.log; cd -
       # pass = "GH013: Repository rule violations" (or "protected branch hook declined") in push-probe.log

3. Merge-queue probe. Record accept or reject verbatim; if accepted, delete it again at once
   so it cannot hold the real merge.

       gh api -X POST repos/omriariav/amq-noc/rulesets --input merge-queue-rule.json 2>&1 | tee $S2/merge-queue-probe.log
       # if it returned an id: gh api -X DELETE repos/omriariav/amq-noc/rulesets/<id>

4. Gate record (the digest covers (#78, f1f5a84) and base 9bedf33; a rebase of #78 or a new
   commit on main re-opens the gate; re-check both SHAs right before rendering):

       gh pr view 78 -R omriariav/amq-noc --json headRefOid --jq .headRefOid; gh api repos/omriariav/amq-noc/git/ref/heads/main --jq .object.sha
       cd /Users/omri/code/amq-squad/spike/gateverify
       go run . render --root $S2/root --spec ../loadtest/s2/gate-spec.json --owner <npub or 64 hex> --issuer-key $S2/issuer.pem
       # throwaway path: go run . issuer-keygen ... and go run . keygen ... first (see S1-report section 2 for the exact flags); real-key path: the owner's npub and the S1 issuer key

5. Owner answers (owner): one line, `approve s2-merge-01 merge <first 16 hex of the digest>`.
   Real key: from Buzz in the DM. Throwaway: typed here; the operator signs it with the
   scratch key (`go run . sign ...`) and verifies (`go run . verify ...`, `envelope-verify`).
   Store the answer event and the verify output in `$S2/`.

6. Merge, under the owner's identity, only after step 5 says `accepted`:

       gh pr ready 78 -R omriariav/amq-noc
       gh pr merge 78 -R omriariav/amq-noc --squash --delete-branch 2>&1 | tee $S2/merge.log
       gh pr view 78 -R omriariav/amq-noc --json state,mergedAt,mergeCommit --jq '"\(.state) \(.mergedAt) \(.mergeCommit.oid)"'

   Expected: GitHub accepts because `ci` passed and the PR rule is satisfied with zero
   reviews. If GitHub asks for a review after all, that is the S2 fail condition ("the merge
   path needs a second identity"); record it and stop.

7. Clean up: delete the S2 ruleset (`gh api -X DELETE repos/omriariav/amq-noc/rulesets/<id>`),
   remove `$S2/push-probe`. Leave the merged workflow in place: every later S6 PR on amq-noc
   gets a `ci` check once it rebases on main, which is useful for the final numbers.

## Report fields (docs/v3-spike/S2-report.md)

PR URL and merge commit; ruleset JSON as applied and the `rules/branches/main` response;
push-probe result verbatim; merge-queue accept or reject verbatim; gate record path, digest,
the answer event id, which key signed it (real or throwaway) and the verifier output;
wall time and owner minutes. Pass: the PR merged only after `ci` passed and the gate was
answered `accepted`. Fail: the ruleset did not block the direct push, or the merge needed a
second identity.
