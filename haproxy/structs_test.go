package haproxy

import (
	"errors"
	"testing"
)

func TestStructs_Error(t *testing.T) {
	err := Error{404, errors.New("not found")}
	if err.Error() == "" {
		t.Errorf("Failed to create custom error")
	}
}
