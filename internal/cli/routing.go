package cli

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
)

const routingUsage = `amq-squad routing - the routing table's weekly report

Usage:
  amq-squad routing report --week [--now RFC3339] [--repo DIR] [--json] [--root DIR | --session S]

report --week reads the session's outcome log
(<AM_ROOT>/extensions/amq-squad/routing/outcomes.jsonl, one row per attempt)
and prints, per class, tier and size band (declared files 1, 2-3, 4+), the
redo-at-higher-tier rate, the cost with redo against one tier up, wait time
with its reasons, and review findings traced to smol outputs, with one
proposed table change per bucket. Proposals are for the owner: nothing is
applied. Runtimes whose rows lack token evidence are left out and named
(plan 16.8); entries with no owner price are named.

The table comes from the binary, ~/.amq-squad/routing.yaml and
<repo>/.amq-squad/routing.yaml (--repo, default the current directory).

Examples:
  amq-squad routing report --week
  amq-squad routing report --week --json --root "$AM_ROOT"
`

// routingNow is the report's clock (tests replace it).
var routingNow = time.Now

func runRouting(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, routingUsage)
		if len(args) == 0 {
			return usageErrorf("routing: name a verb (report)")
		}
		return nil
	}
	switch args[0] {
	case "report":
		return runRoutingReport(args[1:])
	}
	return usageErrorf("routing: unknown verb %q (want report)", args[0])
}

func runRoutingReport(args []string) error {
	f := newStoreFlags("routing report")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, routingUsage) }
	week := f.fs.Bool("week", false, "the seven days before --now (required)")
	nowFlag := f.fs.String("now", "", "end of the window, RFC 3339 (default: now)")
	repo := f.fs.String("repo", "", "repository root for .amq-squad/routing.yaml (default: the current directory)")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if !*week {
		return usageErrorf("routing report: pass --week (the only window in 3.0)")
	}
	now := routingNow()
	if *nowFlag != "" {
		t, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return usageErrorf("routing report: --now: %v", err)
		}
		now = t
	}
	_, root, release, err := f.store(false)
	release()
	if err != nil {
		return err
	}
	repoRoot := *repo
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	home, _ := os.UserHomeDir()
	tbl, err := routing.Load(home, repoRoot)
	if err != nil {
		return fmt.Errorf("routing report: %w", err)
	}
	rows, skipped, err := routing.ReadRows(routing.OutcomePath(root))
	if err != nil {
		return err
	}
	rep := routing.Weekly(rows, now, tbl)
	if *f.jsonOut {
		return printJSONEnvelope("routing_report", rep)
	}
	rep.Print(os.Stdout)
	if skipped > 0 {
		fmt.Printf("warning: %d unreadable line(s) in %s skipped\n", skipped, routing.OutcomePath(root))
	}
	return nil
}

// routingCheck is doctor's routing row: the effective table loads (a file
// outside the strict subset fails), and, when the grant sets a per-day
// budget, the verified registry entries of the chains that have no owner
// price are named: the budget stop does not apply to them (lead ruling
// 2026-10-24).
func routingCheck(home, projectDir string) doctorCheck {
	tbl, err := routing.Load(home, projectDir)
	if err != nil {
		return doctorCheck{Name: "routing", Status: doctorFail, Kind: "routing_invalid", Detail: err.Error()}
	}
	classes, entries := tbl.Size()
	sources := "default table"
	if len(tbl.Sources) > 0 {
		sources = "default table + " + strings.Join(tbl.Sources, " + ")
	}
	c := hostCheck("routing", doctorOK, "%s: %d classes, %d chain entries, exploration_share %v", sources, classes, entries, tbl.ExplorationShare)
	g, gerr := grants.LoadFrom(hostcheck.GrantsPath(home), projectDir)
	if gerr != nil || g.CostPerDayUSD <= 0 {
		return c
	}
	reg, rerr := registry.Read(registry.Path(home))
	if rerr != nil {
		return c
	}
	var unpriced []string
	for _, tier := range routing.Tiers {
		for _, e := range tbl.Tiers[tier].Chain {
			if re, ok := reg.Lookup(e.Runtime, e.Model, e.Effort); ok && re.Verified && re.PrivacyClass != grants.PrivacyLocal {
				if _, priced := tbl.Prices[e]; !priced && !slices.Contains(unpriced, e.Key()) {
					unpriced = append(unpriced, e.Key())
				}
			}
		}
	}
	if len(unpriced) > 0 {
		c.Status = doctorWarn
		c.Detail += fmt.Sprintf("; the grant sets cost_per_day_usd %v but these entries in use have no price in ~/.amq-squad/routing.yaml, so the budget stop does not apply to them: %s",
			g.CostPerDayUSD, strings.Join(unpriced, ", "))
	}
	return c
}
