package cli

import (
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

func findThreadsSession(sessions []state.Session, profile, name string) (state.Session, bool) {
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range sessions {
		if sess.Name == name && squadnamespace.ProfilesEqual(profile, sess.TeamProfile) {
			return sess, true
		}
	}
	return state.Session{}, false
}
