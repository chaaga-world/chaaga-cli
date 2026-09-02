package main

import (
	"regexp"
	"strings"
)

// siblingFilenamePattern mirrors the regex half of
// app/lib/services/filename_validation.dart's isValidSiblingFilename
// exactly — kept in sync by hand; see that file if this ever needs to
// change.
var siblingFilenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// isValidSiblingFilename is a Go port of
// app/lib/services/filename_validation.dart's function of the same name —
// used here to pre-validate local filenames before ever attempting an
// upload, so a file the server would just 404 on (e.g. one with a space) is
// reported clearly instead of silently failing partway through a sync pass.
func isValidSiblingFilename(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	if name == "index.html" || name == "manifest" {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return siblingFilenamePattern.MatchString(name)
}
