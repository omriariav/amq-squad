# S3 embedded pi spike

Throwaway prototype for amq-squad 3.0 phase 1, task S3. Results and the
packaging decision: [docs/v3-spike/S3-report.md](../../docs/v3-spike/S3-report.md).

darwin-arm64 only. Build-time tools: Go 1.27, Python 3, Node 22.21.1 and npm
(for `npm ci` only), Bun (only for the `bun` candidate), curl. None of these is
needed at run time.

```sh
cd spike/embedpi
bash build.sh fetch      # the only networked step; pinned SHA-256 on every download
bash build.sh node       # DECIDED (a): official Node 22.21.1 + pruned npm tree + pinned fd/rg
bash build.sh node-full  # (a) unpruned, for comparison
bash build.sh bun        # (b1) our bun build --compile: dropped, not faithful
bash build.sh upstream   # (b2) upstream's Bun release archive: the only fallback
bash poststrip-test.sh   # extension, HTML export and image attach on the decided build
go test ./...            # launcher unit tests (tiny inert fixtures)
python3 measure.py node  # sizes, cold/warm unpack, prompts, Gatekeeper evidence
./stripped.sh -- ./bin/embedpi-node --version   # env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm
```

Files: `main.go` launcher (go:embed, SHA-256 verified unpack to
`~/.amq-squad/runtime/<version>-<digest12>/`, launch-time integrity check,
2b defaults with a per-seat agent dir, fd/rg links, exec);
`pack.py` normalized tar + per-file manifest; `package.json` and
`package-lock.json` pin `@earendil-works/pi-coding-agent@1.3.0`; `agent/`
holds the pi provider config for Ollama; `measure.py`, `quarantine-test.sh`,
`nonative-test.sh`, `tui-test.sh`, `defaults-test.sh`, `poststrip-test.sh`
(with `probe-ext/s3-probe.ts`) are the probes; `evidence/` keeps their
outputs. Payloads and binaries are gitignored.
