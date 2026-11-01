package redact

import (
	"strings"
	"testing"
)

// The colon forms yamlLineRE misses: no blank after the colon, and a key
// after prose on the same line (the earlier match consumed it).
func TestBlockRedactsColonFormsForEveryCredentialKey(t *testing.T) {
	canary := "Zq9" + "xV7mP2wL4" // built at run time, matches no RealCredentialShapes
	for _, re := range RealCredentialShapes {
		if re.MatchString(canary) {
			t.Fatal("the canary matches a real credential shape")
		}
	}
	for _, key := range []string{"API_KEY", "api_key", "SECRET", "client_secret", "TOKEN", "auth_token", "PASSWORD", "db_passwd", "CREDENTIAL", "PRIVATE_KEY", "OPENAI_KEY", "nsec_value"} {
		for _, in := range []string{
			key + ":" + canary,
			"error: see " + key + ": " + canary + " now",
			"error: see " + key + ":" + canary + " now",
			"(" + key + ":" + canary + ")",
			"a,\t" + key + ": " + canary + "; next",
			"\"" + key + "\":" + canary,
		} {
			out := Block(in)
			if strings.Contains(out, canary) {
				t.Errorf("%q: the value survived: %q", in, out)
			}
		}
	}
	if out := Block("error: see api_key: " + canary + " now"); out != "error: see api_key: "+Placeholder+" now" {
		t.Errorf("the text around the value changed: %q", out)
	}
}

func TestBlockKeepsProseNearAColonKey(t *testing.T) {
	for _, in := range []string{
		"Key: the owner's key",
		"note: the api key is rotated",
		"error: see api_key: rotated now",
		"error: see api_key:rotated now",
		"token:12:30",
		"see password: reverted before the release",
		"TokensIn:0xc0001a2b3c",
		"api_key: " + Placeholder,
		"error: see api_key:" + Placeholder,
		"api_key:null",
		"error: see api_key: ab12cd now", // under the length of a secret
		"see api_key:ab12cd",
		"  token: Zq9xV7mP2wL4 and more", // the yamlValue inner-space rule stays at a line start
		"- password: Zq9xV7mP2wL4 and more",
	} {
		if out := Block(in); out != in {
			t.Errorf("prose changed: %q -> %q", in, out)
		}
	}
}
