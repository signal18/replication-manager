package zapwriter

import (
	"strings"
	"testing"
)

func TestTestCapture(t *testing.T) {
	defer Test()()

	Default().Info("info message")

	if !strings.Contains(TestCapture(), "info message") {
		t.FailNow()
	}

	if strings.Contains(TestCapture(), "info message") {
		t.FailNow()
	}
}

func TestTestString(t *testing.T) {
	defer Test()()

	Default().Info("info message")

	if !strings.Contains(TestString(), "info message") {
		t.FailNow()
	}

	if !strings.Contains(TestString(), "info message") {
		t.FailNow()
	}
}
