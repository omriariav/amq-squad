package gatetarget

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

func TestDigestIsTheS1NonMergeFormula(t *testing.T) {
	// Recompute the S1 GateSpec.TargetDigest for a non-merge class by hand:
	// length-prefixed domain, class, subject_sha256 (spike/gateverify/gate.go).
	subject := sha256.Sum256([]byte("git tag v1"))
	var b []byte
	for _, v := range []string{"amq-squad-gate-target-v1", "tag", hex.EncodeToString(subject[:])} {
		b = binary.BigEndian.AppendUint32(b, uint32(len(v)))
		b = append(b, v...)
	}
	sum := sha256.Sum256(b)
	if got, want := Digest("tag", "git tag v1"), "sha256:"+hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("Digest = %s, want %s", got, want)
	}
	if Digest("tag", "git tag v1") == Digest("github_release", "git tag v1") || Digest("tag", "git tag v1") == Digest("tag", "git tag v2") {
		t.Fatal("digest does not bind action and target")
	}
}

func TestTableConventions(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{Command("git tag", "v1"), "git tag v1"},
		{Command("gh release create", "v1"), "gh release create v1"},
		{Command("gh auth login", ""), "gh auth login"},
		{MCPSend("mcp__slack__send_message", "C1"), "mcp__slack__send_message C1"},
		{Grow("/r/s1", 13), "grow /r/s1 seats=13"},
		{Write("/home/u/.ssh/config"), "write /home/u/.ssh/config"},
		{TagPush("refs/tags/v1"), "git push refs/tags/v1"},
		{GitConfig("credential.helper"), "git config credential.helper"},
		{GWSSend("gmail", "send"), "gws gmail send"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if got := GrowLimit("/r/s1", "files", 40); got != "grow /r/s1 files=40" {
		t.Fatalf("GrowLimit = %q", got)
	}
}

func TestEveryTypedActionIsInTheTaxonomy(t *testing.T) {
	for _, a := range Actions() {
		c, err := operatorauth.LookupAction(a)
		if err != nil || !c.HumanOnly || Class(a) == "" {
			t.Errorf("%s: %+v %v class %q", a, c, err, Class(a))
		}
		if err := Validate(a, "x"); err != nil {
			t.Errorf("Validate(%s) = %v", a, err)
		}
	}
	for _, a := range Commands {
		if Class(a) == "" {
			t.Errorf("command action %q has no class", a)
		}
	}
	if err := Validate("default_branch_push", "x"); err == nil {
		t.Error("merge is not a typed gate")
	}
	if err := Validate("tag", "a\nb"); err == nil {
		t.Error("two-line target accepted")
	}
}
