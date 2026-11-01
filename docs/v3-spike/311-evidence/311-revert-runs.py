# The revert runs of brief 3.1.1-1, the gate question in plain words
# (docs/v3-spike/311-evidence/311-gate-language.md, section "Revert runs").
# Each run reverts one change in the working tree, runs the rows that must
# catch it, prints whether they failed and the failing tests, and restores
# every file it touched from git, even when a run fails or is interrupted
# (try/finally). Run it from a clean checkout of the branch:
#   python3 docs/v3-spike/311-evidence/311-revert-runs.py
# It refuses a tree with any staged, unstaged or untracked change, since it
# restores every file it touched to HEAD exactly. It reaches no network and
# no real Buzz, AMQ or tmux: the rows run on the in-process test relays,
# the fake gh and the stand-in pi.
import subprocess, os, sys
os.chdir(subprocess.check_output(['git','rev-parse','--show-toplevel'],text=True).strip())
def dirty():
    return subprocess.run(['git','status','--porcelain','--untracked-files=all'],capture_output=True,text=True,check=True).stdout
if dirty():
    sys.exit("refused: the tree has staged, unstaged or untracked changes; the runs restore files to HEAD and would drop them:\n"+dirty())
G='go test -count=1 '
GS=G+'./internal/gatestore/ -run '
GV=G+'./internal/gateverify/ -run '
BZ=G+'./internal/buzz/ -run '
CL=G+'./internal/cli/ -run '
MT=G+'./internal/mergetrain/ -run '
PI='cd pi && node --test --test-name-pattern "brief 3.1.1-1" '
RUNS=[0,0]
def run(name, edits, cmd):
    touched=set()
    try:
        for (p,a,b) in edits:
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
        if touched:
            subprocess.run(['git','checkout','HEAD','--']+sorted(touched),check=True)
    if dirty():
        sys.exit("the tree is not clean after a restore:\n"+dirty())

PL='internal/gatestore/plain.go'
RE='internal/gatestore/record.go'
VE='internal/gateverify/verify.go'
CO='internal/gateverify/core.go'
IN='internal/buzz/inbound.go'
LS='internal/cli/lead_start.go'
TC='internal/cli/team_confirm.go'
RF='internal/cli/release_flow.go'
SC='internal/cli/seat_commit.go'
ST='internal/cli/status.go'
CF='internal/status/classify.go'
TR='internal/mergetrain/train.go'
GH='internal/mergetrain/gh.go'
SCR='pi/extensions/lib/screen.ts'
KEY='pi/extensions/lib/keys.ts'
TEAM='pi/extensions/lib/team.ts'

# Rows 1 and 5: the question in three plain lines above a rule
run("the 3.0 text (Render writes the identifiers and the typed lines)", [(RE,'\trec.Text = TextOf(rec)\n','\trec.Text = legacyText(rec.Squad, rec.GateID, rec.TypedClass, rec.Class, rec.Target, rec.Digest)\n')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords|TestNoQuestionAsksForATuple|TestRenderAndValidate'")
run("a hash above the rule (a field's hex run shown whole)", [(PL,'return hexRunRe.ReplaceAllStringFunc(s, func(h string) string { return h[:7] + "…" })','return s')],
    GS+"'TestAQuotedTitleIsCutAt80WithAnEllipsis'")
run("the title not cut at 80 characters", [(PL,'if utf8.RuneCountInString(s) > TitleMax {','if false && utf8.RuneCountInString(s) > TitleMax {')],
    GS+"'TestAQuotedTitleIsCutAt80WithAnEllipsis'")
run("an unrecorded claim (the merge's second line says its checks passed)", [(PL,'q.why = "The train refused it: " + rulePhrases(strings.Split(m[3], ",")) + "."','q.why = "Its checks passed on the train."')],
    GS+"'TestEveryWordOfThePlainLinesTracesToAField'")
run("a sentence over 20 words (every refused rule listed)", [(PL,'return fmt.Sprintf("%s, %s and %d more", ps[0], ps[1], len(ps)-2)','return strings.Join(ps, ", ")')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("the gate id above the rule", [(PL,'\tlines := []string{q.ask}\n','\tlines := []string{r.GateID + ": " + q.ask}\n')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("no class in words (a tag named without saying tag)", [(PL,'q.ask = "Create a tag named " + plain(w[2]) + "?"','q.ask = "Create " + plain(w[2]) + "?"')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("the identifiers below the rule lose the target line", [(PL,'"class: "+typedClass+" ("+action+")", "target: "+target, "digest: "+digest)','"class: "+typedClass+" ("+action+")", "digest: "+digest)')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("the facts dropped at Render", [(RE,'\tfacts := s.Facts.normalize()\n','\tfacts := (&Facts{}).normalize()\n')],
    GS+"'TestAQuotedTitleIsCutAt80WithAnEllipsis' && "+MT+"'TestTheMergeGateAsksInPlainWords'")
run("a 3.1.0 record no longer validates (only the new text)", [(RE,'case r.Text != TextOf(r) && (r.Facts != nil || r.Text != legacyText(r.Squad, r.GateID, r.TypedClass, r.Class, r.Target, r.Digest)):','case r.Text != TextOf(r):')],
    GS+"'TestTheOldTextStillValidatesAndFactsAreSafe'")
run("SafeFacts keeps a branch that does not validate", [(PL,'\tif (&Facts{Branch: f.Branch}).validate() != nil {\n','\tif false {\n')],
    GS+"'TestTheOldTextStillValidatesAndFactsAreSafe'")

# Row 2: the bare yes or no, bound by the thread
run("Verify refuses the bare form", [(VE,'\t\tverb, ok := gatestore.ParseBare(e.Content)\n\t\tif !ok {\n\t\t\treturn Outcome{}, reject(RejectBadTuple','\t\tverb, ok := "", false\n\t\tif !ok {\n\t\t\treturn Outcome{}, reject(RejectBadTuple')],
    GV+"'TestABareYesOrNoInTheThreadIsTheAnswer'")
run("the core binds a bare answer to no gate", [(CO,'\t\tid, berr := c.bareGate(e)\n','\t\tid, berr := "", reject(RejectBadTuple, "no bare form")\n')],
    GV+"'TestABareYesOrNoInTheThreadIsTheAnswer'")
run("a bare yes outside the thread is not re-asked in the one open gate", [(CO,'\t\tif len(open) == 1 {\n','\t\tif false && len(open) == 1 {\n')],
    GV+"'TestABareYesOutsideTheThreadGetsThePlainReask'")
run("a bare answer bound to every gate (not the thread it names)", [(CO,'if slices.ContainsFunc(refs, func(ref string) bool { return slices.Contains(ours, ref) }) {','if true || slices.ContainsFunc(refs, func(ref string) bool { return slices.Contains(ours, ref) }) {')],
    GV+"'TestABareYesInAnotherGatesThreadIsThatGatesNeverThisOnes'")

# Row 3: no text asks for a tuple; the plain re-ask
run("the re-ask asks for the tuple (the 3.0 text)", [(CO,'\treturn "Reply yes or no to this message.\\n\\n" + gatestore.Rule + "\\namq-squad gate " + a.GateID\n','\treturn "amq-squad gate " + a.GateID + ": that answer was not in this thread. Reply here to confirm, with one line:\\n" + strings.Join([]string{a.Verb, a.GateID, a.Class, a.DigestPrefix}, " ")\n')],
    GV+"'TestNoReaskAsksForATuple|TestABareYesOutsideTheThreadGetsThePlainReask|TestThreadReask'")
run("the history re-ask asks for the tuple", [(CO,'".\\nReply yes or no to this message.\\n\\n" + rec.Text','".\\n\\n" + rec.Text + "\\n\\nReply here to confirm, with one line:\\n" + strings.Join([]string{a.Verb, a.GateID, a.Class, a.DigestPrefix}, " ")')],
    GV+"'TestNoReaskAsksForATuple'")
run("the doors text asks for yes <prefix>", [(TC,'''(2) on the phone, reply yes to the lead's Buzz post that relays this plan with its digest %s", prefix, digest)''','''(2) send the signed Buzz DM \\"yes %s\\" (digest %s)", prefix, prefix, digest)''')],
    CL+"'TestTheDoorsTextAsksForNoPrefix'")
run("the plan block asks for yes <prefix>", [(TEAM,'''(or on the phone: reply yes to the lead's Buzz post of this plan)`''','''(or the signed Buzz DM "yes ${p.prefix}")`''')],
    PI+"test/team.test.ts")

# Row 2 at the handler: a bare yes is a gate answer, in its squad
run("a bare yes routed to the lead as a phone request", [(IN,'\tif _, ok := gatestore.ParseBare(content); ok {\n\t\treturn true\n\t}\n','')],
    BZ+"'TestABareYesIsAGateAnswerAtTheHandler'")
run("a bare yes goes to the primary squad whatever its thread", [(IN,'\t\t\treturn h.bareCore(e)\n','\t\t\treturn h.Core, nil\n')],
    BZ+"'TestABareYesGoesToTheSquadWhoseThreadItNames'")

# Row 6: the team confirmation, a bare yes replied to the relayed plan post
run("the handler's team door ignores a bare yes to the plan", [(IN,'if post := h.planPost(vctx, e, relay); post != nil {','if post := h.planPost(vctx, e, relay); false && post != nil {')],
    BZ+"'TestABareYesRepliedToTheRelayedPlanConfirmsTheTeam'")
run("a post the relay never posted taken as the plan post", [(IN,'\tcase parent != "" && posts[parent]:\n','\tcase parent != "":\n')],
    BZ+"'TestABareYesRepliedToTheRelayedPlanConfirmsTheTeam'")
run("a yes to a post naming no proposal refused, not continued", [(IN,'\tif post != nil && errors.Is(err, gateverify.ErrNotAPlan) {\n\t\treturn false\n\t}\n','')],
    BZ+"'TestABareYesRepliedToTheRelayedPlanConfirmsTheTeam'")
run("the lead takes a plan post another key signed", [(LS,'\t\tcase p.PubKey != pin.Body:\n','\t\tcase false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes a yes that replies elsewhere", [(LS,'\t\tcase !slices.Contains(nostr.ThreadRefs(e), p.ID):\n','\t\tcase false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes an altered plan post", [(LS,'\t\tcase !p.CheckID():\n','\t\tcase false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes a bare no as the confirmation", [(LS,'bare && verb == "approve" && req.Post != nil {','bare && req.Post != nil {')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the attachment refuses a post naming no proposal instead of not_a_plan", [(TC,'\t\treturn "", fmt.Errorf("%w: the post names no proposal younger than %s", gateverify.ErrNotAPlan, ConfirmTTL)\n','\t\treturn "", fmt.Errorf("%s: the post names no proposal younger than %s", ReasonNoProposal, ConfirmTTL)\n')],
    CL+"'TestTheAttachmentRoutesABareYesByThePostsProposal'")
run("the attachment hands the lead the yes without the post", [(TC,'Event: raw, Post: post})','Event: raw})')],
    CL+"'TestTheAttachmentRoutesABareYesByThePostsProposal'")

# Rows 1 and 5 at the raisers: what the question's lines read
run("the release prepare raises without the branch it checked", [(RF,'\t\t\tspec.Facts = gatestore.SafeFacts("", *branch)\n','\t\t\tspec.Facts = nil\n')],
    CL+"'TestReleaseVerbPassesTheFileAsTheBodyAndNotesAsTheGateNote'")
run("the merge gate without the PR's title and base", [(TR,', Facts: gatestore.SafeFacts(p.title, req.Base)})','})')],
    MT+"'TestTheMergeGateAsksInPlainWords'")
run("gh pr view does not ask for the title", [(GH,'"number,title,state,isDraft,','"number,state,isDraft,')],
    MT+"'TestTheMergeGateAsksInPlainWords'")
run("the widen's growth gate without the task's title", [(SC,', Facts: gatestore.SafeFacts(title, "")}','}')],
    CL+"'TestSeatCommitAndWidenThroughTheGrowthGate'")

# Row 4: the screen's gates block and the answer key
run("status gates without the question", [(CF,', Question: g.Question})','})')],
    CL+"'TestSeatCommitAndWidenThroughTheGrowthGate'")
run("status gates carry the whole text, not the lines above the rule", [(ST,'Question: gatestore.Plain(g.Text)})','Question: g.Text})')],
    CL+"'TestSeatCommitAndWidenThroughTheGrowthGate'")
run("the gates block shows the identifiers only", [(SCR,'\t\tfor (const l of plain) out.push(`  ${l}`);\n','')],
    PI+"test/keys.test.ts")
run("the answer key shows the identifiers only", [(KEY,'\t\tconst shown = plain ? `${plain}\\n${ids}` : ids;\n','\t\tconst shown = ids;\n')],
    PI+"test/keys.test.ts")
run("the answer key finds no rule in the text", [(KEY,'\tconst at = (text ?? "").indexOf(`\\n\\n${RULE}\\n`);\n','\tconst at = -1;\n')],
    PI+"test/keys.test.ts")

print(f"\n{RUNS[1]} of {RUNS[0]} reverts fail their rows")
