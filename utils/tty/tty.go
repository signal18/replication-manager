package tty

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

// TerminalManager interface defines the methods for terminal management.
type TerminalManager interface {
	LaunchTerminal(sessionID string) (*exec.Cmd, error)
	LaunchSSHTerminal(sessionID string) (*exec.Cmd, error)
}

// TmuxManager handles tmux-based terminal sessions.
type TmuxManager struct{}

func (tm *TmuxManager) LaunchTerminal(sessionID string) (*exec.Cmd, error) {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionID, "bash")
	return cmd, nil
}

func (tm *TmuxManager) LaunchSSHTerminal(sessionID string) (*exec.Cmd, error) {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionID, "bash")
	return cmd, nil
}

// ScreenManager handles screen-based terminal sessions.
type ScreenManager struct{}

func (sm *ScreenManager) LaunchTerminal(sessionID string) (*exec.Cmd, error) {
	cmd := exec.Command("screen", "-dmS", sessionID, "bash")
	return cmd, nil
}

func (tm *ScreenManager) LaunchSSHTerminal(sessionID string) (*exec.Cmd, error) {
	cmd := exec.Command("screen", "-dmS", sessionID, "bash")
	return cmd, nil
}

// Session represents a single terminal session (SSH or local shell).
type Session struct {
	ID         string          `json:"id"`
	Owner      string          `json:"owner"`
	Conn       *websocket.Conn `json:"-"`
	SSHClient  *ssh.Client     `json:"-"`
	SSHSession *ssh.Session    `json:"-"`
	Cmd        *exec.Cmd       `json:"-"`
	Stdin      io.WriteCloser  `json:"-"`
	Stdout     io.Reader       `json:"-"`
	Stderr     io.Reader       `json:"-"`
	Host       string          `json:"host"`
	Port       string          `json:"port"`
	Username   string          `json:"-"`
	Password   string          `json:"-"`
	Keys       []ssh.Signer    `json:"-"`
	WG         sync.WaitGroup  `json:"-"`
	Logger     *logrus.Logger  `json:"-"`
	Manager    *SessionManager `json:"-"`
	writeMu    sync.Mutex      `json:"-"`
	Line       string          `json:"-"`
}

type SignerList []ssh.Signer

// SessionManager handles session persistence and resumption.
type SessionManager struct {
	mu          sync.Mutex
	sshKeys     map[string]SignerList
	sessions    map[string]*Session
	stateFile   string
	AllowResume bool
	TerminalMgr TerminalManager
	Logger      *logrus.Logger
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(stateFile string, allowResume bool, terminalMgr TerminalManager, logger *logrus.Logger) *SessionManager {
	if terminalMgr == nil {
		allowResume = false
	}

	manager := &SessionManager{
		sessions:    make(map[string]*Session),
		stateFile:   stateFile,
		AllowResume: allowResume,
		TerminalMgr: terminalMgr,
		Logger:      logger,
	}

	// Load sessions from the state file on initialization
	manager.loadState()

	return manager
}

func (sm *SessionManager) GetSessions(prefix string) []*Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sessions := make([]*Session, 0)
	if prefix == "" {
		for _, session := range sm.sessions {
			sessions = append(sessions, session)
		}
	} else {
		for id, session := range sm.sessions {
			if id[:len(prefix)] == prefix {
				sessions = append(sessions, session)
			}
		}
	}

	return sessions
}

// SaveState saves the current session state to a file.
func (sm *SessionManager) SaveState() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	file, err := os.Create(sm.stateFile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Save the session metadata (exclude active WebSocket connection and SSH session)
	sessionMetadata := make(map[string]interface{})
	for id, session := range sm.sessions {
		sessionMetadata[id] = map[string]string{
			"host":     session.Host,
			"port":     session.Port,
			"username": session.Username,
			"password": session.Password,
		}
	}

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(sessionMetadata); err != nil {
		return err
	}

	return nil
}

// LoadState loads the session state from a file.
func (sm *SessionManager) loadState() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	file, err := os.Open(sm.stateFile)
	if err != nil {
		return err
	}
	defer file.Close()

	var sessionMetadata map[string]map[string]string
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&sessionMetadata); err != nil {
		return err
	}

	// Restore sessions from the metadata
	for id, metadata := range sessionMetadata {
		session := &Session{
			ID:       id,
			Host:     metadata["host"],
			Port:     metadata["port"],
			Username: metadata["username"],
			Password: metadata["password"],
		}
		sm.sessions[id] = session
	}

	return nil
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(id string) (*Session, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, found := sm.sessions[id]
	if !found {
		return nil, fmt.Errorf("session not found")
	}

	return session, nil
}

// AddSession adds a new session to the manager.
func (sm *SessionManager) AddSession(session *Session) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.sessions[session.ID] = session
}

// RemoveSession removes a session from the manager.
func (sm *SessionManager) RemoveSession(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	delete(sm.sessions, id)
}

// NewSession creates a new session, resuming if allowed.
func (sm *SessionManager) NewSession(username, sessionID string, conn *websocket.Conn) (*Session, error) {
	// Check if session already exists
	existingSession, err := sm.GetSession(sessionID)
	if err == nil {
		if sm.AllowResume {
			// Session found and resume is allowed
			sm.Logger.Println("Resuming session:", sessionID)
			existingSession.safeWriteMessage(websocket.TextMessage, []byte("Session resumed successfully\n"))
			return existingSession, nil
		} else {
			existingSession.Close()
		}
	}

	session := &Session{
		ID:      sessionID,
		Owner:   username,
		Conn:    conn,
		Logger:  sm.Logger,
		Manager: sm,
	}

	session.safeWriteMessage(websocket.TextMessage, []byte("Starting new session...\n"))

	// If session not found or resumption is not allowed, start a new session
	var cmd *exec.Cmd
	if sm.AllowResume {
		cmd, err = sm.TerminalMgr.LaunchTerminal(sessionID)
		if err != nil {

			return nil, err
		}
	} else {
		cmd = exec.Command("bash")
	}
	session.Cmd = cmd

	cmd.Env = append(cmd.Env, "TERM=xterm")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	session.Stdin = stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	session.Stdout = stdout

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	session.Stderr = stderr

	// Start the shell session
	err = cmd.Start()
	if err != nil {
		session.safeWriteMessage(websocket.TextMessage, []byte("Error starting shell session: "+err.Error()+"\n"))
		return nil, err
	}

	// Start WebSocket and SSH keep-alive mechanisms
	session.WG.Add(3)
	go func() {
		defer session.WG.Done()
		session.HandleWebSocketInput()
	}()
	go func() {
		defer session.WG.Done()
		session.HandleOutput()
	}()
	go func() {
		defer session.WG.Done()
		session.keepAliveWebSocket()
	}()

	// Add session to manager to keep track of it
	sm.AddSession(session)

	return session, nil
}

// HandleWebSocketInput manages input from the WebSocket client and writes it to stdin.
func (s *Session) HandleWebSocketInput() {
	for {
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			s.Logger.Println("Error reading from WebSocket:", err)
			break
		}

		// Replace carriage returns with newlines
		if bytes.Equal(msg, []byte("\r")) {
			msg = []byte("\n")
		}

		// Write the message to the shell's stdin
		_, err = s.Stdin.Write(msg)
		if err != nil {
			s.Logger.Println("Error writing to stdin:", err)
			break
		}
	}
}

// HandleOutput manages the shell's stdout and stderr and sends it to the WebSocket.
func (s *Session) HandleOutput() {
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, err := s.Stdout.Read(buffer)
			if err != nil && err != io.EOF {
				s.Logger.Println("Error reading from stdout:", err)
				break
			}
			if n > 0 {
				err := s.safeWriteMessage(websocket.TextMessage, buffer[:n])
				if err != nil {
					s.Logger.Println("Error sending output to WebSocket:", err)
					break
				}
			}
		}
	}()

	go func() {
		buffer := make([]byte, 1024)
		for {
			n, err := s.Stderr.Read(buffer)
			if err != nil && err != io.EOF {
				s.Logger.Println("Error reading from stderr:", err)
				break
			}
			if n > 0 {
				err := s.safeWriteMessage(websocket.TextMessage, buffer[:n])
				if err != nil {
					s.Logger.Println("Error sending stderr to WebSocket:", err)
					break
				}
			}
		}
	}()
}

// keepAliveWebSocket sends a ping to the WebSocket client to keep the connection alive.
func (s *Session) keepAliveWebSocket() {
	ticker := time.NewTicker(30 * time.Second) // Ping every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := s.safeWriteMessage(websocket.PingMessage, []byte{})
			if err != nil {
				s.Logger.Println("Error sending ping:", err)
				return
			}
		}
	}
}

// Close gracefully shuts down the session.
func (s *Session) Close() {
	s.Logger.Println("Closing session:", s.ID)

	// Close WebSocket connection
	s.Conn.Close()

	// Close stdin to signal the command to terminate
	s.Stdin.Close()

	// Kill the terminal process if still running
	if err := s.Cmd.Process.Kill(); err != nil {
		s.Logger.Println("Error killing process:", err)
	}

	// Wait for the process to fully exit
	s.Cmd.Wait()

	// Notify the SessionManager to remove this session
	s.Manager.RemoveSession(s.ID)
}

// safeWriteMessage ensures thread-safe writes to the WebSocket connection.
func (s *Session) safeWriteMessage(messageType int, data []byte) error {
	// add \r after \n for xterm compatibility
	n := len(data)
	if data[n-1] == '\n' {
		data = append(data, '\r')
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.Conn.WriteMessage(messageType, data)
}

// NewSSHSession creates a new SSH session and WebSocket connection, with resumption logic.
func (sm *SessionManager) NewSSHSession(username, sessionID string, conn *websocket.Conn, host, port, sshuser, sshpass, sshkey string) (*Session, error) {
	// Check if session already exists
	existingSession, err := sm.GetSession(sessionID)
	if err == nil && sm.AllowResume {
		// Session found and resume is allowed
		sm.Logger.Println("Resuming session:", sessionID)
		return existingSession, nil
	}

	// Create a new session if it doesn't exist
	session := &Session{
		ID:       sessionID,
		Owner:    username,
		Conn:     conn,
		Host:     host,
		Port:     port,
		Username: sshuser,
		Password: sshpass,
	}

	session.AppendKey(sshkey)

	client, err := session.connectSSH()
	if err != nil {
		return nil, err
	}
	session.SSHClient = client

	sshSession, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	session.SSHSession = sshSession

	stdin, err := sshSession.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := sshSession.StderrPipe()
	if err != nil {
		return nil, err
	}

	session.Stdin = stdin
	session.Stdout = stdout
	session.Stderr = stderr

	if err := sshSession.Start("/bin/bash"); err != nil {
		return nil, err
	}

	// Start WebSocket and SSH keep-alive mechanisms
	session.WG.Add(2)
	go session.keepAliveWebSocket()
	go session.keepAliveSSH()

	// Add session to manager to keep track of it
	sm.AddSession(session)

	// Handle WebSocket input and output as normal
	go session.HandleWebSocketInput()
	go session.HandleOutput()

	return session, nil
}

func (s *Session) AppendKey(keypath string) error {
	key, err := os.ReadFile(keypath)
	if err != nil {
		return err
	} else {
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return err
		}

		if s.Keys == nil {
			s.Keys = make([]ssh.Signer, 0)
		}

		s.Keys = append(s.Keys, signer)
	}

	return nil
}

// connectSSH establishes an SSH connection to the remote server.
func (s *Session) connectSSH() (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: s.Username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(s.Keys...),
			ssh.Password(s.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	addr := s.Host + ":" + s.Port
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, err
	}

	return client, nil
}

// keepAliveSSH sends keep-alive messages to the SSH server every 30 seconds.
func (s *Session) keepAliveSSH() {
	ticker := time.NewTicker(30 * time.Second) // Keep-alive every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_, err := s.SSHSession.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				s.Logger.Println("Error sending SSH keep-alive:", err)
				return
			}
		}
	}
}

// CloseSession stops the shell session and cleans up resources.
func (s *Session) CloseSession(manager *SessionManager) error {
	if err := s.SSHSession.Wait(); err != nil {
		s.Logger.Println("Error waiting for SSH session:", err)
	}

	if err := s.Conn.Close(); err != nil {
		s.Logger.Println("Error closing WebSocket connection:", err)
	}

	manager.RemoveSession(s.ID) // Remove session from the manager

	// Save session state periodically
	if err := manager.SaveState(); err != nil {
		s.Logger.Println("Error saving session state:", err)
	}

	s.WG.Wait()
	return s.SSHClient.Close()
}

// Shutdown gracefully stops all active sessions and cleans up resources.
func (sm *SessionManager) Shutdown() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Iterate over all active sessions and close them
	for _, session := range sm.sessions {
		if err := session.CloseSession(sm); err != nil {
			sm.Logger.Printf("Error closing session %s: %v", session.ID, err)
		} else {
			sm.Logger.Printf("Session %s closed successfully", session.ID)
		}
	}

	// Save the state of all sessions
	if err := sm.SaveState(); err != nil {
		sm.Logger.Printf("Error saving session state: %v", err)
	} else {
		sm.Logger.Println("Session state saved successfully")
	}
}
