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
run("the refusal naming two doors (no Buzz door)",
 [('internal/cli/team_confirm.go','; (3) send the signed Buzz DM \\"yes %s\\"", prefix, digest, project, prefix)','", prefix, digest, project)')],
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
run("the kernel check blind to a lead or seat argv",
 [('internal/cli/team_confirm.go','\t\t\tif leadOrSeatArgv(argv) {','\t\t\tif false && leadOrSeatArgv(argv) {')],
 C+"'TestTheShellDoorIsWitnessed|TestTheOwnersApplyIsTheConfirmation'")
run("the kernel check blind to an ancestor's AM_ME",
 [('internal/cli/team_confirm.go','ok && amqIdentityEnv(env) {','ok && false && amqIdentityEnv(env) {')],
 C+"'TestTheShellDoorIsWitnessed|TestTheOwnersApplyIsTheConfirmation'")
run("the kernel check without the peer's terminal",
 [('internal/cli/team_confirm.go','\tcase !has:\n','\tcase has && false:\n')],
 C+"'TestTheShellDoorIsWitnessed|TestTheOwnersApplyIsTheConfirmation'")
run("the kernel check passing an orphaned chain",
 [('internal/cli/team_confirm.go','\tif !leader {','\tif false && !leader {')],
 C+"'TestTheShellDoorIsWitnessed|TestTheOwnersApplyIsTheConfirmation'")
run("the shell door passing a descendant of the lead's own pid",
 [('internal/cli/team_confirm.go','\t\tif parent == lead {','\t\tif false && parent == lead {')],
 C+"TestTheShellDoorIsWitnessed")
run("the kernel check not re-reading the stamps at the end",
 [('internal/cli/team_confirm.go','\treturn chain, restampHops(chain)\n}','\treturn chain, nil\n}')],
 C+"TestTheShellDoorIsWitnessed")
run("the shell door's witness without the chain the lead read",
 [('internal/cli/lead_start.go','Proof: confirmProof{PIDChain: chain}})','Proof: confirmProof{PIDChain: chain[:0]}})')],
 C+"TestTheShellDoorIsWitnessed")
run("no lead-start.json (team confirm cannot find the lead)",
 [('internal/cli/lead_start.go','\tif err := atomicfile.Write(filepath.Join(s.root, "extensions", "amq-squad", leadStartRecordName), append(rec, \'\\n\'), 0o600); err != nil {','\tif err := atomicfile.Write(filepath.Join(s.root, "extensions", "amq-squad", "elsewhere.json"), append(rec, \'\\n\'), 0o600); err != nil {')],
 C+"TestTheShellDoorIsWitnessed")
run("the owner's apply without the kernel check (the act)",
 [('internal/cli/team.go','\tif _, err := ownerAncestry(confirmSelf(), 0); err != nil {\n\t\treturn err\n\t}\n\troot, err := soloRootOf(dir)','\troot, err := soloRootOf(dir)')],
 C+"TestTheOwnersApplyIsTheConfirmation")
run("the plan-file apply without the kernel check",
 [('internal/cli/team.go','\t\tif _, err := ownerAncestry(confirmSelf(), 0); err != nil {\n\t\t\treturn err\n\t\t}\n\t}\n\tif apply {','\t}\n\tif apply {')],
 C+"TestTheOwnersApplyIsTheConfirmation")
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
 [('internal/cli/lead_start.go','\tif replayed {','\tif false && replayed {')],
 C+"TestTheBuzzDoorIsWitnessed")
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
run("the kernel's terminal read taking tty_nr 0 as a terminal",
 [('internal/procinfo/environ.go','\treturn f[4] != "0", true','\treturn true, true')],
 G+"-run TestHasTTY ./internal/procinfo/")
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
run("the team tools and /squad confirm given to the team lead too",
 [('pi/extensions/amq-squad.ts','\telse if (cfg.solo) installSoloTeam(pi, cfg);','\tif (cfg.lead || cfg.solo) installSoloTeam(pi, cfg);')],
 P+"test/keys.test.ts")
print(f"{RUNS[0]} runs, {RUNS[1]} caught, {RUNS[0]-RUNS[1]} missed")
