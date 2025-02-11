package server

import (
	"fmt"
	"path/filepath"

	jwt "github.com/golang-jwt/jwt"
	"github.com/signal18/replication-manager/utils/tty"
)

func (repman *ReplicationManager) InitWebTTY() {
	// Initialize the session manager
	var terminalManager tty.TerminalManager
	stateFile := filepath.Join(repman.Conf.WorkingDir, "tty.state.json")

	if repman.Conf.TerminalSessionManager == "tmux" {
		terminalManager = &tty.TmuxManager{}
	} else if repman.Conf.TerminalSessionManager == "screen" {
		terminalManager = &tty.ScreenManager{}
	}

	repman.SessionManager = tty.NewSessionManager(stateFile, repman.Conf.TerminalSessionResume, terminalManager, repman.Logrus)
}

// ParseJWT is a reusable function that parses a JWT token and returns the claims
func (repman *ReplicationManager) ParseWebSocketJWT(tokenString string) (map[string]string, error) {

	// Parse the JWT token
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Check the signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)

		return vk, nil
	})

	if err != nil {
		return nil, fmt.Errorf("error parsing JWT: %v", err)
	}

	// Check if the token is valid and return claims
	if token.Valid {
		return repman.GetUserInfoMap(token)
	} else {
		return nil, fmt.Errorf("invalid token")
	}
}
