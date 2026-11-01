# The revert runs of 31-2 PR 2 (docs/v3-spike/31-evidence/31-2-pr2-seat-add.md
# section 4). Each run reverts one change in the working tree, runs the rows
# that must catch it, prints whether they failed and the failing tests, and
# restores the file from git. Run it from a clean checkout of the branch:
#   python3 docs/v3-spike/31-evidence/31-2-pr2-revert-runs.py
# It refuses a dirty tree (a restore from git would drop uncommitted work).
import subprocess, os
os.chdir(subprocess.check_output(['git','rev-parse','--show-toplevel'],text=True).strip())
assert subprocess.run(['git','diff','--quiet']).returncode==0, "tree not clean"
G='go test -count=1 '
def both(*cmds):
    # every command must fail for the revert to count; each one's exit is printed
    parts=[]
    for i,c in enumerate(cmds):
        parts.append(f"{c} ; r{i}=$?")
    cond=" -a ".join(f"$r{i} -ne 0" for i in range(len(cmds)))
    return " ; ".join(parts)+f" ; test {cond} && exit 1 || exit 0"
def run(name, edits, cmd):
    files=set()
    for (p,a,b) in edits:
        s=open(p).read()
        assert a in s, (name,p,a[:70])
        open(p,'w').write(s.replace(a,b,1)); files.add(p)
    r=subprocess.run(cmd,shell=True,capture_output=True,text=True)
    out=r.stdout+r.stderr
    lines=[l.rstrip() for l in out.splitlines() if l.strip().startswith(('--- FAIL','✖','not ok')) or 'build failed' in l]
    print(f"REVERT {name}: {'fails (the row catches it)' if r.returncode!=0 else 'PASSES (the row misses it)'}")
    seen=set()
    for l in lines:
        if l in seen: continue
        seen.add(l); print('   '+l.strip())
    subprocess.run(['git','checkout','--']+sorted(files))
    assert subprocess.run(['git','diff','--quiet']).returncode==0
P="cd pi && node --test "
run("router pools fixed at open (SetRosterPools a no-op)",
 [('internal/routing/router.go','\tr.o.RosterPools = slices.Clone(pools)\n','\t_ = pools\n')],
 both(G+"-run TestARosterSeatAddedByAReload ./internal/routing/", G+"-run TestARelaunchAfterASquadYAMLEdit ./internal/cli/"))
run("the swap at the request, not the drain's end",
 [('internal/scheduler/scheduler.go','\t\t\ts.reload, s.reloadAt = ask.reload, ask.start\n\t\t\tlog(Decision{Kind: "roster_relaunch_requested"','\t\t\ts.reload, s.reloadAt = ask.reload, ask.start\n\t\t\ts.swapReload(log)\n\t\t\tlog(Decision{Kind: "roster_relaunch_requested"')],
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
run("no per-request start in the roster launch",
 [('internal/scheduler/roster.go',', Handle: rs.Handle, Start: s.seatStart[rs.Handle]}',', Handle: rs.Handle}')],
 both(G+"-run TestAReloadSwapsAtTheDrainsEnd ./internal/scheduler/", G+"-run TestARelaunchReloadsSquadYAML ./internal/schedproc/"))
run("marks.json with the launch's start for every seat",
 [('internal/seatlaunch/compile.go','\t\t\tat = own // the key press','\t\t\t_ = own // the key press')],
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
run("no roster_over_cap decision",
 [('internal/scheduler/roster.go','\tif len(r.Roster) > s.Config.MaxSeats {\n','\tif false && len(r.Roster) > s.Config.MaxSeats {\n')],
 both(G+"-run TestAReloadRecomputesTheLeaseCap ./internal/scheduler/", G+"-run TestAReloadOverTheLeaseCap ./internal/cli/"))
run("the screen showing no over-cap line",
 [('pi/extensions/lib/screen.ts','if (sch && sch.max_seats > 0 && sch.live_seats > sch.max_seats) {','if (false && sch) {')],
 P+"test/keys-seat.test.ts")
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
 [('internal/schedproc/roster.go','if why := CheckRelaunchStart(t, launchStart, now); why != "" {','if why := CheckRelaunchStart(t, launchStart, now); false && why != "" {')],
 G+"-run TestARelaunchStartOutOfRange ./internal/schedproc/")
run("the verb only parsing the start",
 [('internal/cli/scheduler_run.go','if why := schedproc.CheckRelaunchStart(t, launchStart, time.Now()); why != "" {','if why := schedproc.CheckRelaunchStart(t, launchStart, time.Now()); false && why != "" {')],
 G+"-run TestSchedulerRelaunchBoundsItsStart ./internal/cli/")
run("the added seats' starts cleared after each launch",
 [('internal/scheduler/roster.go','\tout := rl.LaunchRoster(reqs)\n','\ts.seatStart = nil\n\tout := rl.LaunchRoster(reqs)\n')],
 G+"-run TestAnAddedSeatsStartIsKept ./internal/scheduler/")
run("a removed seat's start kept",
 [('internal/scheduler/roster.go','\t\tdelete(s.seatStart, h)\n','\t\t_ = h\n')],
 G+"-run TestAnAddedSeatsStartIsKept ./internal/scheduler/")
run("a drain end written again on a later launch",
 [('internal/seatlaunch/compile.go','again := perr == nil && prev.Start != nil && prev.Start.Equal(own)','again := false && perr == nil && prev.Start != nil && prev.Start.Equal(own)')],
 G+"-run TestAnAddedSeatKeepsItsStart ./internal/seatlaunch/")
run("the tick's state written from the config captured before the swap",
 [('internal/scheduler/scheduler.go','\tcfg = s.cfg()\n\tif err != nil {','\tif err != nil {')],
 G+"-run TestTheStateWritten ./internal/scheduler/")
