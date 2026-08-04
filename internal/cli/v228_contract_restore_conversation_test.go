package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// AC14: restoring an exited agent resumes its RECORDED conversation, so the
// agent continues with its accumulated context. A restore that spawns a blank
// agent is a failed restore — "resume restores the mind, not just the process."
//
// The conversation id lives in launch.json, and the child command carries it
// (`codex resume <conversation-id>` via the launcher's --conversation). The
// command-composition half is checkable today and pinned below. The
// restore-through-`start` half lands with step 3.

const v228RestorePhaseSkip = "awaiting step 3 (restore/respawn through the simple start path)"

func v228ExitedRecord(conversation string) launch.Record {
	return launch.Record{
		Schema: launch.SchemaVersion, Binary: "codex",
		Role: "dev", Handle: "dev", Session: "ac14",
		TeamProfile: "v228", TeamHome: "/repo", CWD: "/repo",
		Root: "/repo/.agent-mail/v228/ac14", BaseRoot: "/repo/.agent-mail/v228",
		Conversation: conversation,
		AgentPID:     5301, StartedAt: v228Now,
	}
}

func TestV228ContractRestoreCarriesTheRecordedConversation(t *testing.T) {
	requireV228Contract(t)
	const conversation = "conv-ac14-abcdef"
	rec := v228ExitedRecord(conversation)

	args := launchArgsFromRecord(rec)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, conversation) {
		t.Errorf("restore argv dropped the recorded conversation %q: %v", conversation, args)
	}
	if !strings.Contains(joined, "--conversation") {
		t.Errorf("restore argv has no --conversation flag: %v", args)
	}

	command := emitCommand(rec)
	if !strings.Contains(command, conversation) {
		t.Errorf("emitted restore command dropped the conversation %q:\n%s", conversation, command)
	}
}

// A blank respawn is a failed restore: when the record HAS a conversation, no
// composed restore may omit it. This is the assertion that catches a silent
// context loss, which looks like a healthy launch from the outside.
func TestV228ContractRestoreWithoutConversationIsAFailedRestore(t *testing.T) {
	requireV228Contract(t)
	const conversation = "conv-ac14-fedcba"
	withConversation := v228ExitedRecord(conversation)
	blank := v228ExitedRecord("")

	carried := strings.Join(launchArgsFromRecord(withConversation), " ")
	blankArgs := strings.Join(launchArgsFromRecord(blank), " ")

	if strings.Contains(blankArgs, "--conversation") {
		t.Errorf("a record with no conversation must not synthesize one: %s", blankArgs)
	}
	// The two must differ by exactly the conversation binding: if they compose
	// identically, the recorded conversation is being ignored and every restore
	// is blank.
	if carried == blankArgs {
		t.Fatalf("restore argv is identical with and without a recorded conversation; the conversation is not being carried:\n%s", carried)
	}
	if !strings.Contains(carried, conversation) {
		t.Errorf("restore argv for a recorded conversation lacks %q: %s", conversation, carried)
	}
}
