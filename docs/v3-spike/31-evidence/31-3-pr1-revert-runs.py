# The revert runs of 31-3 PR 1 (docs/v3-spike/31-evidence/31-3-squad-verbs.md,
# the revert runs section). Each run reverts one change in the working tree,
# runs the rows that must catch it, prints whether they failed and the failing
# tests, and restores the touched files from git. Run it from a clean checkout
# of the branch:
#   python3 docs/v3-spike/31-evidence/31-3-pr1-revert-runs.py
# It refuses a tree with any staged, unstaged or untracked change, since it
# restores every file it touched to HEAD exactly (index and working tree), and
# it restores them in a finally, even when a run fails or is interrupted.
import subprocess, os, sys
os.chdir(subprocess.check_output(['git','rev-parse','--show-toplevel'],text=True).strip())
def dirty():
    return subprocess.run(['git','status','--porcelain','--untracked-files=all'],capture_output=True,text=True,check=True).stdout
if dirty():
    sys.exit("refused: the tree has staged, unstaged or untracked changes; the runs restore files to HEAD and would drop them:\n"+dirty())
G='go test -count=1 '
C=G+'./internal/cli/ -run '
P="cd pi && node --test "
RUNS=[0,0]  # runs, runs caught
def run(name, edits, cmd):
    files=set()
    try:
        for (p,a,b) in edits:
            s=open(p).read()
            assert a in s, (name,p,a[:70])
            files.add(p)
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
        # back to HEAD exactly, whatever happened above
        if files:
            subprocess.run(['git','checkout','HEAD','--']+sorted(files),check=True)
    if dirty():
        sys.exit("the tree is not clean after a restore:\n"+dirty())

# section 1: team propose --from PLAN
run("a plan read as a squad.yaml draft (no plan branch)",
 [('internal/cli/team.go','\t\tif team.IsPlanText(text) {','\t\tif false && team.IsPlanText(text) {')],
 C+"TestTeamProposeFromAPlan")
run("the plan's effort dropped from squad.yaml",
 [('internal/team/plan.go','Model: seat.Model, Effort: seat.Effort})','Model: seat.Model})')],
 G+"-run TestThePlanRendersTheDocumentedSquadYAML ./internal/team/ && "+C+"TestTeamProposeFromAPlan")
run("the plan read leniently (unknown keys kept quiet)",
 [('internal/team/plan.go','\tdec.DisallowUnknownFields()\n','')],
 G+"-run TestThePlanRendersTheDocumentedSquadYAML ./internal/team/")
run("the proposal not recorded",
 [('internal/cli/team.go','\tif err := writeProposal(root, prop); err != nil {','\tif err := error(nil); root == "" && err != nil {')],
 C+"TestTeamProposeFromAPlan")
# section 2: the record and its check
run("the recorded proposal applied with no confirmation",
 [('internal/cli/team.go','\tif _, err := checkConfirmation(root, digest, dir); err != nil {\n\t\treturn err\n\t}\n\tprop, err','\tprop, err')],
 C+"'TestTeamApplyWithout|TestGuardRefusesTheConfirm'")
run("a plan file applied with no confirmation",
 [('internal/cli/team.go','\tif apply && plan != nil {','\tif false && apply && plan != nil {')],
 C+"TestTeamApplyWithout")
run("no 30-minute bound",
 [('internal/cli/team_confirm.go','age > ConfirmTTL || age < -confirmFutureSkew','age < -confirmFutureSkew')],
 C+"TestTeamApplyWithout")
run("a record without its door's proof accepted",
 [('internal/cli/team_confirm.go','\tif why := proofMissing(rec); why != "" {','\tif why := proofMissing(rec); false && why != "" {')],
 C+"TestTeamApplyWithout")
run("the refusal naming two doors (no Buzz door)",
 [('internal/cli/team_confirm.go','; (3) send the signed Buzz DM \\"yes %s\\"", prefix, digest, project, prefix)','", prefix, digest, project)')],
 C+"TestTeamApplyWithout")
# section 3b (i): door (a)'s kernel-level refusal
run("door (a) blind to a lead or seat argv",
 [('internal/cli/team_confirm.go','\t\t\tif leadOrSeatArgv(argv) {','\t\t\tif false && leadOrSeatArgv(argv) {')],
 C+"TestTeamConfirmRefuses")
run("door (a) blind to an ancestor's AM_ME",
 [('internal/cli/team_confirm.go','ok && amqIdentityEnv(env) {','ok && false && amqIdentityEnv(env) {')],
 C+"TestTeamConfirmRefuses")
run("door (a) without a controlling terminal",
 [('internal/cli/team_confirm.go','\tif !confirmTerminal() {','\tif false && !confirmTerminal() {')],
 C+"TestTeamConfirmRefuses")
run("door (a) passing an orphaned chain",
 [('internal/cli/team_confirm.go','\tif !leader {','\tif false && !leader {')],
 C+"TestTeamConfirmRefuses")
run("the shell door's record without its pid chain (3c)",
 [('internal/cli/team_confirm.go','Proof: confirmProof{PIDChain: chain}}','Proof: confirmProof{PIDChain: chain[:0]}}')],
 C+"TestTeamConfirmRefuses")
# row 1b: the guard
run("the guard letting a seat run team confirm and apply",
 [('internal/cli/hook_guard.go','\tcase cmd == "amq-squad" && sub[0] == "team" && (sub[1] == "confirm" || sub[1] == "apply"):\n\t\ta.ownerOnly = "amq-squad team " + sub[1]\n','')],
 C+"TestGuardRefusesTheConfirm")
run("the guard letting a write into the records",
 [('internal/cli/hook_guard.go','\t\t\tif confirmRecordPath(w, a.env.cwd) {','\t\t\tif false && confirmRecordPath(w, a.env.cwd) {')],
 C+"TestGuardRefusesTheConfirm")
# row 1d: the Buzz door
run("the Buzz door taking any proposal (prefix ignored)",
 [('internal/cli/team_confirm.go','proposedDirName, prefix+"*.json"))','proposedDirName, "*.json"))')],
 C+"TestBuzzDoorConfirms")
run("the Buzz door's record without its event id (3c)",
 [('internal/cli/team_confirm.go','Proof: confirmProof{EventID: eventID}}','Proof: confirmProof{}}')],
 C+"'TestBuzzDoorConfirms|TestTheOwnersSignedYes'")
run("the handler routing the yes (no Buzz door)",
 [('internal/buzz/inbound.go','\tif prefix, ok := gateverify.ParseTeamYes(content); ok && h.Confirm != nil {','\tif prefix, ok := gateverify.ParseTeamYes(content); false && ok && h.Confirm != nil {')],
 G+"-run TestTheBuzzDoor ./internal/buzz/")
run("the yes confirmed before the owner's signature is checked",
 [('internal/buzz/inbound.go','\tif code := h.ownerEvent(e); code != "" {','\tif prefix, ok := gateverify.ParseTeamYes(content); ok && h.Confirm != nil {\n\t\th.teamYes(ctx, &hd, e, prefix)\n\t\treturn hd\n\t}\n\tif code := h.ownerEvent(e); code != "" {')],
 G+"-run TestTheBuzzDoor ./internal/buzz/")
run("a redelivered yes confirmed again (no once-only mark)",
 [('internal/buzz/inbound.go','\tif err := h.seen.put(e.ID, "team", now); err != nil {','\tif err := error(nil); err != nil {')],
 G+"-run TestTheBuzzDoor ./internal/buzz/")
run("the yes parsed loosely",
 [('internal/gateverify/teamyes.go','`^yes ([0-9a-f]{16})$`','`^yes ([0-9a-f]{16})`')],
 G+"-run TestTheBuzzDoor ./internal/buzz/")
run("remote attach's handler without the confirmer",
 [('internal/cli/remote.go',' Confirm: confirmByPrefix,','')],
 C+"TestTheOwnersSignedYes")
# section 3: the start in the lead's process
run("the lead starting with no confirmation",
 [('internal/cli/lead_start.go','\trec, err := checkConfirmation(s.root, req.Digest, s.project)','\trec, err := confirmRecord{At: time.Now()}, error(nil)')],
 C+"TestTheApplyReaches")
run("the socket taking any process (no descent check)",
 [('internal/cli/lead_start.go','\tcase !descendsFrom(pid, s.self):','\tcase false && !descendsFrom(pid, s.self):')],
 C+"TestTheApplyReaches")
run("the lead taking another repository's request",
 [('internal/cli/lead_start.go','\tif filepath.Clean(req.Project) != s.project {','\tif false && filepath.Clean(req.Project) != s.project {')],
 C+"TestTheStartOfAConfirmedTeam")
run("the mayor's checks not applied (a grant that lets everything)",
 [('internal/cli/lead_start.go','\t\tGrant: leadMayorGrant, RegistryPath','\t\tGrant: func() (grants.Resolved, error) {\n\t\t\tg, err := leadMayorGrant()\n\t\t\tg.Mode, g.Fleet = grants.ModeAuto, grants.Fleet{MaxSquads: 9, PermittedRepositories: []string{"owner/app", "other/repo"}}\n\t\t\treturn g, err\n\t\t}, RegistryPath')],
 C+"TestTheMayorsChecks")
run("no R40 fallback (the lead proposes, the owner starts)",
 [('internal/cli/lead_start.go','var r40Fallback = []string{fleet.ReasonNoFleetGrant, fleet.ReasonRepository, fleet.ReasonUnidentified, fleet.ReasonManualMode, fleet.ReasonProvisioning}','var r40Fallback = []string{}')],
 C+"TestTheMayorsChecks")
run("the start writing a digest the owner did not confirm",
 [('internal/cli/lead_start.go','\tif d != w.digest {','\tif false && d != w.digest {')],
 C+"TestTheStartRefusesAPlanThatChanged")
run("the squad files planned over the mayor's spelling of the checkout",
 [('internal/cli/lead_start.go','\td, err := w.teamSquadWriter.Propose(w.project, text)','\td, err := w.teamSquadWriter.Propose(projectPath, text)'),('internal/cli/lead_start.go','func (w *confirmedSquadWriter) Propose(_ string, text string) (string, error) {','func (w *confirmedSquadWriter) Propose(projectPath string, text string) (string, error) {')],
 C+"TestTheStartOfAConfirmedTeam")
run("the start ignoring a held lead lock",
 [('internal/cli/lead_start.go','\t\t\tlock, err := UpLeadLock(root)\n\t\t\tif err != nil {','\t\t\tlock, err := UpLeadLock(root)\n\t\t\tif err != nil && false {')],
 C+"TestTheStartRefusesLeadRunning")
run("the launch budget not starting at the confirmation",
 [('internal/cli/lead_start.go','\t\tos.Setenv(leadStartEnv, l.at.UTC().Format(time.RFC3339Nano))\n','')],
 C+"TestTheStartOfAConfirmedTeam")
run("a second start in the same lead",
 [('internal/cli/lead_start.go','\tif s.lock != nil {\n\t\treturn refused(schedproc.ReasonLeadRunning','\tif false && s.lock != nil {\n\t\treturn refused(schedproc.ReasonLeadRunning')],
 C+"TestTheStartOfAConfirmedTeam")
# row 6: one implementation
run("the session step uncounted (up's and the apply's path not shared)",
 [('internal/cli/up.go','\tcountShared("session")\n','')],
 C+"'TestUpAndTheLeadTeam|TestTheStartOfAConfirmedTeam'")
run("lead --team taking the lead lock outside the shared function",
 [('internal/cli/pi_lead.go','\t\tlock, err := UpLeadLock(teamIdentity.Root)','\t\tlock, err := takeLeadLock(teamIdentity.Root)')],
 C+"TestUpAndTheLeadTeam")
run("lead --team starting the scheduler outside the shared function",
 [('internal/cli/pi_lead.go','\t\tsup, err := UpScheduler(teamProject,','\t\tsup, err := startLeadScheduler(teamProject,')],
 C+"TestUpAndTheLeadTeam")
run("the solo lead not a lead parent of its scheduler",
 [('internal/cli/lead_scheduler.go','\treturn len(f) == 1 && filepath.Base(f[0]) == "amq-squad" || len(f) > 1','\treturn len(f) > 1'),('internal/cli/lead_scheduler.go','f[1] == "lead"\n}','f[1] == "lead" && slices.Contains(f, "--team")\n}'),('internal/cli/lead_scheduler.go','\t"path/filepath"\n','\t"path/filepath"\n\t"slices"\n')],
 C+"TestASoloLeadIsALeadParent")
run("the lead check widened to the binary (any amq-squad parent)",
 [('internal/cli/lead_scheduler.go','filepath.Base(f[0]) == "amq-squad" && f[1] == "lead"','filepath.Base(f[0]) == "amq-squad"')],
 C+"TestASoloLeadIsALeadParent")
run("the solo lead's pi without the solo flag",
 [('internal/cli/pi_lead.go','\t\tsolo["AMQ_SQUAD_SOLO_LEAD"] = "1"\n','')],
 C+"TestTheSoloLeadServes")
# procinfo
run("the environment read stopping at argv",
 [('internal/procinfo/environ.go','\tfor n := 0; n < argc; n++ {','\tfor n := 0; n < argc-1; n++ {')],
 G+"-run TestEnviron ./internal/procinfo/")
# the extension (row 1c, 1f, 3)
run("the TUI door taking any prefix",
 [('pi/extensions/lib/team.ts','if (prefix.toLowerCase() !== p.prefix) return','if (false && prefix.toLowerCase() !== p.prefix) return')],
 P+"test/team.test.ts")
run("the TUI door's record without the pid (3c)",
 [('pi/extensions/lib/team.ts','proof: { pane, pid } };','proof: { pane } };')],
 P+"test/team.test.ts")
run("the shell's argv form unchecked",
 [('pi/extensions/lib/team.ts','\t\tif (cmd === "tmux") return refusalText("tmux");','\t\tif (false) return refusalText("tmux");')],
 P+"test/team.test.ts")
run("the shell's token form reading argv[0] only",
 [('pi/extensions/lib/team.ts','\tif (tokens.some((t) => base(t) === "tmux")) return refusalText("tmux");','\tif (base(tokens[0] ?? "") === "tmux") return refusalText("tmux");')],
 P+"test/team.test.ts")
run("the shell's refusal not wired into tool_call",
 [('pi/extensions/amq-squad.ts','\t\tconst owner = cfg.solo ? soloShellRefusal(event.toolName, event.input) : undefined;','\t\tconst owner = undefined as string | undefined;')],
 P+"test/team.test.ts")
run("propose's plan not on stdin",
 [('pi/extensions/lib/team.ts','{ stdin: JSON.stringify(plan), timeoutMs','{ timeoutMs')],
 P+"test/team.test.ts")
run("the team tools and /squad confirm given to the team lead too",
 [('pi/extensions/amq-squad.ts','\telse if (cfg.solo) installSoloTeam(pi, cfg);','\tif (cfg.lead || cfg.solo) installSoloTeam(pi, cfg);')],
 P+"test/keys.test.ts")
print(f"{RUNS[0]} runs, {RUNS[1]} caught, {RUNS[0]-RUNS[1]} missed")
