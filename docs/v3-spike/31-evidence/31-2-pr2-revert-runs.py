# The revert runs of 31-2 PR 2 (docs/v3-spike/31-evidence/31-2-pr2-seat-add.md
# section 4). Each run reverts one change in the working tree, runs the rows
# that must catch it, prints whether they failed and the failing tests, and
# restores the file from git. Run it from a clean checkout of the branch:
#   python3 docs/v3-spike/31-evidence/31-2-pr2-revert-runs.py
# It refuses a tree with any staged, unstaged or untracked change, since it
# restores every file it touched to HEAD exactly (index and working tree),
# and it restores them even when a run fails or is interrupted.
import subprocess, os, sys
os.chdir(subprocess.check_output(['git','rev-parse','--show-toplevel'],text=True).strip())
def dirty():
    return subprocess.run(['git','status','--porcelain','--untracked-files=all'],capture_output=True,text=True,check=True).stdout
if dirty():
    sys.exit("refused: the tree has staged, unstaged or untracked changes; the runs restore files to HEAD and would drop them:\n"+dirty())
G='go test -count=1 '
RUNS=[0,0]  # runs, runs caught
def both(*cmds):
    # every command must fail for the revert to count; each one's exit is printed
    parts=[]
    for i,c in enumerate(cmds):
        parts.append(f"{c} ; r{i}=$?")
    cond=" -a ".join(f"$r{i} -ne 0" for i in range(len(cmds)))
    return " ; ".join(parts)+f" ; test {cond} && exit 1 || exit 0"
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
P="cd pi && node --test "
run("router pools fixed at open (SetRosterPools a no-op)",
 [('internal/routing/router.go','\tr.o.RosterPools = slices.Clone(pools)\n','\t_ = pools\n')],
 both(G+"-run TestARosterSeatAddedByAReload ./internal/routing/", G+"-run TestARelaunchAfterASquadYAMLEdit ./internal/cli/"))
run("the swap at the request, not the drain's end",
 [('internal/scheduler/scheduler.go','\t\t\tlog(Decision{Kind: "roster_relaunch_requested"','\t\t\ts.swapReload(log)\n\t\t\tlog(Decision{Kind: "roster_relaunch_requested"')],
 G+"-run TestAReloadSwapsAtTheDrainsEnd ./internal/scheduler/")
run("the roster written although Swap failed",
 [('internal/scheduler/roster.go','''			log(Decision{Kind: "roster_reload_failed", Reason: "the running roster relaunches unchanged: " + err.Error()})
			return''','''			log(Decision{Kind: "roster_reload_failed", Reason: "the running roster relaunches unchanged: " + err.Error()})''')],
 G+"-run TestAFailedSwap ./internal/scheduler/")
run("a compile error ignored (the relaunch drains)",
 [('internal/schedproc/roster.go','''			got, err := reload.prepare()
			if err != nil {''','''			got, err := reload.prepare()
			if err != nil && false {''')],
 G+"-run TestARelaunchWhoseSquadDoesNotCompile ./internal/schedproc/")
run("no undo between the swap's writes",
 [('internal/schedproc/roster.go','''	if err := faultAt("pools"); err != nil {
		undoSquad()
		return err
	}''','''	if err := faultAt("pools"); err != nil {
		_ = undoSquad
		return err
	}'''),('internal/schedproc/roster.go','''		if r.Pools != nil {
			r.Pools.SetRosterPools(oldPools)
		}
		undoSquad()
		return err''','''		_ = oldPools
		return err''')],
 G+"-run TestAHalfAppliedReload ./internal/schedproc/")
run("the relaunch without the reload (process.go)",
 [('internal/schedproc/process.go','relaunchDecision(s, d.Reload, o.Start)','relaunchDecision(s, nil, o.Start)')],
 G+"-run TestARelaunchReloadsSquadYAML ./internal/schedproc/")
run("scheduler run wiring no reload",
 [('internal/cli/scheduler_run.go','\tdecisions.Reload = &schedproc.Reload{','\t_ = &schedproc.Reload{')],
 G+"-run TestARelaunchAfterASquadYAMLEdit ./internal/cli/")
run("no key press start for the added seat in the roster launch",
 [('internal/scheduler/roster.go','\t\t\treq.Start, req.AddedAt = press, press','\t\t\treq.AddedAt = press')],
 both(G+"-run TestAReloadSwapsAtTheDrainsEnd ./internal/scheduler/", G+"-run TestARelaunchReloadsSquadYAML ./internal/schedproc/"))
run("marks.json with the launch's start for every seat",
 [('internal/seatlaunch/compile.go','\t\t\tat = own\n','\t\t\t_ = at\n')],
 G+"-run TestTheAddedSeatsMarksStart ./internal/seatlaunch/")
run("relaunch --start not passed to the request",
 [('internal/cli/scheduler_run.go','schedproc.RelaunchRequest{Reason: strings.TrimSpace(*reason), Start: stamp}','schedproc.RelaunchRequest{Reason: strings.TrimSpace(*reason)}')],
 G+"-run TestARelaunchAfterASquadYAMLEdit ./internal/cli/")
run("--add-seat ignored",
 [('internal/cli/team.go','\tif *o.addSeat != "" {\n\t\tif *o.from','\tif false {\n\t\tif *o.from')],
 G+"-run TestTeamAddSeat ./internal/cli/")
run("--add-seat re-rendering the team rules",
 [('internal/cli/team.go','keepRules && rerr == nil {','false && keepRules && rerr == nil {')],
 G+"-run TestTeamAddSeat ./internal/cli/")
run("the add key adding tasks only",
 [('pi/extensions/lib/keys.ts','if (kind === "seat") return this.addSeat(ctx, st);','')],
 P+"test/keys-seat.test.ts")
run("the add key's relaunch without --start",
 [('pi/extensions/lib/keys.ts','"--start", pressed, "--json"','"--json"')],
 P+"test/keys-seat.test.ts")
run("the add key's confirmation skipped",
 [('pi/extensions/lib/keys.ts','const ok = await ctx.ui.confirm(`add seat ${handle} (${spec})`, "this relaunches every seat; continue?");','const ok = true;')],
 P+"test/keys-seat.test.ts")
run("a tool added to the lead (row E)",
 [('pi/extensions/lib/tools.ts','name: "amq_scheduler_relaunch",','name: "amq_scheduler_relaunch_x",')],
 P+"test/keys.test.ts")
run("the swap keeping the start's lease cap",
 [('internal/scheduler/roster.go','\t\ts.Config.MaxSeats = r.MaxSeats\n','\t\t_ = r.MaxSeats\n')],
 both(G+"-run TestAReloadRecomputesTheLeaseCap ./internal/scheduler/", G+"-run TestARelaunchReloadsSquadYAML ./internal/schedproc/", G+"-run TestAReloadRecomputesTheLeaseCapFromTheNewSquad ./internal/cli/"))
run("scheduler run wiring no cap (Reload.Cap returning none)",
 [('internal/cli/scheduler_run.go','Cap: reloadCap(*maxSeats), ','Cap: func(seatlaunch.Squad) (int, error) { return 0, nil }, ')],
 G+"-run TestAReloadRecomputesTheLeaseCapFromTheNewSquad ./internal/cli/")
run("the cap written before Swap (outside the one step)",
 [('internal/scheduler/roster.go',"\tif r.Swap != nil {\n\t\tif err := r.Swap(); err != nil {","\tif r.MaxSeats > 0 {\n\t\ts.Config.MaxSeats = r.MaxSeats\n\t}\n\tif r.Swap != nil {\n\t\tif err := r.Swap(); err != nil {")],
 G+"-run TestAHalfAppliedReload ./internal/schedproc/")
run("no drain segment in status",
 [('internal/status/screen.go','\tif drainEnd != nil && start != nil && !drainEnd.Before(*start) {','\tif false && drainEnd != nil && start != nil && !drainEnd.Before(*start) {')],
 G+"-run TestARelaunchedSeatsBudgetNamesTheDrain ./internal/status/")
run("no drain end recorded by the roster launch",
 [('internal/seatlaunch/roster.go','\topts.DrainEnd = time.Now() //','\t_ = time.Now() //')],
 G+"-run 'TestTheAddedSeatsMarksStart|TestAnAddedSeatKeepsItsStart' ./internal/seatlaunch/")
run("the socket only parsing the start",
 [('internal/schedproc/roster.go','if why := CheckRelaunchStart(t, RunningLaunchStart(launchStart, s.LastRelaunchAt()), now); why != "" {','if why := CheckRelaunchStart(t, RunningLaunchStart(launchStart, s.LastRelaunchAt()), now); false && why != "" {')],
 G+"-run TestARelaunchStartOutOfRange ./internal/schedproc/")
run("the verb only parsing the start",
 [('internal/cli/scheduler_run.go','if why := schedproc.CheckRelaunchStart(t, schedproc.RunningLaunchStart(launchStart, lastRelaunch), time.Now()); why != "" {','if why := schedproc.CheckRelaunchStart(t, schedproc.RunningLaunchStart(launchStart, lastRelaunch), time.Now()); false && why != "" {')],
 G+"-run TestSchedulerRelaunchBoundsItsStart ./internal/cli/")
run("the tick's state written from the config captured before the swap",
 [('internal/scheduler/scheduler.go','\tcfg = s.cfg()\n\tif err != nil {','\tif err != nil {')],
 G+"-run TestTheStateWritten ./internal/scheduler/")
run("added_at not carried into later marks",
 [('internal/seatlaunch/compile.go','\t\t\tm.AddedAt = prev.AddedAt // the record','\t\t\t_ = prev.AddedAt // the record')],
 G+"-run TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart ./internal/seatlaunch/")
run("added_at not written at the press's launch",
 [('internal/seatlaunch/compile.go','\t\t\tm.AddedAt = &added\n','\t\t\t_ = &added\n')],
 G+"-run TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart ./internal/seatlaunch/")
run("status not showing added_at",
 [('internal/status/screen.go','\t\tsl.AddedAt = m.AddedAt\n','\t\t_ = m.AddedAt\n')],
 G+"-run TestARelaunchedSeatsBudgetNamesTheDrain ./internal/status/")
run("no roster_over_cap decision",
 [('internal/scheduler/roster.go','\tif cap := s.Config.MaxSeats; len(r.Roster) > cap {','\tif cap := s.Config.MaxSeats; false && len(r.Roster) > cap {')],
 both(G+"-run TestAReloadRecomputesTheLeaseCap ./internal/scheduler/", G+"-run TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns ./internal/cli/"))
run("the roster trimmed to the cap at a reload (seats beyond it unlaunched)",
 [('internal/schedproc/roster.go','\treturn &scheduler.RosterReload{Roster: roster,','\tif maxSeats > 0 && len(roster) > maxSeats {\n\t\troster = roster[:maxSeats]\n\t}\n\treturn &scheduler.RosterReload{Roster: roster,')],
 both(G+"-run TestAReloadOverTheCapLaunchesEverySeatAndCapsTheLeases ./internal/schedproc/", G+"-run TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns ./internal/cli/"))
run("the screen showing no over-cap line",
 [('pi/extensions/lib/screen.ts','if (sch && sch.max_seats > 0 && sch.live_seats > sch.max_seats) {','if (false && sch) {')],
 P+"test/keys-seat.test.ts")
run("a relaunch carrying no stamp of its own (the squad's start for its seats)",
 [('internal/scheduler/roster.go',', Handle: rs.Handle, Start: s.relaunchAt}',', Handle: rs.Handle}')],
 both(G+"-run TestEveryRelaunchStampsItsOwnStart ./internal/scheduler/", G+"-run TestARelaunchReloadsSquadYAML ./internal/schedproc/", G+"-run TestARelaunchAfterASquadYAMLEdit ./internal/cli/"))
run("the key press kept as a later launch's start",
 [('internal/scheduler/roster.go','\ts.seatStart, s.relaunchAt = nil, time.Time{}','\ts.relaunchAt = time.Time{}')],
 G+"-run TestEveryRelaunchStampsItsOwnStart ./internal/scheduler/")
run("drain_end for the added seat only",
 [('internal/seatlaunch/compile.go','\t\t\tif !drainEnd.IsZero() && !drainEnd.Before(own) {','\t\t\tif _, added := seatAddedAt[rec.Handle]; added && !drainEnd.IsZero() && !drainEnd.Before(own) {')],
 G+"-run TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart ./internal/seatlaunch/")
run("the socket's floor the scheduler's launch start, not the last relaunch",
 [('internal/schedproc/roster.go','RunningLaunchStart(launchStart, s.LastRelaunchAt())','RunningLaunchStart(launchStart, time.Time{})')],
 G+"-run TestARelaunchStartBeforeTheLastRelaunchIsRefused ./internal/schedproc/")
run("the verb's floor the launch record's start, not the last relaunch",
 [('internal/cli/scheduler_run.go','\t\t\tlastRelaunch = *st.LastRelaunchAt\n','\t\t\t_ = st.LastRelaunchAt\n')],
 G+"-run TestSchedulerRelaunchStartFloorIsTheLastRelaunch ./internal/cli/")
run("the cap captured at acceptance, not read again at the drain's end",
 [('internal/schedproc/roster.go','\t\tnext.Cap = func() (int, error) { return r.Cap(q) }\n','')],
 G+"-run TestTheCapInstalledAtTheSwapIsTheGrantReadAtTheDrainsEnd ./internal/schedproc/")
run("a cap that cannot be read at the swap ignored",
 [('internal/scheduler/roster.go','\t\tc, err := r.Cap()\n\t\tif err != nil {','\t\tc, err := r.Cap()\n\t\tif err != nil && false {')],
 G+"-run TestACapThatCannotBeReadAtTheSwapWritesNothing ./internal/scheduler/")
run("drain_end stamped after the roster plan",
 [('internal/seatlaunch/roster.go','\tdrainEnd := time.Now()\n\tq, err := l.PlanRoster(reqs)','\tq, err := l.PlanRoster(reqs)\n\tdrainEnd := time.Now()')],
 G+"-run TestASlowFetchLandsInPrepareNotDrain ./internal/seatlaunch/")
print(f"{RUNS[0]} runs, {RUNS[1]} caught, {RUNS[0]-RUNS[1]} missed")
