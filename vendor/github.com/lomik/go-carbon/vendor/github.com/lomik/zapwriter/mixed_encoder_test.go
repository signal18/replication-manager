package zapwriter

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestMixedEncoder(t *testing.T) {
	cfg := NewConfig()
	cfg.Encoding = "mixed"

	defer testWithConfig(cfg)()

	zap.L().Named("carbonserver").Info("message text", zap.String("key", "value"))

	if !strings.Contains(TestCapture(), `] INFO [carbonserver] message text {"key": "value"}`) {
		t.FailNow()
	}

	zap.L().Info("message text", zap.String("key", "value"))

	if !strings.Contains(TestCapture(), `] INFO message text {"key": "value"}`) {
		t.FailNow()
	}
}
