package server

import (
	"fmt"
	"path/filepath"

	jwt "github.com/golang-jwt/jwt"
	"github.com/signal18/replication-manager/utils/tty"
)

func (repman *ReplicationManager) InitWebTTY() {
	// Initialize the session manager
	stateFile := filepath.Join(repman.Conf.WorkingDir, "tty.state.json")
	repman.SessionManager = tty.NewSessionManager(stateFile, repman.Logrus)
}

func (repman *ReplicationManager) GetTerminalManager() tty.TerminalManager {
	var terminalMgr tty.TerminalManager
	if repman.Conf.TerminalSessionManager == "tmux" {
		terminalMgr = &tty.TmuxManager{}
	} else if repman.Conf.TerminalSessionManager == "screen" {
		terminalMgr = &tty.ScreenManager{}
	}

	return terminalMgr
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
