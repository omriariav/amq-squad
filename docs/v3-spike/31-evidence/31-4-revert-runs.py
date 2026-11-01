# The revert runs of brief 31-4, the two-way phone
# (docs/v3-spike/31-evidence/31-4-two-way-phone.md, section "Revert runs").
# Each run reverts one change in the working tree, runs the rows that must
# catch it, prints whether they failed and the failing tests, and restores
# every file it touched from git (and deletes a file it added), even when a
# run fails or is interrupted. Run it from a clean checkout of the branch:
#   python3 docs/v3-spike/31-evidence/31-4-revert-runs.py
# It refuses a tree with any staged, unstaged or untracked change, since it
# restores every file it touched to HEAD exactly. It reaches no network and
# no real Buzz, AMQ or tmux: the rows run on the in-process test relay and
# fakes (one row uses the installed amq in a scratch root, as the existing
# inbox row does, and is skipped without it).
import subprocess, os, sys
os.chdir(subprocess.check_output(['git','rev-parse','--show-toplevel'],text=True).strip())
def dirty():
    return subprocess.run(['git','status','--porcelain','--untracked-files=all'],capture_output=True,text=True,check=True).stdout
if dirty():
    sys.exit("refused: the tree has staged, unstaged or untracked changes; the runs restore files to HEAD and would drop them:\n"+dirty())
G='go test -count=1 '
B=G+'./internal/buzz/ -run '
P="cd pi && node --test "
RUNS=[0,0]
def run(name, edits, cmd):
    touched, added = set(), set()
    try:
        for (p,a,b) in edits:
            if a is None:
                # a new file the run adds
                assert not os.path.exists(p), (name,p)
                added.add(p)
                open(p,'w').write(b)
                continue
            s=open(p).read()
            assert a in s, (name,p,a[:70])
            touched.add(p)
            open(p,'w').write(s.replace(a,b,1))
        r=subprocess.run(cmd,shell=True,capture_output=True,text=True)
        out=r.stdout+r.stderr
        lines=[l.rstrip() for l in out.splitlines() if l.strip().startswith(('--- FAIL','✖','not ok')) or 'build failed' in l]
        RUNS[0]+=1
        RUNS[1]+=r.returncode!=0
        print(f"REVERT {name}: {'fails (the row catches it)' if r.returncode!=0 else 'PASSES (the row misses it)'}")
        seen=set()
        for l in lines:
            if l in seen: continue
            seen.add(l); print('   '+l.strip())
    finally:
        for p in added:
            if os.path.exists(p):
                os.remove(p)
        if touched:
            subprocess.run(['git','checkout','HEAD','--']+sorted(touched),check=True)
    if dirty():
        sys.exit("the tree is not clean after a restore:\n"+dirty())

R='internal/buzz/relay.go'
I='internal/buzz/inbound.go'
T='internal/buzz/route.go'

# Row 1: the post, its first line, redact.Block, the receipt, the thread
# Codex P2-8: redact.Block replaced by an identity that still compiles, in
# both layers (the whole body before the split, each part after it); the
# row catches it by the canary in the lead's reply reaching the post
run("the post without redact.Block (an identity in its place)", [(R,'\t\tparts[i] = redact.Block(p)\n','\t\tparts[i] = func(s string) string { _ = redact.Block; return s }(p)\n'),
    (R,'splitPost(requestID, thread, redact.Block(body))','splitPost(requestID, thread, body)')],
    B+"TestRelayPostsTheLeadsReplyToTheOwnersDM")
run("the post without its re <request id> line", [(R,'head := redact.Block("re " + requestID)','head := redact.Block(requestID)')],
    B+"TestRelayPostsTheLeadsReplyToTheOwnersDM")
run("the receipt is routed, not accepted <request id>", [(I,'text := "accepted " + rid + " routed " + dest','text := "routed " + dest')],
    B+"TestRelayPostsTheLeadsReplyToTheOwnersDM")
run("the question post off the cockpit thread", [(T,'Labels: labels, Thread: thread})','Labels: labels})')],
    B+"TestRelayPostsTheLeadsReplyToTheOwnersDM")
run("the post not in the request's DM conversation", [(R,'\t\ttags = append(tags, []string{"e", replyTo, "", "reply"})\n','')],
    B+"TestRelayPostsTheLeadsReplyToTheOwnersDM")
# Row 2: the rate bound
run("no progress rate bound", [(R,'if kind == "progress" && rec.ProgressAt != nil && now.Sub(*rec.ProgressAt) < ProgressBound {','if false && kind == "progress" && rec.ProgressAt != nil && now.Sub(*rec.ProgressAt) < ProgressBound {')],
    B+"TestRelayProgressRateBound")
run("the held progress never posts", [(R,'if rec.Pending != "" && (rec.ProgressAt == nil || now.Sub(*rec.ProgressAt) >= ProgressBound) {','if false && rec.Pending != "" && (rec.ProgressAt == nil || now.Sub(*rec.ProgressAt) >= ProgressBound) {')],
    B+"TestRelayProgressRateBound")
run("kind status is not progress", [(R,'case slices.Contains(e.Labels, LabelProgress) || e.Kind == "status":','case slices.Contains(e.Labels, LabelProgress):')],
    B+"TestRelayProgressRateBound")
run("a result does not close the request", [(R,'\tcase "result":\n\t\trec.Closed = true\n','\tcase "result":\n')],
    B+"TestRelayProgressRateBound")
# Row 3: the gate-shaped refusal, per form, and the thread guard
run("no whole-line answer form", [(R,'\tif answerForm(t, g.known) {','\tif false && answerForm(t, g.known) {')],
    B+"'TestRelayRefusesGateShapedLines/whole-line'")
run("the refusal not narrowed (any line containing the word)", [(R,'\tif len(w) == 0 || len(w) > 4 || !answerVerbs[w[0]] {','\tif slices.ContainsFunc(w, func(x string) bool { return answerVerbs[x] }) {\n\t\treturn true\n\t}\n\tif len(w) == 0 || len(w) > 4 || !answerVerbs[w[0]] {')],
    B+"'TestRelayRefusesGateShapedLines/sentences'")
run("no open gate id refusal", [(R,'\t\tif g.open[tok] {','\t\tif false && g.open[tok] {')],
    B+"'TestRelayRefusesGateShapedLines/an_open_gate.s_id'")
run("no open gate question text refusal", [(R,'\t\tif utf8.RuneCountInString(q) >= MinQuestionLine && strings.Contains(t, q) {','\t\tif false && strings.Contains(t, q) {')],
    B+"'TestRelayRefusesGateShapedLines/an_open_gate.s_question'")
run("the poster's gate-thread guard gone", [(R,'\tif g.threads[rec.PostTo] {','\tif false && g.threads[rec.PostTo] {')],
    B+"'TestRelayRefusesGateShapedLines/never_into'")
run("a request opened in a gate thread posts into it", [(R,'\t\t\t\tif g.threads[ref] {\n\t\t\t\t\trec.PostTo = ""','\t\t\t\tif false && g.threads[ref] {\n\t\t\t\t\trec.PostTo = ""')],
    B+"'TestRelayRefusesGateShapedLines/never_into'")
# Row 4: the lead side imports no key path
run("a lead-side package imports the key path", [('internal/status/zz_revert_keypath.go',None,'package status\n\nimport _ "github.com/omriariav/amq-squad/v3/internal/nostr"\n')],
    B+"TestTheLeadSideHoldsNoKeyPathNorPoster")
# Row 5: the restart record
run("handled replies not recorded (a restart posts again)", [(R,'\tif !slices.Contains(rec.Seen, id) {\n\t\trec.Seen = append(rec.Seen, id)\n\t}\n','')],
    B+"TestARestartedAttachmentNeitherRepostsNorSkips")
# Row 6: a gate question is never duplicated by the relay
run("the relay carries gate-shaped replies", [(R,'g.refuse(p); bad {','g.refuse(p); bad && false {'),(R,'g.refuse(parts[i]); bad {','g.refuse(parts[i]); bad && false {')],
    B+"TestAGateQuestionGoesByItsOwnPathOnce")
# Row 7: the lead's tool list (the change it guards against: a relay tool for the lead)
run("a lead relay tool added", [('pi/extensions/lib/tools.ts','\tif (cfg.lead) {\n','\tif (cfg.lead) {\n\t\tnames.push("amq_relay_post");\n')],
    P+"test/tools.test.ts")
# Row 8: the conversation
run("no continuation (every DM a new request)", [(I,'\tcont := h.continuation(e)\n','\tvar cont *status.RelayRecord\n')],
    B+"TestThePhoneConversation")
run("an answer form in the conversation goes to the gate path", [(I,'if cont == nil && isGateAnswer(content) {','if isGateAnswer(content) {')],
    B+"TestThePhoneConversation")
run("a continuation does not follow the request's lead", [(I,'rid, via = cont.RequestID, &Route{Lead: cont.Route}','rid = cont.RequestID')],
    B+"TestThePhoneConversation")
run("a gate-thread answer that names a relay post leaves the gate path", [(R,'\t\tif g.threads[ref] {\n\t\t\treturn nil\n\t\t}','\t\tif false && g.threads[ref] {\n\t\t\treturn nil\n\t\t}')],
    B+"TestAnAnswerInTheGateThreadStaysOnTheGatePath")
# The split, the AMQ thread flag, the attach wiring
run("no split of a long post", [(R,'if len(head)+1+len(body) <= DMLimit {','if true {')],
    B+"TestALongReplyIsSplitIntoAtMostThreeParts")
run("amq send without --thread", [(T,'\tif m.Thread != "" {\n\t\targs = append(args, "--thread", m.Thread)\n\t}\n','')],
    B+"TestAMQThreadsThroughTheCLI")
run("remote attach without the relay", [('internal/cli/remote.go','Poster: remotePoster(b, core.Pin), Threads: remoteThreads}','}')],
    G+"./internal/cli/ -run TestRemoteAttachRelaysTheLeadsReply")
# The lead side: status and the screen
run("status without the requests", [('internal/status/collect.go','\tin.Requests = collectRequests(o.Root, warn)\n','')],
    G+"./internal/status/ -run TestStatusShowsThePhoneRequests")
run("relayed_at not nullable", [('internal/status/compat.go','\t"relayed_at": true,','')],
    G+"./internal/status/ -run TestStatusShowsThePhoneRequests")
run("request fields not redacted", [('internal/status/classify.go','r.RequestID, r.Thread, r.Lead, r.Refused = redact.Block(r.RequestID), redact.Block(r.Thread), redact.Block(r.Lead), redactAll(r.Refused)','_ = r')],
    G+"./internal/status/ -run TestStatusShowsThePhoneRequests")
run("the screen without the requests block", [('pi/extensions/lib/screen.ts','const reqs = st.requests ?? [];','const reqs: RequestRow[] = [];')],
    P+"test/screen.test.ts")
# The Codex round on PR #835
S='internal/status/relay.go'
run("records not validated (load and write)", [(S,'\tif err := r.Validate(requestID); err != nil {\n\t\treturn nil, err\n\t}\n\treturn &r, nil','\treturn &r, nil'),
    (R,'\tif err := r.Validate(r.RequestID); err != nil {\n\t\treturn err\n\t}\n','')],
    B+"TestAPlantedRecordIsRefusedAndNothingPosts")
run("record fields not checked (status)", [(S,'\tif len(bad) > 0 {\n\t\treturn fmt.Errorf','\tif len(bad) > 0 && false {\n\t\treturn fmt.Errorf')],
    G+"./internal/status/ -run TestARelayRecordIsCheckedFieldByField")
run("the checks on the body, not the assembled parts (Codex P1-2)", [(R,'\t\tif line, bad := g.refuse(p); bad {\n\t\t\treturn nil, line, true\n\t\t}\n\t\tparts[i] = redact.Block(p)\n\t\tif line, bad := g.refuse(parts[i]); bad {\n\t\t\treturn nil, line, true\n\t\t}','\t\tparts[i] = redact.Block(p)'),
    (R,'\tparts := splitPost(requestID, thread, redact.Block(body))\n','\tif line, bad := g.refuse(body); bad {\n\t\treturn nil, line, true\n\t}\n\tparts := splitPost(requestID, thread, redact.Block(body))\n')],
    B+"TestASplitNeverStartsAPartWithAnAnswerForm")
run("the publish before the intent (Codex P1-3)", [(R,'if err := save(); err != nil {\n\t\t\treturn r, fmt.Errorf("the intent for','if err := error(nil); err != nil {\n\t\t\treturn r, fmt.Errorf("the intent for')],
    B+"'TestAFailingRecordStorePostsNothing|TestACrashBetweenPublishAndEventWriteIsANamedState'")
run("an unconfirmed intent retried forever (Codex P1-3)", [(R,'\t\tif p.Tries >= 2 {','\t\tif false && p.Tries >= 2 {')],
    B+"TestACrashBetweenPublishAndEventWriteIsANamedState")
run("a failed later part ends the reply (Codex P2-5)", [(R,'\t\t\treturn r, fmt.Errorf("post %s part','\t\t\tif len(p.Events) > 0 {\n\t\t\t\tparts = parts[:i]\n\t\t\t\tbreak\n\t\t\t}\n\t\t\treturn r, fmt.Errorf("post %s part')],
    B+"TestAFailedPartPostsNextTickWithoutRepeatingTheFirst")
run("no normalization (Codex P2-4)", [(R,'func normalizeLine(s string) string {\n','func normalizeLine(s string) string {\n\tif true {\n\t\treturn strings.ToLower(s)\n\t}\n')],
    B+"TestTheChecksSeeThroughInvisibleCharacters")
run("the DM marked handled although its record failed (Codex P2-6)", [(I,'\t\th.final(ctx, hd, "refused - "+RelayRecordFailed)\n\t\treturn','\t\th.final(ctx, hd, "refused - "+RelayRecordFailed)\n\t\t_ = h.seen.put(id, "-", now)\n\t\treturn')],
    B+"TestTheRecordComesBeforeTheQuestion")
run("a failed delivery keeps its record (Codex P2-6)", [(I,'if uerr := undo(); uerr != nil {','if uerr := error(nil); uerr != nil && undo != nil {')],
    B+"TestTheRecordComesBeforeTheQuestion")
run("the helper without AMQ_NO_UPDATE_CHECK (Codex P2-7)", [('internal/buzz/helpers_test.go',', "AMQ_NO_UPDATE_CHECK": "1"}','}')],
    B+"TestTestsExecAMQOnlyThroughTheHelper")
run("a test execs amq directly (Codex P2-7)", [('internal/buzz/zz_revert_amq_test.go',None,'package buzz\n\nimport "os/exec"\n\nvar _ = exec.Command("amq", "version")\n')],
    B+"TestTestsExecAMQOnlyThroughTheHelper")
run("the owner's next turn does not clear an unconfirmed post", [(R,'\t\t\trec.Refusals[i].ClearedAt = timePtr(now)\n','\t\t\t_ = timePtr\n')],
    B+"TestACrashBetweenPublishAndEventWriteIsANamedState")
# The Codex re-check and the plan reviewer's by-file read
run("an ambiguous post failure gives its try back (unbounded retries)", [(R,'\t\t\tif errors.Is(err, ErrNotSent) {','\t\t\tif true {')],
    B+"TestAFailedPostKeepsItsTryUnlessNothingWasSent")
run("a failure before the send consumes a try", [(R,'\t\t\tif errors.Is(err, ErrNotSent) {','\t\t\tif false {')],
    B+"TestAFailedPostKeepsItsTryUnlessNothingWasSent")
run("no minimum question-line length", [(R,'utf8.RuneCountInString(q) >= MinQuestionLine && ','')],
    B+"TestAShortQuestionLineDoesNotRefuseOrdinaryReplies")
run("redaction only after the split (Codex re-check #2)", [(R,'\tparts := splitPost(requestID, thread, redact.Block(body))\n','\tparts := splitPost(requestID, thread, body)\n')],
    B+"TestACanaryAcrossTheSplitBoundaryIsRedacted")
run("marks (Mn, Me) not stripped (Codex re-check #3)", [(R,'case unicode.In(r, unicode.Cf, unicode.Mn, unicode.Me):','case unicode.In(r, unicode.Cf):')],
    B+"TestTheChecksSeeThroughInvisibleCharacters")
run("no confusables fold (Codex re-check #3)", [(R,'if l, ok := confusables[r]; ok {','if l, ok := confusables[r]; ok && false {')],
    B+"TestTheChecksSeeThroughInvisibleCharacters")
run("status shows every historical refusal (Codex re-check #7)", [('internal/status/relay.go','if f.ClearedAt == nil && !f.At.Before(since) {','if f.ClearedAt == nil && (true || !f.At.Before(since)) {')],
    G+"./internal/status/ -run TestStatusShowsThePhoneRequests")
run("an undelivered turn is kept (Codex re-check #5)", [(R,'if r, did, err := recoverUndelivered(dir, rec, now); did {','if r, did, err := recoverUndelivered(dir, rec, now); did && false {')],
    B+"TestAnUndeliveredTurnIsTakenBackAndARollbackFailureIsNamed")
run("a failed rollback ignored (Codex re-check #5)", [(I,'hd.Error = joinErr(hd.Error, RelayRollbackFailed+": "+uerr.Error())','_ = uerr')],
    B+"TestAnUndeliveredTurnIsTakenBackAndARollbackFailureIsNamed")
run("the DM marked handled before the real lead is recorded (Codex re-check #4)", [(I,'\t\th.final(ctx, hd, "refused - "+RelayRecordFailed)\n\t\treturn\n\t}\n\tif err := h.seen.put(id, "-", now); err != nil {','\t\th.final(ctx, hd, "refused - "+RelayRecordFailed)\n\t\t_ = h.seen.put(id, "-", now)\n\t\treturn\n\t}\n\tif err := h.seen.put(id, "-", now); err != nil {')],
    B+"TestTheRealLeadIsRecordedBeforeTheDMIsHandled")
run("a computed exec without its not-amq line (Codex re-check #6)", [('internal/buzz/phone_test.go',' // exec: not amq (the go tool)','')],
    B+"TestTestsExecAMQOnlyThroughTheHelper")
# The receipt as a continuation anchor (the lead's addition)
run("a reply naming only the receipt row is not read", [(R,'\tif relay == nil {\n\t\treturn nil\n\t}\n\tpin := h.Core.Pin','\tif relay == nil || true {\n\t\treturn nil\n\t}\n\tpin := h.Core.Pin')],
    B+"TestAReplyToTheReceiptContinuesTheRequest")
run("a receipt row not checked as the body's", [(R,'if err != nil || row.PubKey != pin.Body || row.VerifySignature() != nil','if err != nil || row.VerifySignature() != nil')],
    B+"TestAReplyToTheReceiptContinuesTheRequest")
run("a reply naming the thread root does not map", [(R,' ||\n\t\t\t\t\tslices.ContainsFunc(r.Turns, func(t status.RelayTurn) bool { return t.Delivered && t.Event == ref })','')],
    B+"TestAReplyToTheReceiptContinuesTheRequest")
print(f"{RUNS[1]} of {RUNS[0]} reverts caught")
sys.exit(0 if RUNS[0]==RUNS[1] else 1)
