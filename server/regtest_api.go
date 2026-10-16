// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// Hooks the regtest package uses to call repman's own API, so a scenario
// exercises the real routes, token check, ACL and feature switches instead of
// calling handlers directly. Wired in RunAllTests.

// regtestAPIToken issues a token as /api/login does for local auth, for the
// first API user of the cluster (by name) holding every grant asked for.
func (repman *ReplicationManager) regtestAPIToken(cl *cluster.Cluster, grants ...string) (string, error) {
	var names []string
	for name := range cl.APIUsers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		u, ok := cl.GetACLUser(name)
		if !ok || u.IsExternal || u.Password == "" {
			continue
		}
		hasAll := true
		for _, g := range grants {
			if !u.Grants[g] {
				hasAll = false
				break
			}
		}
		if !hasAll {
			continue
		}
		return repman.issueJWT(struct {
			Name     string
			Role     string
			Password string
		}{u.User, "Member", repman.Conf.GetEncryptedString(u.Password)}, "")
	}
	return "", errors.New("no local API user holds the grants the test needs")
}

// regtestAPIAddress is the host:port the regtest reaches the API on: the bind
// address, or the loopback when the API listens on every interface.
func (repman *ReplicationManager) regtestAPIAddress() string {
	host := repman.Conf.APIBind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, repman.Conf.APIPort)
}

// regtestAPIRequest sends one request to repman's own API with the token and
// returns the status, headers and body. The API's certificate is often the
// generated self-signed one, and the call never leaves this host, so it is
// not verified.
func (repman *ReplicationManager) regtestAPIRequest(token, method, path string) (int, http.Header, []byte, error) {
	return repman.regtestAPIRequestBody(token, method, path, nil)
}

// regtestAPIRequestBody is regtestAPIRequest with a JSON body (nil for none).
func (repman *ReplicationManager) regtestAPIRequestBody(token, method, path string, payload []byte) (int, http.Header, []byte, error) {
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, "https://"+repman.regtestAPIAddress()+path, reqBody)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, resp.Header, body, err
}

// regtestLoadClusterConfigFromDisk returns the config a start would build for clusterName
// from the files on disk (ReconstructLiveClusterConfig: config.toml, the include directory,
// default.toml and <cluster>/<cluster>.toml, layered as InitConfig does), with its config
// maps. Reading leaves the running server as it was: GetClusterConfig replaces the server's
// maps of the cluster, they are put back.
func (repman *ReplicationManager) regtestLoadClusterConfigFromDisk(clusterName string) (config.Config, error) {
	repman.Lock()
	imm, dyn, ver := repman.ImmuableFlagMaps[clusterName], repman.DynamicFlagMaps[clusterName], repman.VersionConfs[clusterName]
	repman.Unlock()
	conf, err := repman.ReconstructLiveClusterConfig(clusterName)
	repman.Lock()
	conf.ImmuableFlagMap = repman.ImmuableFlagMaps[clusterName]
	conf.DynamicFlagMap = repman.DynamicFlagMaps[clusterName]
	conf.DefaultFlagMap = repman.DefaultFlagMap
	repman.ImmuableFlagMaps[clusterName], repman.DynamicFlagMaps[clusterName], repman.VersionConfs[clusterName] = imm, dyn, ver
	repman.Unlock()
	return conf, err
}
