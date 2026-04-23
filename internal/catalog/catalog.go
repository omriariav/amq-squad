// Package catalog is the built-in directory of role archetypes amq-squad
// understands. It's intentionally small and Go-native so a new role is one
// code change plus a rebuild, not a config file users have to discover.
package catalog

// Role is one entry in the catalog. Field semantics:
//
//	ID               short slug (also used as default handle and session name)
//	Label            human title shown in listings and role.md
//	PreferredBinary  "claude" or "codex", user can override at team init time
//	Description      one-line summary seeded into role.md
//	Skills           slash commands worth invoking in this role's priming
//	DefaultPeers     role IDs this archetype usually talks to (used by slice B)
type Role struct {
	ID              string
	Label           string
	PreferredBinary string
	Description     string
	Skills          []string
	DefaultPeers    []string
}

var roles = []Role{
	{
		ID:              "cpo",
		Label:           "CPO",
		PreferredBinary: "codex",
		Description:     "Owns product strategy, vision, and prioritization. Pushes back on scope and sharpens the why before the what.",
		Skills:          []string{"/product-strategy"},
		DefaultPeers:    []string{"cto", "pm", "designer"},
	},
	{
		ID:              "cto",
		Label:           "CTO",
		PreferredBinary: "codex",
		Description:     "Owns technical direction, architecture, and engineering tradeoffs. Signs off on the shape of the system.",
		DefaultPeers:    []string{"cpo", "fullstack", "qa"},
	},
	{
		ID:              "fullstack",
		Label:           "Fullstack Developer",
		PreferredBinary: "claude",
		Description:     "Implements features end to end across frontend and backend. Writes code that gets merged.",
		DefaultPeers:    []string{"cto", "qa", "pm", "designer"},
	},
	{
		ID:              "qa",
		Label:           "QA Manager",
		PreferredBinary: "claude",
		Description:     "Owns test strategy, regression coverage, and release gating. Turns intent into verifiable checks.",
		DefaultPeers:    []string{"fullstack", "cto", "pm"},
	},
	{
		ID:              "pm",
		Label:           "Project Manager / Product Owner",
		PreferredBinary: "claude",
		Description:     "Translates product strategy into ordered work. Tracks scope, unblocks, and keeps the team aligned on what ships next.",
		DefaultPeers:    []string{"cpo", "fullstack", "qa", "designer"},
	},
	{
		ID:              "designer",
		Label:           "Product Designer",
		PreferredBinary: "claude",
		Description:     "Designs the product surface. Produces UI components, flows, and visual assets, leaning on /frontend-design and /canvas-design.",
		Skills:          []string{"/frontend-design", "/canvas-design"},
		DefaultPeers:    []string{"cpo", "fullstack", "pm"},
	},
}

// All returns a copy of the catalog in display order.
func All() []Role {
	out := make([]Role, len(roles))
	copy(out, roles)
	return out
}

// Lookup returns the role with the given ID, or nil if unknown.
func Lookup(id string) *Role {
	for i := range roles {
		if roles[i].ID == id {
			r := roles[i]
			return &r
		}
	}
	return nil
}

// IDs returns the set of known role IDs in catalog order.
func IDs() []string {
	out := make([]string, len(roles))
	for i, r := range roles {
		out[i] = r.ID
	}
	return out
}
