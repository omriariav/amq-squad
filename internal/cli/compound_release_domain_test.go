package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestCLIReleaseDomainEligibleDefaultAndGuardObservationIsImmutable(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	selected := selectedContextForCLIReleaseFixture(fixture)
	question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)

	classification, err := classifyCLIReleaseQuestion(selected, question)
	if err != nil {
		t.Fatal(err)
	}
	if classification.Disposition != cliReleaseDomainReleaseOwned || !classification.Eligible || classification.Claim == nil || classification.Claim.Role != operatorauth.ReleaseChildTag || classification.Claim.Ordinal != 0 {
		t.Fatalf("classification=%+v", classification)
	}
	if classification.Claim == classification.Resolution.Claim || &classification.Claim.Receipt.Recipients[0] == &classification.Resolution.Claim.Receipt.Recipients[0] {
		t.Fatal("classification claim aliases resolver claim")
	}
	use, err := classification.NewGuardedUse()
	if err != nil {
		t.Fatal(err)
	}
	classification.Claim.Role = "mutated-after-guard-construction"
	classification.Claim.Receipt.Recipients[0] = "mutated-claim-recipient"
	classification.Resolution.Claim.Role = "mutated-resolution-role"
	classification.Resolution.Claim.Receipt.Recipients[0] = "mutated-resolution-recipient"
	duplicate := state.Message{
		ID: "duplicate-observation", From: "user", To: []string{"cto"}, Thread: question.Thread, RawThread: question.RawThread,
		Subject: "STATUS: observed", RawSubject: "STATUS: observed", Kind: state.KindStatus, Priority: state.PriorityNormal,
		Body: "observation", RawBody: "observation", AuthorityRaw: true, SchemaOK: true,
		Created: question.Created.Add(time.Second), RawCreated: question.Created.Add(time.Second).Format(time.RFC3339Nano),
	}
	writeRawCLIReleaseMessage(t, fixture.adapter.root, "cto", "cur", duplicate, nil)
	writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "cur", duplicate, nil)
	rawAlias := duplicate
	rawAlias.ID = "canonical-collision-alias"
	rawAlias.RawThread = "/" + question.Thread
	writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "cur", rawAlias, nil)
	callbacks := 0
	if err := use.Run(func(observation cliReleaseGuardObservation) error {
		callbacks++
		firstQuestion := observation.Question()
		firstMessages := observation.Messages()
		duplicateCopies := 0
		aliasCopies := 0
		for _, message := range firstMessages {
			if message.ID == duplicate.ID {
				duplicateCopies++
			}
			if message.ID == rawAlias.ID {
				aliasCopies++
			}
		}
		if firstQuestion.ID != question.ID || len(firstMessages) == 0 || duplicateCopies != 2 || aliasCopies != 0 {
			t.Fatalf("observation question=%+v messages=%+v", firstQuestion, firstMessages)
		}
		firstQuestion.Context["release_child"] = "mutated"
		firstQuestion.To[0] = "mutated"
		firstMessages[0].To[0] = "mutated"
		for i := range firstMessages {
			if firstMessages[i].Context != nil {
				firstMessages[i].Context["release_child"] = "mutated"
				break
			}
		}
		firstQuestion.AuthorizationRequest.Action = "mutated"
		againQuestion := observation.Question()
		againMessages := observation.Messages()
		messageContextMutated := false
		for _, message := range againMessages {
			messageContextMutated = messageContextMutated || message.Context["release_child"] == "mutated"
		}
		if againQuestion.To[0] == "mutated" || againMessages[0].To[0] == "mutated" || againQuestion.Context["release_child"] == "mutated" || messageContextMutated || againQuestion.AuthorizationRequest.Action == "mutated" {
			t.Fatalf("guard observation accessors leaked mutable aliases: question=%+v messages=%+v", againQuestion, againMessages)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if callbacks != 1 {
		t.Fatalf("guard callbacks=%d", callbacks)
	}
}

func TestCLIReleaseDomainOrdinaryQuestionSurvivesUnrelatedReleaseDegradation(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	selected := selectedContextForCLIReleaseFixture(fixture)
	ordinary := state.Message{
		ID: "ordinary", From: "cto", To: []string{"user"}, Thread: "gate/ordinary", RawThread: "gate/ordinary",
		Subject: "APPROVAL: ordinary", RawSubject: "APPROVAL: ordinary", Kind: state.KindQuestion,
		Body: "body", RawBody: "body", AuthorityRaw: true, SchemaOK: true, Priority: state.PriorityNormal,
		Created: notifyNow.Add(time.Minute), RawCreated: notifyNow.Add(time.Minute).Format(time.RFC3339Nano),
	}
	writeRawCLIReleaseMessage(t, selected.SessionRoot, "user", "new", ordinary, nil)
	// Corrupt an unrelated series artifact. The resolver must degrade release
	// inspection without claiming an exact ordinary message id.
	pointer := filepath.Join(fixture.adapter.project, ".amq-squad", "evidence", fixture.adapter.profile, fixture.adapter.session, "compound-release", active.Pointer.SeriesID, "current.json")
	if err := os.WriteFile(pointer, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ordinary = releaseQuestionForCLIClassification(t, selected.SessionRoot, ordinary.ID)
	classification, err := classifyCLIReleaseQuestion(selected, ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if classification.Disposition != cliReleaseDomainOrdinary || classification.Eligible || classification.Resolution.Degradation == nil {
		t.Fatalf("ordinary question was claimed or degradation absent: %+v", classification)
	}
}

func TestCLIReleaseDomainPhysicalMarkerOwnershipFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		marker   func(any) any
		selected func(cliReleaseSelectedContext) cliReleaseSelectedContext
	}{
		{name: "malformed", marker: func(any) any { return "{" }},
		{name: "v1", marker: func(any) any { return map[string]any{"schema_version": 1} }},
		{name: "orphan", marker: func(marker any) any { return marker }, selected: orphanSelectedReleaseContext},
		{name: "root barrier", marker: func(marker any) any { return marker }, selected: func(selected cliReleaseSelectedContext) cliReleaseSelectedContext {
			selected.SessionRoot += "-wrong"
			return selected
		}},
		{name: "generation mismatch", marker: func(marker any) any { return marker }, selected: func(selected cliReleaseSelectedContext) cliReleaseSelectedContext {
			selected.NamespaceGeneration = "different-generation"
			return selected
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, active := newCLIActiveReleaseAttentionFixture(t)
			exact := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
			selected := selectedContextForCLIReleaseFixture(fixture)
			question := cloneReleaseStateMessage(exact)
			question.ID = "owned-" + strings.ReplaceAll(tc.name, " ", "-")
			question.Created = exact.Created.Add(time.Minute)
			question.RawCreated = question.Created.Format(time.RFC3339Nano)
			question.Context["release_child"] = tc.marker(exact.Context["release_child"])
			if tc.selected != nil {
				selected = tc.selected(selected)
			}
			if tc.name != "root barrier" {
				writeRawCLIReleaseMessage(t, selected.SessionRoot, "user", "new", question, question.Context)
				question = releaseQuestionForCLIClassification(t, selected.SessionRoot, question.ID)
			}
			classification, err := classifyCLIReleaseQuestion(selected, question)
			if tc.name == "root barrier" {
				if err == nil {
					t.Fatalf("wrong selected root did not fail closed: %+v", classification)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if classification.Disposition != cliReleaseDomainReleaseOwned || classification.Eligible {
				t.Fatalf("classification=%+v", classification)
			}
		})
	}
}

func TestCLIReleaseDomainExactSuppressionClaimsMarkerStrippedSelection(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
	context := cloneReleaseStateMessage(question).Context
	delete(context, "release_child")
	messages, _ := state.ScanSessionMessages(fixture.adapter.root, time.Now)
	for _, copy := range messages {
		if copy.ID == question.ID {
			writeRawCLIReleaseMessage(t, fixture.adapter.root, copy.Owner, string(copy.State), copy, context)
		}
	}
	question = releaseQuestionForCLIClassification(t, fixture.adapter.root, question.ID)
	classification, err := classifyCLIReleaseQuestion(selectedContextForCLIReleaseFixture(fixture), question)
	if err != nil || classification.Disposition != cliReleaseDomainReleaseOwned || classification.Eligible || !strings.Contains(classification.Reason, "no physical") {
		t.Fatalf("marker-stripped selected copy classification=%+v err=%v", classification, err)
	}
}

func TestCLIReleaseDomainThreadSuppressionNeverClaimsDistinctOrdinaryID(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	childQuestion := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
	ordinary := state.Message{
		ID: "zz-ordinary-same-thread", From: childQuestion.From, To: append([]string(nil), childQuestion.To...),
		Thread: childQuestion.Thread, RawThread: childQuestion.RawThread, Subject: "APPROVAL: ordinary", RawSubject: "APPROVAL: ordinary",
		Kind: state.KindQuestion, Priority: state.PriorityNormal, Body: "body", RawBody: "body", AuthorityRaw: true, SchemaOK: true,
		Created: childQuestion.Created.Add(time.Minute), RawCreated: childQuestion.Created.Add(time.Minute).Format(time.RFC3339Nano),
	}
	writeRawCLIReleaseMessage(t, fixture.adapter.root, ordinary.To[0], "new", ordinary, nil)
	ordinary = releaseQuestionForCLIClassification(t, fixture.adapter.root, ordinary.ID)
	classification, err := classifyCLIReleaseQuestion(selectedContextForCLIReleaseFixture(fixture), ordinary)
	if err != nil || classification.Disposition != cliReleaseDomainOrdinary || classification.Eligible || classification.Resolution.Disposition != compoundrelease.ResolutionSuppressed {
		t.Fatalf("same-thread ordinary classification=%+v err=%v", classification, err)
	}
}

func TestCLIReleaseDomainSelectedQuestionDriftFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, fixture cliReleaseReceiptFixture, selected state.Message)
	}{
		{name: "changed exact id", mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, selected state.Message) {
			selected.Subject, selected.RawSubject = "APPROVAL: changed", "APPROVAL: changed"
			writeRawCLIReleaseMessage(t, fixture.adapter.root, selected.Owner, string(selected.State), selected, selected.Context)
		}},
		{name: "newer timestamp", mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, selected state.Message) {
			newer := selected
			newer.ID, newer.Created = "newer-question", selected.Created.Add(time.Second)
			newer.RawCreated = newer.Created.Format(time.RFC3339Nano)
			delete(newer.Context, "release_child")
			writeRawCLIReleaseMessage(t, fixture.adapter.root, selected.To[0], "new", newer, newer.Context)
		}},
		{name: "same-time lexical newer", mutate: func(t *testing.T, fixture cliReleaseReceiptFixture, selected state.Message) {
			newer := selected
			newer.ID = "zzzz-same-time-question"
			delete(newer.Context, "release_child")
			writeRawCLIReleaseMessage(t, fixture.adapter.root, selected.To[0], "new", newer, newer.Context)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, active := newCLIActiveReleaseAttentionFixture(t)
			selected := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
			tc.mutate(t, fixture, cloneReleaseStateMessage(selected))
			classification, err := classifyCLIReleaseQuestion(selectedContextForCLIReleaseFixture(fixture), selected)
			if err == nil {
				t.Fatalf("selected question drift accepted: %+v", classification)
			}
		})
	}
}

func TestCLIReleaseDomainDispositionFailsClosedAcrossInspectionErrors(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	selected := selectedContextForCLIReleaseFixture(fixture)
	owned := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
	ordinary := state.Message{ID: "ordinary-error", From: "cto", To: []string{"user"}, Thread: "gate/ordinary-error", RawThread: "gate/ordinary-error", Subject: "APPROVAL: ordinary", RawSubject: "APPROVAL: ordinary", Kind: state.KindQuestion, Priority: state.PriorityNormal, Body: "body", RawBody: "body", AuthorityRaw: true, SchemaOK: true, Created: owned.Created.Add(time.Minute), RawCreated: owned.Created.Add(time.Minute).Format(time.RFC3339Nano)}
	writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "new", ordinary, nil)
	ordinary = releaseQuestionForCLIClassification(t, fixture.adapter.root, ordinary.ID)

	t.Run("marker plus resolver error stays release owned", func(t *testing.T) {
		old := resolveCompoundReleaseAttention
		resolveCompoundReleaseAttention = func(compoundrelease.SessionScope, compoundrelease.ResolveQuery, compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
			return compoundrelease.Resolution{}, errors.New("injected resolver failure")
		}
		t.Cleanup(func() { resolveCompoundReleaseAttention = old })
		classification, err := classifyCLIReleaseQuestion(selected, owned)
		if err == nil || classification.Disposition != cliReleaseDomainReleaseOwned {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
	})

	t.Run("ordinary resolver error stays unknown", func(t *testing.T) {
		old := resolveCompoundReleaseAttention
		resolveCompoundReleaseAttention = func(compoundrelease.SessionScope, compoundrelease.ResolveQuery, compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
			return compoundrelease.Resolution{}, errors.New("injected resolver failure")
		}
		t.Cleanup(func() { resolveCompoundReleaseAttention = old })
		classification, err := classifyCLIReleaseQuestion(selected, ordinary)
		if err == nil || classification.Disposition != cliReleaseDomainUnknown {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
	})

	t.Run("marker plus no scan stays release owned", func(t *testing.T) {
		old := resolveCompoundReleaseAttention
		resolveCompoundReleaseAttention = func(compoundrelease.SessionScope, compoundrelease.ResolveQuery, compoundrelease.InspectionAdapter) (compoundrelease.Resolution, error) {
			return compoundrelease.Resolution{}, nil
		}
		t.Cleanup(func() { resolveCompoundReleaseAttention = old })
		classification, err := classifyCLIReleaseQuestion(selected, owned)
		if err == nil || classification.Disposition != cliReleaseDomainReleaseOwned {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
	})

	t.Run("physical marker plus scan warning stays release owned", func(t *testing.T) {
		old := scanOperatorSessionMessages
		scanOperatorSessionMessages = func(root string, now func() time.Time) ([]state.Message, []state.Warning) {
			messages, warnings := state.ScanSessionMessages(root, now)
			return messages, append(warnings, state.Warning{Path: root, Reason: "injected warning"})
		}
		t.Cleanup(func() { scanOperatorSessionMessages = old })
		classification, err := classifyCLIReleaseQuestion(selected, owned)
		if err == nil || classification.Disposition != cliReleaseDomainReleaseOwned {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
	})

	t.Run("clone failure stays release owned", func(t *testing.T) {
		uncloneable := cloneReleaseStateMessage(owned)
		uncloneable.Context["uncloneable"] = func() {}
		classification, err := classifyCLIReleaseQuestion(selected, uncloneable)
		if err == nil || classification.Disposition != cliReleaseDomainReleaseOwned {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
	})

	t.Run("root mismatch stays release owned", func(t *testing.T) {
		drifted := selected
		drifted.SessionRoot += "-wrong"
		classification, err := classifyCLIReleaseQuestion(drifted, owned)
		if err == nil || classification.Disposition != cliReleaseDomainReleaseOwned {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
	})
}

func TestCLIReleaseDomainDuplicateAndNewerQuestionFailBeforeGuardCallback(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		fixture, active := newCLIActiveReleaseAttentionFixture(t)
		question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
		conflict := cloneReleaseStateMessage(question)
		conflict.Subject, conflict.RawSubject = "APPROVAL: conflict", "APPROVAL: conflict"
		writeRawCLIReleaseMessage(t, fixture.adapter.root, "intruder", "new", conflict, conflict.Context)
		classification, err := classifyCLIReleaseQuestion(selectedContextForCLIReleaseFixture(fixture), question)
		if err == nil || classification.Disposition != cliReleaseDomainReleaseOwned {
			t.Fatalf("duplicate classification=%+v err=%v", classification, err)
		}
	})

	t.Run("fresh exact-gate conflict", func(t *testing.T) {
		fixture, active := newCLIActiveReleaseAttentionFixture(t)
		question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
		classification, err := classifyCLIReleaseQuestion(selectedContextForCLIReleaseFixture(fixture), question)
		if err != nil || !classification.Eligible {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
		use, err := classification.NewGuardedUse()
		if err != nil {
			t.Fatal(err)
		}
		first := state.Message{ID: "conflict", From: "user", To: []string{"cto"}, Thread: question.Thread, RawThread: question.Thread, Subject: "STATUS: first", RawSubject: "STATUS: first", Kind: state.KindStatus, Priority: state.PriorityNormal, Body: "body", RawBody: "body", AuthorityRaw: true, SchemaOK: true, Created: question.Created.Add(time.Second), RawCreated: question.Created.Add(time.Second).Format(time.RFC3339Nano)}
		second := first
		second.Subject, second.RawSubject = "STATUS: second", "STATUS: second"
		writeRawCLIReleaseMessage(t, fixture.adapter.root, "cto", "cur", first, nil)
		writeRawCLIReleaseMessage(t, fixture.adapter.root, "user", "cur", second, nil)
		callbacks := 0
		err = use.Run(func(cliReleaseGuardObservation) error { callbacks++; return nil })
		if err == nil || callbacks != 0 || !strings.Contains(err.Error(), "conflicting message copies") {
			t.Fatalf("guard err=%v callbacks=%d", err, callbacks)
		}
	})

	t.Run("newer same gate", func(t *testing.T) {
		fixture, active := newCLIActiveReleaseAttentionFixture(t)
		question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
		classification, err := classifyCLIReleaseQuestion(selectedContextForCLIReleaseFixture(fixture), question)
		if err != nil || !classification.Eligible {
			t.Fatalf("classification=%+v err=%v", classification, err)
		}
		use, err := classification.NewGuardedUse()
		if err != nil {
			t.Fatal(err)
		}
		newer := state.Message{
			ID: "newer", From: question.From, To: append([]string(nil), question.To...), Thread: question.Thread, RawThread: question.RawThread,
			Subject: "APPROVAL: newer", RawSubject: "APPROVAL: newer", Kind: state.KindQuestion, Priority: state.PriorityNormal,
			Body: "body", RawBody: "body", AuthorityRaw: true, SchemaOK: true,
			Created: question.Created.Add(time.Minute), RawCreated: question.Created.Add(time.Minute).Format(time.RFC3339Nano),
		}
		writeRawCLIReleaseMessage(t, fixture.adapter.root, question.To[0], "new", newer, nil)
		callbacks := 0
		err = use.Run(func(cliReleaseGuardObservation) error { callbacks++; return nil })
		if err == nil || callbacks != 0 || !strings.Contains(err.Error(), "current trusted gate question") {
			t.Fatalf("guard err=%v callbacks=%d", err, callbacks)
		}
	})
}

func TestCLIReleaseDomainSelectedContextNamedAndScopeDrift(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "release", "s")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	selected := cliReleaseSelectedContext{ProjectDir: project, Profile: "release", Session: "s", NamespaceGeneration: "none", BaseRoot: root, SessionRoot: root}
	question := state.Message{ID: "ordinary", From: "cto", To: []string{"user"}, Thread: "gate/ordinary", RawThread: "gate/ordinary", Subject: "APPROVAL: ordinary", RawSubject: "APPROVAL: ordinary", Kind: state.KindQuestion, Priority: state.PriorityNormal, Body: "body", RawBody: "body", AuthorityRaw: true, SchemaOK: true, Created: notifyNow, RawCreated: notifyNow.Format(time.RFC3339Nano)}
	writeRawCLIReleaseMessage(t, root, "user", "new", question, nil)
	question = releaseQuestionForCLIClassification(t, root, question.ID)
	classification, err := classifyCLIReleaseQuestion(selected, question)
	if err != nil || classification.Disposition != cliReleaseDomainOrdinary || classification.Resolution.Degradation != nil {
		t.Fatalf("named exact-root classification=%+v err=%v", classification, err)
	}

	for _, mutate := range []func(*cliReleaseSelectedContext){
		func(s *cliReleaseSelectedContext) { s.BaseRoot = filepath.Dir(s.BaseRoot) },
		func(s *cliReleaseSelectedContext) { s.SessionRoot = filepath.Join(s.SessionRoot, "s") },
		func(s *cliReleaseSelectedContext) { s.ProjectDir += "/../unclean" },
	} {
		drifted := selected
		mutate(&drifted)
		if _, err := classifyCLIReleaseQuestion(drifted, question); err == nil {
			t.Fatalf("selected context drift accepted: %+v", drifted)
		}
	}
}

func TestCLIReleaseMarkerClaimComparatorRejectsScopeRootReceiptAndRoutingDrift(t *testing.T) {
	fixture, active := newCLIActiveReleaseAttentionFixture(t)
	selected := selectedContextForCLIReleaseFixture(fixture)
	question := releaseQuestionForCLIClassification(t, fixture.adapter.root, active.Active.Children[0].QuestionMessageID)
	classification, err := classifyCLIReleaseQuestion(selected, question)
	if err != nil || !classification.Eligible || classification.Claim == nil {
		t.Fatalf("classification=%+v err=%v", classification, err)
	}
	marker := decodeReleaseMarkerForTest(t, question.Context["release_child"])
	newAdapter := func() *cliReleaseInspectionAdapter {
		return newCLIReleaseInspectionAdapter(selected.ProjectDir, selected.Profile, selected.Session, selected.NamespaceGeneration, selected.BaseRoot, selected.SessionRoot)
	}
	if err := validateCLIReleaseMarkerClaim(selected, newAdapter(), question, marker, *classification.Claim); err != nil {
		t.Fatalf("valid marker/claim rejected: %v", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*cliReleaseSelectedContext, *state.Message, *operatorauth.ReleaseChildContext, *compoundrelease.EligibilityClaim)
	}{
		{name: "selected base root", mutate: func(selected *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			selected.BaseRoot += "-drift"
		}},
		{name: "scope project", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Scope.ProjectDir += "-drift"
		}},
		{name: "scope profile", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Scope.Profile = "other"
		}},
		{name: "scope session", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Scope.Session += "-drift"
		}},
		{name: "scope generation", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Scope.NamespaceGeneration += "-drift"
		}},
		{name: "scope parent gate", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Scope.ParentGate += "-drift"
		}},
		{name: "selected root", mutate: func(selected *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			selected.SessionRoot += "-drift"
		}},
		{name: "raw thread", mutate: func(_ *cliReleaseSelectedContext, question *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			question.RawThread = "/" + question.Thread
		}},
		{name: "receipt root", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.Root += "-drift"
		}},
		{name: "receipt path", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.Path += "-drift"
		}},
		{name: "receipt digest", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.ReceiptSHA256 = "sha256:" + strings.Repeat("0", 64)
		}},
		{name: "sender", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.Sender = "other"
		}},
		{name: "recipient", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.Recipients = []string{"other"}
		}},
		{name: "receipt message", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.MessageID += "-drift"
		}},
		{name: "receipt thread", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.Thread += "-drift"
		}},
		{name: "receipt namespace", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.NamespaceID += "-drift"
		}},
		{name: "receipt attempt", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.AttemptID += "-drift"
		}},
		{name: "receipt target", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Receipt.TargetIdentity += "-drift"
		}},
		{name: "typed project", mutate: func(_ *cliReleaseSelectedContext, question *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			question.AuthorizationRequest.Namespace.ProjectDir += "-drift"
		}},
		{name: "typed profile", mutate: func(_ *cliReleaseSelectedContext, question *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			question.AuthorizationRequest.Namespace.Profile = "other"
		}},
		{name: "typed session", mutate: func(_ *cliReleaseSelectedContext, question *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			question.AuthorizationRequest.Namespace.Session += "-drift"
		}},
		{name: "typed namespace id", mutate: func(_ *cliReleaseSelectedContext, question *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			question.AuthorizationRequest.Namespace.NamespaceID += "-drift"
		}},
		{name: "typed generation", mutate: func(_ *cliReleaseSelectedContext, question *state.Message, _ *operatorauth.ReleaseChildContext, _ *compoundrelease.EligibilityClaim) {
			question.AuthorizationRequest.Namespace.Generation += "-drift"
		}},
		{name: "role ordinal", mutate: func(_ *cliReleaseSelectedContext, _ *state.Message, _ *operatorauth.ReleaseChildContext, claim *compoundrelease.EligibilityClaim) {
			claim.Role, claim.Ordinal = operatorauth.ReleaseChildGitHubRelease, 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driftedSelected := selected
			driftedQuestion := cloneReleaseStateMessage(question)
			driftedMarker := marker
			driftedClaim := *classification.Claim
			tc.mutate(&driftedSelected, &driftedQuestion, &driftedMarker, &driftedClaim)
			if err := validateCLIReleaseMarkerClaim(driftedSelected, newAdapter(), driftedQuestion, driftedMarker, driftedClaim); err == nil {
				t.Fatalf("%s drift accepted", tc.name)
			}
		})
	}
}

func selectedContextForCLIReleaseFixture(fixture cliReleaseReceiptFixture) cliReleaseSelectedContext {
	return cliReleaseSelectedContext{
		ProjectDir: fixture.adapter.project, Profile: fixture.adapter.profile, Session: fixture.adapter.session,
		NamespaceGeneration: "none", BaseRoot: filepath.Dir(fixture.adapter.root), SessionRoot: fixture.adapter.root,
	}
}

func orphanSelectedReleaseContext(selected cliReleaseSelectedContext) cliReleaseSelectedContext {
	project := filepath.Join(selected.ProjectDir, "orphan")
	root := filepath.Join(project, ".agent-mail", selected.Session)
	_ = os.MkdirAll(root, 0o755)
	selected.ProjectDir, selected.BaseRoot, selected.SessionRoot = project, filepath.Dir(root), root
	return selected
}

func releaseQuestionForCLIClassification(t *testing.T, root, id string) state.Message {
	t.Helper()
	messages, warnings := state.ScanSessionMessages(root, func() time.Time { return notifyNow.Add(time.Hour) })
	if len(warnings) != 0 {
		t.Fatalf("scan warnings: %+v", warnings)
	}
	question, _, ok := equalCapturedMessageGroup(messages, id, team.DefaultOperatorHandle)
	if !ok {
		t.Fatalf("question %q missing or conflicting: %+v", id, messages)
	}
	return question
}

func decodeReleaseMarkerForTest(t *testing.T, raw any) operatorauth.ReleaseChildContext {
	t.Helper()
	marker, err := operatorauth.DecodeReleaseChild(raw)
	if err != nil {
		t.Fatal(err)
	}
	return marker
}

func writeRawCLIReleaseMessage(t *testing.T, root, owner, box string, message state.Message, context map[string]any) {
	t.Helper()
	header := map[string]any{
		"schema": 1, "id": message.ID, "from": message.From, "to": message.To,
		"thread": message.RawThread, "subject": message.RawSubject, "created": message.RawCreated,
		"priority": string(message.Priority), "kind": string(message.Kind),
	}
	if context != nil {
		header["context"] = context
	}
	raw, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "agents", owner, "inbox", box, message.ID+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte("---json\n"), raw...), []byte("\n---\n"+message.RawBody+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}
