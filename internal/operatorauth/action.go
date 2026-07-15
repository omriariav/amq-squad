package operatorauth

// SupportedActions returns the canonical action kinds accepted by the hard
// action verifier. The returned slice is sorted and detached from catalog
// storage so callers cannot mutate authorization policy.
func SupportedActions() []string {
	items := ActionCapabilities()
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Action
	}
	return out
}
