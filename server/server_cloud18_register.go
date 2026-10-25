// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
)

// Registration through Cloud18 (#2159), declarative: the admin connects the instance to Cloud18
// (device login), repman takes the GitLab token the realm keeps for that person and, with it
// only, declares the instance in GitLab: the domain group (created, or the person must be a
// member), the config and pull projects (GitLab refusing an existing path is the URI's
// uniqueness), one project token per project. That GitLab token is never stored. The business
// side (subscription, billing, chat user) is the BO's, from GitLab. No CRM call, no password.

const (
	// RegStateLogin: waiting for the admin to approve the Cloud18 device login
	RegStateLogin = "login"
	// RegStateLink: the admin's Cloud18 account has no GitLab account linked
	RegStateLink = "link"
	// RegStateTaken: the URI belongs to another instance
	RegStateTaken = "taken"

	cloud18RegisterLoginWait = 16 * time.Minute
)

// cloud18RegisterResult is the status result of a registration through Cloud18: the device
// login while it waits, the page to link a GitLab account, the URI once done.
type cloud18RegisterResult struct {
	Device   *config.Cloud18DeviceLogin `json:"device,omitempty"`
	LinkURL  string                     `json:"linkUrl,omitempty"`
	URI      string                     `json:"uri,omitempty"`
	User     string                     `json:"user,omitempty"`
	Reattach bool                       `json:"reconnect,omitempty"`
}

func (repman *ReplicationManager) setRegStatus(state, message string, r cloud18RegisterResult) {
	b, _ := json.Marshal(r)
	repman.RegStatus.set(state, message, b)
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cloud18 registration: %s", message)
}

// errURITaken: the URI's project exists with a configuration this instance cannot read.
var errURITaken = errors.New("taken")

// encryptedValueRe finds an encrypted value of a saved config.
var encryptedValueRe = regexp.MustCompile(`hash_[0-9a-fA-F]{34,}`)

// cloud18ProjectOwnedHere says whether an existing config project is this instance's (or a
// peer's): its saved config holds no encrypted value yet (a registration that did not finish),
// or this instance's encryption key decrypts them (config.OwnsEncryptedValues).
func (repman *ReplicationManager) cloud18ProjectOwnedHere(glToken string, projectID int) (bool, error) {
	data, err := githelper.GitLabProjectFile(glToken, true, projectID, "default.toml")
	if err != nil {
		return false, err
	}
	values := encryptedValueRe.FindAllString(string(data), 20)
	if len(values) == 0 {
		return true, nil
	}
	return repman.Conf.OwnsEncryptedValues(values), nil
}

// cloud18Register runs a registration through Cloud18 for the URI domain.subdomain.zone,
// reporting each step in RegStatus.
func (repman *ReplicationManager) cloud18Register(domain, subdomain, zone, plan string) {
	uri := domain + "." + subdomain + "." + zone
	res := cloud18RegisterResult{URI: uri}
	conf := repman.Conf

	// 1. the admin's Cloud18 login (device login, approved in any browser)
	if !conf.HasCloud18Login() {
		d, err := conf.StartCloud18DeviceLogin()
		if err != nil {
			repman.setRegStatus(RegStateError, "Cloud18 login not started: "+err.Error(), res)
			return
		}
		res.Device = &d
		repman.setRegStatus(RegStateLogin, fmt.Sprintf("open %s and enter the code %s", d.VerificationURI, d.UserCode), res)
		deadline := time.Now().Add(cloud18RegisterLoginWait)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			st := conf.Cloud18IdentityStatus()
			if st.State == "pending" {
				continue
			}
			if !conf.HasCloud18Login() {
				repman.setRegStatus(RegStateError, "Cloud18 login not completed: "+st.Reason, cloud18RegisterResult{URI: uri})
				return
			}
			break
		}
		res.Device = nil
		if !conf.HasCloud18Login() {
			repman.setRegStatus(RegStateTimeout, "Cloud18 login not approved in time", res)
			return
		}
	}
	res.User = conf.Cloud18IdentityStatus().User

	// 2. the GitLab token the realm keeps for that person: only for the calls below
	glToken, err := conf.Cloud18GitLabBrokerToken()
	if errors.Is(err, config.ErrCloud18GitLabNotLinked) {
		res.LinkURL = conf.Cloud18LinkAccountURL()
		repman.setRegStatus(RegStateLink, err.Error(), res)
		return
	}
	if err != nil {
		repman.setRegStatus(RegStateError, "GitLab token from Cloud18: "+err.Error(), res)
		return
	}
	user, err := githelper.GitLabTokenUser(glToken)
	if err != nil {
		repman.setRegStatus(RegStateError, "GitLab user: "+err.Error(), res)
		return
	}

	// 3. the domain, then the two projects: an existing one must be this instance's (reconnect)
	groupID, err := githelper.GitLabEnsureDomainGroup(glToken, domain, user.ID)
	if err != nil {
		state := RegStateError
		if errors.Is(err, githelper.ErrNotDomainMember) {
			state = RegStateTaken
		}
		repman.setRegStatus(state, "domain "+domain+": "+err.Error(), res)
		return
	}
	name := subdomain + "-" + zone
	mainID, mainTaken, err := githelper.GitLabCreateProjectIn(glToken, groupID, domain, name)
	if err != nil {
		repman.setRegStatus(RegStateError, err.Error(), res)
		return
	}
	if mainTaken {
		owned, err := repman.cloud18ProjectOwnedHere(glToken, mainID)
		if err != nil {
			repman.setRegStatus(RegStateError, "the existing repository of "+uri+" cannot be read: "+err.Error(), res)
			return
		}
		if !owned {
			repman.setRegStatus(RegStateTaken, uri+" is already registered by another replication-manager (its configuration is not encrypted with this instance's key): choose another URI", res)
			return
		}
		res.Reattach = true
	}
	pullID, _, err := githelper.GitLabCreateProjectIn(glToken, groupID, domain, name+"-pull")
	if err != nil {
		repman.setRegStatus(RegStateError, err.Error(), res)
		return
	}

	// 4. the instance's own project tokens: the only GitLab credentials it keeps
	tokenName := "replication-manager " + uri
	expires := time.Now().Add(config.Cloud18GitTokenLifetime)
	mainTok, err := githelper.GitLabCreateProjectToken(glToken, mainID, tokenName, expires)
	if err != nil {
		repman.setRegStatus(RegStateError, "project token: "+err.Error(), res)
		return
	}
	pullTok, err := githelper.GitLabCreateProjectToken(glToken, pullID, tokenName, expires)
	if err != nil {
		repman.setRegStatus(RegStateError, "pull project token: "+err.Error(), res)
		return
	}
	glToken = ""

	conf.Cloud18Domain, conf.Cloud18SubDomain, conf.Cloud18SubDomainZone = domain, subdomain, zone
	conf.Cloud18GitUser = user.Email
	if conf.Cloud18GitUser == "" {
		conf.Cloud18GitUser = user.Username
	}
	if plan != "" {
		conf.Cloud18SubscriptionPlan = plan
	}
	conf.Cloud18GitProjectID, conf.Cloud18GitPullProjectID = mainID, pullID
	conf.SetDynamicFlag("cloud18-git-project-id", mainID)
	conf.SetDynamicFlag("cloud18-git-pull-project-id", pullID)
	end := mainTok.ExpiresAt
	if end == "" {
		end = expires.Format("2006-01-02")
	}
	conf.SetCloud18GitTokens(mainTok.Token, pullTok.Token, end)
	repman.shareGitSecrets()
	conf.Cloud18 = true
	if err := repman.InitGitConfig(conf); err != nil {
		repman.setRegStatus(RegStateError, "registered, the config repository not reached: "+err.Error(), res)
		return
	}
	repman.cloud18PasswordRefused.Store(false)
	repman.Save()
	msg := "registered " + uri + " as " + conf.Cloud18GitUser
	if res.Reattach {
		msg = "reconnected to " + uri + " as " + conf.Cloud18GitUser
	}
	repman.setRegStatus(RegStateOK, msg, res)
}

// shareGitSecrets gives the clusters the server's git tokens.
func (repman *ReplicationManager) shareGitSecrets() {
	for _, key := range []string{"git-acces-token", "git-pull-access-token"} {
		s := repman.Conf.Secrets.Get(key)
		for _, cl := range repman.Clusters {
			cl.Conf.Secrets.Set(key, s)
		}
	}
}

// rotateCloud18GitTokens renews the project tokens of an instance registered through Cloud18 when
// they end soon (or force). A token GitLab refuses raises the request to register again
// (GWARN022, from the state checks: config.Cloud18GitTokenLost).
func (repman *ReplicationManager) rotateCloud18GitTokens(force bool) error {
	conf := repman.Conf
	if !conf.Cloud18GitProjects() || (!force && !conf.Cloud18GitTokensDue(time.Now())) {
		return nil
	}
	if !repman.cloud18GitRotating.CompareAndSwap(false, true) {
		return nil
	}
	defer repman.cloud18GitRotating.Store(false)
	err := conf.RotateCloud18GitTokens(time.Now())
	repman.shareGitSecrets()
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Cloud18 git tokens not renewed: %s", err)
		repman.Save()
		return err
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Cloud18 git tokens renewed until %s", conf.Cloud18GitTokenExpires)
	repman.Save()
	return nil
}
