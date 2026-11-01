# The revert runs of 31-3 PR 2 (docs/v3-spike/31-evidence/31-3-pr2-r53-first-turn.md,
# the revert runs section). Each run reverts one change in the working tree,
# runs the rows that must catch it, prints whether they failed and the failing
# tests, and restores the touched files from git. Run it from a clean checkout
# of the branch:
#   python3 docs/v3-spike/31-evidence/31-3-pr2-revert-runs.py
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
        try:
            r=subprocess.run(cmd,shell=True,capture_output=True,text=True,timeout=600)
        except subprocess.TimeoutExpired as e:
            # a row that hangs is a row that does not pass; say so
            r=subprocess.CompletedProcess(cmd,124,(e.stdout or b'').decode() if isinstance(e.stdout,bytes) else (e.stdout or ''),'timed out after 600 s\n')
        out=r.stdout+r.stderr
        lines=[l.rstrip() for l in out.splitlines() if l.strip().startswith(('--- FAIL','✖','not ok')) or 'build failed' in l or l.startswith('timed out')]
        RUNS[0]+=1
        RUNS[1]+=r.returncode!=0
        print(f"REVERT {name}: {'fails (the row catches it)' if r.returncode!=0 else 'PASSES (the row misses it)'}", flush=True)
        seen=set()
        for l in lines:
            if l in seen: continue
            seen.add(l); print('   '+l.strip(), flush=True)
    finally:
        # back to HEAD exactly, whatever happened above
        if files:
            subprocess.run(['git','checkout','HEAD','--']+sorted(files),check=True)
    if dirty():
        sys.exit("the tree is not clean after a restore:\n"+dirty())

# section 5's verb half: context --host
run("context --host ignored",
 [('internal/cli/hook_context.go','\tif *host {\n\t\td.Host = hostFacts()\n\t}','\t_ = host')],
 C+"TestContextHost")
run("the branch not read from git",
 [('internal/cli/hook_context.go','\t\t\th.Branch = out\n','\t\t\t_ = out\n')],
 C+"TestContextHost")
# Codex on #837, items 5 and 6: the host commands bounded, the facts redacted
run("the host commands with no deadline",
 [('internal/cli/hook_context.go','ctx, cancel := context.WithTimeout(context.Background(), contextHostDeadline)','ctx, cancel := context.WithCancel(context.Background())')],
 C+"TestContextHostKills")
run("the hanging command killed alone, not its group",
 [('internal/cli/hook_context.go','cmd.Cancel = func() error { killGuardGroup(cmd); return nil }','cmd.Cancel = func() error { return cmd.Process.Kill() }')],
 C+"TestContextHostKills")
run("the host commands' output not capped",
 [('internal/cli/hook_context.go','out := &cappedBuffer{max: contextHostOutputCap, over: cancel}','out := &cappedBuffer{max: 1 << 30, over: cancel}')],
 C+"TestContextHostKills")
run("a host fact leaving context --host unredacted",
 [('internal/cli/hook_context.go','\t\t*f = redact.Block(*f)\n','\t\t*f, _ = strings.Clone(*f), redact.Block\n')],
 C+"TestContextHostCarries")
run("the AMQ version not read",
 [('internal/cli/hook_context.go','if out, err := contextHostRun(h.Repository, amq, "--version"); err == nil {','if out, err := contextHostRun(h.Repository, amq, "--version"); err == nil && false {')],
 C+"TestContextHost")
# section 4: the phase marker
run("the yes writing no phase marker (the record)",
 [('internal/cli/lead_start.go','\t\tif err := writePhaseMarker(s.root, s.project, team, s.self); err != nil {','\t\tif err := error(nil); err != nil {')],
 C+"TestTheStartOfAConfirmedTeam")
# Codex on #837, item 2: the switch rides the reply whatever happened to the record
run("a failed record not logged phase_record_failed",
 [('internal/cli/lead_start.go','\t\t\t_ = schedproc.AppendDecision(s.root, confirmNow(), scheduler.Decision{Kind: KindPhaseRecordFailed,','\t\t\t_ = scheduler.Decision{Kind: KindPhaseRecordFailed,'),
  ('internal/cli/lead_start.go','Reason: phaseMarkerPath(s.root) + ": " + err.Error()})\n','Reason: phaseMarkerPath(s.root) + ": " + err.Error()}\n')],
 C+"TestAStartWhosePhaseRecord")
run("a failed record withholding the team identity from the reply",
 [('internal/cli/lead_start.go','Reason: phaseMarkerPath(s.root) + ": " + err.Error()})\n','Reason: phaseMarkerPath(s.root) + ": " + err.Error()})\n\t\t\tteam = nil\n')],
 C+"TestAStartWhosePhaseRecord")
run("the identity not resolved before the start (no team_identity refusal)",
 [('internal/cli/lead_start.go','\tif err != nil {\n\t\treturn refused(ReasonTeamIdentity,','\tif err != nil && false {\n\t\treturn refused(ReasonTeamIdentity,')],
 C+"TestAConfirmedSquadWithNoLead")
run("the start's reply without the team identity",
 [('internal/cli/lead_start.go','Roster: l.roster, Team: team}','Roster: l.roster}')],
 C+"TestTheStartOfAConfirmedTeam")
run("the phase query answering team before the start",
 [('internal/cli/lead_start.go','\t\tif team != nil {\n\t\t\treply = leadStartReply{Outcome: "team", Team: team}','\t\tif true {\n\t\t\treply = leadStartReply{Outcome: "team", Team: team}')],
 C+"TestThePhaseQuery")
run("a restarted solo lead not saying the offer",
 [('internal/cli/pi_lead.go','\t\t\t\t\tfmt.Fprintf(os.Stderr, "amq-squad lead: %s\\n", resumeOffer(m))\n','\t\t\t\t\t_ = m\n')],
 C+"TestASoloLeadRestartedInAMarked")
run("a marker for another repository read",
 [('internal/cli/lead_start.go','m.Phase == PhaseTeam && m.Project == project','m.Phase == PhaseTeam')],
 C+"TestASoloLeadRestartedInAMarked")
# section 4: the switch in the extension
run("the team tools declared before the yes",
 [('pi/extensions/lib/tools.ts','\t\tconst inactive = !cfg.lead;','\t\tconst inactive = false;')],
 P+"test/phase.test.ts")
run("the switch not changing the declared tools (no setActiveTools)",
 [('pi/extensions/lib/team.ts','\t\tthis.pi?.setActiveTools([...this.teamTools]);\n','')],
 P+"test/phase.test.ts")
run("no phase_team refusal of the solo set",
 [('pi/extensions/amq-squad.ts','\t\tif (solo?.phase === "team" && !solo.teamTools.includes(event.toolName)) {','\t\tif (false) {')],
 P+"test/phase.test.ts")
run("the lead's identity not switched to the squad's",
 [('pi/extensions/lib/team.ts','\t\tObject.assign(process.env, { AM_ROOT: m.root,','\t\tObject.assign({}, { AM_ROOT: m.root,')],
 P+"test/phase.test.ts")
run("the switch riding the marker file, not the lead's reply",
 [('pi/extensions/lib/team.ts','\t\tif (d.team) this.switchToTeam(d.team);\n','\t\tthis.switchToTeam(d.team ?? JSON.parse(fs.readFileSync(path.join(this.soloRoot, "extensions", "amq-squad", "phase.json"), "utf8")));\n')],
 P+"test/phase.test.ts")
run("amq_team_apply's success not switching",
 [('pi/extensions/lib/team.ts','\t\tif (d.team) this.switchToTeam(d.team);\n','\t\tif (d.team && false) this.switchToTeam(d.team);\n')],
 P+"test/phase.test.ts")
run("a session started after the yes not switching on the lead's answer",
 [('pi/extensions/lib/team.ts','\t\tif (t) team.switchToTeam(t);\n','\t\tif (t && false) team.switchToTeam(t);\n')],
 P+"test/phase.test.ts")
# Codex on #837, item 3: amq_team_apply runs alone; the solo tools recheck at run time
run("amq_team_apply run beside other calls (no apply_not_alone)",
 [('pi/extensions/lib/team.ts','const refused = team.phaseRefusal("amq_team_apply") ?? team.notAlone(id);','const refused = team.phaseRefusal("amq_team_apply");')],
 P+"test/phase.test.ts")
run("the assistant message's calls not in the batch",
 [('pi/extensions/lib/team.ts','\t\tfor (const c of e.message.content) if (c?.type === "toolCall" && typeof c.id === "string") team.batch.set(c.id, String(c.name));\n','')],
 P+"test/phase.test.ts")
run("the hooks' prepared calls not in the batch",
 [('pi/extensions/lib/team.ts','\t\tteam.batch.set(e.toolCallId, e.toolName);\n','')],
 P+"test/phase.test.ts")
run("the batch not reset at a turn's start",
 [('pi/extensions/lib/team.ts','\t\tteam.batch.clear();\n','')],
 P+"test/phase.test.ts")
run("amq_team_apply not a sequential tool",
 [('pi/extensions/lib/team.ts','\t\texecutionMode: "sequential",\n','')],
 P+"test/phase.test.ts")
run("the refusal not telling the model to call apply alone next turn",
 [('pi/extensions/lib/team.ts',' Call amq_team_apply again on your next turn with nothing else in the batch.`','`')],
 P+"test/phase.test.ts")
run("a solo tool's execute not rechecking the phase",
 [('pi/extensions/lib/team.ts','\t\t\tconst refused = team.phaseRefusal("amq_team_propose");\n\t\t\tif (refused) throw new Error(refused);\n','')],
 P+"test/phase.test.ts")
run("the restart's offer not said",
 [('pi/extensions/lib/team.ts','\t\tif (offered || team.phase === "team") return undefined;','\t\tif (true) return undefined;')],
 P+"test/phase.test.ts")
run("the restart's offer said every turn",
 [('pi/extensions/lib/team.ts','\t\toffered = true;\n','')],
 P+"test/phase.test.ts")
# section 5: the first turn
run("no host line",
 [('pi/extensions/amq-squad.ts','\t\tif (!cfg.lead) return undefined;\n\t\tconst line = await host.next();','\t\tif (true) return undefined;\n\t\tconst line = await host.next();')],
 P+"test/phase.test.ts")
run("the verbs run every turn (no key)",
 [('pi/extensions/lib/firstturn.ts','\t\tif (key === this.lastKey) return undefined;\n','')],
 P+"test/phase.test.ts")
run("an unchanged host line sent again",
 [('pi/extensions/lib/firstturn.ts','\t\tif (line === this.lastLine) return undefined;\n','')],
 P+"test/phase.test.ts")
run("the task store not in the host key",
 [('pi/extensions/lib/firstturn.ts','const paths = [path.join(ext, "tasks", ".generation"), ','const paths = [')],
 P+"test/phase.test.ts")
run("context asked without --host",
 [('pi/extensions/lib/firstturn.ts','hostOf(await contextOf(this.cfg, this.runner, true))','hostOf(await contextOf(this.cfg, this.runner, false))')],
 P+"test/phase.test.ts")
run("closed tasks shown as open",
 [('pi/extensions/lib/firstturn.ts','st.tasks.filter((t) => !CLOSED_TASKS.has(t.task_state ?? ""))','st.tasks')],
 P+"test/phase.test.ts")
run("answered gates shown as open",
 [('pi/extensions/lib/firstturn.ts','st.gates.filter((g) => g.state === "open")','st.gates')],
 P+"test/phase.test.ts")
run("a host fact interpolated unredacted in the extension",
 [('pi/extensions/lib/firstturn.ts','return v && v.trim() ? redactFact(v.trim()) : MISSING;','return v && v.trim() ? v.trim() : MISSING;')],
 P+"test/phase.test.ts")
run("a missing fact left blank, not named missing",
 [('pi/extensions/lib/firstturn.ts','const MISSING = "missing";','const MISSING = "";')],
 P+"test/phase.test.ts")
print(f"{RUNS[0]} runs, {RUNS[1]} caught, {RUNS[0]-RUNS[1]} missed")
