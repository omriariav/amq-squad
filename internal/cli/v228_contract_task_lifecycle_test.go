package cli

import (
	"sync"
	"testing"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

// AC8: task add -> claim -> done, and nothing blocks. Two concurrent claimers
// produce exactly one winner. The store lock + CAS this rests on is already on
// main and stays (it is coordination state, not certification), so these are
// regression pins for the surviving task surface: add | claim | done | list.

func TestV228ContractTaskAddClaimDoneNothingBlocks(t *testing.T) {
	requireV228Contract(t)
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac8"
		actor   = "dev"
	)
	v228SeedProfile(t, project, profile, session, v228StartMembers(session, "cto", actor))

	created, err := taskstore.AddForProfile(project, profile, session, taskstore.AddInput{Title: "ship it"}, v228Now)
	if err != nil {
		t.Fatalf("task add: %v", err)
	}
	if created.Status != taskstore.StatusPending {
		t.Fatalf("added task status = %q, want pending", created.Status)
	}

	claimed, err := taskstore.ClaimForProfile(project, profile, session, created.ID, actor, v228Now)
	if err != nil {
		t.Fatalf("task claim: %v", err)
	}
	if claimed.Status != taskstore.StatusInProgress || claimed.AssignedTo != actor {
		t.Fatalf("claimed task = status %q assigned %q, want in_progress/%s", claimed.Status, claimed.AssignedTo, actor)
	}

	// No evidence, no gate correlation, no generation ref, no successor dispatch:
	// done must not require any of the machinery v2.28 deletes.
	done, err := taskstore.DoneForProfile(project, profile, session, created.ID, actor, "", v228Now)
	if err != nil {
		t.Fatalf("task done blocked on something: %v", err)
	}
	if done.Status != taskstore.StatusCompleted {
		t.Fatalf("done task status = %q, want completed", done.Status)
	}

	listed, err := taskstore.ListForProfile(project, profile, session)
	if err != nil {
		t.Fatalf("task list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].Status != taskstore.StatusCompleted {
		t.Fatalf("task list = %+v, want the one completed task", listed)
	}
}

func TestV228ContractTwoConcurrentClaimersExactlyOneWins(t *testing.T) {
	requireV228Contract(t)
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac8"
	)
	claimers := []string{"dev", "qa"}
	v228SeedProfile(t, project, profile, session, v228StartMembers(session, claimers...))

	created, err := taskstore.AddForProfile(project, profile, session, taskstore.AddInput{Title: "one winner"}, v228Now)
	if err != nil {
		t.Fatalf("task add: %v", err)
	}

	// Both claimers are released from the same barrier, so the store lock and CAS
	// are what decides — no sleeps, no ordering assumption.
	var (
		mu      sync.Mutex
		winners []string
		losers  []error
		wg      sync.WaitGroup
	)
	release := make(chan struct{})
	for _, actor := range claimers {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			<-release
			claimed, err := taskstore.ClaimForProfile(project, profile, session, created.ID, actor, v228Now)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				losers = append(losers, err)
				return
			}
			winners = append(winners, claimed.AssignedTo)
		}(actor)
	}
	close(release)
	wg.Wait()

	if len(winners) != 1 {
		t.Fatalf("concurrent claim produced %d winners (%v) and %d refusals (%v), want exactly one winner",
			len(winners), winners, len(losers), losers)
	}
	if len(losers) != 1 {
		t.Fatalf("loser count = %d (%v), want exactly one explicit refusal", len(losers), losers)
	}

	// The persisted task agrees with the winner: no split-brain assignment.
	listed, err := taskstore.ListForProfile(project, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("task list = %+v, want one task", listed)
	}
	if listed[0].Status != taskstore.StatusInProgress || listed[0].AssignedTo != winners[0] {
		t.Fatalf("persisted task = status %q assigned %q, want in_progress assigned %q",
			listed[0].Status, listed[0].AssignedTo, winners[0])
	}
}
