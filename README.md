# matrix256-go

Go reference implementation of [**matrix256v1**](https://github.com/shitwolfymakes/matrix256) — a SHA-256 fingerprint over the (path, size) records of a rooted filesystem tree.

**Private repository.** Not published as a Go module. The GitHub remote (when added) must be configured private as well; consumers cannot `go get` this module today.

## Dependencies

One runtime dependency. Otherwise pure Go on the standard library:

- `crypto/sha256` — SHA-256 (spec §2.6).
- `os` — directory walk, file metadata.
- `path/filepath` — path manipulation.
- `strings.ToValidUTF8` — UTF-8 with U+FFFD substitution for invalid sequences (spec §2.2).
- `bytes`, `encoding/hex`, `sort`, `strconv` — serialization plumbing.

The Go ecosystem is treated as a supply-chain risk; no third-party modules may be added without explicit justification. The single dep below is irreducible without violating either Go standard library availability ("the stdlib doesn't ship this").

Go 1.25 or newer.

### Dependency: NFC normalization

[`golang.org/x/text/unicode/norm`](https://pkg.go.dev/golang.org/x/text/unicode/norm) — Unicode Normalization Forms C/D/KC/KD. We use only `norm.NFC.String`.

**Why we accept this dep.** matrix256v1 §2.2 mandates that the relative path be normalized to Unicode Normalization Form C before the bytes are hashed. NFC is not a small or self-contained algorithm: it requires the canonical-decomposition mappings, canonical-combining-class data, and composition-exclusion list from the Unicode Character Database — several thousand lines of tables that must be regenerated whenever Unicode updates. Go's `unicode` standard library package exposes letter-classification tables but no normalization routines; the only realistic alternative to this dep is to hand-vendor (and continually re-vendor) those UCD tables in-tree. Without NFC, the implementation is non-conformant for any input that contains non-NFC filenames on a byte-preserving filesystem (conformance fixture #14 demonstrates this exact case).

`golang.org/x/text` is the Go team's own extension to the standard library — maintained under the `golang.org/x/` namespace alongside `golang.org/x/sys`, `golang.org/x/crypto`, etc., and treated as quasi-stdlib by the Go ecosystem.

**Drop this dep the moment Go's standard library exposes Unicode normalization.** If `unicode/norm` (or equivalent) is ever promoted from `golang.org/x/text` into `std`, swap the call site in [`v1/v1.go`](v1/v1.go) (single `norm.NFC.String` invocation in `canonicalRelative`) and remove `golang.org/x/text` from `go.mod`. The matrix256v1 algorithm and digest do not change; this is a pure dep removal.

Note that — unlike the Rust sibling — there is no SHA-256 dep here. Go's standard library ships [`crypto/sha256`](https://pkg.go.dev/crypto/sha256), so spec §2.6 is satisfied by the stdlib alone.

## Library discipline

The library promise is: **a consumer's process must never break because of code in this module.** To enforce this rather than promise it, [`.golangci.yml`](.golangci.yml) configures `golangci-lint` to make the relevant footguns into build errors. CI runs `golangci-lint run ./...` on every push so any new violation fails the build.

| Category | What's guarded | Enforced by |
|---|---|---|
| Panic discipline | No `panic(...)` from library code; no `os.Exit`; no `log.Fatal`/`log.Panic`. Failures return `error` values so callers can `errors.As`-check and re-wrap. | `forbidigo` (banned identifiers `panic`, `os.Exit`, `log.Fatal*`, `log.Panic*`) |
| Output discipline | No `fmt.Print` / `fmt.Println` / `fmt.Printf`, no builtin `print`/`println`. A fingerprint call has no business writing to stdout/stderr. | `forbidigo` (banned identifiers `fmt.Print*`, `print`, `println`) |
| Error checking | Every returned `error` must be inspected — matrix256v1 is all-or-nothing per spec §3, and silently dropped errors would let a partial walk produce a digest. | `errcheck` (with documented exceptions for `bytes.Buffer.Write*` and `hash.Hash.Write`, both of which never error) |
| Correctness | The standard Go correctness suite: shadow detection, unreachable code, suspicious composite literals, dead stores. | `govet`, `staticcheck`, `unused` |
| Security audit | Hardcoded credentials, weak crypto, file-path injection, unsafe permissions. | `gosec` |
| Documentation | Every exported item carries a `// Doc comment.` Public API stays self-describing. | `revive` (`exported` rule) |

The [`tests/`](tests/) directory and any `*_test.go` files are exempt via `exclusions.rules` in [`.golangci.yml`](.golangci.yml) — the conformance runner uses `fmt.Print*` for progress output and `os.Exit` for exit codes, both forbidden in library code.

```
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
golangci-lint run ./...    # run the discipline checks
```

## Usage

```go
package main

import (
	"fmt"
	"log"

	"github.com/shitwolfymakes/matrix256-go/v1"
)

func main() {
	digest, err := v1.Fingerprint("/media/user/DISC")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(digest)
}
```

The module exposes nothing at the top level. Future algorithm versions will be added as sibling subpackages (`v2`, …) so callers always address an explicit version.

## Conformance

This implementation's Tier-1 conformance test is the synthetic fixture suite at [`tests/conformance_test.go`](tests/conformance_test.go). Each fixture is a `t.Run` subtest of `TestConformance`: it constructs the fixture in a temporary directory, runs `v1.Fingerprint`, and asserts the produced digest matches the canonical value from the spec repo's [`conformance_fixtures.json`](https://github.com/shitwolfymakes/matrix256/blob/main/conformance_fixtures.json) (human-readable companion: [`CONFORMANCE_FIXTURES.md`](https://github.com/shitwolfymakes/matrix256/blob/main/CONFORMANCE_FIXTURES.md)). The suite has no external runtime dependency beyond `matrix256-go/v1` itself and runs on every commit in CI.

```
go test ./tests/                                     # all fixtures
go test ./tests/ -v                                  # verbose subtest names
go test ./tests/ -run TestConformance/fixture_05     # one fixture
go test ./tests/ -run 'TestConformance/fixture_(01|05|14)'  # a subset (regex)
go test ./tests/ -generate -v                        # emit JSON for the spec repo
go test ./tests/ -fixtures-json PATH                 # custom expected-digests path
```

By default the test loads `conformance_fixtures.json` from a sibling clone of the spec repo at `../matrix256/`. Override with `-fixtures-json PATH`. Platform-incompatible fixtures (e.g. case-sensitive sort on a case-insensitive filesystem, surrogate-escape paths off Linux) are reported as `--- SKIP` via `t.Skip` rather than failures.

The fixture builders mirror the construction logic of the Python and JavaScript siblings ([`matrix256-py/tests/generate_fixtures.py`](https://github.com/shitwolfymakes/matrix256-py/blob/main/tests/generate_fixtures.py), [`matrix256-js/tests/generate_fixtures.js`](https://github.com/shitwolfymakes/matrix256-js/blob/main/tests/generate_fixtures.js)); all three languages must agree on every fixture's on-disk state and produced digest.

## See also (in the [spec repo](https://github.com/shitwolfymakes/matrix256))

- `SPEC.md` — normative algorithm
- `RATIONALE.md` — design rationale
- `IMPLEMENTERS.md` — practical guidance (encoding, mount handling, bridge discs)
- `CORPUS.md` — known-good digests across real discs
- `CONFORMANCE_FIXTURES.md` / `conformance_fixtures.json` — Tier-1 synthetic fixture suite
