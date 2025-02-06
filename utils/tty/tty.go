package tty

import (
	"io"
	"log"
	"os/exec"

	"github.com/gorilla/websocket"
)

type Session struct {
	ID     string          `json:"id"`
	Conn   *websocket.Conn `json:"-"`
	Cmd    *exec.Cmd       `json:"-"`
	Stdin  io.WriteCloser  `json:"-"`
	Stdout io.Reader       `json:"-"`
	Stderr io.Reader       `json:"-"`
}

type SessionMap map[string]*Session

// NewSession creates a new shell session.
func NewSession(id string, conn *websocket.Conn) (*Session, error) {
	cmd := exec.Command("bash") // Start a new bash shell
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
		ID:     id,
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

	return session, nil
}

// HandleWebSocketInput manages input from the WebSocket client and writes it to stdin.
func (s *Session) HandleWebSocketInput() {
	for {
		// Read WebSocket messages (input from the client)
		_, msg, err := s.Conn.ReadMessage()
		if err != nil {
			log.Println("Error reading from WebSocket:", err)
			break
		}
		// Write message to stdin
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
			// Read from stdout
			n, err := s.Stdout.Read(buffer)
			if err != nil && err != io.EOF {
				log.Println("Error reading from stdout:", err)
				break
			}
			if n > 0 {
				// Send the output back to the WebSocket
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
			// Read from stderr
			n, err := s.Stderr.Read(buffer)
			if err != nil && err != io.EOF {
				log.Println("Error reading from stderr:", err)
				break
			}
			if n > 0 {
				// Send the error output back to the WebSocket
				err := s.Conn.WriteMessage(websocket.TextMessage, buffer[:n])
				if err != nil {
					log.Println("Error sending stderr to WebSocket:", err)
					break
				}
			}
		}
	}()
}

// CloseSession stops the shell session and cleans up resources.
func (s *Session) CloseSession() error {
	err := s.Cmd.Wait() // Wait for the shell process to finish
	if err != nil {
		log.Println("Error waiting for command:", err)
	}
	return s.Conn.Close() // Close the WebSocket connection
}
