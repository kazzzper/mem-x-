# Future agents (spawn when the work shows up, not before)

> These agents are not yet active. Per AGENTS.md's lightweight-first rule,
> they are added to the registry only when the work that justifies them
> actually arrives. Each entry includes the trigger that would activate it,
> so the orchestrator knows when to add them.

## fuzzer — continuous adversarial input

- **Mission:** Keep fuzz targets running continuously (not just in the
  harness), grow the corpus, and route crashes to `engineer`/`security`.
- **Spawn triggers / activation:** the RESP/parser fuzz targets become a
  maintenance burden, or we add new protocol surfaces (RESP3, pub/sub) that
  need continuous fuzzing.
- **Operating instructions:** run `go test -fuzz=FuzzReadCommand
  -fuzztime=10s ./internal/resp` (and any new targets); save any crash input
  to the corpus; classify (crash vs hang vs OOM); route a reproducer to
  `engineer` with the failing input bytes.
- **Hard rules:** never fix a crash silently — always ship the reproducer
  with the fix. No fuzz target may run forever in CI; bound it.
- **Model tier:** 2.

## release — CI/CD & static binaries

- **Mission:** Own the release workflow: CI matrix (linux/darwin/windows ×
  amd64/arm64), static binaries, checksums, provenance, signed artifacts.
- **Spawn triggers / activation:** we ship the first tagged release
  (`v0.1.0`) and need reproducible artifacts + CI.
- **Operating instructions:** add `.github/workflows/ci.yml` running
  `make check` + `make harness` on the matrix; add a release target that
  builds all six targets with `CGO_ENABLED=0`, hashes them
  (`sha256sum`), and attaches provenance (GitHub SBOM/attestations).
- **Hard rules:** artifacts must be reproducible from a clean checkout with
  the pinned toolchain; nothing ships without `govulncheck` green.
- **Model tier:** 2.

## perf — profile-guided optimization

- **Mission:** Deep-dive pprof profiles (CPU, heap, mutex), flame graphs, and
  profile-guided optimization (PGO). Broader than `bench`, which guards
  budgets; `perf` finds *what* to optimize.
- **Spawn triggers / activation:** Phase 2 benchmarks plateau and profiling is
  needed to find the next wins, or PGO becomes part of the release build.
- **Operating instructions:** run the server under a load tool, capture
  `-cpuprofile`/`-memprofile`/`-mutexprofile`, generate flame graphs, and
  rank findings by (occurrence × cost). Hand ranked findings to `bench` for
  before/after validation, then `engineer` to implement.
- **Hard rules:** only act on measured data, not intuition. Each finding must
  include the pprof evidence. No optimization without a `bench` verdict.
- **Model tier:** 3.
