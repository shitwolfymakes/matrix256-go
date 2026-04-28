// Tier-1 conformance tests for matrix256v1 (Go implementation).
//
// Each fixture from the matrix256 spec repo's CONFORMANCE_FIXTURES.md is
// a t.Run subtest that constructs the fixture in a fresh temporary
// directory, runs matrix256-go/v1.Fingerprint against it, and compares
// the produced digest to the canonical value published in the spec
// repo's conformance_fixtures.json companion. A divergence is a
// regression in either this implementation or the spec.
//
// Two roles in one file:
//
//  1. Conformance test harness (default mode). With expected digests
//     loaded from the spec repo, every fixture is verified.
//
//  2. Canonical fixture generator (-generate mode). With go test
//     -generate, the fixtures are constructed and the JSON block is
//     emitted ready to paste into the spec repo's
//     conformance_fixtures.json. Implementers in other languages
//     should treat the construction code in this file (and its
//     Python and JavaScript siblings) as the canonical reference
//     where the markdown's prose is ambiguous.
//
// Stdlib only beyond the matrix256-go/v1 import.
//
// Usage:
//
//	go test ./tests/                                  # all fixtures
//	go test ./tests/ -v                               # verbose output
//	go test ./tests/ -run fixture_05                  # one fixture
//	go test ./tests/ -run 'fixture_(01|02|03)'        # a subset (regex)
//	go test ./tests/ -generate                        # emit JSON for the spec repo
//	go test ./tests/ -fixtures-json PATH              # custom expected-digests path
//
// By default the test loads conformance_fixtures.json from a sibling
// checkout of the spec repo at ../../matrix256/ relative to this
// directory. Override with -fixtures-json PATH.
package tests

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	v1 "github.com/shitwolfymakes/matrix256-go/v1"
)

var (
	generateFlag = flag.Bool("generate", false, "construct fixtures and emit a JSON block of computed digests instead of verifying against the spec repo")
	fixturesJSON = flag.String("fixtures-json", "", "path to conformance_fixtures.json (default: ../../matrix256/conformance_fixtures.json)")
)

// skipFixture signals that the host platform can't construct the
// fixture as written; the test is reported as t.Skip rather than t.Fail.
type skipFixture struct{ reason string }

func (s *skipFixture) Error() string { return s.reason }

func skipf(format string, a ...any) error {
	return &skipFixture{reason: fmt.Sprintf(format, a...)}
}

func isSkip(err error) (string, bool) {
	var s *skipFixture
	if errors.As(err, &s) {
		return s.reason, true
	}
	return "", false
}

// --- Builders ------------------------------------------------------------
//
// Each builder receives a fresh empty directory and constructs the
// fixture state in it. Returning a skipFixture means the platform
// can't host the fixture as written.

func writeFile(p string, data []byte) error {
	return os.WriteFile(p, data, 0o644)
}

func trySymlink(target, link string) error {
	if err := os.Symlink(target, link); err != nil {
		return skipf("symlinks not supported (%v)", err)
	}
	return nil
}

func b1EmptyDir(_ string) error { return nil }

func b2ZeroByte(d string) error {
	return writeFile(filepath.Join(d, "a"), nil)
}

func b3SmallASCII(d string) error {
	return writeFile(filepath.Join(d, "hello.txt"), []byte("hello\n"))
}

func b4TwoFiles(d string) error {
	if err := writeFile(filepath.Join(d, "a"), nil); err != nil {
		return err
	}
	return writeFile(filepath.Join(d, "b"), nil)
}

func b5CaseSensitiveSort(d string) error {
	if err := writeFile(filepath.Join(d, "A"), nil); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(d, "a"), nil); err != nil {
		return skipf("filesystem is case-insensitive (%v)", err)
	}
	names, err := readNames(d)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	if !have["A"] || !have["a"] || len(names) != 2 {
		return skipf("filesystem collapsed 'A' and 'a' (case-insensitive)")
	}
	return nil
}

func b6SlashVsDash(d string) error {
	if err := writeFile(filepath.Join(d, "a-b"), nil); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(d, "a"), 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(d, "a", "b"), nil)
}

func b7NestedDirs(d string) error {
	nested := filepath.Join(d, "dir1", "dir2")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(nested, "file.txt"), nil)
}

func b8SiblingFullPathSort(d string) error {
	if err := os.Mkdir(filepath.Join(d, "a"), 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(d, "a", "z"), nil); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(d, "b"), 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(d, "b", "a"), nil)
}

func b9OnlyEmptySubdir(d string) error {
	return os.Mkdir(filepath.Join(d, "empty"), 0o755)
}

func b10FilePlusEmptySubdir(d string) error {
	if err := writeFile(filepath.Join(d, "hello.txt"), []byte("hello\n")); err != nil {
		return err
	}
	return os.Mkdir(filepath.Join(d, "empty"), 0o755)
}

func b11OnlySymlink(d string) error {
	return trySymlink("nonexistent", filepath.Join(d, "link"))
}

func b12SymlinkAlongsideFile(d string) error {
	if err := writeFile(filepath.Join(d, "real.txt"), []byte("x")); err != nil {
		return err
	}
	return trySymlink("real.txt", filepath.Join(d, "link"))
}

func b13LatinDiacriticsNFC(d string) error {
	// "café.txt" — single composed code point U+00E9. Source bytes are
	// already NFC because Go source files are saved as NFC.
	return writeFile(filepath.Join(d, "café.txt"), nil)
}

func b14LatinDiacriticsNFD(d string) error {
	// 'cafe' + U+0301 (combining acute) — NFD form. The expected
	// digest is the NFC-byte hash, matching fixture 13. Tests that
	// the canonicalization step normalizes NFD → NFC before hashing.
	// Skipped on filesystems that auto-NFC-normalize at write time
	// (e.g. APFS).
	nfdName := "café.txt"
	if err := writeFile(filepath.Join(d, nfdName), nil); err != nil {
		return err
	}
	names, err := readNames(d)
	if err != nil {
		return err
	}
	if len(names) != 1 || names[0] != nfdName {
		return skipf("filesystem auto-normalized the filename at write time")
	}
	return nil
}

func b15Cyrillic(d string) error {
	return writeFile(filepath.Join(d, "привет.txt"), nil)
}

func b16Han(d string) error {
	return writeFile(filepath.Join(d, "你好.txt"), nil)
}

func b17Arabic(d string) error {
	return writeFile(filepath.Join(d, "مرحبا.txt"), nil)
}

func b18Emoji(d string) error {
	return writeFile(filepath.Join(d, "🎵.txt"), nil)
}

func b19MultiScript(d string) error {
	for _, name := range []string{"ascii.txt", "café.txt", "你好.txt", "🎵.txt"} {
		if err := writeFile(filepath.Join(d, name), nil); err != nil {
			return err
		}
	}
	return nil
}

func b20SizeBoundaries(d string) error {
	sizes := []struct {
		name string
		size int
	}{
		{"size_0000000", 0},
		{"size_0000001", 1},
		{"size_0000255", 255},
		{"size_0000256", 256},
		{"size_0065535", 65535},
		{"size_0065536", 65536},
		{"size_1000000", 1000000},
	}
	for _, s := range sizes {
		if err := writeFile(filepath.Join(d, s.name), make([]byte, s.size)); err != nil {
			return err
		}
	}
	return nil
}

func b21ManySmallFiles(d string) error {
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("f%03d", i)
		if err := writeFile(filepath.Join(d, name), nil); err != nil {
			return err
		}
	}
	return nil
}

func b22DeeplyNested(d string) error {
	nested := d
	for _, c := range "abcdefghij" {
		nested = filepath.Join(nested, string(c))
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(nested, "file.txt"), nil)
}

func b23LongFilename(d string) error {
	name := strings.Repeat("a", 200)
	if err := writeFile(filepath.Join(d, name), nil); err != nil {
		return skipf("filesystem rejected 200-byte component (%v)", err)
	}
	return nil
}

func b24SurrogateEscape(d string) error {
	if runtime.GOOS != "linux" {
		return skipf("non-UTF-8 filenames unsupported on %s", runtime.GOOS)
	}
	// Build the path as raw bytes so the invalid 0xff byte survives intact.
	rawName := []byte{0x62, 0x61, 0x64, 0xff, 0x2e, 0x74, 0x78, 0x74} // 'bad\xff.txt'
	full := append([]byte(d+string(filepath.Separator)), rawName...)
	fd, err := syscall.Open(string(full), syscall.O_CREAT|syscall.O_WRONLY|syscall.O_EXCL, 0o644)
	if err != nil {
		return skipf("could not create non-UTF-8 filename (%v)", err)
	}
	if err := syscall.Close(fd); err != nil {
		return err
	}
	return nil
}

func b25PrefixSort(d string) error {
	for _, name := range []string{"foo", "foo.txt", "foobar"} {
		if err := writeFile(filepath.Join(d, name), nil); err != nil {
			return err
		}
	}
	return nil
}

func b26ContentIrrelevance(d string) error {
	return writeFile(filepath.Join(d, "hello.txt"), []byte("world!"))
}

// --- Fixture table -------------------------------------------------------

type fixture struct {
	id           int
	name         string
	build        func(string) error
	requirements []string
}

// subtestName returns the name used by t.Run for this fixture, e.g.
// "fixture_05_case_sensitive_sort". Spaces and other non-alphanumerics
// are replaced with underscores so the name can appear unchanged in
// `go test -run` regex selectors.
func (f fixture) subtestName() string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, f.name)
	return fmt.Sprintf("fixture_%02d_%s", f.id, safe)
}

var fixtures = []fixture{
	{1, "empty directory", b1EmptyDir, nil},
	{2, "single zero-byte file", b2ZeroByte, nil},
	{3, "single small ASCII file", b3SmallASCII, nil},
	{4, "two files at root", b4TwoFiles, nil},
	{5, "case-sensitive sort", b5CaseSensitiveSort, []string{"case_sensitive_fs"}},
	{6, "slash vs dash sort edge case", b6SlashVsDash, nil},
	{7, "nested directories", b7NestedDirs, nil},
	{8, "sibling directories sort by full path", b8SiblingFullPathSort, nil},
	{9, "only an empty subdirectory", b9OnlyEmptySubdir, nil},
	{10, "file plus an empty subdirectory", b10FilePlusEmptySubdir, nil},
	{11, "only a symlink", b11OnlySymlink, []string{"symlinks"}},
	{12, "symlink alongside a file", b12SymlinkAlongsideFile, []string{"symlinks"}},
	{13, "Latin diacritics, NFC source", b13LatinDiacriticsNFC, nil},
	{14, "Latin diacritics, NFD source", b14LatinDiacriticsNFD, []string{"byte_preserving_fs"}},
	{15, "Cyrillic filename", b15Cyrillic, nil},
	{16, "Han filename", b16Han, nil},
	{17, "Arabic filename", b17Arabic, nil},
	{18, "emoji filename", b18Emoji, nil},
	{19, "multi-script directory", b19MultiScript, nil},
	{20, "size boundaries", b20SizeBoundaries, nil},
	{21, "many small files", b21ManySmallFiles, nil},
	{22, "deeply nested file", b22DeeplyNested, nil},
	{23, "long filename", b23LongFilename, []string{"long_component_names"}},
	{24, "surrogate-escape filename byte", b24SurrogateEscape, []string{"non_utf8_filenames"}},
	{25, "prefix and trailing-character sort", b25PrefixSort, nil},
	{26, "content irrelevance (bit-rot tolerance)", b26ContentIrrelevance, nil},
}

// --- Expected-digests JSON ----------------------------------------------

type expectedFixture struct {
	ID                   int      `json:"id"`
	Name                 string   `json:"name"`
	ExpectedDigest       string   `json:"expected_digest"`
	PlatformRequirements []string `json:"platform_requirements"`
}

type expectedDoc struct {
	Version    string            `json:"version"`
	FixtureDoc string            `json:"fixture_doc"`
	Fixtures   []expectedFixture `json:"fixtures"`
}

func loadExpected(p string) (map[int]string, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var doc expectedDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(doc.Fixtures))
	for _, f := range doc.Fixtures {
		out[f.ID] = f.ExpectedDigest
	}
	return out, nil
}

func readNames(d string) ([]string, error) {
	dirents, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(dirents))
	for i, de := range dirents {
		out[i] = de.Name()
	}
	return out, nil
}

func defaultFixturesPath() string {
	// `go test` runs the test binary with cwd set to the package source
	// directory, i.e. matrix256-go/tests. The spec repo lives one level
	// up from that as a sibling clone of matrix256-go.
	return filepath.Join("..", "..", "matrix256", "conformance_fixtures.json")
}

// --- Test entry point ---------------------------------------------------

type generatedFixture struct {
	ID                   int      `json:"id"`
	Name                 string   `json:"name"`
	ExpectedDigest       string   `json:"expected_digest"`
	PlatformRequirements []string `json:"platform_requirements"`
}

type generateBlock struct {
	Version    string             `json:"version"`
	FixtureDoc string             `json:"fixture_doc"`
	Fixtures   []generatedFixture `json:"fixtures"`
}

// TestConformance runs every Tier-1 fixture from CONFORMANCE_FIXTURES.md
// as a subtest. With -generate it emits the JSON block ready to paste
// into the spec repo's conformance_fixtures.json.
func TestConformance(t *testing.T) {
	jsonPath := *fixturesJSON
	if jsonPath == "" {
		jsonPath = defaultFixturesPath()
	}

	var expected map[int]string
	if !*generateFlag {
		var err error
		expected, err = loadExpected(jsonPath)
		if err != nil {
			t.Fatalf(
				"could not load expected digests from %s: %v\n"+
					"Pass -fixtures-json PATH or clone the spec repo as a sibling at ../matrix256/.",
				jsonPath, err)
		}
	}

	var generated []generatedFixture
	for _, fix := range fixtures {
		t.Run(fix.subtestName(), func(t *testing.T) {
			tmp := t.TempDir()
			if err := fix.build(tmp); err != nil {
				if reason, ok := isSkip(err); ok {
					t.Skip(reason)
					return
				}
				t.Fatalf("build fixture: %v", err)
			}
			digest, err := v1.Fingerprint(tmp)
			if err != nil {
				t.Fatalf("fingerprint: %v", err)
			}
			if *generateFlag {
				reqs := fix.requirements
				if reqs == nil {
					reqs = []string{}
				}
				generated = append(generated, generatedFixture{
					ID:                   fix.id,
					Name:                 fix.name,
					ExpectedDigest:       digest,
					PlatformRequirements: reqs,
				})
				t.Logf("generated digest: %s", digest)
				return
			}
			exp, ok := expected[fix.id]
			if !ok {
				t.Fatalf("no expected digest for fixture %d in %s", fix.id, jsonPath)
			}
			if digest != exp {
				t.Errorf("digest mismatch:\n  produced: %s\n  expected: %s", digest, exp)
			}
		})
	}

	if *generateFlag {
		block := generateBlock{
			Version:    "matrix256v1",
			FixtureDoc: "CONFORMANCE_FIXTURES.md",
			Fixtures:   generated,
		}
		out, err := json.MarshalIndent(block, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		fmt.Println()
		fmt.Println("--- conformance_fixtures.json (paste into the matrix256 spec repo) ---")
		fmt.Println(string(out))
	}
}
