// Copyright 2026 wolfy <wolfy@shitwolfymakes.com>
// SPDX-License-Identifier: Apache-2.0

// Package matrix256 — reproducible fingerprints for optical discs and
// filesystem trees.
//
// The active algorithm version lives in the v1 subpackage: a SHA-256
// over a canonical serialization of the (path, size) records of every
// regular file under the walk root. See SPEC.md in the spec repo for
// the normative specification:
// https://github.com/shitwolfymakes/matrix256/blob/main/SPEC.md
//
// Importing code addresses the algorithm explicitly:
//
//	import "github.com/shitwolfymakes/matrix256-go/v1"
//
//	digest, err := v1.Fingerprint("/media/user/DISC")
//
// The module exposes nothing at the top level so future versions can
// be added as sibling subpackages (v2, …) without a "current" default
// that would silently change behavior.
package matrix256
