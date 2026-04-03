package server

import "testing"

func TestTrackedSecretSnapshotChanged(t *testing.T) {
	t.Run("same snapshot", func(t *testing.T) {
		before := map[string]string{"db-servers-credential": "dbuser:pass1"}
		after := map[string]string{"db-servers-credential": "dbuser:pass1"}
		if trackedSecretSnapshotChanged(before, after) {
			t.Fatalf("expected unchanged snapshots to return false")
		}
	})

	t.Run("value changed", func(t *testing.T) {
		before := map[string]string{"db-servers-credential": "dbuser:pass1"}
		after := map[string]string{"db-servers-credential": "dbuser:pass2"}
		if !trackedSecretSnapshotChanged(before, after) {
			t.Fatalf("expected value change to return true")
		}
	})

	t.Run("key added", func(t *testing.T) {
		before := map[string]string{"db-servers-credential": "dbuser:pass1"}
		after := map[string]string{
			"db-servers-credential": "dbuser:pass1",
			"mail-smtp-password":    "smtp-pass",
		}
		if !trackedSecretSnapshotChanged(before, after) {
			t.Fatalf("expected key addition to return true")
		}
	})
}
