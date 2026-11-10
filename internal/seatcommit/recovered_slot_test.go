package seatcommit

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

func TestRecoveredSlotIsMarkedAndIdenticalRefusalTakesNextSlot(t *testing.T) {
	f := newFixture(t)
	tampered := tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	head := readFile(t, filepath.Join(q.Slot, quarantineHeadFile))
	before := snapshotSlot(t, q.Slot)
	res, err := f.recover(q.Seq, 1)
	if err != nil {
		t.Fatal(err)
	}
	mark := readFile(t, filepath.Join(q.Slot, quarantineRecovered))
	for _, want := range []string{"time: ", "lead: cto\n", fmt.Sprintf("link: %d\n", res.NewSeq)} {
		if !strings.Contains(string(mark), want) {
			t.Fatalf("mark lacks %q:\n%s", want, mark)
		}
	}
	assertSlotSnapshot(t, q.Slot, before)
	if err := os.WriteFile(ledgerPath(f.root, f.handle), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)), head, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_, err := ReadLedger(f.root, f.handle)
		var again *QuarantinedError
		if !errors.As(err, &again) || again.Slot != q.Slot+".2" {
			t.Fatalf("refusal %d after recovery = %+v, %v", i+1, again, err)
		}
		if i == 0 {
			if got := readFile(t, filepath.Join(q.Slot+".2", quarantineHeadFile)); !bytes.Equal(got, head) {
				t.Fatal("the next slot does not hold the identical refused head")
			}
		}
	}
	if got := readFile(t, filepath.Join(q.Slot+".2", quarantineBodyFile)); !bytes.Equal(got, tampered) {
		t.Fatal("the next slot does not hold the identical refused body")
	}
	if _, err := os.Lstat(q.Slot + ".3"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the repeated refusal published a third slot: %v", err)
	}
	if got := readFile(t, filepath.Join(q.Slot, quarantineRecovered)); !bytes.Equal(got, mark) {
		t.Fatal("the recovered slot's mark changed")
	}
	assertSlotSnapshot(t, q.Slot, before)
}

func TestPlantedRecoveredNameRetiresSlotWithoutOpeningIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(string) error
	}{
		{name: "symlink", plant: func(path string) error { return os.Symlink("/dev/null", path) }},
		{name: "directory", plant: func(path string) error { return os.Mkdir(path, 0o700) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tamperBase(t, f, f.base, '0')
			q := f.quarantined()
			markPath := filepath.Join(q.Slot, quarantineRecovered)
			if err := tc.plant(markPath); err != nil {
				t.Fatalf("cannot plant %s: %v", tc.name, err)
			}
			var ops []string
			quarantineTrace = func(op, name string) { ops = append(ops, op+" "+name) }
			t.Cleanup(func() { quarantineTrace = nil })
			_, err := ReadLedger(f.root, f.handle)
			var again *QuarantinedError
			if !errors.As(err, &again) || again.Slot != q.Slot+".2" {
				t.Fatalf("identical refusal with planted mark = %+v, %v", again, err)
			}
			for _, op := range ops {
				if strings.Contains(op, markPath) {
					t.Fatalf("the planted mark was opened or read: %v", ops)
				}
			}
		})
	}
	t.Run("unmarked control traces slot reads", func(t *testing.T) {
		f := newFixture(t)
		tamperBase(t, f, f.base, '0')
		q := f.quarantined()
		var ops []string
		quarantineTrace = func(op, name string) { ops = append(ops, op+" "+name) }
		t.Cleanup(func() { quarantineTrace = nil })
		if _, err := ReadLedger(f.root, f.handle); err == nil {
			t.Fatal("the tampered ledger verified")
		}
		for _, name := range []string{quarantineBodyFile, quarantineHeadFile, quarantineSidecar} {
			want := "read " + filepath.Join(q.Slot, name)
			if !slicesContains(ops, want) {
				t.Fatalf("unmarked slot did not trace %q: %v", want, ops)
			}
		}
	})
}

func TestRecoveryMarkFailureWritesNothingAndExistingMarkIsSuccess(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	body, head := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	decisionPath := filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	decisions := optionalFile(t, decisionPath)
	slot := snapshotSlot(t, q.Slot)
	want := errors.New("injected mark create failure")
	old := recoveredMarkCreate
	recoveredMarkCreate = func(slotDir, string, []byte) error { return want }
	t.Cleanup(func() { recoveredMarkCreate = old })
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverWriteFailed || !strings.Contains(err.Error(), quarantineRecovered) || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("mark failure = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("mark failure changed the ledger or head")
	}
	assertSlotSnapshot(t, q.Slot, slot)
	if got := optionalFile(t, decisionPath); !bytes.Equal(got, decisions) {
		t.Fatalf("mark failure changed the decision log: before %q after %q", decisions, got)
	}
	recoveredMarkCreate = old
	recoveredMarkCreate = func(slotDir, string, []byte) error { return fmt.Errorf("wrapped create race: %w", fs.ErrExist) }
	if _, err := f.recover(q.Seq, 1); err != nil {
		t.Fatalf("a wrapped create ErrExist was not idempotent: %v", err)
	}
}

func TestExistingRecoveredMarkPathsSyncSlotBeforeRecovery(t *testing.T) {
	t.Run("planted mark", func(t *testing.T) {
		f := newFixture(t)
		tamperBase(t, f, f.base, '0')
		q := f.quarantined()
		if err := os.WriteFile(filepath.Join(q.Slot, quarantineRecovered), []byte("planted\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var ops []string
		quarantineTrace = func(op, name string) { ops = append(ops, op+" "+name) }
		t.Cleanup(func() { quarantineTrace = nil })
		if _, err := f.recover(q.Seq, 1); err != nil {
			t.Fatal(err)
		}
		if sync, write := indexOf(ops, "fsync "+q.Slot), prefixIndex(ops, "recover-write "); sync < 0 || write <= sync {
			t.Fatalf("existing mark was not synced before recovery write: %v", ops)
		}
	})
	t.Run("already recovered retry", func(t *testing.T) {
		f := newFixture(t)
		tamperBase(t, f, f.base, '0')
		q := f.quarantined()
		if _, err := f.recover(q.Seq, 1); err != nil {
			t.Fatal(err)
		}
		var ops []string
		quarantineTrace = func(op, name string) { ops = append(ops, op+" "+name) }
		t.Cleanup(func() { quarantineTrace = nil })
		if _, err := f.recover(q.Seq, 1); err != nil {
			t.Fatal(err)
		}
		if indexOf(ops, "fsync "+q.Slot) < 0 {
			t.Fatalf("already-recovered retry did not sync its listed mark: %v", ops)
		}
	})
}

func TestRecoveryMarksBeforeBodyWriteAndRetrySucceeds(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	body, head := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	crash := errors.New("crash after recovered mark")
	beforeRecoveryWrite = func() error { return crash }
	t.Cleanup(func() { beforeRecoveryWrite = nil })
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverWriteFailed || !strings.Contains(err.Error(), crash.Error()) {
		t.Fatalf("crash seam = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("crash after mark changed the ledger or head")
	}
	if _, err := os.Lstat(filepath.Join(q.Slot, quarantineRecovered)); err != nil {
		t.Fatalf("crash did not leave the durable mark: %v", err)
	}
	beforeRecoveryWrite = nil
	if _, err := f.recover(q.Seq, 1); err != nil {
		t.Fatalf("retry after marked crash: %v", err)
	}
}

func TestVerifiedAndAlreadyRecoveredPathsMaintainTheMark(t *testing.T) {
	f := newFixture(t)
	original := readFile(t, ledgerPath(f.root, f.handle))
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if err := os.WriteFile(ledgerPath(f.root, f.handle), original, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil || !res.Verified {
		t.Fatalf("verified recovery = %+v, %v", res, err)
	}
	markPath := filepath.Join(q.Slot, quarantineRecovered)
	if _, err := os.Lstat(markPath); err != nil {
		t.Fatalf("verified recovery did not mark its slot: %v", err)
	}
	if err := os.Remove(markPath); err != nil {
		t.Fatal(err)
	}
	again, err := f.recover(q.Seq, 1)
	if err != nil || !again.AlreadyRecovered || again.Note != "" {
		t.Fatalf("already-recovered mark repair = %+v, %v", again, err)
	}
	if _, err := os.Lstat(markPath); err != nil {
		t.Fatalf("already-recovered retry did not restore the mark: %v", err)
	}
	if err := os.Remove(markPath); err != nil {
		t.Fatal(err)
	}
	old := recoveredMarkCreate
	recoveredMarkCreate = func(slotDir, string, []byte) error { return errors.New("injected retry mark failure") }
	t.Cleanup(func() { recoveredMarkCreate = old })
	again, err = f.recover(q.Seq, 1)
	if err != nil || !again.AlreadyRecovered || !strings.Contains(again.Note, quarantineRecovered) {
		t.Fatalf("already-recovered non-fatal note = %+v, %v", again, err)
	}
}

func TestRecoveredMarkIsDurableBeforeRecoveryBodyWrite(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	var ops []string
	quarantineTrace = func(op, name string) { ops = append(ops, op+" "+name) }
	t.Cleanup(func() { quarantineTrace = nil })
	if _, err := f.recover(q.Seq, 1); err != nil {
		t.Fatal(err)
	}
	create, sync, write := -1, -1, -1
	for i, op := range ops {
		switch {
		case strings.HasPrefix(op, "create ") && strings.HasSuffix(op, "/"+quarantineRecovered):
			create = i
		case op == "fsync "+q.Slot:
			sync = i
		case strings.HasPrefix(op, "recover-write "):
			write = i
		}
	}
	if create < 0 || sync <= create || write <= sync {
		t.Fatalf("durability order create < slot sync < body write not observed: %v", ops)
	}
}

func optionalFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return -1
}

func prefixIndex(items []string, prefix string) int {
	for i, item := range items {
		if strings.HasPrefix(item, prefix) {
			return i
		}
	}
	return -1
}

func slicesContains(items []string, want string) bool {
	return indexOf(items, want) >= 0
}
