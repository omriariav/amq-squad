package calc

import "testing"

func TestGreeting(t *testing.T) {
	if got := Greeting(); got != "hello, world" {
		t.Fatalf("Greeting() = %q, want %q", got, "hello, world")
	}
}

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}
