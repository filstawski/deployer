// Package version reports the build version of the Deployer binaries.
package version

// Version is set at build time with
// -ldflags "-X github.com/filstawski/deployer/internal/version.Version=v1.2.3".
// Development builds report "dev".
var Version = "dev"

// IsRelease reports whether this binary was built as a tagged release.
func IsRelease() bool { return Version != "dev" }
