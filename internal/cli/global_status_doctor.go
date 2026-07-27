package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const globalNOCRegistrationDoctorCheck = "global NOC registration"

func doctorCheckGlobalNOCRegistration(t team.Team, profile, workstream string) doctorCheck {
	leadRole := strings.TrimSpace(t.Lead)
	if leadRole == "" {
		return doctorCheck{
			Name: globalNOCRegistrationDoctorCheck, Status: doctorOK,
			Detail: "team has no global NOC registration claim",
		}
	}
	lead, ok := globalStatusLeadMember(t, leadRole)
	if !ok {
		return doctorCheck{
			Name: globalNOCRegistrationDoctorCheck, Status: doctorFail,
			Detail: fmt.Sprintf("team lead role %q is absent; global NOC registration cannot be scoped", leadRole),
		}
	}
	cwd := lead.EffectiveCWD(t.Project)
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, memberHandle(lead))
	if err != nil {
		return doctorCheck{
			Name: globalNOCRegistrationDoctorCheck, Status: doctorWarn,
			Detail: "could not resolve the exact session root for NOC registration inspection: " + err.Error(),
		}
	}
	return doctorCheckGlobalNOCRegistrationAtRoot(t, profile, workstream, absoluteAMQRoot(cwd, env.Root))
}

func doctorCheckGlobalNOCRegistrationAtRoot(t team.Team, profile, workstream, root string) doctorCheck {
	check := doctorCheck{Name: globalNOCRegistrationDoctorCheck}
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if os.IsNotExist(err) {
		check.Status = doctorOK
		check.Detail = "exact session has no global NOC registration claim"
		return check
	}
	if err != nil {
		check.Status = doctorFail
		check.Detail = "exact session agent registry is unreadable: " + err.Error()
		return check
	}

	type candidate struct {
		handle string
		record launch.Record
	}
	var candidates []candidate
	var unreadable []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentDir := filepath.Join(agentsDir, entry.Name())
		rec, readErr := launch.Read(agentDir)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			unreadable = append(unreadable, entry.Name()+": "+readErr.Error())
			continue
		}
		if rec.OrchestratorRegistration == nil {
			continue
		}
		if !rec.External {
			check.Status = doctorFail
			check.Detail = fmt.Sprintf("agent %s carries orchestrator registration provenance without an external launch identity", entry.Name())
			return check
		}
		candidates = append(candidates, candidate{handle: entry.Name(), record: rec})
	}
	if len(unreadable) > 0 {
		check.Status = doctorFail
		check.Detail = "registration inspection is incomplete because launch records are unreadable: " + strings.Join(unreadable, "; ")
		return check
	}
	switch len(candidates) {
	case 0:
		check.Status = doctorOK
		check.Detail = "exact session has no global NOC registration claim"
		return check
	case 1:
	default:
		handles := make([]string, 0, len(candidates))
		for _, item := range candidates {
			handles = append(handles, item.handle)
		}
		check.Status = doctorFail
		check.Detail = "multiple external orchestrator registration claims are present: " + strings.Join(handles, ", ")
		return check
	}

	item := candidates[0]
	registration := item.record.OrchestratorRegistration
	if strings.TrimSpace(item.record.Handle) != item.handle ||
		strings.TrimSpace(registration.Handle) != item.handle {
		check.Status = doctorFail
		check.Detail = fmt.Sprintf(
			"external orchestrator registration handle contradicts launch identity: agent=%s launch=%q registration=%q",
			item.handle, item.record.Handle, registration.Handle,
		)
		return check
	}
	if strings.TrimSpace(item.record.Role) != goalOrchestratorRole ||
		item.record.Session != workstream ||
		!squadnamespace.ProfilesEqual(item.record.TeamProfile, profile) ||
		!sameGlobalStatusPath(item.record.CWD, t.Project) ||
		!sameGlobalStatusPath(item.record.Root, root) {
		check.Status = doctorFail
		check.Detail = fmt.Sprintf(
			"external orchestrator launch identity contradicts exact project/profile/session root for agent %s",
			item.handle,
		)
		return check
	}
	if strings.TrimSpace(registration.Policy) == "" ||
		registration.State != globalNOCRunRegistered ||
		strings.TrimSpace(registration.Handle) == "" ||
		strings.TrimSpace(registration.ExternalRegistrationID) == "" ||
		registration.ExternalGeneration == 0 ||
		registration.RegisteredAt.IsZero() {
		check.Status = doctorFail
		check.Detail = fmt.Sprintf("external orchestrator %s carries incomplete registration provenance", item.handle)
		return check
	}

	nocFields := 0
	if strings.TrimSpace(registration.NOCControlRoot) != "" {
		nocFields++
	}
	if strings.TrimSpace(registration.NOCLaunchID) != "" {
		nocFields++
	}
	if registration.NOCGeneration != 0 {
		nocFields++
	}
	if strings.TrimSpace(registration.NOCRunRegistrationID) != "" {
		nocFields++
	}
	if nocFields == 0 {
		check.Status = doctorOK
		check.Detail = fmt.Sprintf(
			"external orchestrator %s is registered by policy %s without a global NOC binding",
			item.handle, registration.Policy,
		)
		return check
	}
	if nocFields != 4 {
		check.Status = doctorFail
		check.Detail = fmt.Sprintf("external orchestrator %s carries a partial global NOC binding", item.handle)
		return check
	}

	registry, err := readGlobalNOCRegistry(registration.NOCControlRoot)
	if err != nil {
		check.Status = doctorFail
		check.Detail = "claimed global NOC registry is unreadable or invalid: " + err.Error()
		return check
	}
	expectedNamespace := squadnamespace.Resolve(t.Project, profile, workstream)
	var matchingRuns []globalNOCRun
	for i := range registry.Runs {
		if registry.Runs[i].ID == registration.NOCRunRegistrationID {
			matchingRuns = append(matchingRuns, registry.Runs[i])
		}
	}
	if len(matchingRuns) == 0 {
		check.Status = doctorFail
		check.Detail = fmt.Sprintf("global NOC registry has no run registration %s", registration.NOCRunRegistrationID)
		return check
	}
	if len(matchingRuns) > 1 {
		check.Status = doctorFail
		check.Detail = fmt.Sprintf(
			"global NOC registry has %d duplicate run registrations for %s",
			len(matchingRuns), registration.NOCRunRegistrationID,
		)
		return check
	}
	run := &matchingRuns[0]
	if !sameGlobalStatusNamespace(run.Namespace, expectedNamespace) ||
		run.NOCLaunchID != registration.NOCLaunchID ||
		run.NOCGeneration != registration.NOCGeneration ||
		run.State != globalNOCRunRegistered ||
		run.ExternalRegistration == nil ||
		!sameGlobalNOCRegistration(*run.ExternalRegistration, *registration) {
		check.Status = doctorFail
		check.Detail = "global NOC registry binding contradicts the exact project/profile/session launch provenance"
		return check
	}
	check.Status = doctorOK
	check.Detail = fmt.Sprintf(
		"verified %s at NOC generation %d (run %s, external registration %s)",
		run.State, run.NOCGeneration, run.ID, registration.ExternalRegistrationID,
	)
	return check
}

func sameGlobalNOCRegistration(a, b launch.OrchestratorRegistration) bool {
	return strings.TrimSpace(a.Policy) == strings.TrimSpace(b.Policy) &&
		a.State == b.State &&
		strings.TrimSpace(a.Handle) == strings.TrimSpace(b.Handle) &&
		a.ExternalRegistrationID == b.ExternalRegistrationID &&
		a.ExternalGeneration == b.ExternalGeneration &&
		sameGlobalStatusPath(a.NOCControlRoot, b.NOCControlRoot) &&
		a.NOCLaunchID == b.NOCLaunchID &&
		a.NOCGeneration == b.NOCGeneration &&
		a.NOCRunRegistrationID == b.NOCRunRegistrationID &&
		a.RegisteredAt.Equal(b.RegisteredAt)
}
