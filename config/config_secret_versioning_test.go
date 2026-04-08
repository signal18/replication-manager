package config

import "testing"

func TestIsMonitoringSecretVersioningEnabled(t *testing.T) {
	t.Run("non-vault explicit disable is respected", func(t *testing.T) {
		conf := &Config{MonitoringSecretVersioning: false}
		if conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning disabled when explicitly set to false")
		}
	})

	t.Run("non-vault explicit enable is respected", func(t *testing.T) {
		conf := &Config{MonitoringSecretVersioning: true}
		if !conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning enabled when explicitly set to true")
		}
	})

	t.Run("vault explicit disable is respected", func(t *testing.T) {
		conf := &Config{VaultServerAddr: "https://vault.example", MonitoringSecretVersioning: false}
		if conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning disabled when explicitly set with vault")
		}
	})

	t.Run("vault configured explicit enable", func(t *testing.T) {
		conf := &Config{VaultServerAddr: "https://vault.example", MonitoringSecretVersioning: true}
		if !conf.IsMonitoringSecretVersioningEnabled() {
			t.Fatalf("expected secret versioning enabled when explicitly set with vault")
		}
	})
}
