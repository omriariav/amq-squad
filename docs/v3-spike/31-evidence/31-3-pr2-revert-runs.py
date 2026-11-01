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

# section 5's verb half: context --host
run("context --host ignored",
 [('internal/cli/hook_context.go','\tif *host {\n\t\td.Host = hostFacts()\n\t}','\t_ = host')],
 C+"TestContextHost")
run("the branch not read from git",
 [('internal/cli/hook_context.go','\t\t\th.Branch = out\n','\t\t\t_ = out\n')],
 C+"TestContextHost")
run("the AMQ version not read",
 [('internal/cli/hook_context.go','if out, err := contextHostRun(h.Repository, amq, "--version"); err == nil {','if out, err := contextHostRun(h.Repository, amq, "--version"); err == nil && false {')],
 C+"TestContextHost")
# section 4: the phase marker
run("the yes writing no phase marker (the record)",
 [('internal/cli/lead_start.go','\t\t} else if err := writePhaseMarker(s.root, s.project, team, s.self); err != nil {','\t\t} else if err := error(nil); err != nil {')],
 C+"TestTheStartOfAConfirmedTeam")
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
run("a missing fact left blank, not named missing",
 [('pi/extensions/lib/firstturn.ts','const MISSING = "missing";','const MISSING = "";')],
 P+"test/phase.test.ts")
print(f"{RUNS[0]} runs, {RUNS[1]} caught, {RUNS[0]-RUNS[1]} missed")
