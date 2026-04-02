package config

import "testing"

func TestIsMonitoringSecretVersioningEnabled(t *testing.T) {
	t.Run("no vault uses secure default true", func(t *testing.T) {
		conf := &Config{MonitoringSecretVersioning: false}
		if !conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning enabled when vault is not configured")
		}
	})

	t.Run("vault configured defaults false", func(t *testing.T) {
		conf := &Config{VaultServerAddr: "https://vault.example", MonitoringSecretVersioning: false}
		if conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning disabled by default when vault is configured")
		}
	})

	t.Run("vault configured explicit enable", func(t *testing.T) {
		conf := &Config{VaultServerAddr: "https://vault.example", MonitoringSecretVersioning: true}
		if !conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning enabled when explicitly set with vault")
		}
	})
}
