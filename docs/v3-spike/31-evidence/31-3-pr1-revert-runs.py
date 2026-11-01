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
# section 2: the witness (no file is authority)
run("the start without the witness",
 [('internal/cli/lead_start.go','\trec, err := s.witnessFor(strings.TrimSpace(req.Digest))','\trec, err := confirmRecord{At: time.Now()}, error(nil)')],
 C+"'TestTheStartAcceptsOnlyAWitnessed|TestTheApplyReaches'")
run("no 30-minute bound on the witness",
 [('internal/cli/lead_start.go','\tcase confirmNow().Sub(rec.At) > ConfirmTTL:','\tcase false:')],
 C+"TestTheStartAcceptsOnlyAWitnessed")
run("a witness of another digest taken for this one",
 [('internal/cli/lead_start.go','\trec, ok := s.witnessed[digest]\n','\trec, ok := s.witnessed[digest]\n\tfor _, r := range s.witnessed {\n\t\trec, ok = r, true\n\t}\n')],
 C+"TestTheStartAcceptsOnlyAWitnessed")
run("the lead's log read for authority",
 [('internal/cli/lead_start.go','\trec, ok := s.witnessed[digest]\n\ts.wmu.Unlock()\n','\trec, ok := s.witnessed[digest]\n\ts.wmu.Unlock()\n\tif h, herr := digestHex(digest); !ok && herr == nil {\n\t\tif raw, rerr := os.ReadFile(confirmPath(s.root, h)); rerr == nil && json.Unmarshal(raw, &rec) == nil {\n\t\t\tok = true\n\t\t}\n\t}\n')],
 C+"TestTheStartAcceptsOnlyAWitnessed")
run("the refusal naming one door (no Buzz door)",
 [('internal/cli/team_confirm.go','; (2) send the signed Buzz DM \\"yes %s\\" (digest %s)", prefix, prefix, digest)',' (digest %s)", prefix, digest)')],
 C+"TestTheStartAcceptsOnlyAWitnessed")
# the TUI door, witnessed from the lead's own pi
run("the TUI door from any peer",
 [('internal/cli/lead_start.go','\tif piPID <= 1 || pid != piPID {','\tif false {')],
 C+"TestTheTUIDoorIsWitnessed")
run("the TUI door ignoring pi's start stamp",
 [('internal/cli/lead_start.go','\tif now, ok := confirmStamp(pid); !ok || now != piStamp {','\tif now, ok := confirmStamp(pid); false && (!ok || now != piStamp) {')],
 C+"TestTheTUIDoorIsWitnessed")
run("pi's pid not reported at the spawn",
 [('internal/cli/pi_exec_unix.go','\tif piStartedHook != nil {\n\t\t// the solo lead records its pi (pid and start stamp) at the spawn\n\t\tpiStartedHook(cmd.Process.Pid)\n\t}\n','')],
 C+"TestTheTUIDoorIsWitnessed")
run("the solo lead not recording its pi",
 [('internal/cli/pi_lead.go','\t\tpiStartedHook = func(pid int) {\n\t\t\tstamp, _ := procinfo.StartToken(pid)\n\t\t\tstarter.setPi(pid, stamp)\n\t\t}\n','\t\t_ = procinfo.StartToken\n')],
 C+"TestTheSoloLeadServes")
# the shell door, read by the lead from the kernel (and the owner's apply, the act)
run("no lead-start.json (the shell apply cannot see the running lead)",
 [('internal/cli/lead_start.go','\tif err := atomicfile.Write(filepath.Join(s.root, "extensions", "amq-squad", leadStartRecordName), append(rec, \'\\n\'), 0o600); err != nil {','\tif err := atomicfile.Write(filepath.Join(s.root, "extensions", "amq-squad", "elsewhere.json"), append(rec, \'\\n\'), 0o600); err != nil {')],
 C+"TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot")
run("the shell apply with no lead check",
 [('internal/cli/team.go','\tif err := withNoLead(dir, strings.TrimSpace(digest), func() error { return b.plan.Apply(strings.TrimSpace(digest)) }); err != nil {','\tif err := b.plan.Apply(strings.TrimSpace(digest)); err != nil {')],
 C+"TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot")
run("the shell apply ignoring a running solo lead",
 [('internal/cli/team_confirm.go','\t\tc.Close()\n\t\treturn fmt.Errorf("%s: the solo lead of %s is running','\t\tc.Close()\n\t\tif true {\n\t\t\tbreak\n\t\t}\n\t\treturn fmt.Errorf("%s: the solo lead of %s is running')],
 C+"TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot")
run("the shell apply ignoring a team lead's lock",
 [('internal/cli/team_confirm.go','\tif held {','\tif false && held {')],
 C+"TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot")
run("the shell apply's probe failing open on an unreachable record",
 [('internal/cli/team_confirm.go','\t\tif derr != nil {\n\t\t\treturn fmt.Errorf("%s: %s names a solo lead','\t\tif derr != nil {\n\t\t\tbreak\n\t\t}\n\t\tif false {\n\t\t\treturn fmt.Errorf("%s: %s names a solo lead')],
 C+"TestTheShellApplyProbeFailsClosed")
run("the shell apply probing only the proposed session's lock",
 [('internal/cli/team_confirm.go','filepath.Join(project, ".agent-mail", "*", "extensions"','filepath.Join(project, ".agent-mail", "s1", "extensions"')],
 C+"TestTheShellApplyProbeFailsClosed")
run("the lead lock step not taking the apply lock",
 [('internal/cli/up.go','\tunlock, err := takeApplyLock(project)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\tdefer unlock()\n\treturn takeLeadLock(root)','\t_ = project\n\treturn takeLeadLock(root)')],
 C+"TestTheShellApplyProbeFailsClosed")
run("the shell apply not holding the apply lock through the write",
 [('internal/cli/team_confirm.go','\tunlock, err := takeApplyLock(project)\n\tif err != nil {\n\t\treturn err\n\t}\n\tdefer unlock()\n\tif err := noLeadHolds(project, digest); err != nil {','\tif err := noLeadHolds(project, digest); err != nil {')],
 C+"TestTheShellApplyProbeFailsClosed")
run("the discovery record left after a clean exit",
 [('internal/cli/lead_start.go','\t\t_ = os.Remove(filepath.Join(s.root, "extensions", "amq-squad", leadStartRecordName))\n','')],
 C+"TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot")
run("up without the under-a-lead check",
 [('internal/cli/up.go','\tif err := underSoloLead("up", true, true); err != nil {','\tif err := error(nil); err != nil {')],
 C+"TestUpRefusesUnderASoloLead")
run("up not reading AM_ME",
 [('internal/cli/up.go','underSoloLead("up", true, true)','underSoloLead("up", true, false)')],
 C+"TestUpRefusesUnderASoloLead")
run("up not refusing a lead ancestor",
 [('internal/cli/up.go','underSoloLead("up", true, true)','underSoloLead("up", false, true)')],
 C+"TestUpRefusesUnderASoloLead")
run("lead --team without the under-a-lead check",
 [('internal/cli/pi_lead.go','\t\tif err := underSoloLead("lead --team", true, false); err != nil {','\t\tif err := error(nil); err != nil {')],
 C+"TestUpRefusesUnderASoloLead")
run("lead --team not refusing a lead ancestor",
 [('internal/cli/pi_lead.go','underSoloLead("lead --team", true, false)','underSoloLead("lead --team", false, false)')],
 C+"TestUpRefusesUnderASoloLead")
run("scheduler run without the under-a-lead check",
 [('internal/cli/scheduler_run.go','\tif err := underSoloLead("scheduler run", false, !byLead); err != nil {','\tif err := error(nil); err != nil {')],
 C+"TestUpRefusesUnderASoloLead")
run("the lead's pi not recognized",
 [('internal/cli/lead_start.go','\t\tif strings.Contains(a, "/.amq-squad/runtime/pi-") {','\t\tif false && strings.Contains(a, "/.amq-squad/runtime/pi-") {')],
 C+"TestUpRefusesUnderASoloLead")
run("scheduler run refusing for a lead ancestor (the scheduler lead --team starts)",
 [('internal/cli/scheduler_run.go','underSoloLead("scheduler run", false, !byLead)','underSoloLead("scheduler run", true, !byLead)')],
 C+"TestUpRefusesUnderASoloLead")
run("the plan-file apply with no lead check",
 [('internal/cli/team.go','\t\t\tif err := withNoLead(p.ProjectDir, *o.digest, write); err != nil {','\t\t\tif err := write(); err != nil {')],
 C+"TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot")
run("the socket accepting the dropped confirm-shell kind",
 [('internal/cli/lead_start.go','\tcase req.Kind == confirmShellKind:\n','\tcase false && req.Kind == confirmShellKind:\n')],
 C+"TestTheShellDoorIsGone")
# the Buzz door, verified by the lead from the event bytes, with no connection
run("the lead not recomputing the event id from the bytes",
 [('internal/cli/lead_start.go','\tcase !e.CheckID():\n','\tcase false:\n')],
 C+"TestTheBuzzDoorIsWitnessed")
run("the lead taking another key's yes",
 [('internal/cli/lead_start.go','\tcase e.PubKey != pin.Owner:','\tcase false:')],
 C+"TestTheBuzzDoorIsWitnessed")
run("the lead taking an hour-old yes (no window)",
 [('internal/cli/lead_start.go','\tcase e.CreatedAt < now.Add(-buzz.RouteWindow).Unix() || e.CreatedAt > now.Add(buzz.FutureSkew).Unix():','\tcase false:')],
 C+"TestTheBuzzDoorIsWitnessed")
run("the lead witnessing a replayed event (no once-only)",
 [('internal/cli/lead_start.go','\tif s.seen[e.ID] || eventLogged(s.root, e.ID) {','\tif false {')],
 C+"TestTheBuzzDoorIsWitnessed")
run("the once-only not kept across a restart (no event log read)",
 [('internal/cli/lead_start.go','\tif s.seen[e.ID] || eventLogged(s.root, e.ID) {','\tif s.seen[e.ID] {')],
 C+"TestTheBuzzDoorIsWitnessed")
run("the once-only's check and mark not under one lock",
 [('internal/cli/lead_start.go','\ts.wmu.Lock()\n\tdefer s.wmu.Unlock()\n\tif s.seen[e.ID] || eventLogged(s.root, e.ID) {','\ts.wmu.Lock()\n\tseenNow := s.seen[e.ID] || eventLogged(s.root, e.ID)\n\ts.wmu.Unlock()\n\tif seenNow {'),
  ('internal/cli/lead_start.go','\ts.seen[e.ID] = true\n\treturn s.witnessLocked(confirmRecord{','\ts.seen[e.ID] = true\n\treturn s.witness(confirmRecord{')],
 "go test -count=5 ./internal/cli/ -run TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency")
run("the Buzz door's witness without the event id",
 [('internal/cli/lead_start.go','Proof: confirmProof{EventID: e.ID}})','Proof: confirmProof{}})')],
 C+"'TestTheBuzzDoorIsWitnessed|TestTheOwnersSignedYes'")
# row 1b: the guard
run("the guard letting a seat run team confirm and apply",
 [('internal/cli/hook_guard.go','\tcase cmd == "amq-squad" && sub[0] == "team" && (sub[1] == "confirm" || sub[1] == "apply"):\n\t\ta.ownerOnly = "amq-squad team " + sub[1]\n','')],
 C+"TestGuardRefusesTheOwnersVerbs")
run("the guard letting a write into the records",
 [('internal/cli/hook_guard.go','\t\t\tif confirmRecordPath(w, a.env.cwd) {','\t\t\tif false && confirmRecordPath(w, a.env.cwd) {')],
 C+"TestGuardRefusesTheOwnersVerbs")
# row 1d: the Buzz door
run("the handler routing the yes (no Buzz door)",
 [('internal/buzz/inbound.go','\tif prefix, ok := gateverify.ParseTeamYes(content); ok && h.Confirm != nil {','\tif prefix, ok := gateverify.ParseTeamYes(content); false && ok && h.Confirm != nil {')],
 G+"-run TestTheBuzzDoor ./internal/buzz/")
run("the yes confirmed before the owner's signature is checked",
 [('internal/buzz/inbound.go','\tif code := h.ownerEvent(e); code != "" {','\tif prefix, ok := gateverify.ParseTeamYes(content); ok && h.Confirm != nil {\n\t\th.teamYes(ctx, &hd, e, raw, prefix)\n\t\treturn hd\n\t}\n\tif code := h.ownerEvent(e); code != "" {')],
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
run("the socket taking any process (no caller chain)",
 [('internal/cli/lead_start.go','\t\tchain, err := callerChain(pid, s.self)\n\t\tif err != nil {','\t\tchain, err := callerChain(pid, s.self)\n\t\tif false && err != nil {')],
 C+"TestTheApplyReaches")
run("the caller's stamps not re-read before the act",
 [('internal/cli/lead_start.go','\t\tif err := restamp(chain); err != nil {\n\t\t\treply.Refusal','\t\tif err := restamp(chain); false && err != nil {\n\t\t\treply.Refusal')],
 C+"TestTheStartSocketProves")
run("the caller's reparent mid-hop not checked",
 [('internal/cli/lead_start.go','\t\tif again, ok := confirmParent(p); !ok || again != parent {\n\t\t\treturn nil, fmt.Errorf("%s: pid %d was reparented while the lead read it"','\t\tif again, ok := confirmParent(p); false && (!ok || again != parent) {\n\t\t\treturn nil, fmt.Errorf("%s: pid %d was reparented while the lead read it"')],
 C+"TestTheStartSocketProves")
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
 [('internal/cli/lead_start.go','\t\t\tlock, err := UpLeadLock(s.project, root)\n\t\t\tif err != nil {','\t\t\tlock, err := UpLeadLock(s.project, root)\n\t\t\tif err != nil && false {')],
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
 [('internal/cli/pi_lead.go','\t\tlock, err := UpLeadLock(teamProject, teamIdentity.Root)','\t\tlock, err := takeLeadLock(teamIdentity.Root)')],
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
# the extension (row 1c, 1f, 3)
run("the TUI door taking any prefix",
 [('pi/extensions/lib/team.ts','if (prefix.toLowerCase() !== p.prefix) return','if (false && prefix.toLowerCase() !== p.prefix) return')],
 P+"test/team.test.ts")
run("the TUI door sending no pane",
 [('pi/extensions/lib/team.ts','pane: process.env.TMUX_PANE ?? "" });','pane: "" });')],
 P+"test/team.test.ts")
run("the TUI door showing a confirmation the lead refused",
 [('pi/extensions/lib/team.ts','\t\tif (r.outcome !== "witnessed") return','\t\tif (false) return')],
 P+"test/team.test.ts")
run("the shell's argv form unchecked",
 [('pi/extensions/lib/team.ts','\t\tif (cmd.includes("tmux")) return refusalText("tmux");','\t\tif (false) return refusalText("tmux");')],
 P+"test/team.test.ts")
run("the shell's line not dequoted",
 [('pi/extensions/lib/team.ts','\tconst d = dequote(line);','\tconst d = line.toLowerCase();')],
 P+"test/team.test.ts")
run("tmux only as a whole token",
 [('pi/extensions/lib/team.ts','\tif (tokens.some((t) => t.includes("tmux"))) return "tmux";','\tif (tokens.some((t) => t === "tmux")) return "tmux";')],
 P+"test/team.test.ts")
run("no rule for an expansion joined to letters",
 [('pi/extensions/lib/team.ts','|| d.includes("`")) return "an expansion joined to letters";','&& false) return "an expansion joined to letters";')],
 P+"test/team.test.ts")
run("no rule for eval, exec -a and env -S",
 [('pi/extensions/lib/team.ts','\tif (runs && tokens.some((t) => /t.*m.*u.*x/.test(t))) return "tmux";','\tif (false && runs) return "tmux";')],
 P+"test/team.test.ts")
run("the guard's second layer not applied to the solo lead",
 [('internal/cli/hook_guard.go','\tif soloLeadIdentity(id) && slices.Contains(','\tif false && soloLeadIdentity(id) && slices.Contains(')],
 C+"TestTheGuardRefusesTheSoloLeads")
run("the guard's second layer not dequoting",
 [('internal/cli/hook_guard.go','strings.NewReplacer(`$\'`, "", `"`, "", `\'`, "", `\\`, "")','strings.NewReplacer()')],
 C+"TestTheGuardRefusesTheSoloLeads")
run("the shell's refusal not wired into tool_call",
 [('pi/extensions/amq-squad.ts','\t\tconst owner = cfg.solo ? soloShellRefusal(event.toolName, event.input) : undefined;','\t\tconst owner = undefined as string | undefined;')],
 P+"test/team.test.ts")
run("propose's plan not on stdin",
 [('pi/extensions/lib/team.ts','{ stdin: JSON.stringify(plan), timeoutMs','{ timeoutMs')],
 P+"test/team.test.ts")
run("any amq-squad verb allowed in the solo lead's shell",
 [('pi/extensions/lib/team.ts','	return AMQ_SQUAD_ALLOW.includes(words[0] ?? "") ||','	return true ||')],
 P+"test/team.test.ts")
run("the extension's allowlist not the table's (up added)",
 [('pi/extensions/lib/team.ts','export const AMQ_SQUAD_ALLOW = ["status",','export const AMQ_SQUAD_ALLOW = ["up", "status",')],
 P+"test/team.test.ts")
run("any amq-squad verb allowed by the guard's second layer",
 [('internal/cli/hook_guard.go','	return slices.Contains(soloAmqSquadAllow, one) ||','\treturn one != "\\x00" ||')],
 C+"TestTheGuardRefusesTheSoloLeads")
run("the plan block still naming the dropped shell door",
 [('pi/extensions/lib/team.ts','  (or the signed Buzz DM','  (or amq-squad team confirm ${p.digest} in your shell, or the signed Buzz DM')],
 P+"test/team.test.ts")
run("the team tools and /squad confirm given to the team lead too",
 [('pi/extensions/amq-squad.ts','\telse if (cfg.solo) installSoloTeam(pi, cfg);','\tif (cfg.lead || cfg.solo) installSoloTeam(pi, cfg);')],
 P+"test/keys.test.ts")
print(f"{RUNS[0]} runs, {RUNS[1]} caught, {RUNS[0]-RUNS[1]} missed")
