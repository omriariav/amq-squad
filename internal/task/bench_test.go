package task

import (
	"testing"
	"time"
)

func benchStore(b *testing.B) Store {
	b.Helper()
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := s.Add(AddInput{Title: "x", Class: "script"}, t0); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

// BenchmarkTransaction is one committed single-task mutation (journal,
// after-image, three directory fsyncs) on a 30-task store.
func BenchmarkTransaction(b *testing.B) {
	s := benchStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reason := WaitSeatCap
		if i%2 == 0 {
			reason = WaitSeatStarting
		}
		if _, err := s.SetWait("t1", reason, t0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenewInsideInterval is the evidence-ingest renewal when the last
// renewal is younger than the minimum interval: a lock-free read, no write.
func BenchmarkRenewInsideInterval(b *testing.B) {
	s := benchStore(b)
	if _, err := s.Claim("t1", "h", nil, time.Hour, t0); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.Renew("h", time.Hour, time.Minute, t0.Add(time.Second)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRenewWrite is a renewal that writes.
func BenchmarkRenewWrite(b *testing.B) {
	s := benchStore(b)
	if _, err := s.Claim("t1", "h", nil, time.Hour, t0); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := s.Renew("h", time.Hour, 0, t0.Add(time.Duration(i+1)*time.Second)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSnapshot is the lock-free read of context and status.
func BenchmarkSnapshot(b *testing.B) {
	s := benchStore(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Snapshot(s.Dir); err != nil {
			b.Fatal(err)
		}
	}
}
