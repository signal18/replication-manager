package authguard

import (
	"net/http"
	"strings"
	"testing"
)

func TestClient(t *testing.T) {
	options := Options{
		Attempts:              3,
		LockoutDuration:       30,
		MaxLockouts:           3,
		BanDuration:           60,
		AttemptsResetDuration: 60,
		LockoutsResetDuration: 60,
		BindMethod:            BindToUsernameAndIP,
		SyncAfter:             10,
		Store:                 "::memory::",
		LogLevel:              LogLevelError,
	}

	ag, err := New(options)
	if err != nil {
		t.Errorf("unexpected error: %v\n", err)
		return
	}

	req, _ := http.NewRequest("POST", "", strings.NewReader("test"))
	req.Host = "1.2.3.4"

	if !ag.Check("testuser", req) {
		t.Errorf("unexpected user here")
		return
	}

	ag.Complaint("testuser", req)

	_, ok := ag.GetVisitor("testuser", req)
	if !ok {
		t.Errorf("user must be here")
		return
	}

	go func(ag *AuthGuard, req *http.Request) {
		if !ag.Check("testuser", req) {
			t.Errorf("unexpected false here")
			return
		}
	}(ag, req)
}
