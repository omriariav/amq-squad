# amq-owner-sign: set RLIMIT_CORE to 0 before it reads the owner key

## Problem

amq-owner-sign holds the owner key in process memory from the read to the policy publication (up to its 30 s publish timeout), and nothing in the process stops a core dump: with `GOTRACEBACK=crash` in the environment and a non-zero core limit, a SIGABRT or a runtime crash in that window writes the key to disk.

## Evidence (AMQ v0.107.0)

- `cmd/amq-owner-sign/main.go:140-176`: the key is read, then used to sign the grants and the kind 30177 policy, then the policy is published with a 30 s timeout; the key is zeroed only on return (`:144`).
- The process sets no resource limit and does not override `GOTRACEBACK`.
- amq-squad brief 4-5 review (2026-10-24): the owner script now runs the signer under `ulimit -c 0` and `GOTRACEBACK=none`; a crash-path test kills the signer with SIGABRT mid-publish with a throwaway key and finds no core and no key in any output.

## What amq-squad needs

amq-owner-sign sets `RLIMIT_CORE` to 0 (and `debug.SetTraceback("none")`) at start, before it reads the key, so every caller gets the guarantee and not only callers that set the limit.

## Why this belongs in AMQ

The key's lifetime is inside amq-owner-sign; only that process can guarantee no copy of its memory reaches disk.
