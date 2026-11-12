// Package version holds the release number. Bump Version for every release;
// CI sets Commit with -ldflags.
package version

// Version is the release number shown in the page and printed by -version.
const Version = "0.2.1"

// Commit is the short git commit, set by CI at build time.
var Commit = "dev"

// String returns "0.1.0 (abc1234)".
func String() string { return Version + " (" + Commit + ")" }
