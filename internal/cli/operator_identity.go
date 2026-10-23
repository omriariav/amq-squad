package cli

import (
	"fmt"
	"os"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// verifiedOperatorActor is a roster actor whose injected AMQ identity matches
// the selected namespace.
//
// 3.0 (2a-2, lead ruling): identity is the injected AM_ME/AM_ROOT pair only.
// The v2 TMUX_PANE plus launch-record verification is cut; pane identity is not
// an identity source, and seat identity moves to seat-exec in 2b.
type verifiedOperatorActor struct {
	Role, Handle, Profile, Session, Root string
}

var resolveVerifiedOperatorActor = defaultVerifiedOperatorActor
var resolveOperatorActorBaseRoot = scanBaseRootForProject

// defaultVerifiedOperatorActor requires AM_ME to be requiredHandle and AM_ROOT
// to be the namespace's exact AMQ root.
func defaultVerifiedOperatorActor(projectDir, profile, session, requiredRole, requiredHandle string) (verifiedOperatorActor, error) {
	ctx, err := resolveAMQContextForNamespace(projectDir, profile, session, requiredHandle)
	if err != nil {
		return verifiedOperatorActor{}, fmt.Errorf("resolve exact AMQ namespace: %w", err)
	}
	if strings.TrimSpace(os.Getenv("AM_ME")) != requiredHandle || !rootsMatch(os.Getenv("AM_ROOT"), ctx.Root) {
		return verifiedOperatorActor{}, fmt.Errorf("verified actor requires exact AM_ME=%s and AM_ROOT=%s", requiredHandle, ctx.Root)
	}
	return verifiedOperatorActor{Role: requiredRole, Handle: requiredHandle, Profile: squadnamespace.NormalizeProfile(profile), Session: session, Root: ctx.Root}, nil
}

// currentEnvRosterActor resolves the roster member named by AM_ME, if any, and
// verifies its injected identity for the session.
func currentEnvRosterActor(projectDir, profile, session string, cfg team.Team) (verifiedOperatorActor, bool, error) {
	me := strings.TrimSpace(os.Getenv("AM_ME"))
	if me == "" {
		return verifiedOperatorActor{}, false, nil
	}
	for _, member := range cfg.Members {
		if memberHandle(member) == me {
			actor, err := resolveVerifiedOperatorActor(projectDir, profile, session, member.Role, me)
			return actor, true, err
		}
	}
	return verifiedOperatorActor{}, false, nil
}

// memberHandle is a member's AMQ handle: Handle, else Role, else Binary.
func memberHandle(m team.Member) string {
	if m.Handle != "" {
		return m.Handle
	}
	if m.Role != "" {
		return m.Role
	}
	return m.Binary
}
