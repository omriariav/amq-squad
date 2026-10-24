package buzz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// DefaultShareSession is the share session the owner script enrolls.
const DefaultShareSession = "amq-squad"

// DefaultShareRoot is the AMQ root the owner script enrolls the share in,
// ~/.amq-squad/buzz/root, "" without a home directory. It is its own root,
// not a squad's: the share's endpoint is supervised by the attachment.
func DefaultShareRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "buzz", "root")
}

// ShareFor names the share a binding uses: its share_root and share_session,
// else the defaults.
func ShareFor(b gateverify.Binding) (root, session string) {
	root, session = b.ShareRoot, b.ShareSession
	if root == "" {
		root = DefaultShareRoot()
	}
	if session == "" {
		session = DefaultShareSession
	}
	return root, session
}

// Connect loads the binding's share, checks that its body and owner are the
// binding's pin, dials the relay and signs in with the body key and the
// owner's kind 1059 grant (as amq-remote serve signs in). It returns the
// relay, the share and the kind 9 grant for DM posts.
func Connect(ctx context.Context, b gateverify.Binding, pin gateverify.Pin, now time.Time) (*gateverify.WSRelay, Share, []string, error) {
	root, session := ShareFor(b)
	sh, err := LoadShare(root, session)
	if err != nil {
		return nil, Share{}, nil, err
	}
	switch {
	case sh.BodyHex() != pin.Body:
		return nil, Share{}, nil, fmt.Errorf("share %s body %s is not the binding's body %s", session, sh.BodyHex(), pin.Body)
	case sh.Owner != pin.Owner:
		return nil, Share{}, nil, fmt.Errorf("share %s is enrolled to owner %s, the binding pins %s", session, sh.Owner, pin.Owner)
	}
	auth, err := sh.Grant(KindAuthGrant, now)
	if err != nil {
		return nil, Share{}, nil, err
	}
	post, err := sh.Grant(KindDM, now)
	if err != nil {
		return nil, Share{}, nil, err
	}
	relay, err := gateverify.Dial(ctx, b.Relay)
	if err != nil {
		return nil, Share{}, nil, err
	}
	if err := relay.Auth(ctx, gateverify.BodyAuth{Secret: sh.Body, AuthTag: auth}); err != nil {
		relay.Close()
		return nil, Share{}, nil, fmt.Errorf("relay sign-in with the share body: %w", err)
	}
	return relay, sh, post, nil
}

// GateTransport is the 3-1 core's transport seam (cli gateTransport): the
// relay, the body key and the kind 9 grant of the enrolled share.
func GateTransport(ctx context.Context, b gateverify.Binding, pin gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
	relay, sh, post, err := Connect(ctx, b, pin, time.Now())
	if err != nil {
		return nil, nostr.SecretKey{}, nil, err
	}
	if relay == nil {
		return nil, nostr.SecretKey{}, nil, errors.New("no relay")
	}
	return relay, sh.Body, post, nil
}
