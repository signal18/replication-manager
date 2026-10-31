package slackman

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

type failingHook struct{ url string }

func (h failingHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h failingHook) Fire(*logrus.Entry) error {
	return fmt.Errorf("Post %q: dial tcp: connection refused", h.url)
}

// A failed send's error, which logrus prints, does not carry the webhook URL: the URL is
// the credential.
func TestURLRedactingHookError(t *testing.T) {
	const u = "https://hooks.slack.example/services/T0/B0/MARKER"
	h := &urlRedactingHook{Hook: failingHook{url: u}, url: u}
	err := h.Fire(&logrus.Entry{})
	if err == nil || strings.Contains(err.Error(), "MARKER") || !strings.Contains(err.Error(), "<webhook-url>") {
		t.Fatalf("error %v", err)
	}
}
