package tty

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

type TerminalCommandType int

func (t TerminalCommandType) String() string {
	return [...]string{"bash", "mysql", "mytop"}[t]
}

func GetTerminalCommandType(str string) (TerminalCommandType, error) {
	str = strings.ToLower(str)
	switch str {
	case "", "bash":
		return TerminalBash, nil
	case "mysql":
		return TerminalMySQL, nil
	case "mytop":
		return TerminalMyTop, nil
	}
	return TerminalBash, errors.New("Invalid terminal command type")
}

const (
	TerminalBash TerminalCommandType = iota
	TerminalMySQL
	TerminalMyTop
)

// TerminalManager interface defines the methods for terminal management.
type TerminalManager interface {
	LaunchTerminal(sessionID string) (*exec.Cmd, error)
	LaunchSSHTerminal(sessionID string) []byte
}

// TmuxManager handles tmux-based terminal sessions.
type TmuxManager struct{}

func (tm *TmuxManager) LaunchTerminal(sessionID string) (*exec.Cmd, error) {
	cmd := exec.Command("tmux", "new-session", "-A", "-s", sessionID)
	return cmd, nil
}

func (tm *TmuxManager) LaunchSSHTerminal(sessionID string) []byte {
	return []byte("tmux new-session -A -s " + sessionID + "\n")
}

// ScreenManager handles screen-based terminal sessions.
type ScreenManager struct{}

func (sm *ScreenManager) LaunchTerminal(sessionID string) (*exec.Cmd, error) {
	cmd := exec.Command("screen", "-D", "-RR", "-S", sessionID)
	return cmd, nil
}

func (tm *ScreenManager) LaunchSSHTerminal(sessionID string) []byte {
	return []byte("screen -D -RR -S " + sessionID + "\n")
}

// Session represents a single terminal session (SSH or local shell).
type Session struct {
	ID          string              `json:"id"`
	Owner       string              `json:"owner"`
	WorkingDir  string              `json:"working_dir"`
	AllowResume bool                `json:"-"`
	TerminalMgr TerminalManager     `json:"-"`
	Conn        *websocket.Conn     `json:"-"`
	SSHClient   *ssh.Client         `json:"-"`
	SSHSession  *ssh.Session        `json:"-"`
	Cmd         *exec.Cmd           `json:"-"`
	CmdType     TerminalCommandType `json:"-"`
	PTY         *os.File            `json:"-"`
	Stdin       io.WriteCloser      `json:"-"`
	Stdout      io.Reader           `json:"-"`
	Stderr      io.Reader           `json:"-"`
	Host        string              `json:"host"`
	Port        string              `json:"port"`
	Username    string              `json:"-"`
	Password    string              `json:"-"`
	Keys        []ssh.Signer        `json:"-"`
	WG          sync.WaitGroup      `json:"-"`
	Logger      *logrus.Logger      `json:"-"`
	Manager     *SessionManager     `json:"-"`
	writeMu     sync.Mutex          `json:"-"`
	Line        string              `json:"-"`
	closeOnce   sync.Once           `json:"-"`
}

type SignerList []ssh.Signer

// SessionManager handles session persistence and resumption.
type SessionManager struct {
	mu        sync.Mutex
	sessions  map[string]*Session
	stateFile string
	Logger    *logrus.Logger
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager(stateFile string, logger *logrus.Logger) *SessionManager {
	manager := &SessionManager{
		sessions:  make(map[string]*Session),
		stateFile: stateFile,
		Logger:    logger,
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
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(sm.sessions); err != nil {
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
			ID:      id,
			Manager: sm,
			Logger:  sm.Logger,
			Owner:   metadata["owner"],
			Host:    metadata["host"],
			Port:    metadata["port"],
		}
		sm.sessions[id] = session
	}

	return nil
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(id string) (*Session, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, found := sm.sessions[id]
	if !found {
		return nil, false
	}

	return session, true
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

func (sm *SessionManager) CreateSession(conn *websocket.Conn) *Session {
	return &Session{
		Manager: sm,
		Logger:  sm.Logger,
		Conn:    conn,
	}
}

// NewSession creates a new session, resuming if allowed.
func (sm *SessionManager) RunSession(session *Session) (*Session, error) {
	var err error

	oldSession, found := sm.GetSession(session.ID)
	if found {
		// Close the old session if it exists
		oldSession.Close()
	}

	session.SafeWriteMessage(websocket.TextMessage, []byte("Starting new session...\n"))

	var cmd *exec.Cmd
	if session.CmdType == TerminalMySQL {
		cmd = exec.Command("mysql", "-h", session.Host, "-P", session.Port, "-u", session.Username, "-p")
	} else if session.CmdType == TerminalMyTop {
		cmd = exec.Command("mytop", "-h", session.Host, "-P", session.Port, "-u", session.Username, "--prompt")
	} else {
		if session.AllowResume && session.TerminalMgr != nil {
			cmd, err = session.TerminalMgr.LaunchTerminal(session.ID)
			if err != nil {
				return nil, err
			}
		} else {
			cmd = exec.Command("bash")
		}
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	session.Cmd = cmd

	// Start a pseudo-terminal for the command
	ptyFile, err := pty.Start(cmd)
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error starting shell session: "+err.Error()+"\n"))
		return nil, err
	}
	session.PTY = ptyFile
	session.Stdin = ptyFile
	session.Stdout = ptyFile

	if session.CmdType == TerminalMySQL {
		for {
			buf := make([]byte, 1)
			_, err := session.Stdout.Read(buf)
			if err != nil {
				break
			}

			if buf[0] == ':' {
				break
			}
		}
		session.Stdin.Write([]byte(session.Password + "\n"))
	} else if session.CmdType == TerminalMyTop {
		session.Stdin.Write([]byte(session.Password + "\n"))
	} else {
		session.Stdin.Write([]byte("cd " + session.WorkingDir + "\n"))
	}

	pty.Setsize(ptyFile, &pty.Winsize{Cols: 120, Rows: 25})

	// Add session to manager to keep track of it
	sm.AddSession(session)

	// Start WebSocket and SSH keep-alive mechanisms
	session.WG.Add(4)
	go func() {
		defer session.WG.Done()
		session.HandleWebSocketInput()
	}()
	go func() {
		defer session.WG.Done()
		session.HandleOutput(false)
	}()
	go func() {
		defer session.WG.Done()
		session.keepAliveWebSocket()
	}()
	go func() {
		defer session.WG.Done()
		err := cmd.Wait()
		if err != nil {
			session.SafeWriteMessage(websocket.TextMessage, []byte("Session exited with error: "+err.Error()+"\n"))
		} else {
			session.SafeWriteMessage(websocket.TextMessage, []byte("Session exited successfully.\n"))
		}

		// Clean up session after the process exits
		sm.RemoveSession(session.ID)
		session.Close()
	}()

	return session, nil
}

type ResizeMessage struct {
	Type_ string `json:"type"`
	Rows  int    `json:"rows"`
	Cols  int    `json:"cols"`
}

type AuthToken struct {
	Type_ string `json:"type"`
	Token string `json:"token"`
}

func (s *Session) HandleWebToken(ctx context.Context) string {
	for {
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			s.Logger.Println("Error reading from WebSocket:", err)
			break
		}

		var token AuthToken
		if err := json.Unmarshal(msg, &token); err == nil {
			if token.Type_ == "auth" {
				return token.Token
			}
		}

		// Check if the context has been cancelled or timed out
		select {
		case <-ctx.Done():
			return ""
		default:
		}
	}

	return ""
}

// HandleWebSocketInput manages input from the WebSocket client and writes it to stdin.
func (s *Session) HandleWebSocketInput() {
	for {
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			s.Logger.Println("Error reading from WebSocket:", err)
			break
		}

		var resizeMessage ResizeMessage
		if err := json.Unmarshal(msg, &resizeMessage); err == nil {
			if resizeMessage.Type_ == "resize" {
				if s.SSHClient == nil {
					if err := pty.Setsize(s.PTY, &pty.Winsize{Cols: uint16(resizeMessage.Cols), Rows: uint16(resizeMessage.Rows)}); err != nil {
						s.Logger.Println("Error resizing pty:", err)
					}
				} else {
					if err := s.SSHSession.WindowChange(resizeMessage.Rows, resizeMessage.Cols); err != nil {
						s.Logger.Println("Error resizing SSH session:", err)
					}
				}
			}
			continue
		}

		s.Stdin.Write(msg)
		// err = s.SafeWriteMessage(websocket.TextMessage, msg)
		// if err != nil {
		// 	s.Logger.Println("Error sending output to WebSocket:", err)
		// 	break
		// }
	}
}

// HandleOutput manages the shell's stdout and stderr and sends it to the WebSocket.
func (s *Session) HandleOutput(stderr bool) {
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, err := s.Stdout.Read(buffer)
			if err != nil && err != io.EOF {
				s.Logger.Println("Error reading from stdout:", err)
				break
			}
			if n > 0 {
				msgtype := websocket.TextMessage
				// Check if the data is valid UTF-8
				if !utf8.Valid(buffer[:n]) {
					msgtype = websocket.BinaryMessage
				}

				err := s.SafeWriteMessage(msgtype, buffer[:n])
				if err != nil {
					s.Logger.Println("Error sending stdout to WebSocket:", err)
					break
				}
			}
		}
	}()

	if stderr {
		go func() {
			buffer := make([]byte, 1024)
			for {
				n, err := s.Stderr.Read(buffer)
				if err != nil && err != io.EOF {
					s.Logger.Println("Error reading from stderr:", err)
					break
				}
				if n > 0 {
					msgtype := websocket.TextMessage
					// Check if the data is valid UTF-8
					if !utf8.Valid(buffer[:n]) {
						msgtype = websocket.BinaryMessage
					}

					err := s.SafeWriteMessage(msgtype, buffer[:n])
					if err != nil {
						s.Logger.Println("Error sending stderr to WebSocket:", err)
						break
					}
				}
			}
		}()
	}
}

// keepAliveWebSocket sends a ping to the WebSocket client to keep the connection alive.
func (s *Session) keepAliveWebSocket() {
	ticker := time.NewTicker(30 * time.Second) // Ping every 30 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := s.SafeWriteMessage(websocket.PingMessage, []byte{})
			if err != nil {
				s.Logger.Println("Error sending ping:", err)
				return
			}
		}
	}
}

// Close gracefully shuts down the session.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.Logger.Println("Closing session:", s.ID)

		// Close WebSocket connection safely
		if s.Conn != nil {
			if err := s.Conn.Close(); err != nil && !websocket.IsCloseError(err) {
				s.Logger.Println("Error closing WebSocket:", err)
			}
		}

		// Close stdin safely
		if s.Stdin != nil {
			if err := s.Stdin.Close(); err != nil {
				s.Logger.Println("Error closing stdin:", err)
			}
		}

		// Kill the process if it's still running
		if s.Cmd != nil && s.Cmd.Process != nil {
			if err := s.Cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				s.Logger.Println("Error killing process:", err)
			}
		}

		// Wait for process to fully exit without blocking indefinitely
		done := make(chan error, 1)
		go func() {
			if s.Cmd != nil {
				done <- s.Cmd.Wait()
			}
		}()

		select {
		case err := <-done:
			if err != nil && !errors.Is(err, os.ErrProcessDone) {
				s.Logger.Println("Error waiting for process to exit:", err)
			}
		case <-time.After(5 * time.Second):
			s.Logger.Println("Timeout waiting for process to exit")
		}

		// Notify SessionManager to remove this session
		if s.Manager != nil {
			s.Manager.RemoveSession(s.ID)
		}
	})
}

// SafeWriteMessage ensures thread-safe writes to the WebSocket connection.
func (s *Session) SafeWriteMessage(messageType int, data []byte) error {
	// read data and add carriage return each time a newline is encountered
	// this is to ensure that the terminal displays the output correctly

	if messageType == websocket.TextMessage {
		data = bytes.ReplaceAll(data, []byte("\n"), []byte("\n\r"))
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.Conn.WriteMessage(messageType, data)
}

// NewSSHSession creates a new SSH session and WebSocket connection, with resumption logic.
func (sm *SessionManager) RunSSHSession(session *Session) (*Session, error) {
	var err error

	oldSession, found := sm.GetSession(session.ID)
	if found {
		// Close the old session if it exists
		oldSession.Close()
	}

	session.SafeWriteMessage(websocket.TextMessage, []byte("Starting SSH session...\n"))

	client, err := session.connectSSH()
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error connecting to SSH server: "+err.Error()+"\n"))
		return nil, err
	}
	session.SSHClient = client

	sshSession, err := client.NewSession()
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error creating SSH session: "+err.Error()+"\n"))
		return nil, err
	}
	session.SSHSession = sshSession

	stdin, err := sshSession.StdinPipe()
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error getting SSH stdin: "+err.Error()+"\n"))
		return nil, err
	}
	session.Stdin = stdin

	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error getting SSH stdout: "+err.Error()+"\n"))
		return nil, err
	}
	session.Stdout = stdout

	stderr, err := sshSession.StderrPipe()
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error getting SSH stderr: "+err.Error()+"\n"))
		return nil, err
	}
	session.Stderr = stderr

	err = sshSession.RequestPty("xterm-256color", 25, 120, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	})
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error requesting PTY: "+err.Error()+"\n"))
		return nil, err
	}

	if err := sshSession.Shell(); err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Error starting shell: "+err.Error()+"\n"))
		return nil, err
	}

	// Send the terminal command to the SSH session
	if session.AllowResume && session.TerminalMgr != nil {
		session.Stdin.Write(session.TerminalMgr.LaunchSSHTerminal(session.ID))
	}

	// Add session to manager to keep track of it
	sm.AddSession(session)

	// Start WebSocket and SSH keep-alive mechanisms
	session.WG.Add(4)
	go func() {
		defer session.WG.Done()
		session.keepAliveSSH()
	}()

	go func() {
		defer session.WG.Done()
		session.HandleWebSocketInput()
	}()
	go func() {
		defer session.WG.Done()
		session.HandleOutput(true)
	}()
	go func() {
		defer session.WG.Done()
		session.keepAliveWebSocket()
	}()

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
