package server

import (
	"fmt"
	"path/filepath"
	"strconv"

	jwt "github.com/golang-jwt/jwt"
	"github.com/signal18/replication-manager/cluster"
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

func (repman *ReplicationManager) SetSessionValuesFromNode(session *tty.Session, node *cluster.ServerMonitor) error {
	session.Host = node.Host
	switch session.CmdType {
	case tty.TerminalBash:
		session.Port = strconv.Itoa(node.ClusterGroup.Conf.OnPremiseSSHPort)
		session.Username = node.ClusterGroup.GetOnPremiseSSHUser()
		session.Password = node.ClusterGroup.GetOnPremiseSSHPass()
		session.AppendKey(node.ClusterGroup.OnPremiseGetSSHKey())
	case tty.TerminalMySQL, tty.TerminalMyTop:
		session.Port = node.Port
		session.Username = node.User
		session.Password = node.Pass
	default:
		return fmt.Errorf("unsupported command type: %s", session.CmdType)
	}

	return nil
}

func (repman *ReplicationManager) SetSessionValuesFromProxy(session *tty.Session, proxy cluster.DatabaseProxy) error {
	session.Host = proxy.GetHost()
	switch session.CmdType {
	case tty.TerminalBash:
		session.Port = strconv.Itoa(proxy.GetCluster().Conf.OnPremiseSSHPort)
		session.Username = proxy.GetCluster().GetOnPremiseSSHUser()
		session.Password = proxy.GetCluster().GetOnPremiseSSHPass()
		session.AppendKey(proxy.GetCluster().OnPremiseGetSSHKey())
	case tty.TerminalMySQL, tty.TerminalMyTop:
		session.Port = proxy.GetPort()
		session.Username = proxy.GetUser()
		session.Password = proxy.GetPass()
	default:
		return fmt.Errorf("unsupported command type: %s", session.CmdType)
	}
	return nil
}
