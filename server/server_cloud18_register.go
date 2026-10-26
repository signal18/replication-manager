// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
)

// Registration through Cloud18 (#2159), declarative, the same logic the GitLab registration had,
// against Cloud18: the admin's Cloud18 email and password give, through GitLab's own Cloud18 login
// (config.Cloud18GitLabLogin), the person's GitLab token; with it only, repman declares the
// instance in GitLab: the domain group (created, or the person must be a member), the config and
// pull projects (GitLab refusing an existing path is the URI's uniqueness), one project token per
// project. No account yet: the realm's sign-up (config.Cloud18SignUp), the person confirms the
// email (and chooses the password there) and registers again. Neither the password nor that GitLab
// token is stored. The business side (subscription, billing, chat user) is the BO's, from GitLab.
// No CRM call, no browser.

const (
	// RegStateVerify: the Cloud18 account waits for its email confirmation (sign-up done)
	RegStateVerify = "verify"
	// RegStateCredentials: Cloud18 refused the email and password
	RegStateCredentials = "credentials"
	// RegStateTaken: the URI belongs to another instance
	RegStateTaken = "taken"

	// cloud18MigrateRetry: how long a migration that failed for another reason than a refused
	// password waits before it is tried again
	cloud18MigrateRetry = time.Hour
)

// cloud18RegisterResult is the status result of a registration through Cloud18.
type cloud18RegisterResult struct {
	URI      string `json:"uri,omitempty"`
	User     string `json:"user,omitempty"`
	Reattach bool   `json:"reconnect,omitempty"`
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
func (repman *ReplicationManager) cloud18ProjectOwnedHere(conf *config.Config, glToken string, projectID int) (bool, error) {
	data, err := githelper.GitLabProjectFile(glToken, true, projectID, "default.toml")
	if err != nil {
		return false, err
	}
	values := encryptedValueRe.FindAllString(string(data), 20)
	if len(values) == 0 {
		return true, nil
	}
	return conf.OwnsEncryptedValues(values), nil
}

// cloud18Register runs a registration through Cloud18 for the URI domain.subdomain.zone with the
// admin's Cloud18 email and password, reporting each step in RegStatus. signUp, when given, creates
// the Cloud18 account if the realm does not know the email.
func (repman *ReplicationManager) cloud18Register(domain, subdomain, zone, plan, email, password string, signUp *config.Cloud18SignUpForm) {
	uri := domain + "." + subdomain + "." + zone
	res := cloud18RegisterResult{URI: uri, User: email}
	conf := repman.Conf

	// a registered instance registers again only under its own URI (a reconnect): another URI
	// would switch its settings and tokens but not its config repository's clone (git refused,
	// the config no longer leaves the instance)
	if current := repman.registeredInstanceURI(); conf.Cloud18 && current != "" && current != uri {
		repman.setRegStatus(RegStateError, "this replication-manager is registered as "+current+": unregister it first to register it as "+uri, res)
		return
	}

	// 1. the GitLab token of the Cloud18 account, GitLab logging the person in with Cloud18
	glToken, err := conf.Cloud18GitLabLogin(email, password)
	switch {
	case errors.Is(err, config.ErrCloud18AccountNotReady):
		repman.setRegStatus(RegStateVerify, err.Error()+", then register again with that password", res)
		return
	case errors.Is(err, config.ErrCloud18BadCredentials) && signUp != nil:
		signUp.Email = email
		err = conf.Cloud18SignUp(*signUp)
		if errors.Is(err, config.ErrCloud18AccountExists) {
			repman.setRegStatus(RegStateCredentials, "a Cloud18 account exists for "+email+": the password is wrong (reset it on the Cloud18 login page)", res)
			return
		}
		if err != nil {
			repman.setRegStatus(RegStateError, err.Error(), res)
			return
		}
		repman.setRegStatus(RegStateVerify, "Cloud18 account created for "+email+": confirm the email Cloud18 sent (its link lets you choose your password), then register again with that password", res)
		return
	case errors.Is(err, config.ErrCloud18BadCredentials):
		repman.setRegStatus(RegStateCredentials, "Cloud18 refused the email and password of "+email+" (no account yet: register with the account details to create it)", res)
		return
	case err != nil:
		repman.setRegStatus(RegStateError, "Cloud18 login: "+err.Error(), res)
		return
	}
	repman.cloud18Declare(conf, glToken, domain, subdomain, zone, plan, res)
}

// regState is the current registration state.
func (repman *ReplicationManager) regState() string {
	state, _, _ := repman.RegStatus.snapshot()
	return state
}

// cloud18Declare declares the instance in GitLab with the person's GitLab token (used for these
// calls only, never kept): the domain group, the config and pull projects (an existing one must be
// this instance's: a reconnect), one project token per project, then the settings. It returns the
// final registration state. conf is the config being built: at start InitGitConfig runs on the
// config that replaces repman.Conf afterwards, so nothing may be written to repman.Conf here.
func (repman *ReplicationManager) cloud18Declare(conf *config.Config, glToken, domain, subdomain, zone, plan string, res cloud18RegisterResult) string {
	uri := domain + "." + subdomain + "." + zone
	user, err := githelper.GitLabTokenUser(glToken)
	if err != nil {
		repman.setRegStatus(RegStateError, "GitLab user: "+err.Error(), res)
		return repman.regState()
	}
	res.User = user.Email

	// 3. the domain, then the two projects: an existing one must be this instance's (reconnect)
	groupID, err := githelper.GitLabEnsureDomainGroup(glToken, domain, user.ID)
	if err != nil {
		state := RegStateError
		if errors.Is(err, githelper.ErrNotDomainMember) {
			state = RegStateTaken
		}
		repman.setRegStatus(state, "domain "+domain+": "+err.Error(), res)
		return repman.regState()
	}
	name := subdomain + "-" + zone
	mainID, mainTaken, err := githelper.GitLabCreateProjectIn(glToken, groupID, domain, name)
	if err != nil {
		repman.setRegStatus(RegStateError, err.Error(), res)
		return repman.regState()
	}
	if mainTaken {
		owned, err := repman.cloud18ProjectOwnedHere(conf, glToken, mainID)
		if err != nil {
			repman.setRegStatus(RegStateError, "the existing repository of "+uri+" cannot be read: "+err.Error(), res)
			return repman.regState()
		}
		if !owned {
			repman.setRegStatus(RegStateTaken, uri+" is already registered by another replication-manager (its configuration is not encrypted with this instance's key): choose another URI", res)
			return repman.regState()
		}
		res.Reattach = true
	}
	pullID, _, err := githelper.GitLabCreateProjectIn(glToken, groupID, domain, name+"-pull")
	if err != nil {
		repman.setRegStatus(RegStateError, err.Error(), res)
		return repman.regState()
	}

	// 4. the instance's own project tokens: the only GitLab credentials it keeps
	// one name per instance, as GetInstancePATName (its arbitration unique id): peers of an HA pair
	// share the URI and must never revoke each other's tokens
	tokenName := fmt.Sprintf("replication-manager %s-%s-%s-%d", domain, subdomain, zone, conf.ArbitrationSasUniqueId)
	expires := time.Now().Add(config.Cloud18GitTokenLifetime)
	mainTok, err := githelper.GitLabCreateProjectToken(glToken, mainID, tokenName, expires)
	if err != nil {
		repman.setRegStatus(RegStateError, "project token: "+err.Error(), res)
		return repman.regState()
	}
	pullTok, err := githelper.GitLabCreateProjectToken(glToken, pullID, tokenName, expires)
	if err != nil {
		repman.setRegStatus(RegStateError, "pull project token: "+err.Error(), res)
		return repman.regState()
	}
	// a reconnect or a migration replaces the instance's previous tokens, once the new ones exist:
	// never a pile of them, never none
	githelper.GitLabRevokeProjectTokens(glToken, mainID, tokenName, mainTok.ID)
	githelper.GitLabRevokeProjectTokens(glToken, pullID, tokenName, pullTok.ID)
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
	repman.shareGitSecrets(conf)
	conf.Cloud18, conf.Cloud18Unregister = true, false
	conf.SetDynamicFlag("cloud18-unregister", false)
	if err := repman.InitGitConfig(conf); err != nil {
		repman.setRegStatus(RegStateError, "registered, the config repository not reached: "+err.Error(), res)
		return repman.regState()
	}
	repman.cloud18PasswordRefused.Store(false)
	repman.Save()
	if repman.ConfigManager != nil && conf == repman.Conf {
		// the reset cloud18-unregister reaches GitLab now, not at the next periodic push
		repman.ConfigManager.GitPush(conf, repman.ClusterList, false)
	}
	msg := "registered " + uri + " as " + conf.Cloud18GitUser
	if res.Reattach {
		msg = "reconnected to " + uri + " as " + conf.Cloud18GitUser
	}
	repman.setRegStatus(RegStateOK, msg, res)
	return RegStateOK
}

// shareGitSecrets gives the clusters the git tokens of conf.
func (repman *ReplicationManager) shareGitSecrets(conf *config.Config) {
	for _, key := range []string{"git-acces-token", "git-pull-access-token"} {
		s := conf.Secrets.Get(key)
		for _, cl := range repman.Clusters {
			cl.Conf.Secrets.Set(key, s)
		}
	}
}

// rotateCloud18GitTokens renews the project tokens of an instance registered through Cloud18 when
// they end soon (or force). A token GitLab refuses raises the request to register again
// (GWARN022, from the state checks: config.Cloud18GitTokenLost).
func (repman *ReplicationManager) rotateCloud18GitTokens(conf *config.Config, force bool) error {
	if !conf.Cloud18GitProjects() || (!force && !conf.Cloud18GitTokensDue(time.Now())) {
		return nil
	}
	if !repman.cloud18GitRotating.CompareAndSwap(false, true) {
		return nil
	}
	defer repman.cloud18GitRotating.Store(false)
	err := conf.RotateCloud18GitTokens(time.Now())
	repman.shareGitSecrets(conf)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlErr, "Cloud18 git tokens not renewed: %s", err)
		repman.Save()
		return err
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Cloud18 git tokens renewed until %s", conf.Cloud18GitTokenExpires)
	repman.Save()
	return nil
}

// cloud18MigrateToProjectTokens moves an instance registered with the GitLab password to its own
// project tokens, by itself, at start (#2159): its stored GitLab user and password through
// GitLab's own web sign-in (config.GitLabPasswordLogin, not the password grant GitLab ends), then
// the same declaration as a registration, which finds its repositories (its key decrypts their
// config): a reconnect. Nobody has to do anything. When it cannot (a 2FA account, a captcha, a
// GitLab without password sign-in), the instance keeps its password path and says why in the log.
func (repman *ReplicationManager) cloud18MigrateToProjectTokens(conf *config.Config) {
	if !conf.Cloud18 || conf.Cloud18GitProjects() || conf.Cloud18Domain == "" || conf.Cloud18SubDomain == "" || conf.Cloud18SubDomainZone == "" {
		return
	}
	user, password := conf.Cloud18GitUser, conf.GetDecryptedValue("cloud18-gitlab-password")
	if user == "" || password == "" {
		return
	}
	// a refused password is not tried again until it changes (GitLab locks an account after a few
	// failed sign-ins); any other failure waits cloud18MigrateRetry before the next try
	sum := sha256.Sum256([]byte(user + "\x00" + password))
	fingerprint := hex.EncodeToString(sum[:])
	if refused, _ := repman.cloud18MigrateRefused.Load().(string); refused == fingerprint {
		return
	}
	if time.Now().Unix() < repman.cloud18MigrateNext.Load() {
		return
	}
	glToken, err := conf.GitLabPasswordLogin(user, password)
	if err != nil {
		if errors.Is(err, config.ErrGitLabPasswordRefused) {
			repman.cloud18MigrateRefused.Store(fingerprint)
		} else {
			repman.cloud18MigrateNext.Store(time.Now().Add(cloud18MigrateRetry).Unix())
		}
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn,
			"Cloud18: %s stays on its GitLab password (not moved to its project tokens): %s", repman.registeredInstanceURI(), err)
		return
	}
	res := cloud18RegisterResult{URI: conf.Cloud18Domain + "." + conf.Cloud18SubDomain + "." + conf.Cloud18SubDomainZone, User: user}
	state := repman.cloud18Declare(conf, glToken, conf.Cloud18Domain, conf.Cloud18SubDomain, conf.Cloud18SubDomainZone, "", res)
	_, msg, _ := repman.RegStatus.snapshot()
	if state != RegStateOK {
		repman.cloud18MigrateNext.Store(time.Now().Add(cloud18MigrateRetry).Unix())
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn,
			"Cloud18: %s stays on its GitLab password (not moved to its project tokens): %s", res.URI, msg)
		return
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo,
		"Cloud18: %s moved to its own project tokens (%s)", res.URI, msg)
}
