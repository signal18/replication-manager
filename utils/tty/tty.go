package tty

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

// TerminalManager interface defines the methods for terminal management.
type TerminalManager interface {
	LaunchTerminal(session *Session) (*exec.Cmd, error)
}

// TmuxManager handles tmux-based terminal sessions.
type TmuxManager struct{}

func (tm *TmuxManager) LaunchTerminal(session *Session) (*exec.Cmd, error) {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", session.ID, "bash")
	return cmd, nil
}

// ScreenManager handles screen-based terminal sessions.
type ScreenManager struct{}

func (sm *ScreenManager) LaunchTerminal(session *Session) (*exec.Cmd, error) {
	cmd := exec.Command("screen", "-dmS", session.ID, "bash")
	return cmd, nil
}

// Session represents a single terminal session (SSH or local shell).
type Session struct {
	ID         string          `json:"id"`
	Conn       *websocket.Conn `json:"-"`
	SSHClient  *ssh.Client     `json:"-"`
	SSHSession *ssh.Session    `json:"-"`
	Cmd        *exec.Cmd       `json:"-"`
	Stdin      io.WriteCloser  `json:"-"`
	Stdout     io.Reader       `json:"-"`
	Stderr     io.Reader       `json:"-"`
	Host       string          `json:"host"`
	Port       string          `json:"port"`
	Username   string          `json:"username"`
	Password   string          `json:"password"`
	WG         sync.WaitGroup  `json:"-"`
}

// SessionManager handles session persistence and resumption.
type SessionManager struct {
	mu          sync.Mutex
	sessions    map[string]*Session
	stateFile   string
	AllowResume bool
	TerminalMgr TerminalManager
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(stateFile string, allowResume bool, terminalMgr TerminalManager) *SessionManager {
	if terminalMgr == nil {
		allowResume = false
	}

	manager := &SessionManager{
		sessions:    make(map[string]*Session),
		stateFile:   stateFile,
		AllowResume: allowResume,
		TerminalMgr: terminalMgr,
	}

	// Load sessions from the state file on initialization
	manager.loadState()

	return manager
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
func (sm *SessionManager) NewSession(scope, id string, conn *websocket.Conn) (*Session, error) {
	finalID := fmt.Sprintf("%s-%s", scope, id)

	// Check if session already exists
	existingSession, err := sm.GetSession(finalID)
	if err == nil && sm.AllowResume {
		// Session found and resume is allowed
		log.Println("Resuming session:", finalID)
		return existingSession, nil
	}

	// If session not found or resumption is not allowed, start a new session
	var cmd *exec.Cmd
	if sm.AllowResume {
		cmd, err = sm.TerminalMgr.LaunchTerminal(nil)
	} else {
		cmd = exec.Command("bash")
	}

	if err != nil {
		return nil, err
	}

	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	session := &Session{
		ID:     finalID,
		Conn:   conn,
		Cmd:    cmd,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}

	// Start the shell session
	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	// Start WebSocket and SSH keep-alive mechanisms
	session.WG.Add(1)
	go session.keepAliveWebSocket()

	// Add session to manager to keep track of it
	sm.AddSession(session)

	return session, nil
}

// NewSSHSession creates a new SSH session and WebSocket connection, with resumption logic.
func (sm *SessionManager) NewSSHSession(id string, conn *websocket.Conn, host, port, username, password string) (*Session, error) {
	// Check if session already exists
	existingSession, err := sm.GetSession(id)
	if err == nil && sm.AllowResume {
		// Session found and resume is allowed
		log.Println("Resuming session:", id)
		return existingSession, nil
	}

	// Create a new session if it doesn't exist
	session := &Session{
		ID:       id,
		Conn:     conn,
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
	}

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

	return session, nil
}

// connectSSH establishes an SSH connection to the remote server.
func (s *Session) connectSSH() (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: s.Username,
		Auth: []ssh.AuthMethod{
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

// HandleWebSocketInput manages input from the WebSocket client and writes it to stdin.
func (s *Session) HandleWebSocketInput() {
	for {
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			log.Println("Error reading from WebSocket:", err)
			break
		}
		_, err = s.Stdin.Write(msg)
		if err != nil {
			log.Println("Error writing to stdin:", err)
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
				log.Println("Error reading from stdout:", err)
				break
			}
			if n > 0 {
				err := s.Conn.WriteMessage(websocket.TextMessage, buffer[:n])
				if err != nil {
					log.Println("Error sending output to WebSocket:", err)
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
				log.Println("Error reading from stderr:", err)
				break
			}
			if n > 0 {
				err := s.Conn.WriteMessage(websocket.TextMessage, buffer[:n])
				if err != nil {
					log.Println("Error sending stderr to WebSocket:", err)
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
			err := s.Conn.WriteMessage(websocket.PingMessage, []byte{})
			if err != nil {
				log.Println("Error sending ping:", err)
				return
			}
		}
	}
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
				log.Println("Error sending SSH keep-alive:", err)
				return
			}
		}
	}
}

// CloseSession stops the shell session and cleans up resources.
func (s *Session) CloseSession(manager *SessionManager) error {
	if err := s.SSHSession.Wait(); err != nil {
		log.Println("Error waiting for SSH session:", err)
	}

	if err := s.Conn.Close(); err != nil {
		log.Println("Error closing WebSocket connection:", err)
	}

	manager.RemoveSession(s.ID) // Remove session from the manager

	// Save session state periodically
	if err := manager.SaveState(); err != nil {
		log.Println("Error saving session state:", err)
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
			log.Printf("Error closing session %s: %v", session.ID, err)
		} else {
			log.Printf("Session %s closed successfully", session.ID)
		}
	}

	// Save the state of all sessions
	if err := sm.SaveState(); err != nil {
		log.Printf("Error saving session state: %v", err)
	} else {
		log.Println("Session state saved successfully")
	}
}
