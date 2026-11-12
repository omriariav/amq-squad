package gatestore

// D3 of docs/v3-spike/330-a0-sender-auth/README.md defines this one list of
// lower-case bare-answer words. Every reader goes through BareVerb.
var yesWords = []string{"yes", "approve", "y", "ok", "go ahead", "כן"}
var noWords = []string{"no", "deny", "n", "stop", "לא"}

// BareVerb classifies one exact, already-normalized word from the decided list.
func BareVerb(word string) (verb string, ok bool) {
	for _, yes := range yesWords {
		if word == yes {
			return "approve", true
		}
	}
	for _, no := range noWords {
		if word == no {
			return "deny", true
		}
	}
	return "", false
}
