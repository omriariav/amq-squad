# 3.0.1 t7: the redact linearity test as an in-process ratio

Branch v3-301-t7 from origin/v3 at a1a67151. Host: the owner's macOS laptop, 2026-10-30.

## The change

`TestBlockIsLinearOnALongLine` (internal/redact/block_test.go) no longer races Block
against an absolute wall-clock bound. It times Block in one process on a 64 KiB
line and on a 1 MiB line. Each line is a credential-named key, an equals sign and
a one-letter value, repeated, built at run time. The 1 MiB line holds 16x the
values and must take no more than 64x the time. One warm-up Block call on the
small line runs first. The two sizes are then timed in three interleaved pairs,
small then large, and the best of each size counts. If that best ratio is over
the limit, one more interleaved pair runs before the test fails, and the log
line records whether it retried. The "every value redacted" check stays, on the
1 MiB output. The test name is unchanged.

The first commit timed the three small runs before the three large ones. The
Codex pass flagged that a sustained load change would then hit one size only,
so the fix commit interleaves the sizes and adds the single retry.

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

## After (the ratio, first commit: three small timings, then three large)

| run | 64 KiB, 5957 values | 1 MiB, 95325 values | ratio |
|---|---|---|---|
| go test, run 1 | 13 ms | 208 ms | 16.2x |
| go test, run 2 | 14 ms | 211 ms | 15.3x |
| go test, run 3 | 13 ms | 210 ms | 15.7x |
| go test -race, run 1 | 312 ms | 5.111 s | 16.4x |
| go test -race, run 2 | 325 ms | 5.154 s | 15.9x |
| go test (full package gate) | 13 ms | 209 ms | 15.5x |
| go test -race (full package gate) | 314 ms | 5.091 s | 16.2x |

## After (the ratio, fix commit: interleaved pairs, one retry)

| run | 64 KiB, 5957 values | 1 MiB, 95325 values | ratio | retried |
|---|---|---|---|---|
| go test, run 1 | 13 ms | 213 ms | 16.5x | no |
| go test, run 2 | 13 ms | 211 ms | 16.0x | no |
| go test, run 3 | 13 ms | 217 ms | 16.6x | no |
| go test -race, run 1 | 315 ms | 5.058 s | 16.0x | no |
| go test -race, run 2 | 327 ms | 5.216 s | 15.9x | no |

Interleaving did not move the numbers on this idle host. The limit is 64x, so
the measured ratio leaves about 4x headroom. Interleaving spreads a load change
that lasts across the run over both sizes. It does not cancel a burst that lands
on a single timing; the best of three and the one retry absorb that.

## The reverting runs

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

The same stand-in against the fix commit's interleaved test, with the retry
taken:

```
=== RUN   TestBlockIsLinearOnALongLine
    block_test.go:66: 64 KiB, 5957 values: 58ms; 1 MiB, 95325 values: 11.675s; ratio 201.1x (limit 64x); retried true
    block_test.go:70: 16x the input took 201.1x the time (over 64x, after one retry): Block is not linear in a line
--- FAIL: TestBlockIsLinearOnALongLine (47.75s)
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
- `go test -count=1 ./internal/redact`: ok, 7.0 s (first commit); ok, 7.2 s (fix commit).
- `go test -race -count=1 ./internal/redact`: ok, 161.8 s for the package, this test
  16.75 s (first commit); ok, 165.8 s, this test 17.57 s (fix commit).
- `go test -count=1 -run TestNoCredentialShapedValueUnderDocsOrEvidence ./internal/redact`:
  failed on the first commit's note (two lines held the literal pair), ok on the fix commit.
- The line is a credential-named key, an equals sign and a one-letter value,
  repeated and built at run time. The one-letter value matches no
  RealCredentialShapes entry, and no test output prints a value. This note
  describes the pair in words, so the repository-wide credential-shape test
  (TestNoCredentialShapedValueUnderDocsOrEvidence) passes on it.
