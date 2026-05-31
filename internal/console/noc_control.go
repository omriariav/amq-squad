// Package console — noc_control.go: the operator CONTROL layer (PR15).
//
// This is the mutating counterpart to the read-only NOC. It implements the
// GOAL.md safety model verbatim:
//
//   - Read-only is the default. The control keys (a/x/m/b/S/R/o) are ADDITIVE;
//     none of the existing nav/peek/filter/jump keys gain a side effect.
//   - Mutation is DELIBERATE + PREVIEW-FIRST + CONFIRM-GATED. Every mutating
//     action (approve/deny/message/broadcast/stop/resume) is TWO-STEP: the key
//     opens a confirm overlay that shows the EXACT effect — for AMQ writes the
//     literal `amq send …` from act.Preview, for stop/resume the exact lifecycle
//     command plus the affected agents — and NOTHING runs until the operator
//     presses y/enter. Any other key (or esc) CANCELS with zero effect.
//   - Inject-the-seam. The two mutating side effects reach the outside world
//     ONLY through m.sendOp (act.Send) and m.lifecycle (stop/resume), both
//     model fields tests swap for fakes. A declined overlay never touches a
//     seam; a confirmed overlay calls it exactly once with the exact payload.
//   - Focus-if-present-never-spawn. 'o' is the ONLY non-confirmed control key
//     because it is read-only view movement: it focuses an EXISTING tmux window
//     for the squad (via the same switchTo seam jump uses) or, when nothing is
//     running, sets a suggest-up note. It NEVER spawns and NEVER mutates squad
//     state.
package console

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/omriariav/amq-squad/v2/internal/act"
	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// controlKind names a mutating control action for the overlay header + tests.
type controlKind int

const (
	ctlApprove controlKind = iota
	ctlDeny
	ctlMessage
	ctlBroadcast
	ctlStop
	ctlResume
)

func (k controlKind) label() string {
	switch k {
	case ctlApprove:
		return "APPROVE"
	case ctlDeny:
		return "DENY"
	case ctlMessage:
		return "MESSAGE"
	case ctlBroadcast:
		return "BROADCAST"
	case ctlStop:
		return "STOP"
	case ctlResume:
		return "RESUME"
	default:
		return "ACTION"
	}
}

// lifecycleVerb is the stop/resume verb a lifecycleOp carries. It is a small
// string enum so the cli-injected seam can switch without importing console's
// controlKind.
type lifecycleVerb string

const (
	lifecycleStop   lifecycleVerb = "stop"
	lifecycleResume lifecycleVerb = "resume"
)

// lifecycleOp is the exact lifecycle effect a confirmed Stop/Resume runs. It is
// inert data (like act.OpMessage): building one performs no I/O. The cli-injected
// m.lifecycle seam turns it into an executeDown/executeResume call. ProjectDir +
// Session pin WHICH squad; Agents is the affected-roster preview the overlay
// shows so the operator confirms scope, not just verb.
type lifecycleOp struct {
	Verb       lifecycleVerb
	ProjectDir string
	Session    string
	Agents     []string
}

// command renders the EXACT lifecycle command the seam will run, the stop/resume
// analogue of act.Preview — what the overlay shows for confirm. It mirrors the
// real verbs: `amq-squad stop --all` / `amq-squad resume`, scoped by --session.
func (op lifecycleOp) command() string {
	switch op.Verb {
	case lifecycleStop:
		return "amq-squad stop --all --session " + shellToken(op.Session)
	case lifecycleResume:
		return "amq-squad resume --session " + shellToken(op.Session)
	default:
		return "amq-squad " + string(op.Verb)
	}
}

// shellToken is a tiny display-only quoter for the lifecycle preview. The act
// package owns the real shellQuote for AMQ writes; the lifecycle command is
// always a sanitized session name, so a minimal quote keeps the preview honest
// without pulling act's unexported helper across the package boundary.
func shellToken(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./@:", r)) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

// pendingAction is the confirm overlay's state: a previewed, NOT-yet-executed
// mutating action. Exactly one of op (AMQ write) / life (lifecycle) is set. The
// preview is the literal command string the overlay renders and the seam runs.
type pendingAction struct {
	kind    controlKind
	preview string
	op      act.OpMessage // set for approve/deny/message/broadcast
	life    *lifecycleOp  // set for stop/resume
	// affected lists the agents the action touches, shown under the preview so
	// scope is explicit (recipients for an AMQ write, the roster for lifecycle).
	affected []string
}

// inputAction is the (read-only) body/subject editor that precedes the confirm
// overlay for message/broadcast/deny. The operator types here; on enter the
// action BUILDS its OpMessage and transitions to pendingAction so the EXACT
// `amq send` is previewed before any confirm. esc cancels with zero effect.
type inputAction struct {
	kind controlKind
	// stage 0 = subject (broadcast only), 1 = body. message/deny skip straight
	// to body. Captured values become the OpMessage fields.
	stage   int
	subject string
	body    string
	// build turns the captured (subject, body) into the pendingAction. It is a
	// closure so the node context (root/session/thread/handles) is captured at
	// key-press time, not re-resolved after the snapshot may have moved.
	build func(subject, body string) pendingAction
}

// controlEnabled reports whether the control layer is active. It is always true
// in production; it exists so a future read-only-lock flag can disable the
// mutating keys wholesale while leaving nav intact.
func (m *NOCModel) controlEnabled() bool { return true }

// handleControlKey routes a control key when no overlay/editor is open. It
// returns handled=false for any key it does not own so handleKey falls through
// to the existing read-only keymap (the control keys are strictly additive).
//
// Every mutating branch only PREPARES state (a pending overlay or an input
// editor); none calls a seam. The seam is reached exclusively from
// handleConfirmKey after an explicit confirm. The ONLY branch with an immediate
// effect is 'o' (focus), which is read-only view movement.
func (m *NOCModel) handleControlKey(key string) (tea.Cmd, bool) {
	switch key {
	case "a":
		return m.beginApproveOrDeny(ctlApprove), true
	case "x":
		return m.beginApproveOrDeny(ctlDeny), true
	case "m":
		return m.beginMessage(), true
	case "b":
		return m.beginBroadcast(), true
	case "S":
		return m.beginLifecycle(ctlStop), true
	case "R":
		return m.beginLifecycle(ctlResume), true
	case "o":
		return m.focusTeam(), true
	}
	return nil, false
}

// --- approve / deny -------------------------------------------------------

// beginApproveOrDeny opens the confirm flow for an approve/deny on the selected
// needs-you thread. Valid on a needs-you SESSION (acts on its top needs-you
// thread) or a needs-you AGENT (acts on the agent's needs-you thread). On any
// other node it is a no-op note. Approve previews immediately; deny opens the
// reason editor first (so the operator can type a reason), then previews.
func (m *NOCModel) beginApproveOrDeny(kind controlKind) tea.Cmd {
	th, sess, ok := m.selectedNeedsYouThread()
	if !ok {
		m.actNote = strings.ToLower(kind.label()) + " applies to a needs-you thread (a paused agent / open ask) — nothing here needs you"
		return nil
	}
	root := sess.Root
	session := sess.Name
	recipients := nonOperator(th.Participants)
	if kind == ctlApprove {
		op := act.Approve(root, session, th)
		m.pending = &pendingAction{
			kind:     ctlApprove,
			preview:  act.Preview(op),
			op:       op,
			affected: recipients,
		}
		return nil
	}
	// Deny: capture a reason first, then preview act.Deny.
	m.input = &inputAction{
		kind:  ctlDeny,
		stage: 1, // body == reason
		build: func(_, reason string) pendingAction {
			op := act.Deny(root, session, th, reason)
			return pendingAction{kind: ctlDeny, preview: act.Preview(op), op: op, affected: recipients}
		},
	}
	return nil
}

// --- message --------------------------------------------------------------

// beginMessage opens the body editor for a direct message to the selected
// AGENT. On any other node kind it is a no-op note. After the body is entered,
// the action previews a direct message addressed to that single agent.
func (m *NOCModel) beginMessage() tea.Cmd {
	n, ok := m.selectedNode()
	if !ok || n.kind != nodeAgent {
		m.actNote = "message applies to an agent row — select an agent first"
		return nil
	}
	root := n.session.Root
	session := n.session.Name
	handle := strings.TrimSpace(n.agent.Handle)
	if handle == "" {
		m.actNote = "message: selected agent has no handle"
		return nil
	}
	m.input = &inputAction{
		kind:  ctlMessage,
		stage: 1, // body only
		build: func(_, body string) pendingAction {
			// A direct message is addressed to exactly the selected agent, not
			// pinned to a thread (it opens its own). Build the OpMessage
			// explicitly so the recipient is precisely that one handle, then
			// preview the exact `amq send` via act.Preview.
			op := act.OpMessage{
				Root:    root,
				Me:      state.DefaultOperatorHandle,
				To:      handle,
				Subject: "Message from operator",
				Body:    body,
				Kind:    string(state.KindStatus),
				ReplyTo: operatorReplyToConsole(session),
			}
			return pendingAction{kind: ctlMessage, preview: act.Preview(op), op: op, affected: []string{handle}}
		},
	}
	return nil
}

// --- broadcast ------------------------------------------------------------

// beginBroadcast opens the subject→body editor for a broadcast to the selected
// SQUAD (a session or project node). On any other node it is a no-op note. After
// subject + body are entered, the action previews act.Broadcast to the squad's
// non-operator handles.
func (m *NOCModel) beginBroadcast() tea.Cmd {
	handles, root, session, ok := m.selectedSquad()
	if !ok {
		m.actNote = "broadcast applies to a session or project (a squad) — select one first"
		return nil
	}
	recipients := nonOperator(handles)
	if len(recipients) == 0 {
		m.actNote = "broadcast: no agents in this squad to address"
		return nil
	}
	m.input = &inputAction{
		kind:  ctlBroadcast,
		stage: 0, // subject first, then body
		build: func(subject, body string) pendingAction {
			op := act.Broadcast(root, session, handles, subject, body)
			return pendingAction{kind: ctlBroadcast, preview: act.Preview(op), op: op, affected: recipients}
		},
	}
	return nil
}

// --- stop / resume --------------------------------------------------------

// beginLifecycle opens the confirm overlay for stop/resume on the selected
// SQUAD (session or project node). On any other node it is a no-op note. The
// overlay shows the exact lifecycle command + the affected agents; the seam is
// reached only on confirm.
func (m *NOCModel) beginLifecycle(kind controlKind) tea.Cmd {
	handles, _, session, ok := m.selectedSquad()
	if !ok {
		m.actNote = strings.ToLower(kind.label()) + " applies to a session or project (a squad) — select one first"
		return nil
	}
	dir := m.selectedProjectDir()
	verb := lifecycleStop
	if kind == ctlResume {
		verb = lifecycleResume
	}
	op := lifecycleOp{Verb: verb, ProjectDir: dir, Session: session, Agents: handles}
	m.pending = &pendingAction{
		kind:     kind,
		preview:  op.command(),
		life:     &op,
		affected: handles,
	}
	return nil
}

// --- focus / open ('o') ---------------------------------------------------

// focusTeam opens the READ-ONLY focus CONFIRM overlay for the selected squad
// (QA-2 / QA-4b): it does NOT focus immediately. It previews "Open/focus squad
// <session>?" and only a confirmed y/Y/enter runs the focus (performFocusTeam);
// any other key / esc cancels with zero effect. It is read-only view movement —
// never a mutation and never a spawn. (Validity / not-running notes are still
// surfaced eagerly so the operator is not asked to confirm a no-op.)
func (m *NOCModel) focusTeam() tea.Cmd {
	_, _, session, ok := m.selectedSquad()
	if !ok {
		m.actNote = "open applies to a session or project (a squad) — select one first"
		return nil
	}
	projectDir := m.selectedProjectDir()
	m.jumpPending = &pendingFocus{
		prompt: "Open/focus squad " + session + "? (focus its iTerm2 window)",
		run:    func(m *NOCModel) { m.performFocusTeam(session, projectDir) },
	}
	return nil
}

// performFocusTeam focuses an EXISTING tmux window for the squad: resolveSquadWindow
// (read-only) then the switchTo seam, or a suggest-up note when nothing is
// running. It NEVER spawns. It is reached ONLY from the focus-confirm gate, so a
// switchTo call here always corresponds to an operator confirm.
func (m *NOCModel) performFocusTeam(session, projectDir string) {
	panes, err := m.panes()
	if err != nil {
		m.actNote = "tmux not available: " + err.Error()
		return
	}
	target, found := resolveSquadWindow(session, projectDir, panes)
	if !found {
		m.actNote = "team not running — press R to resume or run amq-squad up " + session
		return
	}
	if err := m.switchTo(target); err != nil {
		if nit, isNIT := err.(*noc.NotInTmuxError); isNIT {
			m.actNote = "not inside tmux — run: " + nit.Command
			return
		}
		m.actNote = "open: " + err.Error() + " (try: " + noc.SuggestJump(target) + ")"
		return
	}
	m.actNote = "focused " + noc.SuggestJump(target)
}

// resolveSquadWindow finds an existing tmux window for the squad: a pane whose
// tmux-session name equals the amq session, or (fallback) a pane whose cwd is
// the project dir. Read-only — it only reads the pane list. found=false when no
// window exists (the squad is not running): the caller then suggests up/resume,
// never spawns.
func resolveSquadWindow(session, projectDir string, panes []noc.TmuxPane) (noc.TmuxTarget, bool) {
	// Prefer an exact tmux-session==amq-session match.
	for _, p := range panes {
		if session != "" && p.Session == session {
			return squadTargetFromPane(p), true
		}
	}
	// Fallback: any pane rooted in the project dir (a current-window launch puts
	// the squad in whatever tmux session the operator ran `up` from).
	want := cleanDirForFocus(projectDir)
	if want != "" {
		for _, p := range panes {
			if cleanDirForFocus(p.CWD) == want {
				return squadTargetFromPane(p), true
			}
		}
	}
	return noc.TmuxTarget{}, false
}

// squadTargetFromPane builds a focus target from a squad pane, carrying its
// title token + window name so the cross-session iTerm2 -CC focus (SwitchTo) can
// raise the right native window without switch-client exploding the layout.
func squadTargetFromPane(p noc.TmuxPane) noc.TmuxTarget {
	return noc.TmuxTarget{
		Session:    p.Session,
		Window:     p.Window,
		Pane:       p.Pane,
		Title:      p.Title,
		WindowName: p.WindowName,
	}
}

// --- confirm / input key routing -----------------------------------------

// handleConfirmKey routes a key while the confirm overlay is open. y/enter
// EXECUTES the previewed action through the seam exactly once; ANY other key
// (esc included) CANCELS with zero effect. This is the single gate between a
// keypress and a mutation.
func (m *NOCModel) handleConfirmKey(key string) (tea.Model, tea.Cmd) {
	p := m.pending
	switch key {
	case "y", "Y", "enter":
		m.pending = nil
		m.runPending(p)
		return m, nil
	default:
		// Decline: clear the overlay, call NOTHING.
		m.pending = nil
		m.actNote = strings.ToLower(p.kind.label()) + " cancelled — nothing sent"
		return m, nil
	}
}

// runPending executes a confirmed action through the appropriate seam. It is
// reached ONLY from handleConfirmKey on an explicit confirm, so a seam call
// here always corresponds to an operator confirm.
func (m *NOCModel) runPending(p *pendingAction) {
	if p == nil {
		return
	}
	switch {
	case p.life != nil:
		if m.lifecycle == nil {
			m.actNote = strings.ToLower(p.kind.label()) + " unavailable in this context (no lifecycle backend)"
			return
		}
		if err := m.lifecycle(*p.life); err != nil {
			m.actNote = strings.ToLower(p.kind.label()) + " failed: " + err.Error()
			return
		}
		m.actNote = p.kind.label() + " sent: " + p.preview
	default:
		if m.sendOp == nil {
			m.actNote = strings.ToLower(p.kind.label()) + " unavailable (no AMQ backend)"
			return
		}
		if err := m.sendOp(p.op); err != nil {
			m.actNote = strings.ToLower(p.kind.label()) + " failed: " + err.Error()
			return
		}
		m.actNote = p.kind.label() + " sent: " + p.preview
	}
}

// handleInputKey edits the body/subject editor. enter advances a broadcast from
// subject→body, then on the final stage BUILDS the OpMessage and transitions to
// the confirm overlay (preview-first). esc cancels with zero effect. No seam is
// reached here — the editor only captures text and produces a pending preview.
func (m *NOCModel) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	in := m.input
	switch msg.String() {
	case "esc":
		m.input = nil
		m.actNote = strings.ToLower(in.kind.label()) + " cancelled — nothing sent"
		return m, nil
	case "enter":
		if in.kind == ctlBroadcast && in.stage == 0 {
			// Captured subject; advance to body.
			in.stage = 1
			return m, nil
		}
		// Final stage: build the preview and open the confirm overlay.
		m.input = nil
		m.pending = ptrPending(in.build(in.subject, in.body))
		return m, nil
	case "backspace":
		if in.kind == ctlBroadcast && in.stage == 0 {
			in.subject = dropLast(in.subject)
		} else {
			in.body = dropLast(in.body)
		}
		return m, nil
	default:
		if len(msg.String()) == 1 {
			if in.kind == ctlBroadcast && in.stage == 0 {
				in.subject += msg.String()
			} else {
				in.body += msg.String()
			}
		}
		return m, nil
	}
}

// ptrPending boxes a pendingAction value (the input builder returns a value).
func ptrPending(p pendingAction) *pendingAction { return &p }

// dropLast trims the last byte of a single-line editor buffer.
func dropLast(s string) string {
	if s == "" {
		return s
	}
	return s[:len(s)-1]
}

// --- selection helpers ----------------------------------------------------

// selectedNeedsYouThread returns the needs-you thread the approve/deny keys act
// on for the selected node, plus its owning session. For a SESSION node it is the
// session's top needs-you thread; for an AGENT node it is that agent's
// highest-urgency needs-you thread. ok=false on any other node, or when the node
// has nothing that needs the operator.
func (m *NOCModel) selectedNeedsYouThread() (state.ThreadSummary, state.Session, bool) {
	n, ok := m.selectedNode()
	if !ok {
		return state.ThreadSummary{}, state.Session{}, false
	}
	switch n.kind {
	case nodeSession:
		ny := n.session.Coordination.NeedsYouThreads()
		th, found := mostUrgent(ny, "")
		return th, n.session, found
	case nodeAgent:
		ny := n.session.Coordination.NeedsYouThreads()
		th, found := mostUrgent(ny, n.agent.Handle)
		return th, n.session, found
	default:
		return state.ThreadSummary{}, state.Session{}, false
	}
}

// mostUrgent picks the highest-urgency needs-you thread (lowest AttnReason rank),
// optionally restricted to those a given handle participates in. ok=false when
// the filtered set is empty.
func mostUrgent(threads []state.ThreadSummary, handle string) (state.ThreadSummary, bool) {
	best := -1
	var chosen state.ThreadSummary
	for _, th := range threads {
		if handle != "" && !threadHasParticipant(th, handle) {
			continue
		}
		rank := th.AttnReason.Rank()
		if best < 0 || rank < best {
			best = rank
			chosen = th
		}
	}
	return chosen, best >= 0
}

// selectedSquad returns the handles, root, and session name of the squad the
// broadcast/stop/resume/open keys act on. A SESSION node yields that session; a
// PROJECT node yields its most-attention-worthy session (the first, since the
// tree sorts attention-first). ok=false on root/agent nodes.
func (m *NOCModel) selectedSquad() (handles []string, root, session string, ok bool) {
	n, ok2 := m.selectedNode()
	if !ok2 {
		return nil, "", "", false
	}
	switch n.kind {
	case nodeSession:
		return agentHandles(n.session.Agents), n.session.Root, n.session.Name, true
	case nodeProject:
		sessions := sortedSessions(n.project.Snap.Sessions)
		if len(sessions) == 0 {
			return nil, "", "", false
		}
		s := sessions[0]
		return agentHandles(s.Agents), s.Root, s.Name, true
	default:
		return nil, "", "", false
	}
}

// selectedProjectDir returns the on-disk project dir for the selected node (used
// to pin lifecycle ops and as the focus fallback). Empty when not resolvable.
func (m *NOCModel) selectedProjectDir() string {
	n, ok := m.selectedNode()
	if !ok {
		return ""
	}
	return n.project.Dir
}

// agentHandles collects the (non-empty) handles of a session's agents.
func agentHandles(agents []state.Agent) []string {
	out := make([]string, 0, len(agents))
	for _, a := range agents {
		if h := strings.TrimSpace(a.Handle); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// nonOperator drops the operator handle from a participant/handle list so a
// preview never shows the operator addressing itself. Order is preserved.
func nonOperator(in []string) []string {
	out := make([]string, 0, len(in))
	for _, h := range in {
		h = strings.TrimSpace(h)
		if h == "" || h == state.DefaultOperatorHandle {
			continue
		}
		out = append(out, h)
	}
	return out
}

// operatorReplyToConsole renders the conventional operator reply-to address
// "user@<session>" for the console-built direct message, degrading to the bare
// operator handle when the session is empty (mirrors act.operatorReplyTo, which
// is unexported in that package).
func operatorReplyToConsole(session string) string {
	s := strings.TrimSpace(session)
	if s == "" {
		return state.DefaultOperatorHandle
	}
	return state.DefaultOperatorHandle + "@" + s
}

// cleanDirForFocus normalizes a dir for the focus cwd-fallback comparison.
func cleanDirForFocus(dir string) string {
	return strings.TrimRight(strings.TrimSpace(dir), "/")
}

// --- overlay rendering ----------------------------------------------------

// confirmOverlayView renders the confirm overlay: the action header, the EXACT
// preview (the literal command the seam will run), the affected agents, and the
// y/esc affordance. This is what makes mutation PREVIEW-FIRST: the operator sees
// byte-for-byte what will happen before any confirm.
func (m NOCModel) confirmOverlayView() string {
	p := m.pending
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.needsYou, "CONFIRM "+p.kind.label()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.dim, "this will run:"))
	b.WriteString("\n  ")
	b.WriteString(m.th.paint(m.th.brand, p.preview))
	b.WriteString("\n")
	if len(p.affected) > 0 {
		b.WriteString(m.th.paint(m.th.dim, "affects: "+strings.Join(p.affected, ", ")))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	hint := "y confirm · esc cancel"
	if m.colorMode == ColorAscii {
		hint = "y confirm | esc cancel"
	}
	b.WriteString(m.th.paint(m.th.needsYou, hint))
	return b.String()
}

// focusConfirmOverlayView renders the READ-ONLY focus confirm overlay (jump / J
// / o): the action header, the prompt ("Jump to … (focus its iTerm2 window)" /
// "Open/focus squad …"), and the y/esc affordance. Unlike the mutating confirm
// it carries no `amq send` / lifecycle command — its only effect is terminal
// focus, so the prompt states the focus, not a squad-changing command.
func (m NOCModel) focusConfirmOverlayView() string {
	p := m.jumpPending
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.running, "CONFIRM FOCUS"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.brand, p.prompt))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.dim, "read-only — moves your terminal view only, never squad state"))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	hint := "y focus · esc cancel"
	if m.colorMode == ColorAscii {
		hint = "y focus | esc cancel"
	}
	b.WriteString(m.th.paint(m.th.running, hint))
	return b.String()
}

// inputOverlayView renders the body/subject editor. It shows which field is
// being typed and the running buffer, plus the cancel affordance. It is NOT a
// confirm step — the preview comes after, on the confirm overlay.
func (m NOCModel) inputOverlayView() string {
	in := m.input
	var b strings.Builder
	b.WriteString(m.th.paint(m.th.brand, in.kind.label()))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	cursor := "▏"
	if m.colorMode == ColorAscii {
		cursor = "_"
	}
	field := "body"
	val := in.body
	if in.kind == ctlBroadcast && in.stage == 0 {
		field = "subject"
		val = in.subject
	} else if in.kind == ctlDeny {
		field = "reason"
	}
	// On the broadcast body stage, show the captured subject for context.
	if in.kind == ctlBroadcast && in.stage == 1 {
		b.WriteString(m.th.paint(m.th.dim, "subject: "+in.subject))
		b.WriteString("\n")
	}
	b.WriteString(m.th.paint(m.th.atRisk, field+": "+val+cursor))
	b.WriteString("\n")
	b.WriteString(m.th.paint(m.th.rule, m.thinRule()))
	b.WriteString("\n")
	next := "enter preview · esc cancel"
	if in.kind == ctlBroadcast && in.stage == 0 {
		next = "enter next · esc cancel"
	}
	if m.colorMode == ColorAscii {
		next = strings.ReplaceAll(next, "·", "|")
	}
	b.WriteString(m.th.paint(m.th.dim, next))
	return b.String()
}

// controlFooterKeys is the additive control-key legend appended to the footer.
func controlFooterKeys(ascii bool) string {
	if ascii {
		return "a approve | x deny | m message | b broadcast | S stop | R resume | o open"
	}
	return "a approve · x deny · m message · b broadcast · S stop · R resume · o open"
}

// controlHelpLines is the CONTROL section of the help overlay.
func controlHelpLines() []string {
	return []string{
		"CONTROL (mutating — every one previews + confirms first)",
		"  a                 approve the selected needs-you thread (into AMQ as user)",
		"  x                 deny the selected needs-you thread (type a reason)",
		"  m                 message the selected agent (type a body)",
		"  b                 broadcast to the selected squad (type subject + body)",
		"  S                 stop the selected squad (lifecycle)",
		"  R                 resume the selected squad (lifecycle)",
		"  o                 open/focus the squad's tmux window (read-only; never spawns)",
		"",
		"Every mutating key opens a CONFIRM overlay showing the EXACT command",
		"(amq send … or amq-squad stop/resume). y/enter confirms; any other key",
		"or esc cancels with ZERO effect. 'o' is read-only view movement only.",
	}
}
