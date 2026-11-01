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
KS='pi/extensions/lib/keys.ts'
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

# The review on #838: no token above the rule
run("the external send names its tool and channel id above the rule", [(PL,'q.ask = "Send a message outside the squad" + sendWhere(w) + "?"','q.ask = "Send an outside message with " + strings.Join(w, " ") + "?"')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("the credential change names its git setting above the rule", [(PL,'\t\t\treturn "Change which program git uses for credentials?"\n','\t\t\treturn "Make a credential change to git setting " + w[2] + "?"\n')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("a credentials write names its path above the rule", [(PL,'\t\treturn "Write a credentials file?"\n','\t\treturn "Write a credentials file at " + strings.TrimPrefix(t, "write ") + "?"\n')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("the widen names its protected globs above the rule", [(PL,'\t\t\t\twhat := "1 protected path"\n','\t\t\t\twhat := globs[0]\n')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("a repository named owner/name above the rule", [(PL,'\treturn plain(name)\n}','\t_ = name\n\treturn plain(repo)\n}')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")
run("a generic question names the whole target", [(PL,'\tvar out []string\n\tfor _, w := range strings.Fields(t) {\n\t\tif IsToken(w) {','\tvar out []string\n\tfor _, w := range strings.Fields(t) {\n\t\tif false && IsToken(w) {')],
    GS+"'TestEveryClassTheCatalogRaisesAsksInPlainWords'")

# Codex on #838 (P2): an uppercase hash or a token in a title
run("the sanitizer reads lowercase hex only", [(PL,'var hexRunRe = regexp.MustCompile(`(?i)[0-9a-f]{8,}`)','var hexRunRe = regexp.MustCompile(`[0-9a-f]{8,}`)')],
    GS+"'TestATitleCarriesNoHashOrTokenAboveTheRule'")
run("a token in a title reaches the quoted line", [(PL,'\t\tif IsToken(w) {\n\t\t\twords[i] = "…"','\t\tif false && IsToken(w) {\n\t\t\twords[i] = "…"')],
    GS+"'TestATitleCarriesNoHashOrTokenAboveTheRule'")

# Codex on #838 (P1): the question is redacted at composition and in status
run("the question, its lines and its title not redacted at composition", [
    (PL,'\treturn redact.Block(strings.Join(append(PlainLines(r), "", Rule,','\treturn (strings.Join(append(PlainLines(r), "", Rule,'),
    (PL,'\t\tlines[i] = redact.Block(l)\n','\t\tlines[i] = l\n'),
    (PL,'\twords := strings.Fields(plain(redact.Block(s)))\n','\twords := strings.Fields(plain(s))\n'),
    (PL,'\tc.Title = redact.Block(c.Title)\n','\t_ = redact.Block\n'),
    (PL,'\t\tif f.Title != redact.Block(f.Title) {\n','\t\tif false {\n'),
    (PL,'Title: redact.Block(strings.Join(strings.Fields(title), " ")), Branch','Title: strings.Join(strings.Fields(title), " "), Branch')],
    GS+"'TestACanaryInATitleIsRedactedAtComposition' && "+CL+"'TestACanaryInATitleReachesNoQuestionStatusOrKey'")
run("the stored title not redacted (the question is)", [(PL,'\tc.Title = redact.Block(c.Title)\n','\t_ = redact.Block\n'),
    (PL,'\t\tif f.Title != redact.Block(f.Title) {\n','\t\tif false {\n'),
    (PL,'Title: redact.Block(strings.Join(strings.Fields(title), " ")), Branch','Title: strings.Join(strings.Fields(title), " "), Branch')],
    CL+"'TestACanaryInATitleReachesNoQuestionStatusOrKey'")
run("status does not redact gates[].question", [(CF,'\t\ts.Gates[i].Question = redact.Block(s.Gates[i].Question)\n','\t\t_ = s.Gates[i]\n')],
    CL+"'TestACanaryInATitleReachesNoQuestionStatusOrKey'")

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
run("the history re-ask asks for the tuple", [(CO,'deleted).\\nReply yes or no to this message.\\n\\n" + rec.Text','deleted).\\n\\n" + rec.Text + "\\n\\nReply here to confirm, with one line:\\n" + strings.Join([]string{a.Verb, a.GateID, a.Class, a.DigestPrefix}, " ")')],
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

# Row 6 with Codex's P1 on #838: the team confirmation, a bare yes whose
# direct reply target is the post the relay record marks as the plan
RL='internal/buzz/relay.go'
SR='internal/status/relay.go'
TT='pi/extensions/lib/team.ts'
run("the handler's team door ignores a bare yes to the plan", [(IN,'if plan := h.planPost(vctx, e, relay); plan != nil {','if plan := h.planPost(vctx, e, relay); false && plan != nil {')],
    BZ+"'TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam'")
run("any relay post taken as the plan post (its text decides)", [(IN,'\t\t\tif p.PlanFor != "" && slices.Contains(p.Events, parent) {\n\t\t\t\tplanFor = p.PlanFor\n','\t\t\tif slices.Contains(p.Events, parent) {\n\t\t\t\tplanFor = p.PlanFor + "any"\n')],
    BZ+"'TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam'")
run("the relay ignores the lead's team-plan label", [(RL,'\t\tif v, ok := strings.CutPrefix(l, gateverify.TeamPlanLabel); ok {','\t\tif v, ok := strings.CutPrefix(l, gateverify.TeamPlanLabel); false && ok {')],
    BZ+"'TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam|TestTheRelayReadsThePlanFromItsLabelOnly'")
run("the relay record drops plan_for when the post is published", [(RL,'At: now.UTC(), PlanFor: p.PlanFor})','At: now.UTC()})')],
    BZ+"'TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam'")
run("the relay record takes a malformed plan_for", [(SR,'!p.At.IsZero() && optional(eventIDRe, p.PlanFor)','!p.At.IsZero()')],
    BZ+"'TestABareYesRepliedToTheRecordedPlanPostConfirmsTheTeam'")
run("the lead takes a plan post another key signed", [(LS,'\t\tcase p.PubKey != pin.Body:\n','\t\tcase false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes a yes whose direct reply target is not the plan post", [(LS,'\t\tcase parent != p.ID:\n','\t\tcase parent != p.ID && false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes an altered plan post", [(LS,'\t\tcase !p.CheckID():\n','\t\tcase false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes a bare no as the confirmation", [(LS,'case bare && verb == "approve" && req.Post != nil:','case bare && verb != "" && req.Post != nil:')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes a post without its recorded plan text (Codex re-check)", [(LS,'if len(lines) == 0 || slices.ContainsFunc(lines, func(l string) bool { return !have[l] }) {','if len(lines) == 0 {')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost|TestTheAttachmentRoutesABareYesByTheRecordedPlan'")
run("the lead records a plan text from any peer", [(LS,'\tif piPID <= 1 || pid != piPID {\n\t\treturn leadStartReply{Outcome: "refused", Refusal: ReasonPeerNotLeadPi, Detail: fmt.Sprintf("pid %d is not the pi process this lead spawned (pid %d); only','\tif false {\n\t\treturn leadStartReply{Outcome: "refused", Refusal: ReasonPeerNotLeadPi, Detail: fmt.Sprintf("pid %d is not the pi process this lead spawned (pid %d); only')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("pi sends the plan reply the lead did not record", [(TT,'if (rec.outcome !== "recorded") return','if (rec.outcome === "never") return')],
    PI+"test/team.test.ts")
run("the attachment routes a plan yes by prefix", [(TC,'\t\tif plan != nil {\n\t\t\t_, err = proposalByHex(root, plan.For, at)','\t\tif false {\n\t\t\t_, err = proposalByHex(root, plan.For, at)')],
    CL+"'TestTheAttachmentRoutesABareYesByTheRecordedPlan'")
run("the attachment hands the lead the yes without the post", [(TC,'\t\treq.Post = plan.Raw\n','\t\t_ = plan\n')],
    CL+"'TestTheAttachmentRoutesABareYesByTheRecordedPlan'")
run("the lead's process sends the plan reply without its label", [(TT,'"--id", replyTo, "--labels", `${TEAM_PLAN_LABEL}${hex}`, "--json"','"--id", replyTo, "--json"')],
    PI+"test/team.test.ts")

# Codex P1 on #838: the lead refuses conflicting thread references
run("the lead takes a confirmation in a gate question's thread", [(LS,'if anchors := s.gateAnchors(); slices.ContainsFunc(refs,','if anchors := s.gateAnchors(); false && slices.ContainsFunc(refs,')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")
run("the lead takes a yes naming more than the plan's thread", [(LS,'\t\tcase slices.ContainsFunc(refs, func(r string) bool { return r != p.ID && !slices.Contains(nostr.ThreadRefs(p), r) }):\n','\t\tcase false:\n')],
    CL+"'TestTheLeadWitnessesABareYesRepliedToThePlanPost'")

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
run("the answer key shows the identifiers only", [(KS,'\t\tconst shown = plain ? `${plain}\\n${ids}` : ids;\n','\t\tconst shown = ids;\n')],
    PI+"test/keys.test.ts")
run("the answer key finds no rule in the text", [(KS,'\tconst at = (text ?? "").indexOf(`\\n\\n${RULE}\\n`);\n','\tconst at = -1;\n')],
    PI+"test/keys.test.ts")

print(f"\n{RUNS[1]} of {RUNS[0]} reverts fail their rows")
