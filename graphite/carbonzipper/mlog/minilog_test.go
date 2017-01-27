package mlog

import (
	"bytes"
	"log"
	"testing"

	strftime "github.com/lestrrat/go-strftime"
)

func TestGetOutput(t *testing.T) {
	actual := GetOutput()
	if actual != nil {
		t.Errorf("actual output %v is different from the expected (nil)", actual)
	}
}

func TestSetRawStream(t *testing.T) {
	log.SetFlags(0)
	expectedOutput := &bytes.Buffer{}
	SetRawStream(expectedOutput)
	actualOutput := GetOutput()
	if actualOutput == nil {
		t.Errorf("actual output %v is different from the expected (not nil)", actualOutput)
	}

	logMessage := "sample message"
	expectedLog := logMessage + "\n"
	log.Print(logMessage)
	actualLog := expectedOutput.String()
	if actualLog != expectedLog {
		t.Errorf("actual log '%s' is different from the expected '%s'", actualLog, expectedLog)
	}
}

func TestStrftime(t *testing.T) {
	// Since we're ignoring the error from rotatelogs.New(), we may as well
	// assert that the only error it's likely to give us (a strftime error)
	// isn't likely to happen.
	// This isn't perfect, but it's better than nothing.

	pattern := "/var/log/carbonzipper" + strftimeFormat

	_, err := strftime.New(pattern)

	if err != nil {
		t.Errorf("strftime.New(%q) returned an error: %v", pattern, err)
	}

}
