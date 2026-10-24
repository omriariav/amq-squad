package fleet

// The operator mailbox (plan 3.5, R38; ruling 16): the owner's user handle in
// the operator's AMQ session. Every squad lead sends gates and status to it
// through AMQ cross-project routing (`amq send --project P --session S`,
// which adds the sender-origin metadata a reply needs); the operator relays
// back the same way. Both routes are argv arrays, never shell strings.

// LeadToOperatorArgv is the squad lead's send to the operator mailbox.
func LeadToOperatorArgv(op Operator, s Squad, kind, thread, subject, body string) []string {
	argv := []string{"amq", "send", "--root", s.Root, "--me", s.Lead,
		"--project", op.Project, "--session", op.Session, "--to", OperatorHandle}
	return appendMessage(argv, kind, thread, subject, body)
}

// RelayArgv is the operator's send to a seat of a squad (a relay or an answer
// to a lead question). An empty body leaves --body for the caller to append.
func RelayArgv(op Operator, s Squad, to, thread, body string) []string {
	if to == "" {
		to = s.Lead
	}
	argv := []string{"amq", "send", "--root", op.Root, "--me", OperatorHandle,
		"--project", s.ProjectName(), "--session", s.SessionName(), "--to", to}
	return appendMessage(argv, "answer", thread, "", body)
}

// StatusThread is the thread a squad's status and gate notices use in the
// operator mailbox.
func StatusThread(s Squad) string { return "fleet/" + s.Name }

func appendMessage(argv []string, kind, thread, subject, body string) []string {
	for _, kv := range [][2]string{{"--kind", kind}, {"--thread", thread}, {"--subject", subject}, {"--body", body}} {
		if kv[1] != "" {
			argv = append(argv, kv[0], kv[1])
		}
	}
	return argv
}
