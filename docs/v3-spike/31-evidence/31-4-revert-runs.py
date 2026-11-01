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
run("the post without redact.Block", [(R,'splitPost(rec.RequestID, rec.Thread, redact.Block(body))','splitPost(rec.RequestID, rec.Thread, body)')],
    B+"TestRelayPostsTheLeadsReplyToTheOwnersDM")
run("the post without its re <request id> line", [(R,'head := "re " + requestID','head := requestID')],
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
run("a result does not close the request", [(R,'\t\t\tif kind == "result" {\n\t\t\t\trec.Closed = true\n\t\t\t}\n','')],
    B+"TestRelayProgressRateBound")
# Row 3: the gate-shaped refusal, per form, and the thread guard
run("no whole-line answer form", [(R,'\tif answerForm(t, g.known) {','\tif false && answerForm(t, g.known) {')],
    B+"'TestRelayRefusesGateShapedLines/whole-line'")
run("the refusal eats the sentence (any line starting with the word)", [(R,'\trest := w[1:]\n','\tif len(w) > 0 {\n\t\treturn true\n\t}\n\trest := w[1:]\n')],
    B+"'TestRelayRefusesGateShapedLines/sentences'")
run("no open gate id refusal", [(R,'\t\tif g.open[tok] {','\t\tif false && g.open[tok] {')],
    B+"'TestRelayRefusesGateShapedLines/an_open_gate.s_id'")
run("no open gate question text refusal", [(R,'\t\tif strings.Contains(t, q) {','\t\tif false && strings.Contains(t, q) {')],
    B+"'TestRelayRefusesGateShapedLines/an_open_gate.s_question'")
run("the poster's gate-thread guard gone", [(R,'\tif g.threads[rec.PostTo] {','\tif false && g.threads[rec.PostTo] {')],
    B+"'TestRelayRefusesGateShapedLines/never_into'")
run("a request opened in a gate thread posts into it", [(R,'\t\t\t\tif g.threads[ref] {\n\t\t\t\t\trec.PostTo = ""','\t\t\t\tif false && g.threads[ref] {\n\t\t\t\t\trec.PostTo = ""')],
    B+"'TestRelayRefusesGateShapedLines/never_into'")
# Row 4: the lead side imports no key path
run("a lead-side package imports the key path", [('internal/status/zz_revert_keypath.go',None,'package status\n\nimport _ "github.com/omriariav/amq-squad/v3/internal/nostr"\n')],
    B+"TestTheLeadSideHoldsNoKeyPathNorPoster")
# Row 5: the restart record
run("handled replies not recorded (a restart posts again)", [(R,'\t\trec.Seen = append(rec.Seen, e.ID)\n','')],
    B+"TestARestartedAttachmentNeitherRepostsNorSkips")
# Row 6: a gate question is never duplicated by the relay
run("the relay carries gate-shaped replies", [(R,'if line, bad := g.refuse(body); bad {','if line, bad := g.refuse(body); bad && false {')],
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
print(f"{RUNS[1]} of {RUNS[0]} reverts caught")
sys.exit(0 if RUNS[0]==RUNS[1] else 1)
