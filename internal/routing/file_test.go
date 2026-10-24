package routing

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestDefaultTableHoldsThePlanInvariants(t *testing.T) {
	d := Default()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	classes, entries := d.Size()
	if classes != 6 || entries != 13 {
		t.Fatalf("default table size = %d classes, %d entries", classes, entries)
	}
	for tier, s := range d.Tiers {
		for _, e := range s.Chain {
			if e.Effort == "max" {
				t.Errorf("tier %s: %s: max effort is never a default (6.2)", tier, e.Key())
			}
		}
	}
	if d.ExplorationShare != 0.05 {
		t.Fatalf("exploration_share default = %v", d.ExplorationShare)
	}
	// The probation entry is never first in its chain.
	if last := d.Tiers[TierTask].Chain[len(d.Tiers[TierTask].Chain)-1]; last.Model != "ollama/qwen3.6:27b" {
		t.Fatalf("task chain ends with %s", last.Key())
	}
}

// Required companions (3-2 dry run ruling): a field of the class table,
// default empty; a repository file names the documentation targets its CI
// needs and the widening path adds what the task lacks.
func TestRequiredCompanionsDefaultEmptyAndRepositoryList(t *testing.T) {
	for c, s := range Default().Classes {
		if len(s.RequiredCompanions) != 0 {
			t.Fatalf("class %s ships companions %v", c, s.RequiredCompanions)
		}
	}
	tbl, err := Apply(Default(), "schema: 1\nclasses:\n  implement:\n    required_companions: [SKILL.md, docs/cli/]\n", "repo.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := tbl.Companions("implement", []string{"cmd/x.go", "docs/cli/"}, nil); !reflect.DeepEqual(got, []string{"SKILL.md"}) {
		t.Fatalf("companions = %v", got)
	}
	if got := tbl.Companions("script", []string{"x"}, nil); got != nil {
		t.Fatalf("script companions = %v", got)
	}
	// The class list and the repository's list (3-2's grant) merge as a
	// union: each target once, the ones the task already carries left out.
	repo := []string{"SKILL.md", "README.md", "cmd/x.go"}
	if got := tbl.Companions("implement", []string{"cmd/x.go"}, repo); !reflect.DeepEqual(got, []string{"SKILL.md", "docs/cli/", "README.md"}) {
		t.Fatalf("union = %v", got)
	}
	if got := tbl.Companions("script", nil, repo[:2]); !reflect.DeepEqual(got, []string{"SKILL.md", "README.md"}) {
		t.Fatalf("repository list alone = %v", got)
	}
}

func TestLoadOverlaysUserThenRepository(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(UserPath(home), `# user file
schema: 1
exploration_share: 0.1
tiers:
  plan:
    chain:
      - "codex:gpt-5.6-sol:high"
      - claude:opus:high
`)
	write(RepoPath(repo), `schema: 1
exploration_share: 0
classes:
  script:
    tier: task
privacy_paths:
  - glob: internal/secrets/
    class: local
  - glob: "**/*.pem"
    class: local
sensitive_paths: [internal/auth/]
no_tests: true
`)
	tbl, err := Load(home, repo)
	if err != nil {
		t.Fatal(err)
	}
	if tbl.ExplorationShare != 0 || tbl.Classes["script"].Tier != TierTask || !tbl.NoTests || len(tbl.Sources) != 2 {
		t.Fatalf("table = %+v", tbl)
	}
	if got := tbl.Tiers[TierPlan].Chain[0]; got != (task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}) {
		t.Fatalf("plan chain = %v", tbl.Tiers[TierPlan].Chain)
	}
	if c, g := tbl.RequiredPrivacy([]string{"internal/secrets/key.go"}); c != "local" || g != "internal/secrets/" {
		t.Fatalf("privacy = %s %s", c, g)
	}
	if c, _ := tbl.RequiredPrivacy([]string{"a/b/c.pem"}); c != "local" {
		t.Fatalf("pem privacy = %s", c)
	}
	if c, _ := tbl.RequiredPrivacy([]string{"cmd/main.go"}); c != "vendor" {
		t.Fatalf("plain privacy = %s", c)
	}
	if !tbl.Sensitive([]string{"internal/auth/token.go"}) || tbl.Sensitive([]string{"cmd/"}) {
		t.Fatal("sensitive paths")
	}
	if none, err := Load(t.TempDir(), t.TempDir()); err != nil || len(none.Sources) != 0 {
		t.Fatalf("no files: %v %v", none.Sources, err)
	}
}

// Every refusal row: the file is refused, never partly applied.
func TestRoutingFileRefusals(t *testing.T) {
	cases := []struct {
		name, text, want string
		repo             bool
	}{
		{"anchor", "schema: 1\nexploration_share: &a 0.1\n", "unsupported YAML", false},
		{"alias", "schema: 1\nexploration_share: *a\n", "unsupported YAML", false},
		{"tag", "schema: 1\nexploration_share: !!float 0.1\n", "unsupported YAML", false},
		{"flow map", "schema: 1\nclasses: {plan: {tier: task}}\n", "want a map", false},
		{"second document", "schema: 1\n---\nexploration_share: 0.5\n", "one YAML document", false},
		{"repeated key", "schema: 1\nexploration_share: 0.1\nexploration_share: 0.2\n", "repeated", false},
		{"unknown key", "schema: 1\njev: true\n", "unknown key", false},
		{"malformed line", "schema: 1\njust text\n", "malformed line", false},
		{"missing schema", "exploration_share: 0.1\n", "schema: missing", false},
		{"schema 2", "schema: 2\n", "not supported", false},
		{"share above 1", "schema: 1\nexploration_share: 1.5\n", "share from 0 to 1", false},
		{"unknown class", "schema: 1\nclasses:\n  deploy:\n    tier: task\n", "closed list", false},
		{"unknown tier", "schema: 1\ntiers:\n  huge:\n    chain: [claude:opus:high]\n", "unknown tier", false},
		{"plan routed down", "schema: 1\nclasses:\n  plan:\n    tier: task\n", "never routes below", false},
		{"review routed down", "schema: 1\nclasses:\n  review:\n    tier: plan\n", "never routes below", false},
		{"implement on advisor", "schema: 1\nclasses:\n  implement:\n    tier: advisor\n", "only review runs on the advisor", false},
		{"verifier changed", "schema: 1\nclasses:\n  summarize:\n    verifier: schema\n", "fixed by plan 6.1", false},
		{"bad chain entry", "schema: 1\ntiers:\n  smol:\n    chain: [haiku]\n", "runtime:model:effort", false},
		{"empty chain", "schema: 1\ntiers:\n  smol:\n    chain: []\n", "has no chain", false},
		{"repeated chain entry", "schema: 1\ntiers:\n  plan:\n    chain: [claude:opus:high, claude:opus:high]\n", "repeated", false},
		{"negative cost cap", "schema: 1\ntiers:\n  plan:\n    max_cost_index: -1\n", "at or above 0", false},
		{"privacy in user file", "schema: 1\nprivacy_paths:\n  - glob: x/\n    class: local\n", "only the repository file", false},
		{"privacy class unknown", "schema: 1\nprivacy_paths:\n  - glob: x/\n    class: cloud\n", "want one of", true},
		{"privacy path escapes", "schema: 1\nprivacy_paths:\n  - glob: ../x\n    class: local\n", "relative to the repository root", true},
		{"privacy rule incomplete", "schema: 1\nprivacy_paths:\n  - glob: x/\n", "needs glob and class", true},
		// Codex pass 1, P1-3: a dotted key is matched whole, never by its last part.
		{"dotted key in a privacy rule", "schema: 1\nprivacy_paths:\n  - glob: s/\n    class: local\n    override.class: vendor\n", "unknown key", true},
		{"dotted key in a class row", "schema: 1\nclasses:\n  implement:\n    x.tier: smol\n", "unknown key", false},
		{"dotted class name", "schema: 1\nclasses:\n  x.plan:\n    tier: plan\n", "closed list", false},
		{"dotted key in a tier", "schema: 1\ntiers:\n  smol:\n    y.chain: [claude:haiku:low]\n", "unknown key", false},
		{"dotted tier name", "schema: 1\ntiers:\n  z.smol:\n    chain: [claude:haiku:low]\n", "unknown tier", false},
		{"dotted key in a price", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 1\n    output_usd_per_mtok: 1\n    a.entry: claude:haiku:low\n", "unknown key", false},
		// Codex pass 1, P2-6: non-finite numbers.
		{"share NaN", "schema: 1\nexploration_share: NaN\n", "share from 0 to 1", false},
		{"cost cap infinite", "schema: 1\ntiers:\n  plan:\n    max_cost_index: Inf\n", "finite number", false},
		{"price NaN", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: NaN\n    output_usd_per_mtok: 1\n", "finite number", false},
		{"price infinite", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 1\n    output_usd_per_mtok: +Infinity\n", "finite number", false},
		{"prices in the repository file", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 15\n    output_usd_per_mtok: 75\n", "only the owner's file", true},
		{"price incomplete", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 15\n", "needs entry, input_usd_per_mtok", false},
		{"price repeated", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 1\n    output_usd_per_mtok: 1\n  - entry: claude:opus:high\n    input_usd_per_mtok: 2\n    output_usd_per_mtok: 2\n", "repeated", false},
		{"price negative", "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: -1\n    output_usd_per_mtok: 1\n", "at or above 0", false},
		{"price bad entry", "schema: 1\nprices:\n  - entry: opus\n    input_usd_per_mtok: 1\n    output_usd_per_mtok: 1\n", "runtime:model:effort", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Apply(Default(), c.text, "routing.yaml", c.repo)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

// Routing a class up is allowed; only a mechanical verifier routes down.
func TestRoutingFileTierRules(t *testing.T) {
	for _, ok := range []string{
		"schema: 1\nclasses:\n  summarize:\n    tier: task\n",
		"schema: 1\nclasses:\n  implement:\n    tier: smol\n",
		"schema: 1\nclasses:\n  plan:\n    tier: plan\n",
	} {
		if _, err := Apply(Default(), ok, "r.yaml", false); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	tbl, _ := Apply(Default(), "schema: 1\nclasses:\n  summarize:\n    tier: task\n", "r.yaml", false)
	if _, err := Apply(tbl, "schema: 1\nclasses:\n  summarize:\n    tier: smol\n", "r2.yaml", false); err != nil {
		t.Fatalf("summarize back to its plan tier: %v", err)
	}
}
