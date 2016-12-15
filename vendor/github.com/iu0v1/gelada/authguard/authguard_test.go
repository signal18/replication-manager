package authguard

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAll(t *testing.T) {
	fmt.Println("gelada/authguard: tests will take approximately 15 seconds")

	options := &Options{
		Attempts:    10,
		MaxLockouts: 5,

		AttemptsResetDuration: 2,
		LockoutDuration:       4,
		LockoutsResetDuration: 3,
		BanDuration:           5,
		BindMethod:            BindToUsernameAndIP,
	}

	var b bytes.Buffer

	l := &log{
		LogLevel:       LogLevelError,
		LogDestination: &b,
	}

	req, _ := http.NewRequest("POST", "", strings.NewReader("test"))
	req.Host = "1.2.3.4"

	ag := &AuthGuard{}
	ag.options = options
	ag.logger = l
	pool := map[string]*visitor{}
	ag.data = &visitors{
		bindMethod: ag.options.BindMethod,
		pool:       pool,
	}

	v, ok := ag.visitorGet("testuser", req)
	if ok {
		t.Error("user should not be here")
		return
	}

	// Attempts + 1
	for i := 0; i < ag.options.Attempts+1; i++ {
		ag.Complaint("testuser", req)
	}

	v, ok = ag.visitorGet("testuser", req)
	if !ok {
		t.Error("no user here")
		return
	}

	// attempts check
	if v.Attempts != 10 {
		t.Errorf("lockouts error; need: %d, here: %d\n", 10, v.Attempts)
		return
	}

	// lockouts check
	if v.Lockouts != 1 {
		t.Errorf("lockouts error; need: %d, here: %d\n", 1, v.Lockouts)
		return
	}

	// ban check
	for i := 0; i < ag.options.MaxLockouts; i++ {
		ag.Complaint("testuser", req)
	}
	if !v.Ban {
		t.Error("no ban here")
		return
	}

	// ban reset check
	time.Sleep(time.Duration(ag.options.BanDuration+1) * time.Second)

	ag.visitorDataActualize(v) // trigger user info update
	if v.Ban {
		t.Error("ban here")
		return
	}

	// attempts reset check
	for i := 0; i < ag.options.Attempts-1; i++ {
		ag.Complaint("testuser", req)
	}

	time.Sleep(time.Duration(ag.options.AttemptsResetDuration+1) * time.Second)

	ag.visitorDataActualize(v) // trigger user info update
	if v.Attempts != 0 {
		t.Errorf("attempts reset error; user have %d attempts\n", v.Attempts)
		return
	}

	// lockouts reset check
	for i := 0; i < ag.options.Attempts+1; i++ {
		ag.Complaint("testuser", req)
	}

	time.Sleep(time.Duration(ag.options.LockoutsResetDuration+1) * time.Second)

	ag.visitorDataActualize(v) // trigger user info update
	if v.Lockouts != 0 {
		t.Errorf("lockouts reset error; user have %d lockouts\n", v.Attempts)
		return
	}
}
