package backupmgr

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// a fingerprint recorded without inode and ctime (before they were added, or off Linux)
// writes neither: omitempty leaves a zero inode out, omitzero a zero time
func TestArtifactFingerprintJSONOmitsZeroIdentity(t *testing.T) {
	out, err := json.Marshal(ArtifactFingerprint{Files: 1, Size: 4, ModTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "changeTime") || strings.Contains(string(out), "inode") {
		t.Fatalf("zero identity written: %s", out)
	}
	out, _ = json.Marshal(ArtifactFingerprint{Files: 1, Size: 4, Inode: 7, ChangeTime: time.Unix(2, 0).UTC()})
	if !strings.Contains(string(out), `"changeTime"`) || !strings.Contains(string(out), `"inode":7`) {
		t.Fatalf("identity not written: %s", out)
	}
}
