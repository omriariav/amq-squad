# 3.0.1 t7: the redact linearity test as an in-process ratio

Branch v3-301-t7 from origin/v3 at a1a67151. Host: the owner's macOS laptop, 2026-10-30.

## The change

`TestBlockIsLinearOnALongLine` (internal/redact/block_test.go) no longer races Block
against an absolute wall-clock bound. It times Block on a 64 KiB line and on a
1 MiB line of repeated `password=x ` pairs built at run time, in one process. The
1 MiB line holds 16x the values and must take no more than 64x the time. One
warm-up Block call on the small line runs first, and each size is the best of
three timings. The "every value redacted" check stays, on the 1 MiB output. The
test name is unchanged.

## Files deleted

- `internal/redact/bound_norace_test.go` held `linearBound = 2 * time.Second`.
- `internal/redact/bound_race_test.go` held `linearBound = 20 * time.Second`.

Both existed only to give the old test its bound. A grep for `linearBound` over
the tree before deleting found no reader other than block_test.go:

```
internal/redact/bound_norace_test.go:8:const linearBound = 2 * time.Second
internal/redact/block_test.go:21:	case <-time.After(linearBound):
internal/redact/block_test.go:22:		t.Fatalf("a 1 MiB line with %d values took over %s", strings.Count(line, "="), linearBound)
internal/redact/bound_race_test.go:10:const linearBound = 20 * time.Second
```

## Before (the absolute bound)

| run | 1 MiB, 95325 values | bound |
|---|---|---|
| go test | 241 ms | 2 s |
| go test -race | 4.922 s | 20 s |

## After (the ratio)

| run | 64 KiB, 5957 values | 1 MiB, 95325 values | ratio |
|---|---|---|---|
| go test, run 1 | 13 ms | 208 ms | 16.2x |
| go test, run 2 | 14 ms | 211 ms | 15.3x |
| go test, run 3 | 13 ms | 210 ms | 15.7x |
| go test -race, run 1 | 312 ms | 5.111 s | 16.4x |
| go test -race, run 2 | 325 ms | 5.154 s | 15.9x |
| go test (full package gate) | 13 ms | 209 ms | 15.5x |
| go test -race (full package gate) | 314 ms | 5.091 s | 16.2x |

The limit is 64x, so the measured ratio leaves about 4x headroom. Load on a shared
runner slows both sizes in the same process, so it moves the ratio far less than
it moved the absolute time.

## The reverting run

A throwaway O(n^2) stand-in, not committed: at the top of Block, for every `=`
in the text, a byte loop re-scans the processed prefix counting `=`.

```go
quadSink := 0 // THROWAWAY O(n^2) stand-in: re-scan the processed prefix per value
for i := 0; i < len(text); i++ {
	if text[i] == '=' {
		for j := 0; j < i; j++ {
			if text[j] == '=' {
				quadSink++
			}
		}
	}
}
_ = quadSink
```

```
=== RUN   TestBlockIsLinearOnALongLine
    block_test.go:51: 64 KiB, 5957 values: 56ms; 1 MiB, 95325 values: 11.335s; ratio 203.5x (limit 64x)
    block_test.go:55: 16x the input took 203.5x the time (over 64x): Block is not linear in a line
--- FAIL: TestBlockIsLinearOnALongLine (34.27s)
FAIL
```

A first stand-in used `strings.Count(text[:i], "=")` for the re-scan. That count
is vectorized, so the quadratic term was small next to Block's linear cost at
64 KiB, and the ratio came out at 59.1x (16 ms, 937 ms) and passed. The limit
therefore catches a quadratic term once it is more than about a quarter of the
linear cost at 64 KiB. A smaller quadratic term passes, which is the price of
the generous 64x factor the ruling chose. The byte-loop stand-in above is the
brief's example shape and fails as required.

## Gates

- `go vet ./internal/redact`: clean.
- `go test -count=1 ./internal/redact`: ok, 7.0 s.
- `go test -race -count=1 ./internal/redact`: ok, 161.8 s for the package (this test 16.75 s).
- The line is `password=x ` pairs built at run time; `x` matches no
  RealCredentialShapes entry, and no test output prints a value.
