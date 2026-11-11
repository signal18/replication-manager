// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/signal18/replication-manager/config"
)

// InstanceName is the instance's name in the shared credential stores and config backups
// (CREDENTIAL_STORES.md "Two levels"): <domain>/<subDomain>/<zone>, zone being the location. Registered, it is the
// Cloud18 registration and cannot be changed here.
type InstanceName struct {
	Registered bool   `json:"registered"`
	Domain     string `json:"domain"`
	SubDomain  string `json:"subDomain"`
	Zone       string `json:"zone"`
	// Name is the full name, empty while the company is not entered
	Name string `json:"name"`
}

// InstanceNameForm is the own name of an unregistered instance, as entered in the GUI.
type InstanceNameForm struct {
	Domain    string `json:"domain"`
	SubDomain string `json:"subDomain"`
	Zone      string `json:"zone"`
}

func (repman *ReplicationManager) instanceName() InstanceName {
	c := repman.Conf
	n := InstanceName{Domain: c.InstanceDomain, SubDomain: c.InstanceSubDomain, Zone: c.InstanceSubDomainZone}
	if c.Cloud18 && c.Cloud18Domain != "" && c.Cloud18SubDomain != "" && c.Cloud18SubDomainZone != "" {
		n = InstanceName{Registered: true, Domain: c.Cloud18Domain, SubDomain: c.Cloud18SubDomain, Zone: c.Cloud18SubDomainZone}
	}
	n.Name, _ = c.InstanceStoreName()
	return n
}

// instanceNameACL: the caller holds the server credentials grant on every cluster of the
// instance. The name is the instance's, so an extsysops of one cluster of a shared instance
// does not change it.
func (repman *ReplicationManager) instanceNameACL(w http.ResponseWriter, r *http.Request) (string, bool) {
	clusters := repman.clustersSnapshot()
	if len(clusters) == 0 {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return "", false
	}
	user := ""
	for _, cl := range clusters {
		valid, u := repman.IsValidClusterACL(r, cl)
		if !valid {
			http.Error(w, "No valid ACL: the server credentials grant is needed on every cluster", http.StatusForbidden)
			return "", false
		}
		user = u
	}
	return user, true
}

// errInstanceNameRegistered refuses a change of a registered instance's name.
var errInstanceNameRegistered = errors.New("this replication-manager is registered with Cloud18: its name is its registration")

// setInstanceName validates and sets the own name of an unregistered instance.
func (repman *ReplicationManager) setInstanceName(f InstanceNameForm) error {
	if repman.instanceName().Registered {
		return errInstanceNameRegistered
	}
	for _, part := range []string{f.Domain, f.SubDomain, f.Zone} {
		if err := config.ValidInstanceNamePart(part); err != nil {
			return err
		}
	}
	repman.Conf.InstanceDomain, repman.Conf.InstanceSubDomain, repman.Conf.InstanceSubDomainZone = f.Domain, f.SubDomain, f.Zone
	repman.Save()
	return nil
}

// handlerMuxInstanceName godoc
// @Summary The name of this replication-manager in the shared stores
// @Description <domain>/<subDomain>/<zone>: the unique name of this replication-manager in a shared credential store (Vault, OpenSVC) and a config backup (GitLab). Registered with Cloud18, it is the registration. Otherwise the company, the hostname (prefilled at the first start) and the location (an office, a country), entered by a sysops or extsysops. name is empty while the name is not complete.
// @Tags GlobalSetting
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} InstanceName
// @Failure 403 {string} string "No valid ACL"
// @Router /api/clusters/settings/actions/instance-name [get]
func (repman *ReplicationManager) handlerMuxInstanceName(w http.ResponseWriter, r *http.Request) {
	if _, ok := repman.instanceNameACL(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(repman.instanceName())
}

// handlerMuxSetInstanceName godoc
// @Summary Set the name of this replication-manager in the shared stores
// @Description Sets the company, hostname and location (zone) of an unregistered replication-manager (each 1 to 63 letters, digits, '-', '_' or '.'). Refused on a registered one (409) and on a standby (409: change it on the active instance, the standby takes it from the config push).
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param body body InstanceNameForm true "The name"
// @Success 200 {object} InstanceName
// @Failure 400 {string} string "Invalid name"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "Registered, or a standby"
// @Router /api/clusters/settings/actions/instance-name [post]
func (repman *ReplicationManager) handlerMuxSetInstanceName(w http.ResponseWriter, r *http.Request) {
	user, ok := repman.instanceNameACL(w, r)
	if !ok {
		return
	}
	if repman.Status != ConstMonitorActif {
		http.Error(w, "This instance is a standby: set the name on the active instance, the standby takes it from the config push", http.StatusConflict)
		return
	}
	var f InstanceNameForm
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		http.Error(w, "Invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := repman.setInstanceName(f); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errInstanceNameRegistered) {
			code = http.StatusConflict
		}
		http.Error(w, err.Error(), code)
		return
	}
	n := repman.instanceName()
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Instance name set to %s by %s", n.Name, user)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}
