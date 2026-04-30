// Copyright 2026 wolfy <wolfy@shitwolfymakes.com>
// SPDX-License-Identifier: Apache-2.0

// Package v1 implements matrix256v1 — a SHA-256 fingerprint over the
// (path, size) records of every regular file under a rooted filesystem
// tree. The walk and serialization logic here must stay in lockstep with
// the normative spec in SPEC.md
// (https://github.com/shitwolfymakes/matrix256/blob/main/SPEC.md). If one
// changes, the other must too.
package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Version is the matrix256 algorithm version this package implements
// (spec §5). Distinct from a module or release version; future algorithm
// versions will be added as sibling subpackages with their own Version
// constants.
const Version = "1"

// Entry is a regular file selected for matrix256v1 fingerprinting.
//
// HostPath is the file's absolute path on the host filesystem, retained
// for callers that want to inspect or display entries; it is not part of
// the digest. Relative is the canonical UTF-8 byte sequence for the
// root-relative path ('/' separator, NFC-normalized, U+FFFD substitution
// for invalid sequences, per spec §2.2). Size is the file size in bytes
// per filesystem metadata (spec §2.3); it is never computed by reading
// file contents.
type Entry struct {
	HostPath string
	Relative []byte
	Size     int64
}

// Fingerprint computes the matrix256v1 digest of the filesystem rooted
// at root.
//
// It walks the tree, sorts entries by UTF-8 path bytes (spec §2.4),
// builds the per-entry serialization (<path-bytes> 0x00 <size-ascii>
// 0x0A, spec §2.5) into a single buffer, then SHA-256s the whole thing
// (spec §2.6). Returns 64 lowercase hex digits. Returns the underlying
// error if any directory or file metadata cannot be read — matrix256v1
// is all-or-nothing per spec §3.
//
// Building the buffer fully before hashing mirrors the spec literally.
// Feeding records into SHA-256 incrementally is an implementer's choice
// and produces identical digests, but the explicit form here is easier
// to verify against the spec.
func Fingerprint(root string) (string, error) {
	entries, err := Walk(root)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	for _, e := range entries {
		buf.Write(e.Relative)
		buf.WriteByte(0x00)
		buf.WriteString(strconv.FormatInt(e.Size, 10))
		buf.WriteByte(0x0A)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

// Walk collects every regular file under root, sorted by UTF-8 path
// bytes.
//
// Directories are skipped (their existence is implied by the relative
// paths of contained files), as are symbolic links (not followed, not
// emitted) and other non-file entries (devices, sockets, FIFOs).
// Returns an error on any metadata failure — matrix256v1 is
// all-or-nothing per spec §3.
func Walk(root string) ([]Entry, error) {
	var entries []Entry
	if err := scan(root, nil, &entries); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].Relative, entries[j].Relative) < 0
	})
	return entries, nil
}

// scan walks current, accumulating into out. ancestors is the chain of
// root-relative path components leading to current; each recursive call
// pushes its component before descending and pops on the way out, so
// Entry.Relative can be built directly from ancestors without computing
// a relative path from an absolute one.
func scan(current string, ancestors []string, out *[]Entry) error {
	dirents, err := os.ReadDir(current)
	if err != nil {
		return err
	}
	for _, de := range dirents {
		// Spec §2.1: symlinks are filtered before any other inspection —
		// neither followed nor emitted, regardless of what they point at.
		// Use Info() rather than Type() so that on filesystems where
		// d_type is unavailable (some old NFS mounts) we still get a
		// reliable mode via Lstat fallback.
		fi, err := de.Info()
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := de.Name()
		full := filepath.Join(current, name)
		switch {
		case fi.IsDir():
			next := append(ancestors, name)
			if err := scan(full, next, out); err != nil {
				return err
			}
		case fi.Mode().IsRegular():
			components := append(ancestors, name)
			*out = append(*out, Entry{
				HostPath: full,
				Relative: canonicalRelative(components),
				Size:     fi.Size(),
			})
		}
	}
	return nil
}

// canonicalRelative builds the canonical UTF-8 byte sequence for the
// file whose root-relative path is components: '/'-joined, U+FFFD
// substitution for invalid byte sequences, NFC-normalized.
//
// The U+FFFD pass is required by spec §2.2: "paths that cannot be
// represented as valid Unicode are encoded as UTF-8 with the Unicode
// replacement character ... substituted for each invalid code unit."
// Go strings are arbitrary byte sequences (os.ReadDir on Linux returns
// raw filename bytes verbatim); strings.ToValidUTF8 does the spec's
// substitution.
//
// The norm.NFC.String pass applies spec §2.2's NFC requirement. NFC is
// not in the Go standard library; see the README for the dep
// justification.
//
// Join-then-substitute-then-normalize is equivalent to doing the same
// per-component because the separator '/' is single-byte ASCII (no
// UTF-8 sequence can cross a component boundary), and NFC distributes
// over '/' (U+002F has canonical combining class 0 and is in no
// canonical (de)composition mapping).
func canonicalRelative(components []string) []byte {
	joined := strings.Join(components, "/")
	valid := strings.ToValidUTF8(joined, "\ufffd")
	return []byte(norm.NFC.String(valid))
}
