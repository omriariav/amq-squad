package operatorauth

type MessageFact struct {
	ID       string
	From     string
	Kind     string
	Decision string
	Bound    bool
	After    bool
	Order    int64
}

type Precedence struct {
	Source    string
	Decision  string
	Barrier   bool
	MessageID string
	Order     int64
}

func ResolvePrecedence(facts []MessageFact, humanHandle, selfHandle string) Precedence {
	var self, humanDenied, humanApproval, humanBarrier Precedence
	later := func(current Precedence, candidate Precedence) Precedence {
		if current.MessageID == "" || candidate.Order > current.Order || candidate.Order == current.Order && candidate.MessageID > current.MessageID {
			return candidate
		}
		return current
	}
	for _, fact := range facts {
		if !fact.After {
			continue
		}
		if fact.From == humanHandle {
			if fact.Bound && fact.Decision == "denied" {
				humanDenied = later(humanDenied, Precedence{Source: "human", Decision: "denied", MessageID: fact.ID, Order: fact.Order})
				continue
			}
			if fact.Bound && fact.Decision == "approved" {
				humanApproval = later(humanApproval, Precedence{Source: "human", Decision: "approved", MessageID: fact.ID, Order: fact.Order})
				continue
			}
			humanBarrier = later(humanBarrier, Precedence{Source: "human", Decision: "pending", Barrier: true, MessageID: fact.ID, Order: fact.Order})
			continue
		}
		if fact.From == selfHandle && fact.Bound {
			self = later(self, Precedence{Source: "self_operator", Decision: fact.Decision, MessageID: fact.ID, Order: fact.Order})
		}
	}
	if humanDenied.Source != "" {
		return humanDenied
	}
	if humanApproval.Source != "" {
		return humanApproval
	}
	if humanBarrier.Source != "" {
		return humanBarrier
	}
	return self
}
