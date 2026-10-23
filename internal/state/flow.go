package state

// FlowEdge is one directed inter-agent message flow row for the console flow graph:
// the existing Edge (from->to message count) ENRICHED with the outstanding
// status of the work on that link, derived purely from the session's already
// collapsed threads. It answers "where is work piling up / who owes whom" at a
// glance: a Blocked or Awaiting edge marks a link that is not just busy but
// stuck.
//
// FlowEdge carries NO new scan or clock work — it is a join of two derived
// fields (Coordination.Edges and Coordination.Threads), so a render layer can
// format it without recomputing anything.
type FlowEdge struct {
	From  string
	To    string
	Count int
	// Blocked is true when at least one LIVE (non-stale) thread whose
	// participants include both From and To is ThreadBlocked. The hardest stuck
	// signal on a link.
	Blocked bool
	// Awaiting is true when at least one LIVE (non-stale) thread whose
	// participants include both From and To is ThreadAwaitingReply (and the link
	// is not already Blocked). Someone owes a reply on this link.
	Awaiting bool
}
