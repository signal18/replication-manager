// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"net/http"
	"strings"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
)

func (repman *ReplicationManager) HasActiveCluster() bool {
	repman.Lock()
	defer repman.Unlock()
	for _, cl := range repman.Clusters {
		if cl.IsActive() {
			return true
		}
	}
	return false
}

// RepmanProvider interface implementation for MCPServer.

func (repman *ReplicationManager) GetClusters() map[string]*cluster.Cluster {
	repman.Lock()
	defer repman.Unlock()
	return repman.Clusters
}

func (repman *ReplicationManager) GetClusterByName(name string) *cluster.Cluster {
	return repman.getClusterByName(name)
}

func (repman *ReplicationManager) GetVersion() string {
	return repman.Version
}

func (repman *ReplicationManager) GetFullVersion() string {
	return repman.Fullversion
}

func (repman *ReplicationManager) GetStatus() string {
	return repman.Status
}

func (repman *ReplicationManager) GetConf() *config.Config {
	return repman.Conf
}

func (repman *ReplicationManager) SetClusterSetting(cl *cluster.Cluster, key, value string) error {
	return repman.setClusterSetting(cl, key, value)
}

func (repman *ReplicationManager) SwitchClusterSetting(cl *cluster.Cluster, key string) error {
	return repman.switchClusterSettings(cl, key)
}

func (repman *ReplicationManager) getClusterByName(clname string) *cluster.Cluster {
	var c *cluster.Cluster
	repman.Lock()
	c = repman.Clusters[clname]
	repman.Unlock()
	return c
}

func (repman *ReplicationManager) GetParentClusterFromReplicationSource(source string) *cluster.Cluster {
	repman.Lock()
	defer repman.Unlock()

	for _, c := range repman.Clusters {
		if c.Name == source {
			return c
		}
	}

	return nil
}

func (repman *ReplicationManager) GetDockerRepoPath(reponame string) string {
	for _, c := range repman.ServiceRepos {
		if c.Name == reponame {
			return c.Image
		}
	}

	return ""
}

func (repman *ReplicationManager) GetDockerRepoImage(reponame string, version string) string {
	for _, c := range repman.ServiceRepos {
		if c.Name == reponame {
			for _, v := range c.Tags.Results {
				if v.Name == version {
					return c.Image + ":" + v.Name
				}
			}
		}
	}

	return ""
}

// func (repman *ReplicationManager) GenerateKey(conf *config.Config) error {
// 	var err error
// 	_, err = os.Stat(conf.MonitoringKeyPath)
// 	// Check if the file does not exist
// 	if err == nil {
// 		repman.Logrus.Infof("Repman discovered that key is already generated. Using existing key.")
// 		return nil
// 	} else {
// 		if !os.IsNotExist(err) {
// 			repman.Logrus.Infof("Error when checking key for encryption: %v", err)
// 			return err
// 		}

// 		newdir := "/home/repman/.config/replication-manager/etc"
// 		if conf.WithEmbed == "ON" {
// 			newdir = repman.OsUser.HomeDir + "/.config/replication-manager/etc"
// 		}

// 		newpath := newdir + "/.replication-manager.key"

// 		_, err = os.Stat(newpath)
// 		if err == nil {
// 			Logger.Infof("Repman discovered key in alternative path. Using existing key on %s", newpath)
// 			return nil
// 		}

// 		Logger.Infof("Key not found. Generating : %s", conf.MonitoringKeyPath)

// 		if err = misc.TryOpenFile(conf.MonitoringKeyPath, os.O_WRONLY|os.O_CREATE, 0600, true); err != nil && conf.WithEmbed == "OFF" {
// 			newdir := "/home/repman/.config/replication-manager/etc"
// 			newpath := newdir + "/.replication-manager.key"

// 			Logger.Infof("File %s is not accessible. Try using alternative path: %s", conf.MonitoringKeyPath, newpath)

// 			_, err := os.Stat(newpath)
// 			if err == nil {
// 				Logger.Infof("Repman discovered key in alternative path. Using existing key on %s", newpath)
// 				return nil
// 			}

// 			_, err = os.Stat(newdir)
// 			if err != nil {
// 				if !os.IsNotExist(err) {
// 					Logger.Errorf("Can't access %s : %v", newdir, err)
// 					return err
// 				} else {
// 					err = os.MkdirAll(newdir, 0755)
// 					if err != nil {
// 						Logger.Errorf("Can't create directory %s : %v", newdir, err)
// 						return err
// 					}
// 				}
// 			}

// 			if err := misc.TryOpenFile(newpath, os.O_WRONLY|os.O_CREATE, 0600, true); err != nil {
// 				Logger.Errorf("Can't write keys in %s : %v", newdir, err)
// 				return err
// 			}

// 			// New path is writable
// 			conf.MonitoringKeyPath = newpath
// 			Logger.Infof("Path writable. Flag 'monitoring-key-path' set to: %s.", newpath)
// 			Logger.Infof("Generating key on: %s", conf.MonitoringKeyPath)

// 		}

// 		p := crypto.Password{}
// 		var err error
// 		p.Key, err = crypto.Keygen()
// 		if err != nil {
// 			Logger.Errorf("Error when generating key for encryption: %v", err)
// 			return err
// 		}
// 		err = crypto.WriteKey(p.Key, conf.MonitoringKeyPath, false)
// 		if err != nil {
// 			Logger.Errorf("Error when writing key for encryption: %v", err)
// 			return err
// 		}
// 	}

// 	return nil
// }

// GetName returns the name for cluster configuration
// This is used to identify the default cluster in the configuration
func (repman *ReplicationManager) GetName() string {
	return "default"
}

// AuthenticateMCP resolves the bearer of an MCP request into a principal
// (issue #1838): a user-issued API token (#1835) or an interactive login JWT,
// the same two credentials the REST API accepts. The password claim of a login
// JWT and the token record travel in Principal.Auth so AuthorizeMCP can re-run
// the cluster ACL exactly as IsValidClusterACL does.
func (repman *ReplicationManager) AuthenticateMCP(r *http.Request) (*repmanmcp.Principal, error) {
	if t, isAPIToken, ok := repman.apiTokenFromRequest(r); ok {
		return &repmanmcp.Principal{User: t.User, AuthMethod: "token", TokenID: t.ID, TokenLabel: t.Label, Remote: r.RemoteAddr, Auth: t}, nil
	} else if isAPIToken {
		return nil, fmt.Errorf("API token invalid, revoked, expired or disabled")
	}
	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("unexpected JWT claims")
	}
	info, ok := claims["CustomUserInfo"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("JWT carries no user")
	}
	user, _ := info["Name"].(string)
	password, _ := info["Password"].(string)
	method := "password"
	if profile, ok := info["profile"].(string); ok && strings.Contains(profile, repman.Conf.OAuthProvider) {
		user, _ = info["email"].(string)
		method = "oidc"
	}
	if user == "" {
		return nil, fmt.Errorf("JWT carries no user name")
	}
	return &repmanmcp.Principal{User: user, AuthMethod: method, Remote: r.RemoteAddr, Auth: password}, nil
}

// AuthorizeMCP runs the cluster ACL for a principal on the REST URL an MCP tool
// mirrors. For an API token the scope and the narrowed grants apply, as on REST.
func (repman *ReplicationManager) AuthorizeMCP(p *repmanmcp.Principal, clusterName string, url string) bool {
	if p == nil {
		return false
	}
	cl := repman.getClusterByName(clusterName)
	if cl == nil {
		return false
	}
	ok := false
	switch p.AuthMethod {
	case "token":
		t, isToken := p.Auth.(*APIToken)
		if !isToken || t == nil {
			return false
		}
		if tokenURLInScope(t, cl, url) {
			ok = cl.IsValidACLQuiet(repman.tokenPrincipalFor(t, cl), "", url, "token")
		}
	default:
		password, _ := p.Auth.(string)
		ok = cl.IsValidACLQuiet(p.User, password, url, p.AuthMethod)
	}
	if !ok {
		repman.logSecurityEvent("mcp_denied", p.User, p.Remote, fmt.Sprintf("MCP %s denied on %s", p.String(), url))
	}
	return ok
}

// LogSecurityEvent exposes the security log to the MCP package.
func (repman *ReplicationManager) LogSecurityEvent(event, user, remoteAddr, msg string) {
	repman.logSecurityEvent(event, user, remoteAddr, msg)
}
