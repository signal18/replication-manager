package cluster

import (
	"runtime"
	"testing"
)

// artifactIdentityRead: inode and ctime are read on Linux only (artifactStatIdentity);
// elsewhere a fingerprint matches nothing, so a backup is never proven nor its read skipped.
var artifactIdentityRead = runtime.GOOS == "linux"

// requireArtifactIdentity skips a test whose point is a fingerprint that matches.
func requireArtifactIdentity(t *testing.T) {
	t.Helper()
	if !artifactIdentityRead {
		t.Skip("inode and ctime are read on Linux only: a fingerprint matches nothing on " + runtime.GOOS)
	}
}
