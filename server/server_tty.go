package server

import (
	"fmt"
	"path/filepath"
	"strconv"

	jwt "github.com/golang-jwt/jwt"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
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
	mycluster := node.ClusterGroup
	session.Orchestrator = mycluster.GetOrchestrator()
	session.ServiceName = mycluster.Name + "/svc/" + node.Name
	session.ServiceContainerName = "container#db"
	session.ServiceAgentName = node.Agent

	apiUser, ok := mycluster.APIUsers[session.Owner]
	if !ok {
		return fmt.Errorf("user %s not found in cluster %s", session.Owner, mycluster.Name)
	}

	switch session.CmdType {
	case tty.TerminalBash:
		session.Port = strconv.Itoa(mycluster.Conf.OnPremiseSSHPort)
		session.Username = mycluster.GetOnPremiseSSHUser()
		session.Password = mycluster.GetOnPremiseSSHPass()
		session.AppendKey(mycluster.OnPremiseGetSSHKey())
	case tty.TerminalMySQL, tty.TerminalMyTop:
		session.Port = node.Port
		if apiUser.Roles[config.RoleSysOps] {
			// SysOps can connect to the database using the root user
			session.Username = mycluster.GetDbUser()
			session.Password = mycluster.GetDbPass()
			session.Arguments = mycluster.GetMySQLClientParams(node, config.RoleSysOps, true)
		} else if apiUser.Roles[config.RoleSponsor] {
			// Sponsor can connect to the database using the sponsor user
			session.Username = mycluster.GetSponsorUser()
			session.Password = mycluster.GetSponsorPass()

			if session.Username == "" || session.Password == "" {
				err := node.CreateDBUserFromConfig("sponsor")
				if err != nil {
					return fmt.Errorf("unable to create sponsor user: %v", err)
				}
				session.Username = mycluster.GetSponsorUser()
				session.Password = mycluster.GetSponsorPass()
			}
			session.Arguments = mycluster.GetMySQLClientParams(node, config.RoleSponsor, true)
		} else if apiUser.Roles[config.RoleDBOps] || apiUser.Roles[config.RoleExtSysOps] || apiUser.Roles[config.RoleExtDBOps] || apiUser.Grants[config.GrantDBTerminal] {
			// External SysOps, DBOps and the user has the grant can connect to the database using the dba user
			session.Username = mycluster.GetDbaUser()
			session.Password = mycluster.GetDbaPass()

			if session.Username == "" || session.Password == "" {
				err := node.CreateDBUserFromConfig("dba")
				if err != nil {
					return fmt.Errorf("unable to create dba user: %v", err)
				}
				session.Username = mycluster.GetDbaUser()
				session.Password = mycluster.GetDbaPass()
			}

			session.Arguments = mycluster.GetMySQLClientParams(node, config.RoleDBOps, true)
		} else {
			return fmt.Errorf("user %s does not have the required roles or grant to connect to the database", session.Owner)
		}
	default:
		return fmt.Errorf("unsupported command type: %s", session.CmdType)
	}

	return nil
}

func (repman *ReplicationManager) SetSessionValuesFromProxy(session *tty.Session, proxy cluster.DatabaseProxy) error {

	session.Host = proxy.GetHost()
	mycluster := proxy.GetCluster()
	session.ServiceName = mycluster.Name + "/svc/" + proxy.GetName()
	session.Orchestrator = mycluster.GetOrchestrator()
	session.ServiceAgentName = proxy.GetAgent()

	session.ServiceContainerName = "container#prx"
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
