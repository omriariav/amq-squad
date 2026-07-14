package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type rmPaneWork struct {
	Role        string
	Handle      string
	Record      launch.Record
	RecordFound bool
	Member      team.Member
	MemberFound bool
	Request     PaneCleanupRequest
	Prepared    PaneCleanupPreparation
	Pane        PaneCleanupResult
	AgentStatus string
	AgentDetail string
}

func snapshotRmPaneWork(root string, tm team.Team, projectDir, profile, session, baseRoot string, requested bool) ([]rmPaneWork, error) {
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			info, rootErr := os.Stat(root)
			if rootErr == nil && info.IsDir() {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("enumerate agent mailboxes %q: %w", agentsDir, err)
	}
	members := map[string]team.Member{}
	for _, member := range tm.Members {
		members[strings.TrimSpace(member.Handle)] = member
	}
	work := make([]rmPaneWork, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("inspect agent mailbox %q: %w", filepath.Join(agentsDir, entry.Name()), infoErr)
		}
		if !info.IsDir() {
			continue
		}
		handle := entry.Name()
		item := rmPaneWork{Handle: handle, Pane: paneCleanupUnavailableWithoutRecord(requested, "launch record unavailable"), AgentStatus: "not_requested"}
		item.Request.Requested = requested
		rec, readErr := launch.Read(filepath.Join(root, "agents", handle))
		if readErr == nil {
			item.Record, item.RecordFound = rec, true
			item.Role = strings.TrimSpace(rec.Role)
			if strings.TrimSpace(rec.Handle) != "" {
				item.Handle = strings.TrimSpace(rec.Handle)
			}
		} else {
			item.AgentDetail = "read launch record: " + readErr.Error()
		}
		member, ok := members[item.Handle]
		if !ok && item.Role != "" {
			for _, candidate := range tm.Members {
				if candidate.Role == item.Role {
					member, ok = candidate, true
					break
				}
			}
		}
		if ok {
			item.Member, item.MemberFound = member, true
			item.Role = member.Role
		}
		if item.RecordFound && item.MemberFound {
			cwd := member.EffectiveCWD(tm.Project)
			item.Request = paneCleanupRequestForMember(tm, projectDir, profile, session, member, item.Handle, cwd, root, baseRoot, rec, requested, PaneCleanupAgentAttestation{})
		} else if item.RecordFound {
			item.Request = PaneCleanupRequest{Requested: requested, Record: rec, Scope: PaneCleanupScope{
				ProjectDir: projectDir, TeamHome: tm.Project, Profile: profile, Root: root, BaseRoot: baseRoot,
				Session: session, Role: "", Handle: item.Handle, Binary: "", CWD: rec.CWD,
			}}
		}
		work = append(work, item)
	}
	sort.Slice(work, func(i, j int) bool { return work[i].Handle < work[j].Handle })
	return work, nil
}

func plannedRmManifestEntries(work []rmPaneWork) []paneCleanupManifestEntry {
	entries := make([]paneCleanupManifestEntry, 0, len(work))
	for _, item := range work {
		identity := PaneCleanupIdentity{}
		if item.RecordFound {
			identity = identityForCleanup(item.Request)
		}
		entries = append(entries, paneCleanupManifestEntry{Role: item.Role, Handle: item.Handle, Requested: item.Request.Requested, Identity: identity})
	}
	return entries
}

func attestAndStopRmAgents(work []rmPaneWork, liveSet map[string]bool, stopAgents bool, term processTerminator, probe state.Probe, deps PaneCleanupDependencies) {
	if term == nil {
		term = newSignalTerminator(false)
	}
	for i := range work {
		item := &work[i]
		if item.RecordFound && recordIsExternal(item.Record) {
			item.Pane = PreparePaneCleanup(item.Request, deps).Result
			item.AgentStatus = "external"
			continue
		}
		if !item.RecordFound || !item.MemberFound {
			if item.Request.Requested {
				item.Pane = paneCleanupUnavailableWithoutRecord(true, "launch record or configured member unavailable")
			} else {
				item.Pane = PreparePaneCleanup(item.Request, deps).Result
			}
			item.AgentStatus = "not_live"
			continue
		}
		if !stopAgents || !liveSet[item.Handle] {
			item.Pane = PreparePaneCleanup(item.Request, deps).Result
			item.AgentStatus = "not_signaled"
			item.AgentDetail = "no pre-signal live PID/binary attestation; pane preserved"
			continue
		}
		pid := item.Record.AgentPID
		binary := strings.TrimSpace(item.Record.Binary)
		alive := pid > 0 && probe.PIDAlive(pid)
		binaryMatch := alive && binary != "" && probe.ProcessMatch(pid, agentProcessMatcher(binary))
		att := PaneCleanupAgentAttestation{PID: pid, Binary: binary, Live: alive, BinaryMatch: binaryMatch}
		item.Request.Attestation = att
		item.Prepared = PreparePaneCleanup(item.Request, deps)
		item.Pane = item.Prepared.Result
		if !alive || !binaryMatch {
			item.AgentStatus = "not_live"
			item.AgentDetail = "recorded PID/binary could not be verified immediately before signal"
			continue
		}
		if err := term.Terminate(pid); err != nil {
			item.AgentStatus = "signal_failed"
			item.AgentDetail = err.Error()
			if item.Prepared.Ready {
				item.Pane = paneCleanupPreservedAfterPreparation(item.Prepared, "agent signal failed; pane preserved")
			}
			continue
		}
		item.AgentStatus = "stopped"
		item.AgentDetail = fmt.Sprintf("%s sent to pid %d", signalNameOf(term), pid)
	}
}

func preservePreparedRmPanes(work []rmPaneWork, detail string) {
	for i := range work {
		if work[i].Prepared.Ready {
			work[i].Pane = paneCleanupPreservedAfterPreparation(work[i].Prepared, detail)
		}
	}
}

func stoppedRmAgentCount(work []rmPaneWork) int {
	count := 0
	for _, item := range work {
		if item.AgentStatus == "stopped" {
			count++
		}
	}
	return count
}

func closePreparedRmPanes(work []rmPaneWork, deps PaneCleanupDependencies) {
	for i := range work {
		if work[i].Prepared.Ready && work[i].AgentStatus == "stopped" {
			work[i].Pane = ClosePreparedPane(work[i].Prepared, deps)
		}
	}
}

func finalRmManifestEntries(work []rmPaneWork) []paneCleanupManifestEntry {
	entries := make([]paneCleanupManifestEntry, 0, len(work))
	for _, item := range work {
		identity := PaneCleanupIdentity{}
		if item.RecordFound {
			identity = identityForCleanup(item.Request)
		}
		pane := item.Pane
		entries = append(entries, paneCleanupManifestEntry{Role: item.Role, Handle: item.Handle, Requested: item.Request.Requested,
			Identity: identity, AgentStatus: item.AgentStatus, AgentDetail: item.AgentDetail, Pane: &pane})
	}
	return entries
}

func rmPanePartial(work []rmPaneWork) int {
	count := 0
	for _, item := range work {
		switch item.Pane.Outcome {
		case PaneCleanupClosed, PaneCleanupAlreadyGone, PaneCleanupNotRequested:
		default:
			count++
		}
	}
	return count
}

type rmPaneSummary struct {
	Closed                int `json:"closed"`
	AlreadyGone           int `json:"already_gone"`
	NotRequested          int `json:"not_requested"`
	Preserved             int `json:"preserved"`
	CloseFailed           int `json:"close_failed"`
	InspectionUnavailable int `json:"inspection_unavailable"`
}

type rmCleanupJSONReport struct {
	Role   string            `json:"role"`
	Handle string            `json:"handle"`
	Agent  downAgentJSON     `json:"agent"`
	Pane   PaneCleanupResult `json:"pane"`
}

type rmCleanupEnvelopeData struct {
	Project           string                `json:"project"`
	Profile           string                `json:"profile"`
	Session           string                `json:"session"`
	Root              string                `json:"root"`
	Operation         string                `json:"operation"`
	PreparedManifest  string                `json:"prepared_manifest"`
	FinalManifest     string                `json:"final_manifest,omitempty"`
	FinalCandidate    string                `json:"final_manifest_candidate,omitempty"`
	NamespaceMutation string                `json:"namespace_mutation"`
	Finalization      string                `json:"finalization_status"`
	Reports           []rmCleanupJSONReport `json:"reports"`
	Summary           rmPaneSummary         `json:"summary"`
}

func summarizeRmPaneWork(work []rmPaneWork) rmPaneSummary {
	var out rmPaneSummary
	for _, item := range work {
		addPaneOutcomeSummary(&out, item.Pane.Outcome)
	}
	return out
}

func addPaneOutcomeSummary(out *rmPaneSummary, outcome PaneCleanupOutcome) {
	switch outcome {
	case PaneCleanupClosed:
		out.Closed++
	case PaneCleanupAlreadyGone:
		out.AlreadyGone++
	case PaneCleanupNotRequested:
		out.NotRequested++
	case PaneCleanupCloseFailed:
		out.CloseFailed++
	case PaneCleanupInspectionUnavailable:
		out.InspectionUnavailable++
	default:
		out.Preserved++
	}
}

func rmCleanupJSONReports(work []rmPaneWork) []rmCleanupJSONReport {
	out := make([]rmCleanupJSONReport, 0, len(work))
	for _, item := range work {
		out = append(out, rmCleanupJSONReport{Role: item.Role, Handle: item.Handle,
			Agent: downAgentJSON{Outcome: downStatus(item.AgentStatus), Detail: item.AgentDetail}, Pane: item.Pane})
	}
	return out
}

func renderRmPaneResults(out io.Writer, work []rmPaneWork) {
	for _, item := range work {
		fmt.Fprintf(out, "pane %-12s agent=%-14s pane=%s detail=%s\n", item.Role, item.AgentStatus, item.Pane.Outcome, item.Pane.Detail)
		for _, mismatch := range item.Pane.Mismatches {
			fmt.Fprintf(out, "  mismatch %s expected=%q actual=%q\n", mismatch.Field, mismatch.Expected, mismatch.Actual)
		}
		if item.Pane.Recovery != nil {
			fmt.Fprintf(out, "  recovery pane=%s session=%s window=%s\n", item.Pane.Recovery.Identity.PaneID, item.Pane.Recovery.Identity.TmuxSession, item.Pane.Recovery.Identity.WindowID)
		}
	}
	s := summarizeRmPaneWork(work)
	fmt.Fprintf(out, "pane cleanup summary: %d closed, %d already_gone, %d not_requested, %d preserved, %d close_failed, %d inspection_unavailable\n",
		s.Closed, s.AlreadyGone, s.NotRequested, s.Preserved, s.CloseFailed, s.InspectionUnavailable)
	if s.Preserved+s.CloseFailed+s.InspectionUnavailable > 0 {
		fmt.Fprintln(out, "recovery: inspect the retained prepared/final pane-cleanup manifests; preserved panes require explicit operator review")
	}
}

func paneManifestPrepareError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("pane cleanup manifest prepare failed before lifecycle mutation: %w", err)
}

func paneManifestFinalizePartial(handle paneCleanupManifestHandle, err error) error {
	if err == nil {
		return nil
	}
	return &PartialError{Message: fmt.Sprintf("pane cleanup finalization uncertain; prepared evidence retained at %s", handle.Prepared), Cause: err}
}
