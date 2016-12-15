// Package authguard provides a tool for handle and processing login attempts.
//
// It's designed for use with a Gelada (https://github.com/iu0v1/gelada), but
// it can operate as an independent package.
package authguard

import (
	"bytes"
	"crypto/md5"
	"encoding/gob"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// LogHandlerFunc type for log handler function
type LogHandlerFunc func(message string, lvl LogLevelType)

// LogLevelType declare the level of informatyvity of log message
type LogLevelType int

// predefined LogLevelType levels
const (
	LogLevelNone LogLevelType = iota
	LogLevelInfo
	LogLevelError

	LogLevelErrorOnly
)

// log - struct for internal log service
type log struct {
	LogLevel       LogLevelType
	LogDestination io.Writer
	Handler        LogHandlerFunc
}

func (l *log) Log(message string, lvl LogLevelType) {
	if l.Handler != nil {
		l.Handler(message, lvl)
		return
	}

	if l.LogLevel == 0 {
		return
	}

	if lvl <= l.LogLevel {
		if l.LogLevel == LogLevelErrorOnly {
			if lvl == LogLevelErrorOnly {
				fmt.Fprintf(l.LogDestination, "gelada/authguard: %s\n", message)
			}
			return
		}
		fmt.Fprintf(l.LogDestination, "gelada/authguard: %s\n", message)
	}
}

// BindType type for BindMethod option.
type BindType int

// BindMethod types
const (
	BindToNothing       BindType = iota // no bind
	BindToIP                            // bind to user host (IP)
	BindToUsernameAndIP                 // bind to host and username
)

// Options - structure, which is used to configure authguard.
type Options struct {
	// Attempts - the number of password attempts.
	Attempts int

	// LockoutDuration - lock duration after the end of password attempts.
	// Seconds.
	LockoutDuration int

	// MaxLockouts - the maximum amount of lockouts, before ban.
	MaxLockouts int

	// BanDuration - duration of ban.
	// Seconds.
	BanDuration int

	// AttemptsResetDuration - time after which to reset the number of attempts.
	// Seconds.
	AttemptsResetDuration int

	// LockoutsResetDuration - time after which to reset the number of lockouts.
	LockoutsResetDuration int

	// BindMethod - visitor binding type. Only IP or IP + username.
	BindMethod BindType

	// SyncAfter - sync data with the Store file after X updates.
	SyncAfter int

	// Store - place for store user data.
	// Filepath.
	//
	// If Store == "::memory::", then Gelada does not place the data in the file
	// and store everything in memory.
	Store string

	// Exceptions - Hosts(IP) whitelist.
	Exceptions []string

	// LogLevel provides the opportunity to choose the level of
	// information messages.
	// Each level includes the messages from the previous level,
	// except LogLevelErrorOnly.
	// LogLevelNone       - no messages // 0
	// LogLevelInfo       - info        // 1
	// LogLevelError      - error       // 2
	// LogLevelErrorOnly  - only errors // 3
	//
	// Default: LogLevelNone.
	LogLevel LogLevelType

	// LogDestination provides the opportunity to choose the own
	// destination for log messages (errors, info, etc).
	//
	// Default: 'os.Stdout'.
	LogDestination io.Writer

	// LogHandler takes log messages to bypass the internal
	// mechanism of the message processing
	//
	// If LogHandler is selected - all log settings will be ignored.
	LogHandler LogHandlerFunc

	// ProxyIPHeaderName - http header name for handle user IP behind proxy
	ProxyIPHeaderName string

	// TODO
	// Backend // ql || gob || ...
}

// Visitor contain info about the current user
// and provide some helper methods.
type Visitor struct { // used as simple visitor, but with immutable struct data
	Username  string
	Host      string
	UserAgent string

	Attempts int
	Lockouts int

	Ban bool

	ResetAttemptsAfter time.Time
	ResetLockoutsAfter time.Time
	LockUntil          time.Time

	v *visitor
}

// Reset all attempts, lockouts and bans.
func (v *Visitor) Reset() {
	v.v.reset()
	v.Attempts = v.v.Attempts
	v.Lockouts = v.v.Lockouts
	v.Ban = v.v.Ban
	v.ResetAttemptsAfter = v.v.ResetAttemptsAfter
	v.ResetLockoutsAfter = v.v.ResetLockoutsAfter
	v.LockUntil = v.v.LockUntil
}

// LockRemainingTime - return the time until the lockouts ends, in seconds.
func (v *Visitor) LockRemainingTime() int {
	return v.v.lockRemainingTime()
}

// LockDate - return the raw time until the lockouts ends.
func (v *Visitor) LockDate() time.Time {
	return v.v.lockDate()
}

// visitor internal struct contain data about a current visitor
// and have some helper methods
type visitor struct {
	Username  string
	Host      string
	UserAgent string

	Attempts int
	Lockouts int

	Ban bool

	ResetAttemptsAfter time.Time
	ResetLockoutsAfter time.Time
	LockUntil          time.Time

	ag *AuthGuard
	mu sync.Mutex
}

// reset  attempts, lockouts and bans
func (v *visitor) reset() {
	var t time.Time

	v.mu.Lock()
	defer v.mu.Unlock()

	v.Attempts = 0
	v.Lockouts = 0
	v.Ban = false
	v.ResetAttemptsAfter = t
	v.ResetLockoutsAfter = t
	v.LockUntil = t

	v.ag.sync()
}

// return the time until the lock ends, in seconds
func (v *visitor) lockRemainingTime() int {
	v.mu.Lock()
	defer v.mu.Unlock()

	currentTime := time.Now().Local()
	lrt := int(v.LockUntil.Sub(currentTime).Seconds())
	if lrt <= 0 {
		return 0
	}

	return lrt
}

// return the raw time until the lockouts ends
func (v *visitor) lockDate() time.Time {
	return v.LockUntil
}

// visitors contain visitors and current BindMethod
type visitors struct {
	BindMethod BindType
	Pool       map[string]*visitor
	mu         sync.Mutex
}

// AuthGuard - main struct.
type AuthGuard struct {
	options     *Options
	data        *visitors
	logger      *log
	syncTrigger int
	mu          sync.Mutex
}

// New - init and return new AuthGuard struct.
func New(o Options) (*AuthGuard, error) {
	ag := &AuthGuard{options: &o}

	switch {
	case ag.options.BindMethod == 0:
		return ag, fmt.Errorf("%s\n", "BindMethod can't be 0 (BindToNothing)")
	case ag.options.BindMethod >= 3 || ag.options.BindMethod < 0:
		return ag, fmt.Errorf("BindMethod can't be %d\n", ag.options.BindMethod)
	}

	if ag.options.Store == "::memory::" {
		v := map[string]*visitor{}
		ag.data = &visitors{
			BindMethod: ag.options.BindMethod,
			Pool:       v,
		}
	} else if ag.options.Store == "" {
		return ag, fmt.Errorf("LoginRoute not declared\n")
	} else {
		data := &visitors{}

		// open file
		file, err := os.OpenFile(
			ag.options.Store,
			os.O_CREATE|os.O_RDWR|os.O_SYNC,
			640,
		)
		if err != nil {
			return ag, fmt.Errorf("error to open Store file: %v\n", err)
		}

		// decode to struct
		dec := gob.NewDecoder(file)
		if err := dec.Decode(&data); err != nil {
			if err != io.EOF {
				return ag, fmt.Errorf("error to read Store file: %v\n", err)
			}
			// enpty Store
			v := map[string]*visitor{}
			data.BindMethod = ag.options.BindMethod
			data.Pool = v
		}

		// check BindMethod
		if data.BindMethod != ag.options.BindMethod {
			var b string
			if data.BindMethod == BindToIP {
				b = "BindToIp"
			} else {
				b = "BinBindToUsernameAndIP"
			}

			e0 := "Store BindMethod mismatch error."
			e1 := "Store uses a different BindMethod to store data"
			e2 := "You must create new Store or use BindMethod from old Store."
			return ag, fmt.Errorf("%s %s (%s). %s\n", e0, e1, b, e2)
		}

		// update pointers in clients
		for _, visitor := range data.Pool {
			visitor.ag = ag
		}

		ag.data = data
	}

	if ag.options.Attempts < 0 {
		return ag, fmt.Errorf("Attempts can not be a negative number")
	}

	if ag.options.LockoutDuration < 0 {
		return ag, fmt.Errorf("LockoutDuration can not be a negative number")
	}

	if ag.options.MaxLockouts < 0 {
		return ag, fmt.Errorf("MaxLockouts can not be a negative number")
	}

	if ag.options.BanDuration < 0 {
		return ag, fmt.Errorf("BanDuration can not be a negative number")
	}

	if ag.options.AttemptsResetDuration < 0 {
		return ag, fmt.Errorf("AttemptsResetDuration can not be a negative number")
	}

	if ag.options.LockoutsResetDuration < 0 {
		return ag, fmt.Errorf("LockoutsResetDuration can not be a negative number")
	}

	if ag.options.SyncAfter < 0 {
		return ag, fmt.Errorf("SyncAfter can not be a negative number")
	}

	if ag.options.LogDestination == nil {
		o.LogDestination = os.Stdout
	}

	l := &log{
		LogLevel:       ag.options.LogLevel,
		LogDestination: ag.options.LogDestination,
		Handler:        ag.options.LogHandler,
	}
	ag.logger = l

	return ag, nil
}

// Sync current data with Store immediately.
func (ag *AuthGuard) Sync() error {
	if ag.options.Store == "::memory::" {
		return nil
	}

	ag.mu.Lock()
	defer ag.mu.Unlock()

	var buf bytes.Buffer

	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&ag.data); err != nil {
		return fmt.Errorf("error to encode Store file: %v\n", err)
	}

	if err := ioutil.WriteFile(ag.options.Store, buf.Bytes(), 640); err != nil {
		return fmt.Errorf("error to write Store file: %v", err)
	}

	return nil
}

// internal sync
func (ag *AuthGuard) sync() {
	if ag.options.Store == "::memory::" {
		return
	}

	ag.mu.Lock()
	defer ag.mu.Unlock()

	if ag.options.SyncAfter == 0 {
		return
	}

	var buf bytes.Buffer

	ag.syncTrigger++
	if ag.syncTrigger == ag.options.SyncAfter {
		ag.syncTrigger = 0
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(&ag.data); err != nil {
			msg := fmt.Sprintf("error to encode Store file [on sys sync]: %v", err)
			ag.logger.Log(msg, LogLevelError)
			return
		}
	}

	if err := ioutil.WriteFile(ag.options.Store, buf.Bytes(), 640); err != nil {
		msg := fmt.Sprintf("error to write Store file [on sys sync]: %v", err)
		ag.logger.Log(msg, LogLevelError)
		return
	}
}

// Check by the presence of lockouts.
// 'true' if there is no locks.
func (ag *AuthGuard) Check(username string, req *http.Request) bool {
	// check exceptions
	for _, e := range ag.options.Exceptions {
		if e == ag.getHost(req) {
			return true
		}
	}

	// get client
	v, ok := ag.visitorGet(username, req)
	if !ok {
		return true
	}

	// check client
	v.mu.Lock()
	defer v.mu.Unlock()

	ag.visitorDataActualize(v)

	currentTime := time.Now().Local()
	if v.LockUntil.After(currentTime) {
		if !v.Ban {
			ag.complaint(v)
		}
		return false
	}

	return true
}

// complaint is used to check and process failed login attempt
func (ag *AuthGuard) complaint(v *visitor) {
	currentTime := time.Now().Local()

	if v.Attempts < ag.options.Attempts {
		v.Attempts++
		v.ResetAttemptsAfter = currentTime.Add(
			time.Duration(ag.options.AttemptsResetDuration) * time.Second,
		)
		ag.sync()
		return
	}

	// first lockout
	if v.Lockouts == 0 {
		v.Lockouts++
		v.LockUntil = currentTime.Add(
			time.Duration(ag.options.LockoutDuration) * time.Second,
		)
		v.ResetLockoutsAfter = currentTime.Add(
			time.Duration(ag.options.LockoutsResetDuration) * time.Second,
		)
		ag.sync()
		msg := fmt.Sprintf("visitor has been locked [ name: %s | host: %s | locks: %d ]",
			v.Username,
			v.Host,
			v.Lockouts,
		)
		ag.logger.Log(msg, LogLevelInfo)
		return
	}

	if v.Lockouts < ag.options.MaxLockouts {
		v.Lockouts++
		v.ResetLockoutsAfter = currentTime.Add(
			time.Duration(ag.options.LockoutsResetDuration) * time.Second,
		)
		ag.sync()
		msg := fmt.Sprintf("visitor has increased the number of locks [ name: %s | host: %s | locks: %d ]",
			v.Username,
			v.Host,
			v.Lockouts,
		)
		ag.logger.Log(msg, LogLevelInfo)
		return
	}

	v.Ban = true
	v.LockUntil = currentTime.Add(time.Duration(ag.options.BanDuration) * time.Second)
	ag.sync()
	msg := fmt.Sprintf("visitor has has been banned [ name: %s | host: %s | locks: %d ]",
		v.Username,
		v.Host,
		v.Lockouts,
	)
	ag.logger.Log(msg, LogLevelInfo)
}

// Complaint is used to report a failed login attempt.
func (ag *AuthGuard) Complaint(username string, req *http.Request) {
	var v *visitor
	var ok bool

	currentTime := time.Now().Local()

	v, ok = ag.visitorGet(username, req)
	if !ok {
		// create new visitor
		v = &visitor{
			Username:  username,
			Host:      ag.getHost(req),
			UserAgent: req.UserAgent(),
			Attempts:  1,
			ResetAttemptsAfter: currentTime.Add(
				time.Duration(ag.options.AttemptsResetDuration) * time.Second,
			),
			ag: ag,
		}

		ag.data.mu.Lock()
		defer ag.data.mu.Unlock()

		var id [16]byte

		switch ag.options.BindMethod {
		case BindToIP:
			id = md5.Sum([]byte(ag.getHost(req)))
		case BindToUsernameAndIP:
			id = md5.Sum([]byte(username + ag.getHost(req)))
		}

		ag.data.Pool[string(id[:])] = v
		ag.sync()
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	ag.complaint(v)
}

// get visitor
func (ag *AuthGuard) visitorGet(username string, req *http.Request) (*visitor, bool) {
	ag.mu.Lock()
	defer ag.mu.Unlock()

	ag.data.mu.Lock()
	defer ag.data.mu.Unlock()

	var id [16]byte

	switch ag.options.BindMethod {
	case BindToIP:
		id = md5.Sum([]byte(ag.getHost(req)))
	case BindToUsernameAndIP:
		id = md5.Sum([]byte(username + ag.getHost(req)))
	}

	v := ag.data.Pool[string(id[:])]
	if v == nil {
		return nil, false
	}

	ag.visitorDataActualize(v)

	return v, true
}

// process and update the visitor data
func (ag *AuthGuard) visitorDataActualize(v *visitor) {
	currentTime := time.Now().Local()

	// ban check
	if v.Ban {
		// if the lockout time has ended - reset data
		if v.LockUntil.Before(currentTime) {
			v.Attempts = 0
			v.Lockouts = 0
			v.Ban = false
			ag.sync()
			return
		}
	}

	// attempts check
	if v.Attempts > 0 && !v.Ban && v.LockUntil.Before(currentTime) {
		if v.ResetAttemptsAfter.Before(currentTime) {
			v.Attempts = 0
			ag.sync()
		} else {
			return
		}
	}

	// lockouts check
	if v.Lockouts > 0 && !v.Ban {
		if v.ResetLockoutsAfter.Before(currentTime) {
			v.Lockouts = 0
		}
		ag.sync()
	}
}

// GetAllVisitors return all aviable Visitors.
func (ag *AuthGuard) GetAllVisitors() []*Visitor {
	visitors := []*Visitor{}

	ag.data.mu.Lock()
	defer ag.data.mu.Unlock()

	for _, visitor := range ag.data.Pool {
		v := &Visitor{
			Username:           visitor.Username,
			Host:               visitor.Host,
			UserAgent:          visitor.UserAgent,
			Attempts:           visitor.Attempts,
			Lockouts:           visitor.Lockouts,
			Ban:                visitor.Ban,
			ResetAttemptsAfter: visitor.ResetAttemptsAfter,
			ResetLockoutsAfter: visitor.ResetLockoutsAfter,
			LockUntil:          visitor.LockUntil,
			v:                  visitor,
		}
		visitors = append(visitors, v)
	}

	return visitors
}

// GetVisitor returns current Visitor.
func (ag *AuthGuard) GetVisitor(username string, req *http.Request) (*Visitor, bool) {
	visitor, ok := ag.visitorGet(username, req)
	if !ok {
		return nil, false
	}

	v := &Visitor{
		Username:           visitor.Username,
		Host:               visitor.Host,
		UserAgent:          visitor.UserAgent,
		Attempts:           visitor.Attempts,
		Lockouts:           visitor.Lockouts,
		Ban:                visitor.Ban,
		ResetAttemptsAfter: visitor.ResetAttemptsAfter,
		ResetLockoutsAfter: visitor.ResetLockoutsAfter,
		LockUntil:          visitor.LockUntil,
		v:                  visitor,
	}

	return v, true
}

// ClearUntrackedVisitors is used to release the Store data from visitors,
// who do not have any violations. Store will be synchronized after the process.
//
// Need to reduce the space occupied by the Store.
func (ag *AuthGuard) ClearUntrackedVisitors() {
	visitors := map[string]*visitor{}

	ag.data.mu.Lock()
	defer ag.data.mu.Unlock()

	for id, v := range ag.data.Pool {
		v.mu.Lock()
		currentTime := time.Now().Local()

		if v.Attempts == 0 &&
			v.Lockouts == 0 &&
			!v.Ban &&
			v.LockUntil.Before(currentTime) {
			continue
		}

		vn := &visitor{
			Username:           v.Username,
			Host:               v.Host,
			UserAgent:          v.UserAgent,
			Attempts:           v.Attempts,
			Lockouts:           v.Lockouts,
			Ban:                v.Ban,
			ResetAttemptsAfter: v.ResetAttemptsAfter,
			ResetLockoutsAfter: v.ResetLockoutsAfter,
			LockUntil:          v.LockUntil,
			ag:                 ag,
		}
		visitors[id] = vn
		v.mu.Unlock()
	}

	ag.data.Pool = visitors
	ag.sync()
}

// getHost - get host from http.Request
func (ag *AuthGuard) getHost(req *http.Request) string {
	IP := req.Header.Get(ag.options.ProxyIPHeaderName)
	if IP == "" {
		return strings.Split(req.Host, ":")[0]
	}
	return IP
}
