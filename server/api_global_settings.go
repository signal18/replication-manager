// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// api_global_settings.go
// Global (server-scoped) settings API: handlers and appliers for
// /api/clusters/settings/actions/{switch|set|clear}. These settings carry the
// scope:"server" struct tag in config.Config and apply to the whole server.
package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

// handlerMuxSwitchGlobalSettings handles the switching of global settings for the server.
// @Summary Switch global settings for the server
// @Description This endpoint switches the global settings for the server.
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param settingName path string true "Setting Name"
// @Param state path string false "Toggle state (on/off)"
// @Success 200 {string} string "Successfully switched setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/switch/{settingName} [post]
// @Router /api/clusters/settings/actions/switch/{settingName}/{state} [post]
func (repman *ReplicationManager) handlerMuxSwitchGlobalSettings(w http.ResponseWriter, r *http.Request) {
	var value string
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	setting := vars["settingName"]
	serverScope := config.IsScope(setting, "server")
	if !serverScope {
		http.Error(w, "setting is not in global scope", http.StatusNotImplemented)
		return
	}

	var mycluster *cluster.Cluster
	if cName, ok := vars["clusterName"]; ok {
		mycluster = repman.getClusterByName(cName)
	} else {
		for _, v := range repman.Clusters {
			if v != nil {
				mycluster = v
				break
			}
		}
	}

	if v, ok := vars["state"]; ok {
		value = strings.ToLower(v)
		if value != "on" && value != "off" {
			http.Error(w, "Invalid state. Only accept on/off", http.StatusBadRequest)
			return
		}
	}

	if mycluster != nil {
		valid, user := repman.IsValidClusterACL(r, mycluster)
		if valid {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch global setting %s", setting)
			err := repman.switchServerSetting(user, r.URL.Path, setting, value)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to set value for %s: %s", setting, err.Error()), http.StatusBadRequest)
				return
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s", setting), http.StatusForbidden)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxSetGlobalSettings handles the setting of global settings for the server.
// @Summary Set global settings for the server
// @Description This endpoint sets the global settings for the server.
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param settingName path string true "Setting Name"
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string false "Cluster Name"
// @Param settingValue path string true "Setting Value"
// @Success 200 {string} string "Successfully set setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/set/{settingName}/{settingValue} [post]
func (repman *ReplicationManager) handlerMuxSetGlobalSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	setting := vars["settingName"]
	serverScope := config.IsScope(setting, "server")
	if !serverScope && !isProvAppTemplateRepoSetting(setting) {
		http.Error(w, "Setting Not Found", http.StatusNotImplemented)
		return
	}
	value := ""
	if settingValue, ok := vars["settingValue"]; ok {
		value = settingValue
	}

	var mycluster *cluster.Cluster
	// path := r.URL.Path
	if cName, ok := vars["clusterName"]; ok {
		mycluster = repman.getClusterByName(cName)
		r.URL.Path = strings.Replace(r.URL.Path, "/api/clusters/"+vars["clusterName"], "/api/clusters", 1)
	} else {
		for _, v := range repman.Clusters {
			if v != nil {
				mycluster = v
				break
			}
		}
	}

	if mycluster != nil {
		valid, user := repman.IsValidClusterACL(r, mycluster)
		if valid {
			// || (user != "" && mycluster.IsURLPassACL(user, path, false)) {
			//Set server scope
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Option '%s' is a shared values between clusters", setting)
			err := repman.setServerSetting(user, r.URL.Path, setting, value)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotImplemented)
				return
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s. path: %s", setting, r.URL.Path), http.StatusForbidden)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

func (repman *ReplicationManager) setRepmanSetting(name string, value string) error {
	var isactive = strings.ToLower(value) == "on"
	var v int

	fmtLog, logArgs := GetApiChangeLogFormat(name, value)

	//not immutable
	if !repman.Conf.IsVariableImmutable(name) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", fmtLog, logArgs...)
	} else {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Overwriting an immutable parameter defined in config , please use config-merge command to preserve them between restart")
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", fmtLog, logArgs...)
	}

	switch name {
	case "api-token-timeout":
		val, _ := strconv.Atoi(value)
		repman.Conf.SetApiTokenTimeout(val)
	case "cloud18":
		if value == "true" {
			// Set Cloud18=true optimistically so the UI sees the state change immediately.
			// InitGitConfig makes 5+ sequential HTTP calls to GitLab (OAuth token, user
			// lookup, group access, PAT rotation/creation) and takes several seconds.
			// Running it synchronously would block the HTTP handler and serialize all
			// other browser requests during that time. Run it in a background goroutine
			// so the response returns immediately. If it fails, Cloud18 is reverted to
			// false and the UI will reflect that on the next monitor poll.
			repman.Conf.Cloud18 = true
			go func() {
				if err := repman.InitGitConfig(repman.Conf); err != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
						"Cloud18 connect failed: %s", err.Error())
					repman.Conf.Cloud18 = false
				}
				// Save final state (Cloud18 + any PAT/GitUrl set by InitGitConfig).
				repman.ConfigManager.SaveConfig(repman, false)
			}()
		} else {
			repman.Conf.Cloud18 = false
		}
	case "cloud18-domain":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18Domain = value
	case "cloud18-sub-domain":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18SubDomain = value
	case "cloud18-sub-domain-zone":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18SubDomainZone = value
	case "cloud18-gitlab-user":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18GitUser = value
	case "cloud18-gitlab-password":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		repman.Conf.Cloud18GitPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.Cloud18GitPassword
		new_secret.OldValue = repman.Conf.GetDecryptedValue("cloud18-gitlab-password")
		repman.Conf.Secrets["cloud18-gitlab-password"] = new_secret
	case "cloud18-gateway-domain-name":
		repman.Conf.Cloud18GatewayDomainName = value
	case "cloud18-gateway-service":
		repman.Conf.Cloud18GatewayService = value
	case "cloud18-domain-add-script":
		repman.Conf.Cloud18DomainAddScript = value
	case "cloud18-domain-drop-script":
		repman.Conf.Cloud18DomainDropScript = value
	case "cloud18-domain-user":
		repman.Conf.Cloud18DomainUser = value
	case "cloud18-domain-secret":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		repman.Conf.Cloud18DomainSecret = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.Cloud18DomainSecret
		new_secret.OldValue = repman.Conf.GetDecryptedValue("cloud18-domain-secret")
		repman.Conf.Secrets["cloud18-domain-secret"] = new_secret
	case "api-bind":
		repman.Conf.APIBind = value
	case "api-port":
		repman.Conf.APIPort = value
	case "api-public-url":
		repman.Conf.APIPublicURL = value
	case "arbitration-external-hosts":
		repman.Conf.ArbitrationSasHosts = value
	case "arbitration-external-secret":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		repman.Conf.ArbitrationSasSecret = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.ArbitrationSasSecret
		new_secret.OldValue = repman.Conf.GetDecryptedValue("arbitration-external-secret")
		repman.Conf.Secrets["arbitration-external-secret"] = new_secret
	case "arbitration-external-unique-id":
		v, _ = strconv.Atoi(value)
		repman.Conf.ArbitrationSasUniqueId = v
	case "arbitration-failed-master-script":
		repman.Conf.ArbitrationFailedMasterScript = value
	case "arbitration-peer-hosts":
		repman.Conf.ArbitrationPeerHosts = value
	case "arbitration-read-timeout":
		v, _ = strconv.Atoi(value)
		repman.Conf.ArbitrationReadTimout = v
	case "git-acces-token":
		repman.Conf.GitAccesToken = value
	case "git-monitoring-ticker":
		v, _ = strconv.Atoi(value)
		repman.Conf.GitMonitoringTicker = v
	case "git-url":
		repman.Conf.GitUrl = value
	case "git-username":
		repman.Conf.GitUsername = value
	case "graphite-carbon-api-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonApiPort = v
	case "graphite-carbon-link-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonLinkPort = v
	case "graphite-carbon-host":
		repman.Conf.GraphiteCarbonHost = value
	case "graphite-carbon-pickle-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPicklePort = v
	case "graphite-carbon-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPort = v
	case "graphite-carbon-pprof-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPprofPort = v
	case "graphite-carbon-server-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonServerPort = v
	case "http-bind-address":
		repman.Conf.BindAddr = value
	case "http-port":
		repman.Conf.HttpPort = value
	case "http-session-lifetime":
		v, _ = strconv.Atoi(value)
		repman.Conf.SessionLifeTime = v
	case "monitoring-address":
		repman.Conf.MonitorAddress = value
	case "prov-service-plan-registry":
		repman.Conf.ProvServicePlanRegistry = value
	case "prov-service-plan":
		repman.Conf.ProvServicePlan = value
	case "prov-app-template-repo":
		repman.Conf.ProvAppTemplateRepo = value
	case "prov-app-template-repo-branch":
		repman.Conf.ProvAppTemplateRepoBranch = value
	case "prov-app-template-repo-user":
		repman.Conf.ProvAppTemplateRepoUser = value
	case "prov-app-template-repo-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return fmt.Errorf("unable to base64-decode value: %w", err)
		}
		repman.Conf.ProvAppTemplateRepoPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.ProvAppTemplateRepoPassword
		new_secret.OldValue = repman.Conf.GetDecryptedValue("prov-app-template-repo-password")
		repman.Conf.Secrets["prov-app-template-repo-password"] = new_secret
	case "prov-app-template-repo-timeout":
		parsedTimeout, err := parseProvAppTemplateRepoTimeout(value)
		if err != nil {
			return err
		}
		repman.Conf.ProvAppTemplateRepoTimeout = parsedTimeout
	case "prov-app-template-repo-allow-override":
		trimmed := strings.TrimSpace(strings.ToLower(value))
		if trimmed == "on" {
			trimmed = "true"
		} else if trimmed == "off" {
			trimmed = "false"
		}
		parsed, err := strconv.ParseBool(trimmed)
		if err != nil {
			return fmt.Errorf("invalid boolean value for %s: %q", name, value)
		}
		repman.Conf.ProvAppTemplateRepoAllowOverride = parsed
	case "sysbench-binary-path":
		_, storeValue, err := resolveExecutablePath(value, "sysbench-binary-path")
		if err != nil {
			return err
		}
		repman.Conf.SysbenchBinaryPath = storeValue
	case "backup-mydumper-path":
		_, storeValue, err := resolveExecutablePath(value, "backup-mydumper-path")
		if err != nil {
			return err
		}
		repman.Conf.BackupMyDumperPath = storeValue
	case "backup-myloader-path":
		_, storeValue, err := resolveExecutablePath(value, "backup-myloader-path")
		if err != nil {
			return err
		}
		repman.Conf.BackupMyLoaderPath = storeValue
	case "backup-mysqlbinlog-path":
		_, storeValue, err := resolveExecutablePath(value, "backup-mysqlbinlog-path")
		if err != nil {
			return err
		}
		repman.Conf.BackupMysqlbinlogPath = storeValue
	case "backup-mysqlclient-path":
		_, storeValue, err := resolveExecutablePath(value, "backup-mysqlclient-path")
		if err != nil {
			return err
		}
		repman.Conf.BackupMysqlclientPath = storeValue
	case "backup-mysqldump-path":
		_, storeValue, err := resolveExecutablePath(value, "backup-mysqldump-path")
		if err != nil {
			return err
		}
		repman.Conf.BackupMysqldumpPath = storeValue
	case "backup-restic-binary-path":
		_, storeValue, err := resolveExecutablePath(value, "backup-restic-binary-path")
		if err != nil {
			return err
		}
		repman.Conf.BackupResticBinaryPath = storeValue
	case "haproxy-binary-path":
		_, storeValue, err := resolveExecutablePath(value, "haproxy-binary-path")
		if err != nil {
			return err
		}
		repman.Conf.HaproxyBinaryPath = storeValue
	case "maxscale-binary-path":
		_, storeValue, err := resolveExecutablePath(value, "maxscale-binary-path")
		if err != nil {
			return err
		}
		repman.Conf.MxsBinaryPath = storeValue
	case "log-file-level", "log-level-file":
		val, _ := strconv.Atoi(value)
		repman.Conf.LogFileLevel = val
		repman.UpdateFileHookLogLevel(repman.fileHook.(*s18log.RotateFileHook), val)
	case "log-git-level", "log-level-git":
		val, _ := strconv.Atoi(value)
		repman.Conf.SetLogGitLevel(val)
	case "log-support-level", "log-level-support":
		val, _ := strconv.Atoi(value)
		repman.Conf.SetLogSupportLevel(val)
	case "log-stats-level", "log-level-stats":
		val, _ := strconv.Atoi(value)
		repman.Conf.LogStatsLevel = val
	case "log-heartbeat-level", "log-level-heartbeat":
		val, _ := strconv.Atoi(value)
		repman.Conf.LogHeartbeatLevel = val
		repman.Conf.ImmuableFlagMap["log-level-heartbeat"] = val
	case "mail-smtp-addr":
		repman.Conf.SetMailSmtpAddr(value)
		repman.Mailer.UpdateAddress(value)
	case "mail-smtp-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		repman.Conf.MailSMTPPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.MailSMTPPassword
		new_secret.OldValue = repman.Conf.GetDecryptedValue("mail-smtp-password")
		repman.Conf.Secrets["mail-smtp-password"] = new_secret
		repman.Mailer.UpdateAuth(repman.Conf.MailSMTPUser, new_secret.Value)
	case "mail-smtp-user":
		repman.Conf.SetMailSmtpUser(value)
		repman.Mailer.UpdateAuth(repman.Conf.MailSMTPUser, repman.Conf.GetDecryptedValue("mail-smtp-password"))
	case "mail-to":
		repman.Conf.SetMailTo(value)
	case "mail-from":
		repman.Conf.SetMailFrom(value)
		repman.Mailer.SetFrom(value)
	case "cloud18-shared":
		if repman.Conf.Cloud18 {
			repman.Conf.Cloud18Shared = isactive
		}
	case "api-https-bind":
		repman.Conf.APIHttpsBind = isactive
	case "api-server":
		repman.Conf.ApiServ = isactive
	case "api-swagger-enabled":
		repman.Conf.ApiSwaggerEnabled = isactive
	case "arbitration-external":
		if isactive && !repman.Conf.IsEligibleForArbitration() {
			return errors.New("arbitration requires a registered Cloud18 account with a support or partner subscription plan")
		}
		repman.Conf.Arbitration = isactive
	case "graphite-embedded":
		repman.Conf.GraphiteEmbedded = isactive
	case "graphite-blacklist":
		repman.Conf.GraphiteBlacklist = isactive
	case "graphite-metrics":
		repman.Conf.GraphiteMetrics = isactive
	case "http-server":
		repman.Conf.HttpServ = isactive
	case "http-use-react":
		repman.Conf.HttpUseReact = isactive
	case "monitoring-save-config":
		repman.Conf.ConfRewrite = isactive
	case "sysbench-v1":
		repman.Conf.SysbenchV1 = isactive
	case "scheduler-db-servers-receiver-use-ssl":
		repman.Conf.SchedulerReceiverUseSSL = isactive
	case "mail-smtp-tls-skip-verify":
		repman.Conf.MailSMTPTLSSkipVerify = isactive
		repman.Mailer.UpdateTLSConfig(repman.Conf.MailSMTPTLSSkipVerify)
	case "monitoring-log-api-login":
		repman.Conf.MonitoringLogAPILogin = isactive
	case "monitoring-log-api-login-silent-users":
		repman.Conf.MonitoringLogAPILoginSilentUsers = value
	case "cloud18-peer-health-mode":
		if value == "peering" || value == "smart" || value == "pulling" {
			repman.Conf.Cloud18PeerHealthMode = value
			repman.PeerManager.HealthMode = value
		}
	case "mail-max-pool":
		v, _ = strconv.Atoi(value)
		repman.Conf.MailMaxPool = v
		repman.Mailer.UpdateMaxPool(v)
	case "mail-timeout":
		v, _ = strconv.Atoi(value)
		repman.Conf.MailTimeout = v
		repman.Mailer.UpdateTimeout(v)
	default:
		return errors.New("setting not found")
	}

	repman.ConfigManager.SaveConfig(repman, false)
	return nil
}

func (repman *ReplicationManager) switchRepmanSetting(name string) error {
	//not immutable
	if !repman.Conf.IsVariableImmutable(name) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", name)
	} else {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Overwriting an immutable parameter defined in config , please use config-merge command to preserve them between restart")
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", name)
	}

	switch name {
	case "cloud18-shared":
		repman.Conf.SwitchCloud18Shared()
	case "api-https-bind":
		repman.Conf.APIHttpsBind = !repman.Conf.APIHttpsBind
	case "api-server":
		repman.Conf.ApiServ = !repman.Conf.ApiServ
	case "api-swagger-enabled":
		repman.Conf.ApiSwaggerEnabled = !repman.Conf.ApiSwaggerEnabled
	case "arbitration-external":
		if !repman.Conf.Arbitration && !repman.Conf.IsEligibleForArbitration() {
			return errors.New("arbitration requires a registered Cloud18 account with a support or partner subscription plan")
		}
		repman.Conf.Arbitration = !repman.Conf.Arbitration
	case "graphite-embedded":
		repman.Conf.GraphiteEmbedded = !repman.Conf.GraphiteEmbedded
	case "graphite-blacklist":
		repman.Conf.GraphiteBlacklist = !repman.Conf.GraphiteBlacklist
	case "graphite-metrics":
		repman.Conf.GraphiteMetrics = !repman.Conf.GraphiteMetrics
	case "http-server":
		repman.Conf.HttpServ = !repman.Conf.HttpServ
	case "http-use-react":
		repman.Conf.HttpUseReact = !repman.Conf.HttpUseReact
	case "monitoring-save-config":
		repman.Conf.ConfRewrite = !repman.Conf.ConfRewrite
	case "sysbench-v1":
		repman.Conf.SysbenchV1 = !repman.Conf.SysbenchV1
	case "scheduler-db-servers-receiver-use-ssl":
		repman.Conf.SchedulerReceiverUseSSL = !repman.Conf.SchedulerReceiverUseSSL
	case "mail-smtp-tls-skip-verify":
		repman.Conf.SwitchMailSmtpTlsSkipVerify()
		repman.Mailer.UpdateTLSConfig(repman.Conf.MailSMTPTLSSkipVerify)
	case "log-support":
		repman.Conf.LogSupport = !repman.Conf.LogSupport
	case "log-heartbeat":
		repman.Conf.LogHeartbeat = !repman.Conf.LogHeartbeat
		repman.Conf.ImmuableFlagMap["log-heartbeat"] = repman.Conf.LogHeartbeat
	case "monitoring-log-api-login":
		repman.Conf.MonitoringLogAPILogin = !repman.Conf.MonitoringLogAPILogin
	case "cloud18-disable-peers":
		repman.Conf.Cloud18DisablePeers = !repman.Conf.Cloud18DisablePeers
	case "cloud18-disable-for-sale":
		// Free plans cannot disable marketplace
		if repman.Conf.Cloud18SubscriptionPlan != "" && repman.Conf.Cloud18SubscriptionPlan != "free" {
			repman.Conf.Cloud18DisableForSale = !repman.Conf.Cloud18DisableForSale
		}
	default:
		return errors.New("setting not found")
	}
	repman.ConfigManager.SaveConfig(repman, false)
	return nil
}

func (repman *ReplicationManager) setServerSetting(user string, URL string, name string, value string) error {
	err := repman.setRepmanSetting(name, value)
	if err != nil {
		return err
	}
	if isProvAppTemplateRepoSetting(name) {
		// Intentionally do not fan out to clusters: these are global defaults
		// that clusters may inherit/override independently.
		return nil
	}

	for _, cl := range repman.Clusters {
		//Don't print error with no valid ACL
		if cl.IsURLPassACL(user, URL, false) {
			repman.setClusterSetting(cl, name, value)
		}
	}

	return nil
}

func (repman *ReplicationManager) switchServerSetting(user string, URL string, name string, value string) error {
	if value == "" {

		err := repman.switchRepmanSetting(name)
		if err != nil {
			return err
		}
		for cname, cl := range repman.Clusters {
			//Don't print error with no valid ACL
			if cl.IsURLPassACL(user, fmt.Sprintf(URL, cname), false) {
				repman.switchClusterSettings(cl, name)
			}
		}
	} else {
		err := repman.setRepmanSetting(name, value)
		if err != nil {
			return err
		}
		if isProvAppTemplateRepoSetting(name) {
			// Intentionally do not fan out to clusters: these are global defaults
			// that clusters may inherit/override independently.
			return nil
		}

		for _, cl := range repman.Clusters {
			//Don't print error with no valid ACL
			if cl.IsURLPassACL(user, URL, false) {
				repman.setClusterSetting(cl, name, value)
			}
		}
	}

	return nil
}
