// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codegangsta/negroni"
	"github.com/iancoleman/strcase"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dockerhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/splitdump"
)

type LogLevelEnum string

const (
	LogLevelErr   LogLevelEnum = "ERROR"
	LogLevelWarn  LogLevelEnum = "WARN"
	LogLevelInfo  LogLevelEnum = "INFO"
	LogLevelDebug LogLevelEnum = "DEBUG"
)

func (repman *ReplicationManager) apiClusterUnprotectedHandler(router *mux.Router) {
	router.Handle("/api/clusters/{clusterName}/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/health", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterHealth)),
	))

	router.Handle("/api/clusters/{clusterName}/jobs-log-level/{task}/{level}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterCheckJobLogLevel)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/master-physical-backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterMasterPhysicalBackup)),
	))
	router.Handle("/api/clusters/{clusterName}/is-in-errstate/{errstate}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterIsInErrState)),
	))

}

func (repman *ReplicationManager) apiClusterProtectedHandler(router *mux.Router) {

	router.Handle("/api/clusters/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxCluster)),
	))

	router.Handle("/api/clusters/{clusterName}/opensvc-gateway", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterGatewayServiceNodes)),
	))

	router.Handle("/api/clusters/{clusterName}/opensvc-stats", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterOpenSVCDaemonStatus)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS ACTIONS
	router.Handle("/api/clusters/{clusterName}/settings", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSettings)),
	))

	router.Handle("/api/clusters/{clusterName}/plugins", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterPlugins)),
	))
	router.Handle("/api/clusters/{clusterName}/plugins/reload", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterPluginsReload)),
	))

	router.Handle("/api/clusters/{clusterName}/tags", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterTags)),
	))

	router.Handle("/api/clusters/{clusterName}/jobs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterGetJobEntries)),
	))

	router.Handle("/api/clusters/{clusterName}/backups", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterBackups)),
	))

	router.Handle("/api/clusters/{clusterName}/backups/stats", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterBackupStat)),
	))

	router.Handle("/api/clusters/{clusterName}/backups/reconcile", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterBackupReconcile)),
	))

	router.Handle("/api/clusters/{clusterName}/backups/{backupID}/delete", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterBackupDelete)),
	))

	router.Handle("/api/clusters/{clusterName}/terminals", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerGetTerminalSessionList)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/snapshots", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSnapshots)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/stats", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSnapshotStat)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/fetch", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticFetch)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/purge/{snapshotID}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticPurge)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/unlock", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticUnlock)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/unlock/{force}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticUnlock)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/init", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticInitRepo)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/init/{force}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticInitRepo)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetResticTaskQueue)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-current", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetResticCurrentTask)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue/resume", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticTaskQueueResume)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue/pause", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticTaskQueuePause)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue/cancel/{taskID}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxCancelResticTask)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue/move/{moveType}/{taskID}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticTaskQueueMove)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue/move/{moveType}/{taskID}/{afterID}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticTaskQueueMove)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/task-queue/reset", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResetResticTaskQueue)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/restore-config", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticRestoreConfig)),
	))

	router.Handle("/api/clusters/{clusterName}/restic/restore-config/{force}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticRestoreConfig)),
	))

	router.Handle("/api/clusters/{clusterName}/certificates", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterCertificates)),
	))

	router.Handle("/api/clusters/{clusterName}/queryrules", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterQueryRules)),
	))
	router.Handle("/api/clusters/{clusterName}/top", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterTop)),
	))
	router.Handle("/api/clusters/{clusterName}/shardclusters", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterShardClusters)),
	))
	router.Handle("/api/clusters/{clusterName}/send-vault-token", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSendVaultToken)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/reload", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSettingsReload)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/reload-plan-info", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReloadPlanInfo)),
	))
	router.Handle("/api/clusters/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchGlobalSettings)),
	))
	router.Handle("/api/clusters/settings/actions/switch/{settingName}/{state}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchGlobalSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/switch/{settingName}/{state}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSettings)),
	))
	router.Handle("/api/clusters/settings/actions/set/{settingName}/{settingValue:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetGlobalSettings)),
	))
	router.Handle("/api/clusters/settings/actions/clear/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetGlobalSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/clear/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettings)),
	))
	router.Handle("/api/clusters/settings/actions/reload-clusters-plans", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReloadPlans)),
	))
	router.Handle("/api/clusters/settings/actions/reload-clusters-plan-info", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReloadPlansInfo)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set-cron/{settingName}/{settingValue:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetCron)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/add-db-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddTag)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/drop-db-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropTag)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/preserved-variables-cnf", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetPreservedVarsCnf)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/save-preserved-variables-cnf", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSavePreservedVarsCnf)),
	))
	router.Handle("/api/clusters/{clusterName}/configurator/tags/{tagName}/content", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetTagContent)),
	))
	router.Handle("/api/clusters/{clusterName}/configurator/tags/{tagName}/dochelp", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetTagDocHelp)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/add-proxy-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddProxyTag)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/drop-proxy-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropProxyTag)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/reset-failover-control", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterResetFailoverControl)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/discover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettingsDiscover)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/apply-dynamic-config", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterApplyDynamicConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/generate-configs/{servertype}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterRegenerateConfigs)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/preserve-variable/{variableName}/{preserve}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterVariablesPreserve)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/add/{clusterShardingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterShardingAdd)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/switchover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/failover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxFailover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/certificates-rotate", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRotateKeys)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/certificates-reload", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterReloadCertificates)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/reset-sla", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResetSla)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/bootstrap/{topology}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxBootstrapReplication)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/cleanup", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxBootstrapReplicationCleanup)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/staging-refresh", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRefreshStagingCluster)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/staging-reload-script", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReloadStagingScript)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/provision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/unprovision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/cancel-rolling-restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesCancelRollingRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/cancel-rolling-reprov", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesCancelRollingReprov)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStopTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/start-traffic", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStartTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic-staging", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStopTrafficStaging)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/start-traffic-staging", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStartTrafficStaging)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/sysbench", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSysbench)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/waitdatabases", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterWaitDatabases)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type}/{tag:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/dropserver/{serverName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerDropByName)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/dropserver/{host}/{port}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerDrop)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/dropserver/{host}/{port}/{type}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerDrop)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/jobs-upgrade", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterAllowJobsUpgrade)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/rolling", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRolling)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/rotate-passwords", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerRotatePasswords)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/send-email", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSendEmail)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/send-alert/{hooktype}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSendAlert)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/refresh-apps-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppRefreshTemplateFromRepo)),
	))

	router.Handle("/api/clusters/{clusterName}/docker/actions/registry-connect", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerDockerRegistryConnect)),
	))
	router.Handle("/api/clusters/{clusterName}/docker/browse/{imageRef:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerDockerImageFilesystemDir)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaReshardTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table/{clusterList}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaReshardTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/move-table/{clusterShard}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaMoveTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/universal-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaUniversalTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaChecksumTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-repair-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaChecksumRepairTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/all/actions/checksum-schema", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterChecksumSchema)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/monitor-schemas", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterMonitorSchemas)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/checksum-all-tables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaChecksumAllTable)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/checksum-repair-all-tables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaChecksumRepairAllTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/analyze-table/{persistent}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaAnalyzeTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/all/actions/analyze-schema/{persistent}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterAnalyzeSchema)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/analyze-all-tables/{persistent}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaAnalyzeAllTables)),
	))

	router.Handle("/api/clusters/{clusterName}/schema", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchema)),
	))

	router.Handle("/api/clusters/{clusterName}/graphite-filterlist", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterGraphiteFilterList)),
	))

	router.Handle("/api/clusters/{clusterName}/settings/actions/set-graphite-filterlist/{filterType}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSetGraphiteFilterList)),
	))

	router.Handle("/api/clusters/{clusterName}/settings/actions/reload-graphite-filterlist", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterReloadGraphiteFilterList)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/reset-graphite-filterlist/{template}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterResetGraphiteFilterList)),
	))
	//PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY

	router.Handle("/api/clusters/actions/add/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterAdd)),
	))

	router.Handle("/api/clusters/actions/delete/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterDelete)),
	))

	router.Handle("/api/clusters/actions/rename/{clusterName}/{newClusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterRename)),
	))

	router.Handle("/api/clusters/{clusterName}/topology/servers", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServers)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/servers/count", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersCount)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/master", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxMaster)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaves)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves/count", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlavesCount)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves/index/{slaveIndex}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaveIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves/index/{slaveIndex}/attr/{attrName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaveAttributeByIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/state/{state}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetServersByState)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/state/{state}/count", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetServersByStateCount)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/state/{state}/index/{index}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetServerByStateAndIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/state/{state}/index/{index}/attr/{attrName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetServerAttributeByStateAndIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/logs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxLog)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/http-logs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxWebLog)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/http-logs/{logType}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxWebLog)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/proxies", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxies)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/apps", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxApps)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/alerts", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAlerts)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/crashes", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxCrashes)),
	))
	//PROTECTED ENDPOINTS FOR TESTS

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/all", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxTests)),
	))
	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxOneTest)),
	))

	// endpoint to fetch Cluster.DiffVariables
	router.Handle("/api/clusters/{clusterName}/diffvariables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerDiffVariables)),
	))

	router.Handle("/api/clusters/{clusterName}/users/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddClusterUser)),
	))

	router.Handle("/api/clusters/{clusterName}/users/update", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxUpdateClusterUser)),
	))

	router.Handle("/api/clusters/{clusterName}/users/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropClusterUser)),
	))

	router.Handle("/api/clusters/{clusterName}/users/send-credentials", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSendCredentials)),
	))
	router.Handle("/api/clusters/{clusterName}/ext-role/subscribe", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSubscribeExternalOps)),
	))
	router.Handle("/api/clusters/{clusterName}/ext-role/quote", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQuoteExternalOps)),
	))
	router.Handle("/api/clusters/{clusterName}/ext-role/accept", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAcceptExternalOps)),
	))
	router.Handle("/api/clusters/{clusterName}/ext-role/refuse", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRefuseExternalOps)),
	))
	router.Handle("/api/clusters/{clusterName}/ext-role/end", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRemoveExternalOps)),
	))

	router.Handle("/api/clusters/{clusterName}/sales/accept-subscription", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAcceptSubscription)),
	))

	router.Handle("/api/clusters/{clusterName}/sales/refuse-subscription", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRejectSubscription)),
	))

	router.Handle("/api/clusters/{clusterName}/sales/end-subscription", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRemoveSponsor)),
	))

	router.Handle("/api/clusters/{clusterName}/subscribe", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSubscribe)),
	))

	router.Handle("/api/clusters/{clusterName}/unsubscribe", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRejectSubscription)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/staging-reseed-from-parent", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReseedFromParent)),
	))

	// S3 Provider CRUD routes
	router.Handle("/api/clusters/{clusterName}/s3providers", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProvidersGet)),
	)).Methods(http.MethodGet)
	router.Handle("/api/clusters/{clusterName}/s3providers/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderAdd)),
	)).Methods(http.MethodPost)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderModify)),
	)).Methods(http.MethodPut)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderModify)),
	)).Methods(http.MethodPost)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderDrop)),
	)).Methods(http.MethodDelete)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderDrop)),
	)).Methods(http.MethodGet)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/references", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderReferences)),
	)).Methods(http.MethodGet)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/sync/preview", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderSyncPreview)),
	)).Methods(http.MethodPost)
	router.Handle("/api/clusters/{clusterName}/s3providers/{name}/sync/apply", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterS3ProviderSyncApply)),
	)).Methods(http.MethodPost)

	// Register restic-specific routes
	repman.RegisterResticRoutes(router)

	// Security remediation endpoints
	router.Handle("/api/clusters/{clusterName}/security/remediation-plan", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSecurityRemediationPlan)),
	))
	router.Handle("/api/clusters/{clusterName}/security/apply-fix", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSecurityApplyFix)),
	))
	router.Handle("/api/clusters/{clusterName}/security/fix-state/{errKey}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSecurityFixState)),
	))
}

// @Summary Retrieve servers for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of servers"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/servers [get]
func (repman *ReplicationManager) handlerMuxServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	var err error

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		type ServersContainer struct {
			servers []map[string]interface{}
		}

		res := ServersContainer{
			servers: make([]map[string]interface{}, 0),
		}
		for _, srv := range mycluster.GetServers() {
			if srv == nil {
				continue
			}

			var cont map[string]interface{}
			data, _ := json.Marshal(srv)
			data, err = sjson.SetBytes(data, "binaryLogFiles", srv.BinaryLogFiles.ToNewMap())
			if err != nil {
				http.Error(w, "Encoding error: "+err.Error(), http.StatusInternalServerError)
				return
			}
			err = json.Unmarshal(data, &cont)
			if err != nil {
				http.Error(w, "Encoding error: "+err.Error(), http.StatusInternalServerError)
				return
			}
			res.servers = append(res.servers, cont)
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(res.servers)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Return number of servers for that specific named cluster
// @Description Return number of servers for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Number of servers"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/servers/count [get]
func (repman *ReplicationManager) handlerMuxServersCount(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strconv.Itoa(len(mycluster.Servers))))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Retrieve all servers by state for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param state path string true "Server State"
// @Success 200 {array} cluster.ServerMonitor "server by state"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/state/{state} [get]
func (repman *ReplicationManager) handlerMuxGetServersByState(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	var err error

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		type ServersContainer struct {
			servers []map[string]interface{}
		}

		res := ServersContainer{
			servers: make([]map[string]interface{}, 0),
		}
		for _, srv := range mycluster.GetServersByState(vars["state"]) {
			var cont map[string]interface{}
			data, _ := json.Marshal(srv)
			data, err = sjson.SetBytes(data, "binaryLogFiles", srv.BinaryLogFiles.ToNewMap())
			if err != nil {
				http.Error(w, "Encoding error: "+err.Error(), http.StatusInternalServerError)
				return
			}
			err = json.Unmarshal(data, &cont)
			if err != nil {
				http.Error(w, "Encoding error: "+err.Error(), http.StatusInternalServerError)
				return
			}
			res.servers = append(res.servers, cont)
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(res.servers)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Return number of servers for that specific named cluster
// @Description Return number of servers for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param state path string true "Server State"
// @Success 200 {string} string "Number of servers"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/state/{state}/count [get]
func (repman *ReplicationManager) handlerMuxGetServersByStateCount(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		counter := 0
		for _, srv := range mycluster.Servers {
			if srv.State == vars["state"] {
				counter++
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strconv.Itoa(counter)))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Retrieve server by state and index for a specific cluster
// @Description This endpoint retrieves the server for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param state path string true "Server State"
// @Param index path string true "Index"
// @Success 200 {object} cluster.ServerMonitor "server by state"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/state/{state}/index/{index} [get]
func (repman *ReplicationManager) handlerMuxGetServerByStateAndIndex(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		index, err := strconv.Atoi(vars["index"])
		if err != nil {
			http.Error(w, "Invalid index", http.StatusInternalServerError)
			return
		}

		srv, err := mycluster.GetServerByStateAndIndex(vars["state"], index)
		if srv == nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data, _ := json.Marshal(srv)
		data, err = sjson.SetBytes(data, "binaryLogFiles", srv.BinaryLogFiles.ToNewMap())
		if err != nil {
			http.Error(w, "Encoding error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(data)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Retrieve server attributes by state and index for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param state path string true "Server State"
// @Param index path string true "Index"
// @Param attrName path string true "Attribute Name with dot notation"
// @Success 200 {object} cluster.ServerMonitor "Server (partial based on attrName)"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/state/{state}/index/{index}/attr/{attrName} [get]
func (repman *ReplicationManager) handlerMuxGetServerAttributeByStateAndIndex(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		index, err := strconv.Atoi(vars["index"])
		if err != nil {
			http.Error(w, "Invalid index", http.StatusInternalServerError)
			return
		}

		srv, err := mycluster.GetServerByStateAndIndex(vars["state"], index)
		if srv == nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		re := regexp.MustCompile(`\.\[(\d+)\]`)
		var value []byte
		var resultval gjson.Result
		var jsonpath = re.ReplaceAllString(vars["attrName"], `.$1`) // replace .[n] with .n for gjson compatibility

		if vars["attrName"] == "binaryLogFiles" {
			value, _ = json.Marshal(srv.BinaryLogFiles.ToNewMap())
		} else {
			if strings.HasPrefix(vars["attrName"], "binaryLogFiles.") {
				data, _ := json.Marshal(srv.BinaryLogFiles.ToNewMap())
				resultval = gjson.GetBytes(data, strings.TrimPrefix(jsonpath, "binaryLogFiles."))
			} else {
				data, _ := json.Marshal(srv)
				resultval = gjson.GetBytes(data, jsonpath)
			}

			if !resultval.Exists() {
				http.Error(w, "Attribute not found", http.StatusInternalServerError)
				return
			}

			value = []byte(resultval.String())
		}

		w.WriteHeader(http.StatusOK)
		w.Write(value)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Shows the slaves for that specific named cluster
// @Description Shows the slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "A list of slaves"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves [get]
func (repman *ReplicationManager) handlerMuxSlaves(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetSlaves())
		var srvs []*cluster.ServerMonitor

		err := json.Unmarshal(data, &srvs)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
		for i := range srvs {
			srvs[i].Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(srvs)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Return number of slaves for that specific named cluster
// @Description Return number of slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Number of slaves"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves/count [get]
func (repman *ReplicationManager) handlerMuxSlavesCount(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strconv.Itoa(len(mycluster.GetSlaves()))))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Shows the slaves for that specific named cluster
// @Description Shows the slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param slaveIndex path string true "Slave Index (start from 0)"
// @Success 200 {object} cluster.ServerMonitor "Slave Data"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves/index/{slaveIndex} [get]
func (repman *ReplicationManager) handlerMuxSlaveIndex(w http.ResponseWriter, r *http.Request) {
	// Marshal/unmarshal for obfuscation deep copy of struct
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	uname := repman.GetUserFromRequest(r)
	if _, ok := mycluster.APIUsers[uname]; !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	index, err := strconv.Atoi(vars["slaveIndex"])
	if err != nil {
		http.Error(w, "Invalid slave index", http.StatusBadRequest)
		return
	}

	slave := mycluster.GetSlaveByIndex(index)
	if slave == nil {
		http.Error(w, "Slave not found", http.StatusNotFound)
		return
	}

	data, err := json.Marshal(slave)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error marshaling slave: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var srv cluster.ServerMonitor
	err = json.Unmarshal(data, &srv)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error unmarshaling slave: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	srv.Pass = "XXXXXXXX"
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err = e.Encode(&srv)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: %v", err)
		// Cannot send http.Error here as headers are already sent
		return
	}
}

// @Summary Shows the slaves for that specific named cluster
// @Description Shows the slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param slaveIndex path string true "Slave Index (start from 0)"
// @Param attrName path string true "Attribute Name (using json path notation split by dot)"
// @Success 200 {object} cluster.ServerMonitor "Slave Attribute (partial based on attrName)"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves/index/{slaveIndex}/attr/{attrName} [get]
func (repman *ReplicationManager) handlerMuxSlaveAttributeByIndex(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		uname := repman.GetUserFromRequest(r)
		if _, ok := mycluster.APIUsers[uname]; !ok {
			http.Error(w, "No Valid ACL", http.StatusInternalServerError)
			return
		}

		index, err := strconv.Atoi(vars["slaveIndex"])
		if err != nil {
			http.Error(w, "Invalid index", http.StatusInternalServerError)
			return
		}

		slave := mycluster.GetSlaveByIndex(index)
		if slave == nil {
			http.Error(w, "Slave not found", http.StatusInternalServerError)
			return
		}

		re := regexp.MustCompile(`\.\[(\d+)\]`)
		var value []byte
		var resultval gjson.Result
		var jsonpath = re.ReplaceAllString(vars["attrName"], `.$1`) // replace .[n] with .n for gjson compatibility
		// get the value from the json path
		// if the attribute is binaryLogFiles, we need to convert the map to json
		// if the attribute is binaryLogFiles.*, we need to convert the map to json and get the value from the json path
		// otherwise, we just get the value from the json path
		if vars["attrName"] == "binaryLogFiles" {
			value, _ = json.Marshal(slave.BinaryLogFiles.ToNewMap())
		} else {
			if strings.HasPrefix(vars["attrName"], "binaryLogFiles.") {
				data, _ := json.Marshal(slave.BinaryLogFiles.ToNewMap())
				resultval = gjson.GetBytes(data, strings.TrimPrefix(jsonpath, "binaryLogFiles."))
			} else {
				data, _ := json.Marshal(slave)
				resultval = gjson.GetBytes(data, jsonpath)
			}

			// if the value is not found, return an error
			if !resultval.Exists() {
				http.Error(w, "Attribute not found", http.StatusInternalServerError)
				return
			}

			// convert the value to bytes
			value = []byte(resultval.String())
		}

		// Write the value to the response
		w.WriteHeader(http.StatusOK)
		w.Write(value)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Shows the proxies for that specific named cluster
// @Description Shows the proxies for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "A list of proxies"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/proxies [get]
func (repman *ReplicationManager) handlerMuxProxies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetProxies())
		var prxs []*cluster.Proxy
		err := json.Unmarshal(data, &prxs)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(prxs)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Shows the alerts for that specific named cluster
// @Description Shows the alerts for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.Alerts "A list of alerts"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/alerts [get]
func (repman *ReplicationManager) handlerMuxAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	a := new(cluster.Alerts)
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		a.Errors = mycluster.GetStateMachine().GetOpenErrors()
		a.Warnings = mycluster.GetStateMachine().GetOpenWarnings()
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(a)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Rotate keys for a specific cluster
// @Description Rotate the keys for the specified cluster
// @Tags ClusterCertificates
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Keys rotated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/certificates-rotate [post]
func (repman *ReplicationManager) handlerMuxRotateKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.KeyRotation()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// @Summary Reset SLA for a specific cluster
// @Description Reset the SLA for the specified cluster
// @Tags ClusterActions
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "SLA reset successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/reset-sla [post]
func (repman *ReplicationManager) handlerMuxResetSla(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetEmptySla()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxFailover handles the failover process for a given cluster.
// @Summary Handles the failover process for a given cluster.
// @Description This endpoint triggers a master failover for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered failover"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/failover [post]
func (repman *ReplicationManager) handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.MasterFailover(true)
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterShardingAdd handles the addition of a sharding cluster to an existing cluster.
// @Summary Add a sharding cluster to an existing cluster
// @Description This endpoint adds a sharding cluster to an existing cluster and triggers a rolling restart.
// @Tags ClusterTopology
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param clusterShardingName path string true "Cluster Sharding Name"
// @Success 200 {string} string "Sharding cluster added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/add/{clusterShardingName} [post]
func (repman *ReplicationManager) handlerMuxClusterShardingAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		repman.AddCluster(vars["clusterShardingName"], vars["clusterName"])
		mycluster.RollingRestart()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxRolling handles the rolling restart process for a given cluster.
// @Summary Handles the rolling restart process for a given cluster.
// @Description This endpoint triggers a rolling restart for the specified cluster.
// @Tags ClusterMaintenance
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered rolling restart"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/rolling [post]
func (repman *ReplicationManager) handlerMuxRolling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.RollingRestart()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxStartTraffic handles the start traffic process for a given cluster.
// @Summary Start traffic for a specific cluster
// @Description This endpoint starts traffic for the specified cluster.
// @Tags ClusterTraffics
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully started traffic"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/start-traffic [post]
func (repman *ReplicationManager) handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetTraffic(true)
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxStopTraffic handles the stop traffic process for a given cluster.
// @Summary Stop traffic for a specific cluster
// @Description This endpoint stops traffic for the specified cluster.
// @Tags ClusterTraffics
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully stopped traffic"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/stop-traffic [post]
func (repman *ReplicationManager) handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetTraffic(false)
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxStartTraffic handles the start traffic process for a given cluster.
// @Summary Start traffic for a specific cluster
// @Description This endpoint starts traffic for the specified cluster.
// @Tags ClusterTraffics
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully started traffic"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/start-traffic-staging [post]
func (repman *ReplicationManager) handlerMuxStartTrafficStaging(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetTrafficStaging(true)
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxStopTraffic handles the stop traffic process for a given cluster.
// @Summary Stop traffic for a specific cluster
// @Description This endpoint stops traffic for the specified cluster.
// @Tags ClusterTraffics
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully stopped traffic"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/stop-traffic-staging [post]
func (repman *ReplicationManager) handlerMuxStopTrafficStaging(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetTrafficStaging(false)
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxBootstrapReplicationCleanup handles the cleanup process for replication bootstrap.
// @Summary Cleanup replication bootstrap for a specific cluster
// @Description This endpoint triggers the cleanup process for replication bootstrap for the specified cluster.
// @Tags ClusterReplication
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully cleaned up replication bootstrap"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/replication/cleanup [post]
func (repman *ReplicationManager) handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		err := mycluster.BootstrapReplicationCleanup()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error Cleanup Replication: %s", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxBootstrapReplication handles the bootstrap replication process for a given cluster.
// @Summary Bootstrap replication for a specific cluster
// @Description This endpoint triggers the bootstrap replication process for the specified cluster.
// @Tags ClusterReplication
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param topology path string true "Topology"
// @Success 200 {string} string "Successfully bootstrapped replication"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/replication/bootstrap/{topology} [post]
func (repman *ReplicationManager) handlerMuxBootstrapReplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if err := mycluster.BootstrapTopology(vars["topology"]); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error bootstraping topology %s", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := mycluster.BootstrapReplication(true); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error bootstraping replication %s", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxServicesProvision handles the provisioning of services for a given cluster.
// @Summary Provision services for a specific cluster
// @Description This endpoint provisions services for the specified cluster.
// @Tags ClusterProvision
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully provisioned services"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/services/actions/provision [post]
func (repman *ReplicationManager) handlerMuxServicesProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		err := mycluster.Bootstrap()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error Bootstrap Micro Services + replication: %s", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxServicesUnprovision handles the unprovisioning of services for a given cluster.
// @Summary Unprovision services for a specific cluster
// @Description This endpoint unprovisions services for the specified cluster.
// @Tags ClusterProvision
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully unprovisioned services"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/services/actions/unprovision [post]
func (repman *ReplicationManager) handlerMuxServicesUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.Unprovision()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxServicesCancelRollingRestart handles the cancellation of rolling restart for a given cluster.
// @Summary Cancel rolling restart for a specific cluster
// @Description This endpoint cancels the rolling restart for the specified cluster.
// @Tags ClusterMaintenance
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully cancelled rolling restart"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/cancel-rolling-restart [post]
func (repman *ReplicationManager) handlerMuxServicesCancelRollingRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.CancelRollingRestart()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxServicesCancelRollingReprov handles the cancellation of rolling reprovision for a given cluster.
// @Summary Cancel rolling reprovision for a specific cluster
// @Description This endpoint cancels the rolling reprovision for the specified cluster.
// @Tags ClusterProvision
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully cancelled rolling reprovision"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/cancel-rolling-reprov [post]
func (repman *ReplicationManager) handlerMuxServicesCancelRollingReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.CancelRollingReprov()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxSetSettingsDiscover handles the discovery of settings for a given cluster.
// @Summary Discover settings for a specific cluster
// @Description This endpoint triggers the discovery of settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully discovered settings"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/discover [post]
func (repman *ReplicationManager) handlerMuxSetSettingsDiscover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		err := mycluster.ConfigDiscovery()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterResetFailoverControl handles the reset of failover control for a given cluster.
// @Summary Reset failover control for a specific cluster
// @Description This endpoint resets the failover control for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reset failover control"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/reset-failover-control [post]
func (repman *ReplicationManager) handlerMuxClusterResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.ResetFailoverCtr()
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxSwitchover handles the switchover process for a given cluster.
// @Summary Handles the switchover process for a given cluster.
// @Description This endpoint triggers a master switchover for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param prefmaster formData string false "Preferred Master"
// @Success 200 {string} string "Successfully triggered switchover"
// @Failure 400 {string} string "Master failed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/switchover [post]
func (repman *ReplicationManager) handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive switchover request")
		savedPrefMaster := mycluster.GetPreferedMasterList()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if mycluster.IsMasterFailed() {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Master failed, cannot initiate switchover")
			http.Error(w, "Master failed", http.StatusBadRequest)
			return
		}
		r.ParseForm() // Parses the request body
		newPrefMaster := r.Form.Get("prefmaster")
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API force for prefered master: %s", newPrefMaster)
		if mycluster.IsInHostList(newPrefMaster) {
			mycluster.SetPrefMaster(newPrefMaster)
		} else {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Prefered master: not found in database servers %s", newPrefMaster)
		}
		mycluster.MasterFailover(false)
		mycluster.SetPrefMaster(savedPrefMaster)

	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxMaster handles the HTTP request to retrieve the master of a specified cluster.
// @Summary Retrieve master of a cluster
// @Description This endpoint retrieves the master of a specified cluster and returns it in JSON format.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.ServerMonitor "Master server"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/master [get]
func (repman *ReplicationManager) handlerMuxMaster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		m := mycluster.GetMaster()
		var srvs *cluster.ServerMonitor
		if m != nil {
			data, _ := json.Marshal(m)

			err := json.Unmarshal(data, &srvs)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error decoding JSON: ", err)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}
			srvs.Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(srvs)
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterCertificates handles the retrieval of client certificates for a given cluster.
// @Summary Retrieve client certificates for a specific cluster
// @Description This endpoint retrieves the client certificates for the specified cluster.
// @Tags ClusterCertificates
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of client certificates"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/certificates [get]
func (repman *ReplicationManager) handlerMuxClusterCertificates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		certs, err := mycluster.GetClientCertificates()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = e.Encode(certs)
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterTags handles the retrieval of tags for a given cluster.
// @Summary Retrieve tags for a specific cluster
// @Description This endpoint retrieves the tags for the specified cluster.
// @Tags ClusterTags
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of tags"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/tags [get]
func (repman *ReplicationManager) handlerMuxClusterTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.Configurator.GetDBModuleTags())
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterBackups handles the retrieval of backups for a given cluster.
// @Summary Retrieve backups for a specific cluster
// @Description This endpoint retrieves the backups for the specified cluster.
// @Description Required grant: cluster-show-backups
// @Tags ClusterBackups
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[int64]backupmgr.BackupMetadata "List of backups"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/backups [get]
func (repman *ReplicationManager) handlerMuxClusterBackups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetBackups())
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

type resticSnapshotResponse struct {
	backupmgr.BackupSnapshot
	Metadata       []*cluster.SnapshotMetadataSummary `json:"metadata,omitempty"`
	MetadataReady  bool                               `json:"metadataReady"`
	MetadataStatus string                             `json:"metadataStatus"`
	MetadataError  string                             `json:"metadataError,omitempty"`
}

type resticSnapshotsResponse struct {
	RepoPath  string                   `json:"repo_path,omitempty"`
	Stats     backupmgr.BackupStat     `json:"stats"`
	Snapshots []resticSnapshotResponse `json:"snapshots"`
}

// handlerMuxClusterBackupStat handles the retrieval of backup stats for a given cluster.
// @Summary Retrieve backup stats for a specific cluster
// @Description This endpoint retrieves the backup stats for the specified cluster.
// @Description Required grant: cluster-show-backups
// @Tags ClusterBackups
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} backupmgr.BackupStat "List of backups"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/backups/stats [get]
func (repman *ReplicationManager) handlerMuxClusterBackupStat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetBackupStat())
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterBackupReconcile handles the manual reconciliation of backup metadata with restic snapshots.
// @Summary Reconcile backup metadata with restic snapshots
// @Description This endpoint triggers a manual reconciliation check to detect drift between backup metadata files and restic snapshots.
// @Description Required grant: db-backup
// @Tags ClusterBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.ReconciliationReport "Reconciliation report with orphaned and missing metadata"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster not found"
// @Failure 500 {string} string "Reconciliation failed"
// @Router /api/clusters/{clusterName}/backups/reconcile [post]
func (repman *ReplicationManager) handlerMuxClusterBackupReconcile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	report, err := mycluster.ReconcileSnapshotMetadata()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err = e.Encode(report)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API encoding error %s", err)
	}
}

// handlerMuxClusterBackupDelete handles deletion of a non-restic backup by ID.
// @Summary Delete a backup by ID
// @Description Deletes a non-restic backup for the specified cluster.
// @Description Required grant: db-backup
// @Tags ClusterBackups
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param backupID path string true "Backup ID"
// @Success 200 {object} map[string]interface{} "Backup deleted"
// @Failure 400 {string} string "Invalid request"
// @Failure 409 {string} string "Backup running"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Delete failed"
// @Router /api/clusters/{clusterName}/backups/{backupID}/delete [post]
func (repman *ReplicationManager) handlerMuxClusterBackupDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	backupID := strings.TrimSpace(vars["backupID"])
	if backupID == "" {
		http.Error(w, "No backup ID", http.StatusBadRequest)
		return
	}
	backupIDInt, err := strconv.ParseInt(backupID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid backup ID", http.StatusBadRequest)
		return
	}

	meta, err := mycluster.DeleteBackupByID(backupIDInt)
	if err != nil {
		switch {
		case errors.Is(err, cluster.ErrBackupInProgress):
			http.Error(w, err.Error(), http.StatusConflict)
			return
		case errors.Is(err, cluster.ErrBackupInvalidID), errors.Is(err, cluster.ErrBackupNotFound):
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	if err := e.Encode(map[string]interface{}{
		"id":      meta.Id,
		"message": "Local backup deleted; restic snapshots retained",
	}); err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterShardClusters handles the retrieval of shard clusters for a given cluster.
// @Summary Retrieve shard clusters for a specific cluster
// @Description This endpoint retrieves the shard clusters for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of shard clusters"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/shardclusters [get]
func (repman *ReplicationManager) handlerMuxClusterShardClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.ShardProxyGetShardClusters())
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterQueryRules handles the retrieval of query rules for a given cluster.
// @Summary Retrieve query rules for a specific cluster
// @Description This endpoint retrieves the query rules for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of query rules"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/queryrules [get]
func (repman *ReplicationManager) handlerMuxClusterQueryRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetQueryRules())
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterTop handles the retrieval of top metrics for a given cluster.
// @Summary Retrieve top metrics for a specific cluster
// @Description This endpoint retrieves the top metrics for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName query string false "Server Name"
// @Success 200 {object} map[string]interface{} "Top metrics"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/top [get]
func (repman *ReplicationManager) handlerMuxClusterTop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		svname := r.URL.Query().Get("serverName")
		if svname != "" {
			node := mycluster.GetServerFromName(svname)
			if node == nil {
				http.Error(w, "Not a Valid Server!", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetTopMetrics(svname))
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxSwitchSettings handles the switching of settings for a given cluster.
// @Summary Switch settings for a specific cluster
// @Description This endpoint switches the settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param settingName path string true "Setting Name"
// @Param state path string false "Toggle state (on/off)"
// @Success 200 {string} string "Successfully switched setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/switch/{settingName} [post]
// @Router /api/clusters/{clusterName}/settings/actions/switch/{settingName}/{state} [post]
func (repman *ReplicationManager) handlerMuxSwitchSettings(w http.ResponseWriter, r *http.Request) {
	var value string
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	cName := vars["clusterName"]
	setting := vars["settingName"]

	// Should be handled with global settings
	serverScope := config.IsScope(setting, "server")
	if serverScope {
		r.URL.Path = strings.Replace(r.URL.Path, "/api/clusters/"+vars["clusterName"], "/api/clusters/", 1)
		repman.handlerMuxSwitchGlobalSettings(w, r)
		return
	}

	if v, ok := vars["state"]; ok {
		value = strings.ToLower(v)
		if value != "on" && value != "off" {
			http.Error(w, "Invalid state. Only accept on/off", http.StatusBadRequest)
			return
		}
	}

	mycluster := repman.getClusterByName(cName)
	if mycluster != nil {
		valid, _ := repman.IsValidClusterACL(r, mycluster)
		if valid {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", setting)
			if value == "" {
				err := repman.switchClusterSettings(mycluster, setting)
				if err != nil {
					http.Error(w, "Setting Not Found", http.StatusNotImplemented)
					return
				}
			} else {
				err := repman.setClusterSetting(mycluster, setting, value)
				if err != nil {
					http.Error(w, fmt.Sprintf("Failed to set value for %s: %s", setting, err.Error()), http.StatusBadRequest)
					return
				}
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for %s in cluster %s", setting, vars["clusterName"]), http.StatusForbidden)
			return
		}

	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

}

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

func (repman *ReplicationManager) switchClusterSettings(mycluster *cluster.Cluster, setting string) error {
	switch setting {
	case "verbose":
		mycluster.SwitchVerbosity()
	case "failover-mode":
		mycluster.SwitchInteractive()
	case "failover-readonly-state":
		mycluster.SwitchReadOnly()
	case "failover-restart-unsafe":
		mycluster.SwitchFailoverRestartUnsafe()
	case "failover-at-sync":
		mycluster.SwitchFailSync()
	case "failover-divergent-data":
		mycluster.SwitchFailoverDivergentData()
	case "force-slave-no-gtid-mode":
		mycluster.SwitchForceSlaveNoGtid()
	case "switchover-lower-release":
		mycluster.SwitchFailoverLowerRelease()
	case "failover-event-status":
		mycluster.SwitchFailoverEventStatus()
	case "failover-event-scheduler":
		mycluster.SwitchFailoverEventScheduler()
	case "delay-stat-capture":
		mycluster.SwitchDelayStatCapture()
	case "print-delay-stat":
		mycluster.SwitchPrintDelayStat()
	case "print-delay-stat-history":
		mycluster.SwitchPrintDelayStatHistory()
	case "failover-check-delay-stat":
		mycluster.SwitchFailoverCheckDelayStat()
	case "autorejoin":
		mycluster.SwitchRejoin()
	case "autoseed":
		mycluster.SwitchAutoseed()
	case "autorejoin-backup-binlog":
		mycluster.SwitchRejoinBackupBinlog()
	case "autorejoin-flashback":
		mycluster.SwitchRejoinFlashback()
	case "autorejoin-flashback-on-sync":
		mycluster.SwitchRejoinSemisync()
	case "autorejoin-flashback-on-unsync": //?????
	case "autorejoin-slave-positional-heartbeat":
		mycluster.SwitchRejoinPseudoGTID()
	case "autorejoin-zfs-flashback":
		mycluster.SwitchRejoinZFSFlashback()
	case "autorejoin-mysqldump":
		mycluster.SwitchRejoinDump()
	case "autorejoin-logical-backup":
		mycluster.SwitchRejoinLogicalBackup()
	case "autorejoin-physical-backup":
		mycluster.SwitchRejoinPhysicalBackup()
	case "autorejoin-force-restore":
		mycluster.SwitchRejoinForceRestore()
	case "switchover-at-sync":
		mycluster.SwitchSwitchoverSync()
	case "switchover-lock-user-on-freeze":
		mycluster.SwitchSwitchoverLockUserOnFreeze()
	case "check-replication-filters":
		mycluster.SwitchCheckReplicationFilters()
	case "check-replication-state":
		mycluster.SwitchRplChecks()
	case "scheduler-db-servers-logical-backup":
		mycluster.SwitchSchedulerBackupLogical()
	case "scheduler-db-servers-physical-backup":
		mycluster.SwitchSchedulerBackupPhysical()
	case "scheduler-db-servers-logs":
		mycluster.SwitchSchedulerDatabaseLogs()
	case "scheduler-jobs-ssh":
		mycluster.SwitchSchedulerDbJobsSsh()
	case "scheduler-db-servers-logs-table-rotate":
		mycluster.SwitchSchedulerDatabaseLogsTableRotate()
	case "scheduler-rolling-restart":
		mycluster.SwitchSchedulerRollingRestart()
	case "scheduler-rolling-reprov":
		mycluster.SwitchSchedulerRollingReprov()
	case "scheduler-db-servers-optimize":
		mycluster.SwitchSchedulerDatabaseOptimize()
	case "scheduler-db-servers-analyze":
		mycluster.SwitchSchedulerDatabaseAnalyze()
	case "scheduler-alert-disable":
		mycluster.SwitchSchedulerAlertDisable()
	case "graphite-metrics":
		mycluster.SwitchGraphiteMetrics()
	case "graphite-embedded":
		mycluster.SwitchGraphiteEmbedded()
	case "graphite-whitelist":
		mycluster.SwitchGraphiteMetrics()
	case "graphite-blacklist":
		mycluster.SwitchGraphiteBlacklist()
	case "monitoring-performance-schema-mutex":
		mycluster.SwitchMonitorPFSMutex()
	case "monitoring-performance-schema-latch":
		mycluster.SwitchMonitorPFSLatch()
	case "monitoring-performance-schema-memory":
		mycluster.SwitchMonitorPFSMemory()
	case "monitoring-performance-schema-instruments":
		mycluster.SwitchMonitorPFSInstruments()
	case "monitoring-performance-schema-queries":
		mycluster.SwitchMonitorPFSQueries()
	case "monitoring-performance-schema-queries-explain":
		mycluster.SwitchMonitorPFSQueriesExplain()
	case "shardproxy-copy-grants":
		mycluster.SwitchProxysqlCopyGrants()
	case "proxysql-copy-grants":
		mycluster.SwitchProxysqlCopyGrants()
	case "proxysql-bootstrap-users":
		mycluster.SwitchProxysqlCopyGrants()
	case "proxysql-bootstrap-variables":
		mycluster.SwitchProxysqlBootstrapVariables()
	case "proxysql-bootstrap-hostgroups":
		mycluster.SwitchProxysqlBootstrapHostgroups()
	case "proxysql-bootstrap-servers":
		mycluster.SwitchProxysqlBootstrapServers()
	case "proxysql-bootstrap-query-rules":
		mycluster.SwitchProxysqlBootstrapQueryRules()
	case "proxysql-bootstrap":
		mycluster.SwitchProxysqlBootstrap()
	case "proxysql":
		mycluster.SwitchProxySQL()
	case "proxy-servers-read-on-master":
		mycluster.SwitchProxyServersReadOnMaster()
	case "proxy-servers-read-on-master-no-slave":
		mycluster.SwitchProxyServersReadOnMasterNoSlave()
	case "proxy-servers-backend-compression":
		mycluster.SwitchProxyServersBackendCompression()
	case "database-heartbeat":
		mycluster.SwitchTraffic()
	case "database-heartbeat-staging":
		mycluster.SwitchTrafficStaging()
	case "test":
		mycluster.SwitchTestMode()
	case "prov-net-cni":
		mycluster.SwitchProvNetCNI()
	case "prov-db-config-preserve":
		mycluster.Conf.ProvDBConfigPreserve = !mycluster.Conf.ProvDBConfigPreserve
	case "prov-db-start-fetch-config":
		mycluster.Conf.ProvDbStartFetchConfig = !mycluster.Conf.ProvDbStartFetchConfig
		mycluster.CheckNeedConfigFetch()
	case "prov-db-apply-dynamic-config":
		mycluster.SwitchDBApplyDynamicConfig()
	case "prov-docker-daemon-private":
		mycluster.SwitchProvDockerDaemonPrivate()
	case "prov-object-allow-overwrite":
		mycluster.Conf.ProvObjectAllowOverwrite = !mycluster.Conf.ProvObjectAllowOverwrite
	case "backup-restic-aws":
		mycluster.SwitchBackupResticAws()
	case "backup-restic":
		mycluster.SwitchBackupRestic()
	case "backup-restic-purge-prune":
		mycluster.Conf.BackupResticPurgePrune = !mycluster.Conf.BackupResticPurgePrune
	case "backup-restic-purge-prune-compact":
		mycluster.Conf.BackupResticPurgePruneCompact = !mycluster.Conf.BackupResticPurgePruneCompact
	case "backup-restic-purge-prune-repack-cacheable-only":
		mycluster.Conf.BackupResticPurgePruneRepackCacheableOnly = !mycluster.Conf.BackupResticPurgePruneRepackCacheableOnly
	case "backup-restic-purge-prune-repack-small":
		mycluster.Conf.BackupResticPurgePruneRepackSmall = !mycluster.Conf.BackupResticPurgePruneRepackSmall
	case "backup-restic-purge-prune-repack-uncompressed":
		mycluster.Conf.BackupResticPurgePruneRepackUncompressed = !mycluster.Conf.BackupResticPurgePruneRepackUncompressed
	case "backup-restic-allow-unsafe-mount":
		mycluster.Conf.BackupResticAllowUnsafeMount = !mycluster.Conf.BackupResticAllowUnsafeMount
		if mycluster.ResticManager != nil {
			mycluster.ResticManager.AllowUnsafeMount = mycluster.Conf.BackupResticAllowUnsafeMount
		}
	case "backup-restic-mount-allow-other":
		mycluster.Conf.BackupResticMountAllowOther = !mycluster.Conf.BackupResticMountAllowOther
	case "backup-restic-mount-no-default-permissions":
		mycluster.Conf.BackupResticMountNoDefaultPermissions = !mycluster.Conf.BackupResticMountNoDefaultPermissions
	case "backup-restic-mount-owner-root":
		mycluster.Conf.BackupResticMountOwnerRoot = !mycluster.Conf.BackupResticMountOwnerRoot
	case "backup-restic-mount-no-lock":
		mycluster.Conf.BackupResticMountNoLock = !mycluster.Conf.BackupResticMountNoLock
	case "backup-restic-mount-quiet":
		mycluster.Conf.BackupResticMountQuiet = !mycluster.Conf.BackupResticMountQuiet
	case "backup-binlogs":
		mycluster.SwitchBackupBinlogs()
	case "compress-backups":
		mycluster.SwitchCompressBackups()
	case "backup-reseed-remote-decompress":
		mycluster.Conf.BackupReseedRemoteDecompress = !mycluster.Conf.BackupReseedRemoteDecompress
	case "backup-split-mysql-user":
		mycluster.Conf.BackupSplitMysqlUser = !mycluster.Conf.BackupSplitMysqlUser
	case "backup-restore-mysql-user":
		mycluster.Conf.BackupRestoreMysqlUser = !mycluster.Conf.BackupRestoreMysqlUser
	case "backup-mysqldump-splitdump":
		mycluster.Conf.BackupMysqldumpSplitDump = !mycluster.Conf.BackupMysqldumpSplitDump
	case "backup-splitdump-create-databases":
		mycluster.Conf.BackupSplitdumpCreateDatabases = !mycluster.Conf.BackupSplitdumpCreateDatabases
	case "backup-restore-definer-strict":
		mycluster.Conf.BackupRestoreDefinerStrict = !mycluster.Conf.BackupRestoreDefinerStrict
	case "backup-check-free-space":
		mycluster.Conf.BackupCheckFreeSpace = !mycluster.Conf.BackupCheckFreeSpace
	case "backup-estimate-size":
		mycluster.Conf.BackupEstimateSize = !mycluster.Conf.BackupEstimateSize
	case "backup-restic-purge-oldest-on-disk-space":
		mycluster.Conf.BackupResticPurgeOldestOnDiskSpace = !mycluster.Conf.BackupResticPurgeOldestOnDiskSpace
	case "monitoring-pause":
		mycluster.SwitchMonitoringPause()
	case "monitoring-save-config":
		mycluster.SwitchMonitoringSaveConfig()
	case "monitoring-queries":
		mycluster.SwitchMonitoringQueries()
	case "monitoring-scheduler":
		mycluster.SwitchMonitoringScheduler()
	case "monitoring-schema-change":
		mycluster.SwitchMonitoringSchemaChange()
	case "monitoring-schema-columns":
		mycluster.SwitchMonitoringSchemaColumns()
	case "monitoring-schema-indexes":
		mycluster.SwitchMonitoringSchemaIndexes()
	case "monitoring-schema-scheduler":
		mycluster.SwitchMonitoringSchemaScheduler()
	case "monitoring-checksum-scheduler":
		mycluster.SwitchMonitoringChecksumScheduler()
	case "monitoring-schema-on-replicas":
		mycluster.SwitchMonitoringSchemaOnReplicas()
	case "monitoring-capture":
		mycluster.SwitchMonitoringCapture()
	case "monitoring-innodb-status":
		mycluster.SwitchMonitoringInnoDBStatus()
	case "monitoring-variable-diff":
		mycluster.SwitchMonitoringVariableDiff()
	case "monitoring-processlist":
		mycluster.SwitchMonitoringProcesslist()
	case "monitoring-processlist-inactive":
		mycluster.SwitchMonitoringProcesslistInactive()
	case "monitoring-processlist-transactions":
		mycluster.SwitchMonitoringProcesslistTransactions()
	case "monitoring-processlist-information-schema":
		mycluster.SwitchMonitoringProcesslistInformationSchema()
	case "force-slave-readonly":
		mycluster.SwitchForceSlaveReadOnly()
	case "force-binlog-row":
		mycluster.SwitchForceBinlogRow()
	case "force-slave-semisync":
		mycluster.SwitchForceSlaveSemisync()
	case "force-slave-Heartbeat":
		mycluster.SwitchForceSlaveHeartbeat()
	case "force-slave-gtid":
		mycluster.SwitchForceSlaveGtid()
	case "force-slave-gtid-mode-strict":
		mycluster.SwitchForceSlaveGtidStrict()
	case "force-slave-idempotent":
		mycluster.SwitchForceSlaveModeIdempotent()
	case "force-slave-strict":
		mycluster.SwitchForceSlaveModeStrict()
	case "force-slave-serialized":
		mycluster.SwitchForceSlaveParallelModeSerialized()
	case "force-slave-minimal":
		mycluster.SwitchForceSlaveParallelModeMinimal()
	case "force-slave-conservative":
		mycluster.SwitchForceSlaveParallelModeConservative()
	case "force-slave-optimistic":
		mycluster.SwitchForceSlaveParallelModeOptimistic()
	case "force-slave-aggressive":
		mycluster.SwitchForceSlaveParallelModeAggressive()
	case "force-binlog-compress":
		mycluster.SwitchForceBinlogCompress()
	case "force-binlog-annotate":
		mycluster.SwitchForceBinlogAnnotate()
	case "force-binlog-slow-queries":
		mycluster.SwitchForceBinlogSlowqueries()
	case "log-sql-in-monitoring":
		mycluster.SwitchLogSQLInMonitoring()
	case "log-writer-election":
		mycluster.SwitchLogWriterElection()
	case "log-sst":
		mycluster.SwitchLogSST()
	case "log-heartbeat":
		mycluster.SwitchLogHeartbeat()
	case "log-config-load":
		mycluster.SwitchLogConfigLoad()
	case "log-git":
		mycluster.SwitchLogGit()
	case "log-support":
		mycluster.SwitchLogSupport()
	case "log-backup-stream":
		mycluster.SwitchLogBackupStream()
	case "log-orchestrator":
		mycluster.SwitchLogOrchestrator()
	case "log-vault":
		mycluster.SwitchLogVault()
	case "log-topology":
		mycluster.SwitchLogTopology()
	case "log-proxy":
		mycluster.SwitchLogProxy()
	case "proxysql-debug":
		mycluster.SwitchProxysqlDebug()
	case "haproxy-debug":
		mycluster.SwitchHaproxyDebug()
	case "proxyjanitor-debug":
		mycluster.SwitchProxyJanitorDebug()
	case "maxscale-debug":
		mycluster.SwitchMxsDebug()
	case "force-binlog-purge":
		mycluster.SwitchForceBinlogPurge()
	case "force-binlog-purge-on-restore":
		mycluster.SwitchForceBinlogPurgeOnRestore()
	case "force-binlog-purge-replicas":
		mycluster.SwitchForceBinlogPurgeReplicas()
	case "multi-master-concurrent-write":
		mycluster.SwitchMultiMasterConcurrentWrite()
	case "multi-master-ring-unsafe":
		mycluster.SwitchMultiMasterRingUnsafe()
	case "dynamic-topology":
		mycluster.SwitchDynamicTopology()
	case "replication-no-relay":
		mycluster.SwitchReplicationNoRelay()
	case "prov-db-force-write-config":
		mycluster.SwitchForceWriteConfig()
	case "backup-keep-until-valid":
		mycluster.SwitchBackupKeepUntilValid()
	case "mail-smtp-tls-skip-verify":
		mycluster.Conf.SwitchMailSmtpTlsSkipVerify()
	case "cloud18-shared":
		mycluster.Conf.SwitchCloud18Shared()
	case "cloud18-open-dbops":
		mycluster.SwitchCloud18OpenDbops()
	case "cloud18-subscribed-dbops":
		mycluster.SwitchCloud18SubscribedDbops()
	case "cloud18-open-sysops":
		mycluster.SwitchCloud18OpenSysops()
	case "cloud18-alert":
		mycluster.Conf.SwitchCloud18Alert()
		if mycluster.Conf.Cloud18Alert {
			mycluster.LogSlack.Activate("cloud18", true)
		} else {
			mycluster.LogSlack.Deactivate("cloud18", true)
		}
	case "topology-staging":
		mycluster.SwitchTopologyStaging()
	case "analyze-use-persistent":
		mycluster.SwitchAnalyzeUsePersistent()
	case "backup-restic-repo-append-cluster":
		mycluster.Conf.BackupResticRepoAppendCluster = !mycluster.Conf.BackupResticRepoAppendCluster
	case "log-plugin":
		mycluster.Conf.LogPlugin = !mycluster.Conf.LogPlugin
	case "monitoring-binlog-events":
		mycluster.SwitchMonitorBinlogEvents()
	default:
		return errors.New("setting not found")
	}
	mycluster.ConfigManager.SaveConfig(mycluster, false)
	return nil
}

// handlerMuxSetSettings handles the setting of settings for a given cluster.
// @Summary Set settings for a specific cluster
// @Description This endpoint sets the settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param settingName path string true "Setting Name"
// @Param settingValue path string true "Setting Value"
// @Success 200 {string} string "Successfully set setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue} [post]
func (repman *ReplicationManager) handlerMuxSetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	cName := vars["clusterName"]
	setting := vars["settingName"]
	value := ""
	if settingValue, ok := vars["settingValue"]; ok {
		value = settingValue
	}

	// Should be handled with global settings
	serverScope := config.IsScope(setting, "server")
	if serverScope {
		repman.handlerMuxSetGlobalSettings(w, r)
		return
	}

	mycluster := repman.getClusterByName(cName)
	if mycluster != nil {
		valid, delegator := repman.IsValidClusterACL(r, mycluster)
		if valid {
			err := repman.setClusterSetting(mycluster, setting, value)
			if err != nil {
				errCode := 500
				if err.Error() == "Setting not found" {
					errCode = 501
				}

				http.Error(w, "Failed to set cluster setting: "+err.Error(), errCode)
				return
			}

			if setting == "cloud18-dba-user-credentials" {
				err = repman.SendDBACredentialsMail(mycluster, "dbops", delegator)
				if err != nil {
					http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
					return
				}
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for %s in cluster %s", setting, vars["clusterName"]), http.StatusForbidden)
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

// handlerMuxSetCron handles the setting of cron jobs for a given cluster.
// @Summary Set cron jobs for a specific cluster
// @Description This endpoint sets the cron jobs for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param settingName path string true "Setting Name"
// @Param settingValue path string true "Setting Value"
// @Success 200 {string} string "Successfully set cron job"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/set-cron/{settingName}/{settingValue} [post]
func (repman *ReplicationManager) handlerMuxSetCron(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		setting := vars["settingName"]
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		cronValue, err := url.QueryUnescape(vars["settingValue"])
		if err != nil {
			http.Error(w, "Bad cron pattern", http.StatusBadRequest)
		}
		repman.setClusterSetting(mycluster, setting, cronValue)
		return
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

func setIsActive(value string) *bool {
	val := strings.ToLower(value)

	switch val {
	case "on", "true":
		b := true
		return &b
	case "off", "false":
		b := false
		return &b
	case "":
		return nil // nil means no change
	default:
		return nil // any other value also means no change
	}
}

func applyIsActive(oldValue bool, isactive *bool) bool {
	if isactive == nil {
		return oldValue
	}
	return *isactive
}

func isProvAppTemplateRepoSetting(name string) bool {
	switch name {
	case "prov-app-template-repo",
		"prov-app-template-repo-branch",
		"prov-app-template-repo-user",
		"prov-app-template-repo-password",
		"prov-app-template-repo-timeout":
		return true
	default:
		return false
	}
}

func parseProvAppTemplateRepoTimeout(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid value for prov-app-template-repo-timeout: %q", value)
	}
	if parsed < 1 || parsed > 600 {
		return 0, fmt.Errorf("prov-app-template-repo-timeout must be between 1 and 600")
	}
	return parsed, nil
}

func normalizeCompressionOverride(value string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" || trimmed == "auto" {
		return "auto", nil
	}
	switch trimmed {
	case "1", "true", "yes", "y", "on":
		return "true", nil
	case "0", "false", "no", "n", "off":
		return "false", nil
	default:
		return "", fmt.Errorf("invalid compression override: %q", value)
	}
}

var resticSizePattern = regexp.MustCompile(`^[0-9]+([kKmMgGtTpPeE][bB]?)?$`)

func splitResticList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ' ', '\t', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func validateResticPurgePathList(value string) error {
	for _, item := range splitResticList(value) {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if !filepath.IsAbs(trimmed) {
			return fmt.Errorf("backup-restic-purge-path must be absolute: %s", trimmed)
		}
	}
	return nil
}

func validateResticSizeValue(value, setting string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if !resticSizePattern.MatchString(trimmed) {
		return fmt.Errorf("invalid value for %s: %q", setting, value)
	}
	return nil
}

func resolveExecutablePath(value, setting string) (string, string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", "", nil
	}
	resolved := trimmed
	storeValue := trimmed
	if strings.ContainsRune(trimmed, filepath.Separator) {
		abs, err := filepath.Abs(trimmed)
		if err != nil {
			return "", "", fmt.Errorf("%s is invalid: %w", setting, err)
		}
		resolved = abs
		storeValue = abs
	} else {
		path, err := exec.LookPath(trimmed)
		if err != nil {
			return "", "", fmt.Errorf("%s not found in PATH: %s", setting, trimmed)
		}
		resolved = path
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("%s not found at %s: %w", setting, resolved, err)
	}
	if info.Mode().Perm()&0111 == 0 {
		return "", "", fmt.Errorf("%s is not executable: %s", setting, resolved)
	}
	return resolved, storeValue, nil
}

// Only decode logged values for settings that are base64-decoded on input.
var base64LogValueSettings = map[string]struct{}{
	"alert-script":                        {},
	"alert-slack-url":                     {},
	"alert-teams-proxy-url":               {},
	"alert-teams-url":                     {},
	"backup-load-script":                  {},
	"backup-logical-post-script":          {},
	"backup-mydumper-options":             {},
	"backup-mydumper-regex":               {},
	"backup-myloader-options":             {},
	"backup-mysqlclient-options":          {},
	"backup-mysqldump-options":            {},
	"backup-physical-post-script":         {},
	"backup-restic-aws-access-secret":     {},
	"backup-restic-aws-endpoint":          {},
	"backup-restic-aws-prefix":            {},
	"backup-restic-local-repository":      {},
	"backup-restic-password":              {},
	"backup-restic-repository":            {},
	"backup-save-script":                  {},
	"cloud18-alert-slack-url":             {},
	"cloud18-dba-user-credentials":        {},
	"cloud18-gitlab-password":             {},
	"cloud18-sponsor-user-credentials":    {},
	"mail-smtp-password":                  {},
	"topology-staging-post-detach-script": {},
	"topology-staging-refresh-script":     {},
}

func GetApiChangeLogFormat(name, value string) (string, []interface{}) {
	switch name {
	case "replication-credential", "db-servers-credential", "proxysql-servers-credential", "proxy-servers-backend-max-connections", "proxy-servers-backend-max-replication-lag", "maxscale-servers-credential", "shardproxy-servers-credential", "mail-smtp-password", "mail-smtp-user", "mail-to", "mail-from", "cloud18-gitlab-user", "cloud18-gitlab-password", "cloud18-domain-secret", "backup-restic-aws-access-key-id", "backup-restic-aws-access-secret", "backup-restic-password", "cloud18-dba-user-credentials", "cloud18-sponsor-user-credentials":
		return "API receive set setting %s to ****", []interface{}{name}
	default:
		logValue := value
		if _, ok := base64LogValueSettings[name]; ok {
			if decoded, ok := decodeBase64LogValue(value); ok {
				logValue = decoded
			}
		}
		return "API receive set setting %s to %s", []interface{}{name, logValue}
	}
}

func decodeBase64LogValue(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	if len(trimmed)%4 != 0 {
		return "", false
	}
	for i := 0; i < len(trimmed); i++ {
		switch c := trimmed[i]; {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '+' || c == '/' || c == '=':
		default:
			return "", false
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil || len(decoded) == 0 {
		return "", false
	}
	if !utf8.Valid(decoded) {
		return "", false
	}
	decodedValue := strings.TrimSpace(string(decoded))
	if decodedValue == "" {
		return "", false
	}
	return decodedValue, true
}

func (repman *ReplicationManager) setClusterSetting(mycluster *cluster.Cluster, name string, value string) error {
	var isactive = setIsActive(value)
	var err error
	trackedSecretsBefore := mycluster.TrackedSecretCompareSnapshot()

	fmtlog, logargs := GetApiChangeLogFormat(name, value)

	//not immutable
	if !mycluster.Conf.IsVariableImmutable(name) {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", fmtlog, logargs...)
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Overwriting an immutable parameter defined in config , please use config-merge command to preserve them between restart")
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", fmtlog, logargs...)
	}
	if isProvAppTemplateRepoSetting(name) && !mycluster.Conf.ProvAppTemplateRepoAllowOverride {
		return errors.New("cluster override is disabled for prov-app-template-repo* settings")
	}

	switch name {
	case "replication-credential":
		mycluster.SetReplicationCredential(value)
	case "failover-max-slave-delay":
		val, _ := strconv.ParseInt(value, 10, 64)
		mycluster.SetRplMaxDelay(val)
	case "switchover-wait-route-change":
		mycluster.SetSwitchoverWaitRouteChange(value)
	case "failover-limit":
		val, _ := strconv.Atoi(value)
		mycluster.SetFailLimit(val)
	case "backup-keep-last":
		err = mycluster.SetBackupKeepLastN(value)
		if err != nil {
			return err
		}
	case "backup-keep-hourly":
		err = mycluster.SetBackupKeepHourly(value)
		if err != nil {
			return err
		}
	case "backup-keep-daily":
		err = mycluster.SetBackupKeepDaily(value)
		if err != nil {
			return err
		}
	case "backup-keep-monthly":
		err = mycluster.SetBackupKeepMonthly(value)
		if err != nil {
			return err
		}
	case "backup-keep-weekly":
		err = mycluster.SetBackupKeepWeekly(value)
		if err != nil {
			return err
		}
	case "backup-keep-yearly":
		err = mycluster.SetBackupKeepYearly(value)
		if err != nil {
			return err
		}
	case "backup-keep-within":
		err = mycluster.SetBackupKeepWithin(value)
		if err != nil {
			return err
		}
	case "backup-splitdump-file-size":
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			if _, err := splitdump.ParseSizeBytes(trimmed); err != nil {
				return fmt.Errorf("invalid value for backup-splitdump-file-size: %q", value)
			}
		}
		mycluster.Conf.BackupSplitdumpFileSize = trimmed
	case "backup-splitdump-create-databases":
		mycluster.Conf.BackupSplitdumpCreateDatabases = applyIsActive(mycluster.Conf.BackupSplitdumpCreateDatabases, isactive)
	case "backup-restore-definer-strict":
		mycluster.Conf.BackupRestoreDefinerStrict = applyIsActive(mycluster.Conf.BackupRestoreDefinerStrict, isactive)
	case "backup-keep-within-hourly":
		err = mycluster.SetBackupKeepWithinHourly(value)
		if err != nil {
			return err
		}
	case "backup-keep-within-daily":
		err = mycluster.SetBackupKeepWithinDaily(value)
		if err != nil {
			return err
		}
	case "backup-keep-within-monthly":
		err = mycluster.SetBackupKeepWithinMonthly(value)
		if err != nil {
			return err
		}
	case "backup-keep-within-weekly":
		err = mycluster.SetBackupKeepWithinWeekly(value)
		if err != nil {
			return err
		}
	case "backup-keep-within-yearly":
		err = mycluster.SetBackupKeepWithinYearly(value)
		if err != nil {
			return err
		}
	case "backup-disk-treshold-warn":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.BackupDiskTresholdWarn = val
	case "backup-disk-treshold-crit":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.BackupDiskTresholdCrit = val
	case "backup-estimate-size-percentage":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.BackupEstimateSizePercentage = val
	case "backup-growth-percentage":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.BackupGrowthPercentage = val
	case "compress-backups-parallel-blocks":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for compress-backups-parallel-blocks: %q", value)
		}
		if val < 1 || val > 32 {
			return fmt.Errorf("compress-backups-parallel-blocks must be between 1 and 32, got %d", val)
		}
		mycluster.Conf.CompressBackupsParallelBlocks = val
	case "compress-backups-decompress-buffer-size":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for compress-backups-decompress-buffer-size: %q", value)
		}
		if val <= 0 {
			return fmt.Errorf("compress-backups-decompress-buffer-size must be greater than 0, got %d", val)
		}
		mycluster.Conf.CompressBackupsDecompressBufferSize = val
	case "backup-reseed-remote-decompress":
		mycluster.Conf.BackupReseedRemoteDecompress = applyIsActive(mycluster.Conf.BackupReseedRemoteDecompress, isactive)
	case "compress-backups-logical":
		normalized, err := normalizeCompressionOverride(value)
		if err != nil {
			return err
		}
		mycluster.Conf.CompressBackupsLogical = normalized
	case "compress-backups-physical":
		normalized, err := normalizeCompressionOverride(value)
		if err != nil {
			return err
		}
		mycluster.Conf.CompressBackupsPhysical = normalized
	case "compress-backups-compression-level":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for compress-backups-compression-level: %q", value)
		}
		if val < 1 || val > 9 {
			return fmt.Errorf("compress-backups-compression-level must be between 1 and 9, got %d", val)
		}
		mycluster.Conf.CompressBackupsCompressionLevel = val
	case "backup-restic-purge-oldest-on-disk-space":
		mycluster.Conf.BackupResticPurgeOldestOnDiskSpace = applyIsActive(mycluster.Conf.BackupResticPurgeOldestOnDiskSpace, isactive)
	case "backup-restic-allow-unsafe-mount":
		mycluster.Conf.BackupResticAllowUnsafeMount = applyIsActive(mycluster.Conf.BackupResticAllowUnsafeMount, isactive)
		if mycluster.ResticManager != nil {
			mycluster.ResticManager.AllowUnsafeMount = mycluster.Conf.BackupResticAllowUnsafeMount
		}
	case "backup-restic-mount-target-dir":
		mycluster.Conf.BackupResticMountTargetDir = strings.TrimSpace(value)
	case "backup-restic-mount-host":
		mycluster.Conf.BackupResticMountHost = strings.TrimSpace(value)
	case "backup-restic-mount-tag":
		err = mycluster.SetBackupResticMountTag(value)
		if err != nil {
			return err
		}
	case "backup-restic-mount-path":
		mycluster.Conf.BackupResticMountPath = strings.TrimSpace(value)
	case "backup-restic-mount-path-template":
		mycluster.Conf.BackupResticMountPathTemplate = strings.TrimSpace(value)
	case "backup-restic-mount-time-template":
		mycluster.Conf.BackupResticMountTimeTemplate = strings.TrimSpace(value)
	case "backup-restic-mount-allow-other":
		mycluster.Conf.BackupResticMountAllowOther = applyIsActive(mycluster.Conf.BackupResticMountAllowOther, isactive)
	case "backup-restic-mount-no-default-permissions":
		mycluster.Conf.BackupResticMountNoDefaultPermissions = applyIsActive(mycluster.Conf.BackupResticMountNoDefaultPermissions, isactive)
	case "backup-restic-mount-owner-root":
		mycluster.Conf.BackupResticMountOwnerRoot = applyIsActive(mycluster.Conf.BackupResticMountOwnerRoot, isactive)
	case "backup-restic-mount-no-lock":
		mycluster.Conf.BackupResticMountNoLock = applyIsActive(mycluster.Conf.BackupResticMountNoLock, isactive)
	case "backup-restic-mount-verbose":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for backup-restic-mount-verbose: %q", value)
		}
		if val < 0 || val > 3 {
			return fmt.Errorf("backup-restic-mount-verbose must be between 0 and 3, got %d", val)
		}
		mycluster.Conf.BackupResticMountVerbose = val
	case "backup-restic-mount-quiet":
		mycluster.Conf.BackupResticMountQuiet = applyIsActive(mycluster.Conf.BackupResticMountQuiet, isactive)
	case "backup-restic-purge-oldest-on-disk-threshold":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.BackupResticPurgeOldestOnDiskThreshold = val
	case "backup-restic-tags":
		err = mycluster.SetBackupResticTags(value)
		if err != nil {
			return err
		}
	case "backup-restic-host":
		err = mycluster.SetBackupResticHost(value)
		if err != nil {
			return err
		}
	case "backup-restic-purge-group-by":
		err = mycluster.SetBackupResticPurgeGroupBy(value)
		if err != nil {
			return err
		}
	case "backup-restic-purge-keep-tag":
		err = mycluster.SetBackupResticPurgeKeepTag(value)
		if err != nil {
			return err
		}
	case "backup-restic-purge-host":
		mycluster.Conf.BackupResticPurgeHost = strings.TrimSpace(value)
	case "backup-restic-purge-tag":
		err = mycluster.SetBackupResticPurgeTag(value)
		if err != nil {
			return err
		}
	case "backup-restic-purge-path":
		if err := validateResticPurgePathList(value); err != nil {
			return err
		}
		mycluster.Conf.BackupResticPurgePath = strings.TrimSpace(value)
	case "backup-restic-purge-prune-max-unused":
		if err := validateResticSizeValue(value, "backup-restic-purge-prune-max-unused"); err != nil {
			return err
		}
		mycluster.Conf.BackupResticPurgePruneMaxUnused = strings.TrimSpace(value)
	case "backup-restic-purge-prune-max-repack-size":
		if err := validateResticSizeValue(value, "backup-restic-purge-prune-max-repack-size"); err != nil {
			return err
		}
		mycluster.Conf.BackupResticPurgePruneMaxRepackSize = strings.TrimSpace(value)
	case "backup-restic-timeout":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for backup-restic-timeout: %q", value)
		}
		mycluster.Conf.BackupResticTimeout = val
		if mycluster.ResticManager != nil {
			mycluster.ResticManager.SetOperationTimeout(mycluster.Conf.GetResticTimeout())
		}
	case "backup-restic-dump-timeout":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for backup-restic-dump-timeout: %q", value)
		}
		mycluster.Conf.BackupResticDumpTimeout = val
		if mycluster.ResticManager != nil {
			mycluster.ResticManager.SetDumpTimeout(mycluster.Conf.GetResticDumpTimeout())
		}
	case "backup-restic-dir-mode":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for backup-restic-dir-mode: %q", value)
		}
		oldValue := mycluster.Conf.BackupResticDirMode
		mycluster.Conf.BackupResticDirMode = val
		if err := mycluster.Conf.ValidateResticPermissions(); err != nil {
			mycluster.Conf.BackupResticDirMode = oldValue
			return err
		}
		if mycluster.ResticManager != nil {
			mycluster.ResticManager.SetPermissions(mycluster.Conf.GetResticDirMode(), mycluster.Conf.GetResticFileMode())
		}
	case "backup-restic-file-mode":
		val, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for backup-restic-file-mode: %q", value)
		}
		oldValue := mycluster.Conf.BackupResticFileMode
		mycluster.Conf.BackupResticFileMode = val
		if err := mycluster.Conf.ValidateResticPermissions(); err != nil {
			mycluster.Conf.BackupResticFileMode = oldValue
			return err
		}
		if mycluster.ResticManager != nil {
			mycluster.ResticManager.SetPermissions(mycluster.Conf.GetResticDirMode(), mycluster.Conf.GetResticFileMode())
		}
	case "backup-logical-type":
		mycluster.SetBackupLogicalType(value)
	case "backup-physical-type":
		mycluster.SetBackupPhysicalType(value)
	case "backup-binlog-type":
		mycluster.SetBackupBinlogType(value)
	case "backup-binlog-script":
		mycluster.SetBackupBinlogScript(value)
	case "binlog-parse-mode":
		mycluster.SetBinlogParseMode(value)
	case "db-servers-hosts":
		mycluster.SetDbServerHosts(value)
	case "db-servers-credential":
		mycluster.Conf.User = value
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.User
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("db-servers-credential")
		mycluster.Conf.Secrets["db-servers-credential"] = new_secret
		mycluster.SetClusterMonitorCredentialsFromConfig()
		// mycluster.SetDbServersMonitoringCredential(value)
	case "prov-service-plan":
		mycluster.SetServicePlan(value)
	case "prov-net-cni-cluster":
		mycluster.SetProvNetCniCluster(value)
	case "prov-orchestrator-cluster":
		mycluster.SetProvOrchestratorCluster(value)
	case "prov-db-disk-size":
		mycluster.SetDBDiskSize(value)
	case "prov-db-cpu-cores":
		mycluster.SetDBCores(value)
	case "prov-db-memory":
		mycluster.SetDBMemorySize(value)
	case "prov-db-disk-iops":
		mycluster.SetDBDiskIOPS(value)
	case "prov-db-max-connections":
		mycluster.SetDBMaxConnections(value)
	case "prov-db-expire-log-days":
		mycluster.SetDBExpireLogDays(value)
	case "prov-db-agents":
		mycluster.SetProvDbAgents(value)
	case "prov-proxy-agents":
		mycluster.SetProvProxyAgents(value)
	case "prov-orchestrator":
		mycluster.SetProvOrchestrator(value)
	case "prov-sphinx-img":
		mycluster.SetProvSphinxImage(value)
	case "prov-db-image":
		mycluster.SetProvDBImage(value)
	case "prov-db-disk-type":
		mycluster.SetProvDbDiskType(value)
	case "prov-db-disk-fs":
		mycluster.SetProvDbDiskFS(value)
	case "prov-db-disk-pool":
		mycluster.SetProvDbDiskPool(value)
	case "prov-db-disk-device":
		mycluster.SetProvDbDiskDevice(value)
	case "prov-db-service-type":
		mycluster.SetProvDbServiceType(value)
	case "proxysql-servers-credential":
		mycluster.SetProxyServersCredential(value, config.ConstProxySqlproxy)
	case "proxy-servers-backend-max-connections":
		mycluster.SetProxyServersBackendMaxConnections(value)
	case "proxy-servers-backend-max-replication-lag":
		mycluster.SetProxyServersBackendMaxReplicationLag(value)
	case "maxscale-servers-credential":
		mycluster.SetProxyServersCredential(value, config.ConstProxyMaxscale)
	case "shardproxy-servers-credential":
		mycluster.SetProxyServersCredential(value, config.ConstProxySpider)
	case "prov-proxy-disk-size":
		mycluster.SetProxyDiskSize(value)
	case "prov-proxy-cpu-cores":
		mycluster.SetProxyCores(value)
	case "prov-proxy-memory":
		mycluster.SetProxyMemorySize(value)
	case "prov-proxy-docker-proxysql-img":
		mycluster.SetProvProxySQLImage(value)
	case "prov-proxy-docker-maxscale-img":
		mycluster.SetProvMaxscaleImage(value)
	case "prov-proxy-docker-haproxy-img":
		mycluster.SetProvHaproxyImage(value)
	case "prov-proxy-docker-shardproxy-img":
		mycluster.SetProvShardproxyImage(value)
	case "prov-proxy-disk-type":
		mycluster.SetProvProxyDiskType(value)
	case "prov-proxy-disk-fs":
		mycluster.SetProvProxyDiskFS(value)
	case "prov-proxy-disk-pool":
		mycluster.SetProvProxyDiskPool(value)
	case "prov-proxy-disk-device":
		mycluster.SetProvProxyDiskDevice(value)
	case "prov-proxy-service-type":
		mycluster.SetProvProxyServiceType(value)
	case "monitoring-address":
		mycluster.SetMonitoringAddress(value)
	case "scheduler-db-servers-logical-backup-cron":
		mycluster.SetSchedulerDbServersLogicalBackupCron(value)
	case "scheduler-db-servers-logs-cron":
		mycluster.SetSchedulerDbServersLogsCron(value)
	case "scheduler-db-servers-logs-table-rotate-cron":
		mycluster.SetSchedulerDbServersLogsTableRotateCron(value)
	case "scheduler-db-servers-optimize-cron":
		mycluster.SetSchedulerDbServersOptimizeCron(value)
	case "scheduler-db-servers-analyze-cron":
		mycluster.SetSchedulerDbServersAnalyzeCron(value)
	case "scheduler-db-servers-physical-backup-cron":
		mycluster.SetSchedulerDbServersPhysicalBackupCron(value)
	case "scheduler-rolling-reprov-cron":
		mycluster.SetSchedulerRollingReprovCron(value)
	case "scheduler-rolling-restart-cron":
		mycluster.SetSchedulerRollingRestartCron(value)
	case "scheduler-sla-rotate-cron":
		mycluster.SetSchedulerSlaRotateCron(value)
	case "scheduler-jobs-ssh-cron":
		mycluster.SetSchedulerJobsSshCron(value)
	case "scheduler-alert-disable-cron":
		mycluster.SetSchedulerAlertDisableCron(value)
	case "monitoring-schema-scheduler-cron":
		mycluster.SetMonitoringSchemaSchedulerCron(value)
	case "monitoring-checksum-scheduler-cron":
		mycluster.SetMonitoringChecksumSchedulerCron(value)
	case "monitoring-schema-ignore-tables":
		mycluster.SetMonitoringSchemaIgnoreTables(value)
	case "backup-binlogs-keep":
		mycluster.SetBackupBinlogsKeep(value)
	case "delay-stat-rotate":
		mycluster.SetDelayStatRotate(value)
	case "print-delay-stat-interval":
		mycluster.SetPrintDelayStatInterval(value)
	case "log-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevel(val)
	case "log-writer-election-level", "log-level-writer-election":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogWriterElectionLevel(val)
	case "log-sst-level", "log-level-sst":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogSSTLevel(val)
	case "log-heartbeat-level", "log-level-heartbeat":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogHeartbeatLevel(val)
	case "log-sql-level", "log-level-sql":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogSQLLevel(val)
	case "log-config-load-level", "log-level-config-load":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogConfigLoadLevel(val)
	case "log-git-level", "log-level-git":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.SetLogGitLevel(val)
	case "log-support-level", "log-level-support":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.SetLogSupportLevel(val)
	case "log-backup-stream-level", "log-level-backup-stream":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogBackupStreamLevel(val)
	case "log-orchestrator-level", "log-level-orchestrator":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogOrchestratorLevel(val)
	case "log-vault-level", "log-level-vault":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogVaultLevel(val)
	case "log-topology-level", "log-level-topology":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogTopologyLevel(val)
	case "log-proxy-level", "log-level-proxy":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogProxyLevel(val)
	case "proxysql-log-level", "log-level-proxysql":
		val, _ := strconv.Atoi(value)
		mycluster.SetProxysqlLogLevel(val)
	case "haproxy-log-level", "log-level-haproxy":
		val, _ := strconv.Atoi(value)
		mycluster.SetHaproxyLogLevel(val)
	case "proxyjanitor-log-level", "log-level-proxyjanitor":
		val, _ := strconv.Atoi(value)
		mycluster.SetProxyJanitorLogLevel(val)
	case "maxscale-log-level", "log-level-maxscale":
		val, _ := strconv.Atoi(value)
		mycluster.SetMxsLogLevel(val)
	case "force-binlog-purge-total-size":
		val, _ := strconv.Atoi(value)
		mycluster.SetForceBinlogPurgeTotalSize(val)
	case "force-binlog-purge-min-replica":
		val, _ := strconv.Atoi(value)
		mycluster.SetForceBinlogPurgeMinReplica(val)
	case "log-graphite-level", "log-level-graphite":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogGraphiteLevel(val)
	case "log-binlog-purge-level", "log-level-binlog-purge":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogBinlogPurgeLevel(val)
	case "log-archive-level", "log-level-restic":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogResticLevel(val)
	case "log-mailer-level", "log-level-mailer":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogMailerLevel(val)
	case "graphite-whitelist-template":
		mycluster.SetGraphiteWhitelistTemplate(value)
	case "topology-target":
		mycluster.BootstrapTopology(value)
	case "log-task-level", "log-level-task":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogTaskLevel(val)
	case "log-optimize-level", "log-level-database-optimize":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevelDatabaseOptimize(val)
	case "log-external-script-level", "log-level-external-script":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogExternalScriptLevel(val)
	case "log-stats-level", "log-level-stats":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.LogStatsLevel = val
	case "log-fetch-errorlog-level", "log-level-database-errors":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevelDatabaseErrors(val)
	case "log-fetch-sqlerrorlog-level", "log-level-database-sql-errors":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevelDatabaseSqlErrors(val)
	case "log-fetch-slowquery-level", "log-level-database-slowquery":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevelDatabaseSlowquery(val)
	case "log-fetch-auditlog-level", "log-level-database-audit":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevelDatabaseAudit(val)
	case "monitoring-ignore-errors":
		mycluster.SetMonitorIgnoreErrors(value)
	case "monitoring-capture-trigger":
		mycluster.SetMonitorCaptureTrigger(value)
	case "api-token-timeout":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.SetApiTokenTimeout(val)
	case "sst-send-buffer":
		val, _ := strconv.Atoi(value)
		mycluster.SetSSTBufferSize(val)
	case "alert-pushover-app-token":
		mycluster.SetAlertPushoverAppToken(value)
	case "alert-pushover-user-token":
		mycluster.SetAlertPushoverUserToken(value)
	case "alert-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.SetAlertScript(string(val))
	case "alert-slack-channel":
		mycluster.SetAlertSlackChannel(value)
	case "alert-slack-url":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.SetAlertSlackUrl(string(val))
	case "alert-slack-user":
		mycluster.SetAlertSlackUser(value)
	case "cloud18-alert":
		oldValue := mycluster.Conf.Cloud18Alert
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.Conf.Cloud18Alert = newValue
			if mycluster.Conf.Cloud18Alert {
				mycluster.LogSlack.Activate("cloud18", true)
			} else {
				mycluster.LogSlack.Deactivate("cloud18", true)
			}
		}
	case "cloud18-alert-slack-channel":
		mycluster.SetCloud18AlertSlackChannel(value)
	case "cloud18-alert-slack-url":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.SetCloud18AlertSlackUrl(string(val))
	case "cloud18-alert-slack-user":
		mycluster.SetCloud18AlertSlackUser(value)
	case "alert-teams-proxy-url":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.SetAlertTeamsProxyUrl(string(val))
	case "alert-teams-state":
		mycluster.SetAlertTeamsState(value)
	case "alert-teams-url":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.SetAlertTeamsUrl(string(val))
	case "monitoring-alert-trigger":
		mycluster.SetMonitoringAlertTriggerl(value)
	case "mail-smtp-addr":
		mycluster.Conf.SetMailSmtpAddr(value)
	case "mail-smtp-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.MailSMTPPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.MailSMTPPassword
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("mail-smtp-password")
		mycluster.Conf.Secrets["mail-smtp-password"] = new_secret
		mycluster.Mailer.UpdateAuth(mycluster.Conf.MailSMTPUser, new_secret.Value)
	case "mail-smtp-user":
		mycluster.Conf.SetMailSmtpUser(value)
		mycluster.Mailer.UpdateAuth(value, mycluster.Conf.GetDecryptedValue("mail-smtp-password"))
	case "mail-to":
		mycluster.Conf.SetMailTo(value)
	case "mail-from":
		mycluster.Conf.SetMailFrom(value)
		mycluster.Mailer.SetFrom(value)
	case "scheduler-alert-disable-time":
		val, _ := strconv.Atoi(value)
		mycluster.SetSchedulerAlertDisableTime(val)
	case "cloud18":
		mycluster.Conf.Cloud18 = (value == "true")
		if mycluster.Conf.Cloud18 && mycluster.Conf.Cloud18Alert {
			mycluster.LogSlack.Activate("cloud18", true)
		} else {
			mycluster.LogSlack.Deactivate("cloud18", true)
		}
	case "cloud18-domain":
		mycluster.Conf.Cloud18Domain = value
	case "cloud18-sub-domain":
		mycluster.Conf.Cloud18SubDomain = value
	case "cloud18-sub-domain-zone":
		mycluster.Conf.Cloud18SubDomainZone = value
	case "cloud18-gitlab-user":
		mycluster.Conf.Cloud18GitUser = value
	case "cloud18-gitlab-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.Cloud18GitPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.Cloud18GitPassword
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("cloud18-gitlab-password")
		mycluster.Conf.Secrets["cloud18-gitlab-password"] = new_secret
	case "cloud18-domain-secret":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.Cloud18DomainSecret = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.Cloud18DomainSecret
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("cloud18-domain-secret")
		mycluster.Conf.Secrets["cloud18-domain-secret"] = new_secret
	case "cloud18-platform-description":
		mycluster.Conf.Cloud18PlatformDescription = value
	case "log-level-file":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.LogFileLevel = val
	case "backup-restic-local-repository":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupResticLocalRepository = string(val)
		mycluster.ReloadResticEnv()
	case "backup-restic-repository":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupResticRepository = string(val)
		mycluster.ReloadResticEnv()
	case "backup-restic-aws-access-key-id":
		mycluster.Conf.BackupResticAwsAccessKeyId = value
	case "backup-restic-aws-access-secret":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupResticAwsAccessSecret = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.BackupResticAwsAccessSecret
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret")
		mycluster.Conf.Secrets["backup-restic-aws-access-secret"] = new_secret
	case "backup-restic-aws-region":
		mycluster.Conf.BackupResticAwsRegion = value
		mycluster.ReloadResticEnv()
	case "backup-restic-aws-endpoint":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupResticAwsEndpoint = string(val)
		mycluster.ReloadResticEnv()
	case "backup-restic-aws-bucket":
		mycluster.Conf.BackupResticAwsBucket = value
		mycluster.ReloadResticEnv()
	case "backup-restic-aws-prefix":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupResticAwsPrefix = string(val)
		mycluster.ReloadResticEnv()
	case "backup-restic-additional-env":
		if err := cluster.ValidateResticAdditionalEnvOverrides(value); err != nil {
			return fmt.Errorf("invalid backup-restic-additional-env: %w", err)
		}
		mycluster.Conf.BackupResticAdditionalEnv = value
		mycluster.ReloadResticEnv()
	case "backup-restic-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		newval := string(val)
		if mycluster.Conf.BackupRestic {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic backup is already enabled, testing new password validity")

			// Test if new password is valid. If yes, can be used directly
			err = mycluster.ResticManager.TestPassword(newval)
			// If not valid, test if old password is valid (rotate password)
			if err != nil {
				err2 := mycluster.ResticManager.TestPassword(mycluster.Conf.GetDecryptedValue("backup-restic-password"))
				if err2 == nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Old restic password is valid, rotating password to new one")

					// Old password is valid, we can use it to rotate password using ChangeResticRepoPassword
					err = mycluster.ChangeResticRepoPassword(newval)
					if err != nil {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unable to change restic password: "+err.Error())
						return errors.New("Unable to change restic password: " + err.Error())
					}
				} else {
					// Old password is not valid, just use new password (maybe first time setup)
					// If both old and new password are not valid, this will be detected later when trying to do a backup
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Both old and new restic password are not valid, assuming first time setup")
					mycluster.SetResticPassword(newval)
				}
			} else {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "New restic password is valid, using it")
				mycluster.SetResticPassword(newval)
			}
		} else {
			mycluster.SetResticPassword(newval)
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic password updated")

	case "backup-mydumper-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupMyDumperOptions = string(val)
	case "backup-mydumper-regex":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupMyDumperRegex = string(val)
	case "backup-myloader-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupMyLoaderOptions = string(val)
	case "backup-mysqldump-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupMysqldumpOptions = string(val)
	case "backup-mysqlclient-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupMysqlclientOptions = string(val)
	case "replication-manager-cli-path":
		_, storeValue, err := resolveExecutablePath(value, "replication-manager-cli-path")
		if err != nil {
			return err
		}
		mycluster.Conf.ReplicationManagerCliPath = storeValue
	case "backup-logical-post-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupLogicalPostScript = string(val)
	case "backup-physical-post-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupPhysicalPostScript = string(val)
	case "cloud18-monthly-infra-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlyInfraCost = val
	case "cloud18-monthly-license-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlyLicenseCost = val
	case "cloud18-monthly-sysops-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlySysopsCost = val
	case "cloud18-monthly-dbops-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlyDbopsCost = val
	case "cloud18-cost-currency":
		mycluster.Conf.Cloud18CostCurrency = value
	case "cloud18-database-read-write-split-srv-record":
		mycluster.SetCloud18DatabaseReadWriteSplitSrvRecord(value)
	case "cloud18-database-read-srv-record":
		mycluster.SetCloud18DatabaseReadSrvRecord(value)
	case "cloud18-database-read-write-srv-record":
		mycluster.SetCloud18DatabaseReadWriteSrvRecord(value)
	case "cloud18-dba-user-credentials":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		err = mycluster.SetDatabaseCredentials("dba", string(val))
		if err != nil {
			return err
		}
	case "cloud18-sponsor-user-credentials":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		err = mycluster.SetDatabaseCredentials("sponsor", string(val))
		if err != nil {
			return err
		}
	case "cloud18-cloud18-dbops":
		if value != "" && value != mycluster.Conf.Cloud18GitUser {
			dbops := repman.CreateDBOpsForm(value)
			if dbuser, ok := mycluster.APIUsers[value]; !ok {
				err = mycluster.AddUser(dbops, mycluster.Conf.Cloud18GitUser, true)
			} else {
				dbops.Grants = mycluster.AppendGrants(dbops.Grants, &dbuser)
				dbops.Roles = mycluster.AppendRoles(dbops.Roles, &dbuser)
				err = mycluster.UpdateUser(dbops, mycluster.Conf.Cloud18GitUser, true)
			}

			if err != nil {
				return err
			}

			mycluster.Conf.Cloud18DbOps = value
		}
	case "backup-save-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupSaveScript = string(val)
	case "backup-load-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.BackupLoadScript = string(val)
	case "topology-staging-refresh-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.TopologyStagingRefreshScript = string(val)
	case "topology-staging-post-detach-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("unable to decode")
		}
		mycluster.Conf.TopologyStagingPostDetachScript = string(val)
	case "replication-multisource-head-clusters":
		mycluster.Conf.ReplicationMultisourceHeadClusters = value
	case "replication-source-name":
		mycluster.Conf.MasterConn = value
	case "replication-master-retry-count":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.MasterRetryCount = val
	case "db-servers-tls-ssl-mode":
		mycluster.Conf.HostsTlsSslMode = value

	// Switches
	case "verbose":
		mycluster.Conf.Verbose = applyIsActive(mycluster.Conf.Verbose, isactive)
	case "failover-mode":
		mycluster.SetInteractive(applyIsActive(mycluster.Conf.Interactive, isactive))
	case "failover-readonly-state":
		mycluster.SetReadOnly(applyIsActive(mycluster.Conf.ReadOnly, isactive))
		mycluster.Configurator.Init(*mycluster.Conf, mycluster.Logrus)
	case "failover-restart-unsafe":
		mycluster.Conf.FailRestartUnsafe = applyIsActive(mycluster.Conf.FailRestartUnsafe, isactive)
	case "failover-at-sync":
		mycluster.Conf.FailSync = applyIsActive(mycluster.Conf.FailSync, isactive)
	case "failover-divergent-data":
		mycluster.Conf.FailoverDivergentData = applyIsActive(mycluster.Conf.FailoverDivergentData, isactive)
	case "force-slave-no-gtid-mode":
		mycluster.Conf.ForceSlaveNoGtid = applyIsActive(mycluster.Conf.ForceSlaveNoGtid, isactive)
	case "switchover-lower-release":
		mycluster.Conf.SwitchLowerRelease = applyIsActive(mycluster.Conf.SwitchLowerRelease, isactive)
	case "failover-event-status":
		mycluster.Conf.FailEventStatus = applyIsActive(mycluster.Conf.FailEventStatus, isactive)
	case "failover-event-scheduler":
		mycluster.Conf.FailEventScheduler = applyIsActive(mycluster.Conf.FailEventScheduler, isactive)
	case "delay-stat-capture":
		mycluster.Conf.DelayStatCapture = applyIsActive(mycluster.Conf.DelayStatCapture, isactive)
		if !mycluster.Conf.DelayStatCapture {
			mycluster.Conf.FailoverCheckDelayStat = false
			mycluster.Conf.PrintDelayStat = false
			mycluster.Conf.PrintDelayStatHistory = false
		}
	case "print-delay-stat":
		mycluster.Conf.PrintDelayStat = applyIsActive(mycluster.Conf.PrintDelayStat, isactive)
	case "print-delay-stat-history":
		mycluster.Conf.PrintDelayStatHistory = applyIsActive(mycluster.Conf.PrintDelayStatHistory, isactive)
	case "failover-check-delay-stat":
		mycluster.Conf.FailoverCheckDelayStat = applyIsActive(mycluster.Conf.FailoverCheckDelayStat, isactive)
	case "autorejoin":
		mycluster.Conf.Autorejoin = applyIsActive(mycluster.Conf.Autorejoin, isactive)
	case "autoseed":
		mycluster.Conf.Autoseed = applyIsActive(mycluster.Conf.Autoseed, isactive)
	case "autorejoin-backup-binlog":
		mycluster.Conf.AutorejoinBackupBinlog = applyIsActive(mycluster.Conf.AutorejoinBackupBinlog, isactive)
	case "autorejoin-flashback":
		mycluster.Conf.AutorejoinFlashback = applyIsActive(mycluster.Conf.AutorejoinFlashback, isactive)
	case "autorejoin-flashback-on-sync":
		mycluster.Conf.AutorejoinSemisync = applyIsActive(mycluster.Conf.AutorejoinSemisync, isactive)
	case "autorejoin-slave-positional-heartbeat":
		mycluster.Conf.AutorejoinSlavePositionalHeartbeat = applyIsActive(mycluster.Conf.AutorejoinSlavePositionalHeartbeat, isactive)
	case "autorejoin-zfs-flashback":
		mycluster.Conf.AutorejoinZFSFlashback = applyIsActive(mycluster.Conf.AutorejoinZFSFlashback, isactive)
	case "autorejoin-mysqldump":
		mycluster.Conf.AutorejoinMysqldump = applyIsActive(mycluster.Conf.AutorejoinMysqldump, isactive)
	case "autorejoin-logical-backup":
		mycluster.Conf.AutorejoinLogicalBackup = applyIsActive(mycluster.Conf.AutorejoinLogicalBackup, isactive)
	case "autorejoin-physical-backup":
		mycluster.Conf.AutorejoinPhysicalBackup = applyIsActive(mycluster.Conf.AutorejoinPhysicalBackup, isactive)
	case "autorejoin-force-restore":
		mycluster.Conf.AutorejoinForceRestore = applyIsActive(mycluster.Conf.AutorejoinForceRestore, isactive)
	case "switchover-at-sync":
		mycluster.Conf.SwitchSync = applyIsActive(mycluster.Conf.SwitchSync, isactive)
	case "switchover-lock-user-on-freeze":
		mycluster.Conf.SwitchLockUserOnFreeze = applyIsActive(mycluster.Conf.SwitchLockUserOnFreeze, isactive)
	case "check-replication-filters":
		mycluster.Conf.CheckReplFilter = applyIsActive(mycluster.Conf.CheckReplFilter, isactive)
	case "check-replication-state":
		mycluster.Conf.RplChecks = applyIsActive(mycluster.Conf.RplChecks, isactive)
	case "scheduler-db-servers-logical-backup":
		oldValue := mycluster.Conf.SchedulerBackupLogical
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerBackupLogical()
		}
	case "scheduler-db-servers-physical-backup":
		oldValue := mycluster.Conf.SchedulerBackupPhysical
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerBackupPhysical()
		}
	case "scheduler-db-servers-logs":
		oldValue := mycluster.Conf.SchedulerDatabaseLogs
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerDatabaseLogs()
		}
	case "scheduler-jobs-ssh":
		oldValue := mycluster.Conf.SchedulerJobsSSH
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerDbJobsSsh()
		}
	case "scheduler-db-servers-logs-table-rotate":
		oldValue := mycluster.Conf.SchedulerDatabaseLogsTableRotate
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerDatabaseLogsTableRotate()
		}
	case "scheduler-rolling-restart":
		oldValue := mycluster.Conf.SchedulerRollingRestart
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerRollingRestart()
		}
	case "scheduler-rolling-reprov":
		oldValue := mycluster.Conf.SchedulerRollingReprov
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerRollingReprov()
		}
	case "scheduler-db-servers-optimize":
		oldValue := mycluster.Conf.SchedulerDatabaseOptimize
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerDatabaseOptimize()
		}
	case "scheduler-db-servers-analyze":
		oldValue := mycluster.Conf.SchedulerDatabaseAnalyze
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchSchedulerDatabaseAnalyze()
		}
	case "monitoring-schema-scheduler":
		oldValue := mycluster.Conf.MonitorSchemaScheduler
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchMonitoringSchemaScheduler()
		}
	case "monitoring-checksum-scheduler":
		oldValue := mycluster.Conf.MonitorChecksumScheduler
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.SwitchMonitoringChecksumScheduler()
		}
	case "scheduler-alert-disable":
		mycluster.Conf.SchedulerAlertDisable = applyIsActive(mycluster.Conf.SchedulerAlertDisable, isactive)
	case "graphite-metrics":
		mycluster.Conf.GraphiteMetrics = applyIsActive(mycluster.Conf.GraphiteMetrics, isactive)
	case "graphite-embedded":
		mycluster.Conf.GraphiteEmbedded = applyIsActive(mycluster.Conf.GraphiteEmbedded, isactive)
	case "graphite-whitelist":
		mycluster.Conf.GraphiteWhitelist = applyIsActive(mycluster.Conf.GraphiteWhitelist, isactive)
	case "graphite-blacklist":
		mycluster.Conf.GraphiteBlacklist = applyIsActive(mycluster.Conf.GraphiteBlacklist, isactive)
	case "shardproxy-copy-grants":
		mycluster.Conf.MdbsProxyCopyGrants = applyIsActive(mycluster.Conf.MdbsProxyCopyGrants, isactive)
	case "proxysql-copy-grants", "proxysql-bootstrap-users":
		mycluster.Conf.ProxysqlCopyGrants = applyIsActive(mycluster.Conf.ProxysqlCopyGrants, isactive)
	case "proxysql-bootstrap-variables":
		mycluster.Conf.ProxysqlBootstrapVariables = applyIsActive(mycluster.Conf.ProxysqlBootstrapVariables, isactive)
	case "proxysql-bootstrap-hostgroups":
		mycluster.Conf.ProxysqlBootstrapHG = applyIsActive(mycluster.Conf.ProxysqlBootstrapHG, isactive)
	case "proxysql-bootstrap", "proxysql-bootstrap-servers":
		mycluster.Conf.ProxysqlBootstrap = applyIsActive(mycluster.Conf.ProxysqlBootstrap, isactive)
	case "proxysql-bootstrap-query-rules":
		mycluster.Conf.ProxysqlBootstrapQueryRules = applyIsActive(mycluster.Conf.ProxysqlBootstrapQueryRules, isactive)
	case "proxysql":
		mycluster.Conf.ProxysqlOn = applyIsActive(mycluster.Conf.ProxysqlOn, isactive)
	case "proxy-servers-read-on-master":
		mycluster.Conf.PRXServersReadOnMaster = applyIsActive(mycluster.Conf.PRXServersReadOnMaster, isactive)
		mycluster.Configurator.Init(*mycluster.Conf, mycluster.Logrus)
	case "proxy-servers-read-on-master-no-slave":
		mycluster.Conf.PRXServersReadOnMasterNoSlave = applyIsActive(mycluster.Conf.PRXServersReadOnMasterNoSlave, isactive)
		mycluster.Configurator.Init(*mycluster.Conf, mycluster.Logrus)
	case "proxy-servers-backend-compression":
		mycluster.Conf.PRXServersBackendCompression = applyIsActive(mycluster.Conf.PRXServersBackendCompression, isactive)
	case "database-heartbeat":
		mycluster.Conf.TestInjectTraffic = applyIsActive(mycluster.Conf.TestInjectTraffic, isactive)
	case "database-heartbeat-staging":
		mycluster.Conf.TestInjectTrafficStaging = applyIsActive(mycluster.Conf.TestInjectTrafficStaging, isactive)
	case "test":
		mycluster.Conf.Test = applyIsActive(mycluster.Conf.Test, isactive)
	case "prov-net-cni":
		mycluster.Conf.ProvNetCNI = applyIsActive(mycluster.Conf.ProvNetCNI, isactive)
	case "prov-db-config-preserve":
		mycluster.Conf.ProvDBConfigPreserve = applyIsActive(mycluster.Conf.ProvDBConfigPreserve, isactive)
	case "prov-db-start-fetch-config":
		mycluster.Conf.ProvDbStartFetchConfig = applyIsActive(mycluster.Conf.ProvDbStartFetchConfig, isactive)
		mycluster.CheckNeedConfigFetch()
	case "prov-db-apply-dynamic-config":
		mycluster.Conf.ProvDBApplyDynamicConfig = applyIsActive(mycluster.Conf.ProvDBApplyDynamicConfig, isactive)
	case "prov-docker-daemon-private":
		mycluster.Conf.ProvDockerDaemonPrivate = applyIsActive(mycluster.Conf.ProvDockerDaemonPrivate, isactive)
	case "prov-object-allow-overwrite":
		mycluster.Conf.ProvObjectAllowOverwrite = applyIsActive(mycluster.Conf.ProvObjectAllowOverwrite, isactive)
	case "backup-restic-aws":
		mycluster.Conf.BackupResticAws = applyIsActive(mycluster.Conf.BackupResticAws, isactive)
	case "backup-restic":
		oldValue := mycluster.Conf.BackupRestic
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.Conf.BackupRestic = newValue
			mycluster.CheckResticInstallation()
		}
	case "backup-restic-purge-prune":
		mycluster.Conf.BackupResticPurgePrune = applyIsActive(mycluster.Conf.BackupResticPurgePrune, isactive)
	case "backup-restic-purge-prune-compact":
		mycluster.Conf.BackupResticPurgePruneCompact = applyIsActive(mycluster.Conf.BackupResticPurgePruneCompact, isactive)
	case "backup-restic-purge-prune-repack-cacheable-only":
		mycluster.Conf.BackupResticPurgePruneRepackCacheableOnly = applyIsActive(mycluster.Conf.BackupResticPurgePruneRepackCacheableOnly, isactive)
	case "backup-restic-purge-prune-repack-small":
		mycluster.Conf.BackupResticPurgePruneRepackSmall = applyIsActive(mycluster.Conf.BackupResticPurgePruneRepackSmall, isactive)
	case "backup-restic-purge-prune-repack-uncompressed":
		mycluster.Conf.BackupResticPurgePruneRepackUncompressed = applyIsActive(mycluster.Conf.BackupResticPurgePruneRepackUncompressed, isactive)
	case "backup-binlogs":
		oldValue := mycluster.Conf.BackupBinlogs
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.Conf.BackupBinlogs = newValue
			if mycluster.Conf.BackupBinlogs {
				for _, sv := range mycluster.GetServers() {
					go sv.CheckBinaryLogs(true)
				}
			}
		}
	case "compress-backups":
		mycluster.Conf.CompressBackups = applyIsActive(mycluster.Conf.CompressBackups, isactive)
	case "backup-split-mysql-user":
		mycluster.Conf.BackupSplitMysqlUser = applyIsActive(mycluster.Conf.BackupSplitMysqlUser, isactive)
	case "backup-restore-mysql-user":
		mycluster.Conf.BackupRestoreMysqlUser = applyIsActive(mycluster.Conf.BackupRestoreMysqlUser, isactive)
	case "backup-mysqldump-splitdump":
		mycluster.Conf.BackupMysqldumpSplitDump = applyIsActive(mycluster.Conf.BackupMysqldumpSplitDump, isactive)
	case "backup-check-free-space":
		mycluster.Conf.BackupCheckFreeSpace = applyIsActive(mycluster.Conf.BackupCheckFreeSpace, isactive)
	case "backup-estimate-size":
		mycluster.Conf.BackupEstimateSize = applyIsActive(mycluster.Conf.BackupEstimateSize, isactive)
	case "monitoring-pause":
		mycluster.Conf.MonitorPause = applyIsActive(mycluster.Conf.MonitorPause, isactive)
	case "monitoring-save-config":
		mycluster.Conf.ConfRewrite = applyIsActive(mycluster.Conf.ConfRewrite, isactive)
	case "monitoring-queries":
		mycluster.Conf.MonitorQueries = applyIsActive(mycluster.Conf.MonitorQueries, isactive)
	case "monitoring-scheduler":
		mycluster.SetMonitoringScheduler(applyIsActive(mycluster.Conf.MonitorScheduler, isactive))
	case "monitoring-schema-change":
		mycluster.Conf.MonitorSchemaChange = applyIsActive(mycluster.Conf.MonitorSchemaChange, isactive)
	case "monitoring-schema-columns":
		mycluster.Conf.MonitorSchemaColumns = applyIsActive(mycluster.Conf.MonitorSchemaColumns, isactive)
	case "monitoring-schema-indexes":
		mycluster.Conf.MonitorSchemaIndexes = applyIsActive(mycluster.Conf.MonitorSchemaIndexes, isactive)
	case "monitoring-schema-on-replicas":
		mycluster.Conf.MonitorSchemaOnReplicas = applyIsActive(mycluster.Conf.MonitorSchemaOnReplicas, isactive)
	case "monitoring-capture":
		mycluster.Conf.MonitorCapture = applyIsActive(mycluster.Conf.MonitorCapture, isactive)
	case "monitoring-innodb-status":
		mycluster.Conf.MonitorInnoDBStatus = applyIsActive(mycluster.Conf.MonitorInnoDBStatus, isactive)
	case "monitoring-variable-diff":
		mycluster.Conf.MonitorVariableDiff = applyIsActive(mycluster.Conf.MonitorVariableDiff, isactive)
	case "monitoring-processlist":
		mycluster.Conf.MonitorProcessList = applyIsActive(mycluster.Conf.MonitorProcessList, isactive)
	case "monitoring-performance-schema-queries":
		mycluster.Conf.MonitorPFSQueries = applyIsActive(mycluster.Conf.MonitorPFSQueries, isactive)
	case "monitoring-performance-schema-queries-period":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.MonitorPFSQueriesPeriod = val
	case "monitoring-performance-schema-queries-explain":
		mycluster.Conf.MonitorPFSQueriesExplain = applyIsActive(mycluster.Conf.MonitorPFSQueriesExplain, isactive)
	case "monitoring-performance-schema-queries-explain-delay":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.MonitorPFSQueriesExplainDelay = val
	case "monitoring-performance-schema-queries-explain-purge-period":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.MonitorPFSQueriesExplainPurgePeriod = val
	case "force-slave-readonly":
		mycluster.Conf.ForceSlaveReadOnly = applyIsActive(mycluster.Conf.ForceSlaveReadOnly, isactive)
	case "force-binlog-row":
		mycluster.Conf.ForceBinlogRow = applyIsActive(mycluster.Conf.ForceBinlogRow, isactive)
	case "force-slave-semisync":
		mycluster.Conf.ForceSlaveSemisync = applyIsActive(mycluster.Conf.ForceSlaveSemisync, isactive)
	case "force-slave-Heartbeat":
		mycluster.Conf.ForceSlaveHeartbeat = applyIsActive(mycluster.Conf.ForceSlaveHeartbeat, isactive)
	case "force-slave-gtid":
		mycluster.Conf.ForceSlaveGtid = applyIsActive(mycluster.Conf.ForceSlaveGtid, isactive)
	case "force-slave-gtid-mode-strict":
		mycluster.Conf.ForceSlaveGtidStrict = applyIsActive(mycluster.Conf.ForceSlaveGtidStrict, isactive)
	case "force-slave-idempotent":
		mycluster.Conf.ForceSlaveIdempotent = applyIsActive(mycluster.Conf.ForceSlaveIdempotent, isactive)
	case "force-slave-strict":
		mycluster.Conf.ForceSlaveStrict = applyIsActive(mycluster.Conf.ForceSlaveStrict, isactive)
	case "force-slave-serialized":
		if isactive != nil {
			if *isactive {
				mycluster.Conf.ForceSlaveParallelMode = "SERIALIZED"
			} else if mycluster.Conf.ForceSlaveParallelMode == "SERIALIZED" {
				mycluster.Conf.ForceSlaveParallelMode = ""
			}
		}
	case "force-slave-minimal":
		if isactive != nil {
			if *isactive {
				mycluster.Conf.ForceSlaveParallelMode = "MINIMAL"
			} else if mycluster.Conf.ForceSlaveParallelMode == "MINIMAL" {
				mycluster.Conf.ForceSlaveParallelMode = ""
			}
		}
	case "force-slave-conservative":
		if isactive != nil {
			if *isactive {
				mycluster.Conf.ForceSlaveParallelMode = "CONSERVATIVE"
			} else if mycluster.Conf.ForceSlaveParallelMode == "CONSERVATIVE" {
				mycluster.Conf.ForceSlaveParallelMode = ""
			}
		}
	case "force-slave-optimistic":
		if isactive != nil {
			if *isactive {
				mycluster.Conf.ForceSlaveParallelMode = "OPTIMISTIC"
			} else if mycluster.Conf.ForceSlaveParallelMode == "OPTIMISTIC" {
				mycluster.Conf.ForceSlaveParallelMode = ""
			}
		}
	case "force-slave-aggressive":
		if isactive != nil {
			if *isactive {
				mycluster.Conf.ForceSlaveParallelMode = "AGGRESSIVE"
			} else if mycluster.Conf.ForceSlaveParallelMode == "AGGRESSIVE" {
				mycluster.Conf.ForceSlaveParallelMode = ""
			}
		}
	case "force-binlog-compress":
		mycluster.Conf.ForceBinlogCompress = applyIsActive(mycluster.Conf.ForceBinlogCompress, isactive)
	case "force-binlog-annotate":
		mycluster.Conf.ForceBinlogAnnotate = applyIsActive(mycluster.Conf.ForceBinlogAnnotate, isactive)
	case "force-binlog-slow-queries":
		mycluster.Conf.ForceBinlogSlowqueries = applyIsActive(mycluster.Conf.ForceBinlogSlowqueries, isactive)
	case "log-sql-in-monitoring":
		oldValue := mycluster.Conf.LogSQLInMonitoring
		newValue := applyIsActive(mycluster.Conf.LogSQLInMonitoring, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogSQLLevel = 1
			} else {
				mycluster.Conf.LogSQLLevel = 0
			}
			mycluster.Conf.LogSQLInMonitoring = newValue
		}
	case "log-writer-election":
		oldValue := mycluster.Conf.LogWriterElection
		newValue := applyIsActive(mycluster.Conf.LogWriterElection, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogWriterElectionLevel = 1
			} else {
				mycluster.Conf.LogWriterElectionLevel = 0
			}
			mycluster.Conf.LogWriterElection = newValue
		}
	case "log-sst":
		oldValue := mycluster.Conf.LogSST
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogSSTLevel = 1
			} else {
				mycluster.Conf.LogSSTLevel = 0
			}
			mycluster.Conf.LogSST = newValue
		}
	case "log-heartbeat":
		oldValue := mycluster.Conf.LogHeartbeat
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogHeartbeatLevel = 1
			} else {
				mycluster.Conf.LogHeartbeatLevel = 0
			}
			mycluster.Conf.LogHeartbeat = newValue
		}
	case "log-config-load":
		oldValue := mycluster.Conf.LogConfigLoad
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogConfigLoadLevel = 1
			} else {
				mycluster.Conf.LogConfigLoadLevel = 0
			}
			mycluster.Conf.LogConfigLoad = newValue
		}
	case "log-git":
		oldValue := mycluster.Conf.LogGit
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogGitLevel = 1
			} else {
				mycluster.Conf.LogGitLevel = 0
			}
			mycluster.Conf.LogGit = newValue
		}
	case "log-support":
		oldValue := mycluster.Conf.LogSupport
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogSupportLevel = 1
			} else {
				mycluster.Conf.LogSupportLevel = 0
			}
			mycluster.Conf.LogSupport = newValue
		}
	case "log-backup-stream":
		oldValue := mycluster.Conf.LogBackupStream
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogBackupStreamLevel = 1
			} else {
				mycluster.Conf.LogBackupStreamLevel = 0
			}
			mycluster.Conf.LogBackupStream = newValue
		}
	case "log-orchestrator":
		oldValue := mycluster.Conf.LogOrchestrator
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogOrchestratorLevel = 1
			} else {
				mycluster.Conf.LogOrchestratorLevel = 0
			}
			mycluster.Conf.LogOrchestrator = newValue
		}
	case "log-vault":
		oldValue := mycluster.Conf.LogVault
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogVaultLevel = 1
			} else {
				mycluster.Conf.LogVaultLevel = 0
			}
			mycluster.Conf.LogVault = newValue
		}
	case "log-topology":
		oldValue := mycluster.Conf.LogTopology
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogTopologyLevel = 1
			} else {
				mycluster.Conf.LogTopologyLevel = 0
			}
			mycluster.Conf.LogTopology = newValue
		}
	case "log-proxy":
		oldValue := mycluster.Conf.LogProxy
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.LogProxyLevel = 1
			} else {
				mycluster.Conf.LogProxyLevel = 0
			}
			mycluster.Conf.LogProxy = newValue
		}
	case "proxysql-debug":
		oldValue := mycluster.Conf.ProxysqlDebug
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.ProxysqlLogLevel = 1
			} else {
				mycluster.Conf.ProxysqlLogLevel = 0
			}
			mycluster.Conf.ProxysqlDebug = newValue
		}
	case "haproxy-debug":
		oldValue := mycluster.Conf.HaproxyDebug
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.HaproxyLogLevel = 1
			} else {
				mycluster.Conf.HaproxyLogLevel = 0
			}
			mycluster.Conf.HaproxyDebug = newValue
		}
	case "proxyjanitor-debug":
		oldValue := mycluster.Conf.ProxyJanitorDebug
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.ProxyJanitorLogLevel = 1
			} else {
				mycluster.Conf.ProxyJanitorLogLevel = 0
			}
			mycluster.Conf.ProxyJanitorDebug = newValue
		}
	case "maxscale-debug":
		oldValue := mycluster.Conf.MxsDebug
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			if newValue {
				mycluster.Conf.MxsLogLevel = 1
			} else {
				mycluster.Conf.MxsLogLevel = 0
			}
			mycluster.Conf.MxsDebug = newValue
		}
	case "force-binlog-purge":
		mycluster.Conf.ForceBinlogPurge = applyIsActive(mycluster.Conf.ForceBinlogPurge, isactive)
	case "force-binlog-purge-on-restore":
		mycluster.Conf.ForceBinlogPurgeOnRestore = applyIsActive(mycluster.Conf.ForceBinlogPurgeOnRestore, isactive)
	case "force-binlog-purge-replicas":
		mycluster.Conf.ForceBinlogPurgeReplicas = applyIsActive(mycluster.Conf.ForceBinlogPurgeReplicas, isactive)
	case "multi-master-concurrent-write":
		mycluster.Conf.MultiMasterConcurrentWrite = applyIsActive(mycluster.Conf.MultiMasterConcurrentWrite, isactive)
	case "multi-master-ring-unsafe":
		mycluster.Conf.MultiMasterRingUnsafe = applyIsActive(mycluster.Conf.MultiMasterRingUnsafe, isactive)
	case "dynamic-topology":
		mycluster.Conf.DynamicTopology = applyIsActive(mycluster.Conf.DynamicTopology, isactive)
	case "replication-no-relay":
		mycluster.Conf.ReplicationNoRelay = applyIsActive(mycluster.Conf.ReplicationNoRelay, isactive)
	case "prov-db-force-write-config":
		oldValue := mycluster.Conf.ProvDBForceWriteConfig
		newValue := applyIsActive(oldValue, isactive)
		if oldValue != newValue {
			mycluster.Conf.ProvDBForceWriteConfig = newValue
			if newValue {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Configurator force write config files activated. Will replace config files on next provision.")
			} else {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Configurator force write config files de-activated. Will create config files with suffix (.new) for conflicting files on next provision.")
			}
		}
	case "backup-keep-until-valid":
		mycluster.Conf.BackupKeepUntilValid = applyIsActive(mycluster.Conf.BackupKeepUntilValid, isactive)
	case "mail-smtp-tls-skip-verify":
		mycluster.Conf.MailSMTPTLSSkipVerify = applyIsActive(mycluster.Conf.MailSMTPTLSSkipVerify, isactive)
	case "cloud18-shared":
		mycluster.Conf.Cloud18Shared = applyIsActive(mycluster.Conf.Cloud18Shared, isactive)
	case "cloud18-open-dbops":
		mycluster.Conf.Cloud18OpenDbops = applyIsActive(mycluster.Conf.Cloud18OpenDbops, isactive)
	case "cloud18-open-sysops":
		mycluster.Conf.Cloud18OpenSysops = applyIsActive(mycluster.Conf.Cloud18OpenSysops, isactive)
	case "topology-staging":
		mycluster.Conf.TopologyStaging = applyIsActive(mycluster.Conf.TopologyStaging, isactive)
	case "analyze-use-persistent":
		mycluster.Conf.AnalyzeUsePersistent = applyIsActive(mycluster.Conf.AnalyzeUsePersistent, isactive)
	case "log-plugin":
		mycluster.Conf.LogPlugin = applyIsActive(mycluster.Conf.LogPlugin, isactive)
	case "log-level-plugin":
		v, _ := strconv.Atoi(value)
		mycluster.Conf.LogPluginLevel = v
	case "prov-app-template-repo":
		mycluster.Conf.ProvAppTemplateRepo = value
	case "prov-app-template-repo-branch":
		mycluster.Conf.ProvAppTemplateRepoBranch = value
	case "prov-app-template-repo-user":
		mycluster.Conf.ProvAppTemplateRepoUser = value
	case "prov-app-template-repo-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return fmt.Errorf("unable to base64-decode value: %w", err)
		}
		mycluster.Conf.ProvAppTemplateRepoPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.ProvAppTemplateRepoPassword
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("prov-app-template-repo-password")
		mycluster.Conf.Secrets["prov-app-template-repo-password"] = new_secret
	case "prov-app-template-repo-timeout":
		parsedTimeout, err := parseProvAppTemplateRepoTimeout(value)
		if err != nil {
			return err
		}
		mycluster.Conf.ProvAppTemplateRepoTimeout = parsedTimeout
	default:
		// Check if this is a plugin-config key: "plugin-config-<pluginname>-<key>"
		// e.g. "plugin-config-errorlog-timeframe-hours"      → PluginConfig["errorlog"]["timeframe-hours"]
		//      "plugin-config-plugin-connection-storm-sleep-ratio-threshold"
		//                                                     → PluginConfig["plugin-connection-storm"]["sleep-ratio-threshold"]
		//
		// SplitN(rest, "-", 2) is wrong for plugin names that contain hyphens (all external
		// plugins are named "plugin-*").  Instead we match against the registered plugin names
		// and pick the longest match so that the plugin name and config key are unambiguous.
		if strings.HasPrefix(name, "plugin-config-") {
			rest := strings.TrimPrefix(name, "plugin-config-")
			var matchedPlugin, matchedKey string
			for _, p := range mycluster.PluginRegistry().All() {
				pname := p.Name()
				prefix := pname + "-"
				if strings.HasPrefix(rest, prefix) && len(pname) > len(matchedPlugin) {
					matchedPlugin = pname
					matchedKey = strings.TrimPrefix(rest, prefix)
				}
			}
			if matchedPlugin != "" && matchedKey != "" {
				if mycluster.Conf.PluginConfig == nil {
					mycluster.Conf.PluginConfig = make(map[string]map[string]string)
				}
				if mycluster.Conf.PluginConfig[matchedPlugin] == nil {
					mycluster.Conf.PluginConfig[matchedPlugin] = make(map[string]string)
				}
				mycluster.Conf.PluginConfig[matchedPlugin][matchedKey] = value
				mycluster.ConfigManager.SaveConfig(mycluster, false)
				return nil
			}
		}
		return errors.New("setting not found")
	}
	if trackedSecretSnapshotChanged(trackedSecretsBefore, mycluster.TrackedSecretCompareSnapshot()) {
		mycluster.MarkSecretVersionStoreDirty()
		mycluster.ReconcileSecretVersionStore()
	}

	mycluster.ConfigManager.SaveConfig(mycluster, false)
	return nil
}

func trackedSecretSnapshotChanged(before, after map[string]string) bool {
	if len(before) != len(after) {
		return true
	}

	for k, v := range before {
		if after[k] != v {
			return true
		}
	}

	return false
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
	case "api-port ":
		repman.Conf.APIPort = value
	case "api-public-url":
		repman.Conf.APIPublicURL = value
	case "arbitration-external-hosts":
		repman.Conf.ArbitrationSasHosts = value
	case "arbitration-external-secret":
		repman.Conf.ArbitrationSasSecret = value
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
	case "graphite-carbon-pprof-port ":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPprofPort = v
	case "graphite-carbon-server-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonServerPort = v
	case "http-bind-address ":
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
	case "arbitration-external ":
		repman.Conf.Arbitration = isactive
	case "graphite-embedded":
		repman.Conf.GraphiteEmbedded = isactive
	case "graphite-blacklist  ":
		repman.Conf.GraphiteBlacklist = isactive
	case "graphite-metrics ":
		repman.Conf.GraphiteMetrics = isactive
	case "http-server":
		repman.Conf.HttpServ = isactive
	case "http-use-react ":
		repman.Conf.HttpUseReact = isactive
	case "monitoring-save-config  ":
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
	case "arbitration-external ":
		repman.Conf.Arbitration = !repman.Conf.Arbitration
	case "graphite-embedded":
		repman.Conf.GraphiteEmbedded = !repman.Conf.GraphiteEmbedded
	case "graphite-blacklist  ":
		repman.Conf.GraphiteBlacklist = !repman.Conf.GraphiteBlacklist
	case "graphite-metrics ":
		repman.Conf.GraphiteMetrics = !repman.Conf.GraphiteMetrics
	case "http-server":
		repman.Conf.HttpServ = !repman.Conf.HttpServ
	case "http-use-react ":
		repman.Conf.HttpUseReact = !repman.Conf.HttpUseReact
	case "monitoring-save-config  ":
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
	case "monitoring-log-api-login":
		repman.Conf.MonitoringLogAPILogin = !repman.Conf.MonitoringLogAPILogin
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

type ReloadPlanRequest struct {
	Download *bool `json:"download"`
}

func shouldDownloadFromRequest(r *http.Request) bool {
	if r == nil || r.Body == nil {
		return true
	}
	var payload ReloadPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		return true
	}
	if payload.Download == nil {
		return true
	}
	return *payload.Download
}

// handlerMuxReloadPlans handles the reloading of cluster plans.
// @Summary Reload cluster plans
// @Description This endpoint reloads the cluster plans for all clusters.
// @Tags ClusterActions
// @Param body body ReloadPlanRequest false "Reload plan repository options"
// @Success 200 {string} string "Successfully reloaded plans"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/reload-clusters-plans [post]
func (repman *ReplicationManager) handlerMuxReloadPlans(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	shouldDownload := shouldDownloadFromRequest(r)

	var mycluster *cluster.Cluster
	for _, v := range repman.Clusters {
		if v != nil {
			mycluster = v
			break
		}
	}

	if mycluster != nil {
		valid, apiuser := repman.IsValidClusterACL(r, mycluster)
		if valid {
			if shouldDownload {
				repman.InitServicePlans()
			}
			for _, cl := range repman.Clusters {
				//Don't print error with no valid ACL
				if cl.IsURLPassACL(apiuser, r.URL.Path, false) {
					cl.SetServicePlan(cl.Conf.ProvServicePlan)
				}
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Successfully reloaded plans"))
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s", r.URL.Path), http.StatusForbidden)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxReloadPlansInfo handles the reloading of cluster plan information.
// @Summary Reload cluster plan information (all clusters)
// @Description This endpoint reloads the cluster plan information for all clusters without reapplying plans.
// @Tags ClusterActions
// @Param body body ReloadPlanRequest false "Reload plan repository options"
// @Success 200 {string} string "Successfully reloaded plan information"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/reload-clusters-plan-info [post]
func (repman *ReplicationManager) handlerMuxReloadPlansInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	shouldDownload := shouldDownloadFromRequest(r)

	var mycluster *cluster.Cluster
	for _, v := range repman.Clusters {
		if v != nil {
			mycluster = v
			break
		}
	}

	if mycluster != nil {
		valid, apiuser := repman.IsValidClusterACL(r, mycluster)
		if valid {
			if shouldDownload {
				repman.InitServicePlans()
			}
			for _, cl := range repman.Clusters {
				//Don't print error with no valid ACL
				if cl.IsURLPassACL(apiuser, r.URL.Path, false) {
					cl.SetServicePlanInfos(cl.Conf.ProvServicePlan)
				}
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Successfully reloaded plan information"))
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s", r.URL.Path), http.StatusForbidden)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxReloadPlanInfo handles the reloading of cluster plan information.
// @Summary Reload cluster plan information
// @Description This endpoint reloads the cluster plan information for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded plan information"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/reload-plan-info [post]
func (repman *ReplicationManager) handlerMuxReloadPlanInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		// Intentionally skip download toggle here to avoid global plan repo reload side effects.
		mycluster.SetServicePlanInfos(mycluster.Conf.ProvServicePlan)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Successfully reloaded plan information"))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxAddTag handles the addition of a tag to a given cluster.
// @Summary Add a tag to a specific cluster
// @Description This endpoint adds a tag to the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/add-db-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxAddTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.AddDBTag(vars["tagValue"], false)
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxGetTagContent returns the my.cnf snippet for a configurator tag.
func (repman *ReplicationManager) handlerMuxGetTagContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	content := mycluster.Configurator.GetTagMyCnf(vars["tagName"])
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(content))
}

// handlerMuxGetTagDocHelp returns documentation links for the variables in a tag.
// Enterprise-only: returns 403 for free-plan or unregistered instances.
func (repman *ReplicationManager) handlerMuxGetTagDocHelp(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster Not Found", http.StatusNotFound)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	plan := mycluster.Conf.Cloud18SubscriptionPlan
	if plan == "" || plan == "free" {
		http.Error(w, `{"error":"Documentation help requires a support or partner plan. Upgrade at https://cloud18.io"}`, http.StatusForbidden)
		return
	}

	tagName := vars["tagName"]
	varNames := mycluster.Configurator.GetTagVariableNames(tagName)

	pluginDataDir := mycluster.Conf.ShareDir + "/plugins/data"
	dh := configurator.NewDocHelp(pluginDataDir)
	matched, unknown := dh.LookupVariables(varNames)

	result := configurator.DocHelpResult{
		Tag:              tagName,
		Variables:        matched,
		UnknownVariables: unknown,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handlerMuxAddProxyTag handles the addition of a proxy tag to a given cluster.
// @Summary Add a proxy tag to a specific cluster
// @Description This endpoint adds a proxy tag to the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/add-proxy-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxAddProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if vars["tagValue"] == "" {
			http.Error(w, "Empty tag value", http.StatusInternalServerError)
			return
		}
		mycluster.AddProxyTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxDropTag handles the removal of a tag from a given cluster.
// @Summary Remove a tag from a specific cluster
// @Description This endpoint removes a tag from the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag removed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/drop-db-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxDropTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.DropDBTag(vars["tagValue"], false)
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxDropProxyTag handles the removal of a proxy tag from a given cluster.
// @Summary Remove a proxy tag from a specific cluster
// @Description This endpoint removes a proxy tag from the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag removed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/drop-proxy-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxDropProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.DropProxyTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxLog handles the retrieval of logs for a given cluster.
// @Summary Retrieve logs for a specific cluster
// @Description This endpoint retrieves the logs for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of logs"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/logs [get]
func (repman *ReplicationManager) handlerMuxLog(w http.ResponseWriter, r *http.Request) {
	var clusterlogs []string
	vars := mux.Vars(r)

	// (?i) = case-insensitive
	pattern := fmt.Sprintf(`(?i)(^|[^A-Za-z0-9_\-])%s([^A-Za-z0-9_\-]|$)`, regexp.QuoteMeta(strings.TrimSpace(vars["clusterName"])))
	re := regexp.MustCompile(pattern)

	for _, slog := range repman.tlog.Buffer {
		if re.MatchString(slog) {
			clusterlogs = append(clusterlogs, slog)
		}
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(clusterlogs)
	if err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}
}

// handlerMuxWebLog handles the retrieval of web logs.
// @Summary Retrieve web logs
// @Description This endpoint retrieves the web logs.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {array} string "List of web logs"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/http-logs [get]
// @Router /api/clusters/{clusterName}/topology/http-logs/{logType} [get]
func (repman *ReplicationManager) handlerMuxWebLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	cl := repman.getClusterByName(vars["clusterName"])
	if cl == nil {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, cl); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var logs any
	if logType, ok := vars["logType"]; ok {
		logs = cl.GetWebLogsByType(logType)
	} else {
		logs = cl.GetAllWebLogs()
	}

	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(logs)
	if err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}
}

// handlerMuxCrashes handles the retrieval of crashes for a given cluster.
// @Summary Retrieve crashes for a specific cluster
// @Description This endpoint retrieves the crashes for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of crashes"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/topology/crashes [get]
func (repman *ReplicationManager) handlerMuxCrashes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetCrashes())
		if err != nil {
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxOneTest handles the execution of a specific test for a given cluster.
// @Summary Run a specific test for a given cluster
// @Description This endpoint runs a specific test for the specified cluster.
// @Tags ClusterTest
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param testName path string true "Test Name"
// @Param provision formData string false "Provision the cluster before running the test"
// @Param unprovision formData string false "Unprovision the cluster after running the test"
// @Success 200 {object} cluster.Test "Test result"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/tests/actions/run/{testName} [post]
func (repman *ReplicationManager) handlerMuxOneTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		r.ParseForm() // Parses the request body
		if r.Form.Get("provision") == "true" {
			mycluster.SetTestStartCluster(true)
		}
		if r.Form.Get("unprovision") == "true" {
			mycluster.SetTestStopCluster(true)
		}

		res := repman.RunAllTests(mycluster, vars["testName"], "")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")

		if len(res) > 0 {
			err := e.Encode(res[0])
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				mycluster.SetTestStartCluster(false)
				mycluster.SetTestStopCluster(false)
				return
			}
		} else {
			var test cluster.Test
			test.Result = "FAIL"
			test.Name = vars["testName"]
			err := e.Encode(test)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				mycluster.SetTestStartCluster(false)
				mycluster.SetTestStopCluster(false)
				return
			}

		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		mycluster.SetTestStartCluster(false)
		mycluster.SetTestStopCluster(false)
		return
	}
	mycluster.SetTestStartCluster(false)
	mycluster.SetTestStopCluster(false)
}

// handlerMuxTests handles the execution of all tests for a given cluster.
// @Summary Run all tests for a given cluster
// @Description This endpoint runs all tests for the specified cluster.
// @Tags ClusterTest
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} cluster.Test "List of test results"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/tests/actions/run/all [post]
func (repman *ReplicationManager) handlerMuxTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		res := repman.RunAllTests(mycluster, "ALL", "")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(res)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxSettingsReload handles the reloading of cluster settings.
// @Summary Reload cluster settings
// @Description This endpoint reloads the settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded settings"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/reload [post]
func (repman *ReplicationManager) handlerMuxSettingsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	repman.InitConfig(*repman.Conf, true)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		//mycluster.ReloadConfig(repman.Confs[vars["clusterName"]])
		mycluster.ReloadConfig(*mycluster.Conf)
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}

}

// handlerMuxServerAdd handles the addition of a server to a given cluster.
// @Summary Add a server to a specific cluster
// @Description This endpoint adds a server to the specified cluster.
// @Tags ClusterMonitor
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param host path string true "Host"
// @Param port path string true "Port"
// @Param type path string false "Type"
// @Param tag path string false "Tag"
// @Success 200 {string} string "Monitor added"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "Error adding new monitor"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type}/{tag} [post]
// @Router /api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type} [post]
// @Router /api/clusters/{clusterName}/actions/addserver/{host}/{port} [post]
func (repman *ReplicationManager) handlerMuxServerAdd(w http.ResponseWriter, r *http.Request) {
	defer repman.LogPanicToFile()

	var err error
	var updateImg bool
	var repopath, repoimg string
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			w.WriteHeader(403)
			w.Write([]byte(`{"msg":"No valid ACL"}`))
			return
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive new %s monitor to be added %s", vars["type"], vars["host"]+":"+vars["port"])
		var srvtype, host, port, tag, template string
		srvtype = vars["type"]
		host = strings.TrimSpace(vars["host"])
		port = strings.TrimSpace(vars["port"])
		tag = strings.TrimSpace(vars["tag"])

		if srvtype == "" {
			if port == "0" || port == "" {
				port = "3306"
			}
			err = mycluster.AddSeededServer(host + ":" + port)
		} else if srvtype == "app" {
			// Add app monitor
			if host == "" {
				http.Error(w, "Host is required for app monitor", http.StatusBadRequest)
				return
			}

			if port == "" || port == "0" {
				http.Error(w, "Port is required for app monitor", http.StatusBadRequest)
				return
			}

			portNumber, convErr := strconv.Atoi(port)
			if convErr != nil || portNumber < 1 || portNumber > 65535 {
				http.Error(w, "Port must be between 1 and 65535", http.StatusBadRequest)
				return
			}

			var formData DockerRegistryLoginForm
			if r.Body != nil {
				decoder := json.NewDecoder(r.Body)
				err = decoder.Decode(&formData)
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error decoding JSON: %s", err.Error())
					w.WriteHeader(400)
					w.Write([]byte(`{"msg":"Error decoding JSON: ` + err.Error() + `"}`))
					return
				}

				if formData.IsPrivate {
					formData.URL = strings.TrimSpace(formData.URL)
					formData.Username = strings.TrimSpace(formData.Username)
					if formData.URL == "" {
						http.Error(w, "Registry URL is required for private registry", http.StatusBadRequest)
						return
					}
					if formData.Password == "" {
						http.Error(w, "Registry credential is required for private registry", http.StatusBadRequest)
						return
					}

					err := mycluster.AddDockerPrivateRegistryCredentials(formData.URL, formData.Username, formData.Password, formData.Update)
					if err != nil {
						// Only warn don't exit if error is not nil
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Error adding Docker private registry credentials: %s", err.Error())
					}
				}

				if formData.Template != "" {
					template = strings.TrimSpace(formData.Template)
				}
			}

			if tag == "" && template == "" {
				http.Error(w, "Docker image is required for app monitor", http.StatusBadRequest)
				return
			}

			err = mycluster.AddSeededApp(host, port, tag, template)
		} else {
			repopath = repman.GetDockerRepoPath(srvtype)

			if repman.MonitorType[srvtype] == "proxy" {
				// update image if tag is not empty
				if tag != "" {
					updateImg = true

					// check if repository list is exists
					repoimg = repman.GetDockerRepoImage(srvtype, tag)

					if repoimg == "" && repopath == "" {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Repository path not found for proxy %s. Skipping proxy image update", srvtype)
						updateImg = false
					} else if repopath != "" {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Repository image with tag %s not found for proxy %s. Changing to the latest tag.", tag, srvtype)
						repoimg = repopath + ":latest"
					}

					// update image
					if updateImg {
						switch srvtype {
						case config.ConstProxyHaproxy:
							mycluster.Conf.ProvProxHaproxyImg = repoimg
						case config.ConstProxySqlproxy:
							mycluster.Conf.ProvProxProxysqlImg = repoimg
						case config.ConstProxyMaxscale:
							mycluster.Conf.ProvProxMaxscaleImg = repoimg
						case config.ConstProxySphinx:
							mycluster.Conf.ProvSphinxImg = repoimg
						}
					}
				}
				err = mycluster.AddSeededProxy(srvtype, host, port, "", "")
			} else if repman.MonitorType[srvtype] == "database" {
				// Check if new database repo is different with current repo
				oldrepopath := strings.Split(mycluster.Conf.ProvDbImg, ":")[0]
				if oldrepopath != repopath {
					updateImg = true

					// if no tag, use the latest version
					if tag == "" {
						tag = "latest"
					}
				}

				if tag != "" {
					updateImg = true
					repoimg = repman.GetDockerRepoImage(srvtype, tag)

					if repoimg == "" && repopath == "" {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Repository path not found for database %s. Skipping database image update", srvtype)
						updateImg = false
					} else if repopath != "" {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Repository image with tag %s not found for database %s. Changing to the latest tag.", tag, srvtype)
						repoimg = repopath + ":latest"
					}
				}

				if port == "0" || port == "" {
					port = "3306"
				}

				// update image
				if updateImg {
					switch srvtype {
					case "mariadb":
						mycluster.Conf.ProvDbImg = repoimg
					case "percona":
						mycluster.Conf.ProvDbImg = repoimg
					case "mysql":
						mycluster.Conf.ProvDbImg = repoimg
					}
				}
				err = mycluster.AddSeededServer(host + ":" + port)
			}
		}

		// This will only return duplicate error
		if err != nil {
			errStr := fmt.Sprintf("Error adding new %s monitor of %s: %s", srvtype, host+":"+port, err.Error())
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, errStr)
			w.WriteHeader(409)
			w.Write([]byte(`{"msg":"` + errStr + `"}`))
			return
		} else {
			w.WriteHeader(200)
			w.Write([]byte(`{"msg":"Monitor added"}`))
			return
		}
	} else {
		w.WriteHeader(500)
		w.Write([]byte(`{"msg":"Cluster Not Found"}`))
		return
	}

}

// handlerMuxServerDrop handles the HTTP request to drop a server monitor from a cluster.
//
// @Summary Drop a server monitor from a cluster
// @Description This endpoint allows dropping a server monitor or proxy monitor from a specified cluster.
// @Tags ClusterMonitor
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param type path string false "Monitor Type (proxy or database)"
// @Param host path string true "Host"
// @Param port path string true "Port"
// @Success 200 {string} string "Monitor dropped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /cluster/{clusterName}/actions/dropserver/{host}/{port}/{type} [post]
// @Router /cluster/{clusterName}/actions/dropserver/{host}/{port} [post]
func (repman *ReplicationManager) handlerMuxServerDrop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive drop %s monitor command for %s", vars["type"], vars["host"]+":"+vars["port"])
		if vars["type"] == "" {
			mycluster.RemoveServerMonitor(vars["host"], vars["port"])
		} else {
			if mycluster.MonitorType[vars["type"]] == "app" {
				mycluster.RemoveAppMonitor(vars["host"], vars["port"])
			} else if mycluster.MonitorType[vars["type"]] == "proxy" {
				mycluster.RemoveProxyMonitor(vars["type"], vars["host"], vars["port"])
			} else if mycluster.MonitorType[vars["type"]] == "database" {
				mycluster.RemoveServerMonitor(vars["host"], vars["port"])
			}
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}

}

// handlerMuxServerDropByName handles the HTTP request to drop a server monitor from a cluster by its name.
// @Summary Drop a server monitor from a cluster by name
// @Description This endpoint allows dropping a server monitor or proxy monitor from a specified cluster.
// @Tags ClusterMonitor
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Monitor Server ID"
// @Success 200 {string} string "Monitor dropped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /cluster/{clusterName}/actions/dropserver/{serverName} [post]
func (repman *ReplicationManager) handlerMuxServerDropByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if vars["serverName"] == "" {
			http.Error(w, "No server name provided", http.StatusBadRequest)
			return
		}

		node := mycluster.GetServerFromName(vars["serverName"])
		prx := mycluster.GetProxyFromName(vars["serverName"])
		if node == nil && prx == nil {
			http.Error(w, "No server found with name "+vars["serverName"], http.StatusBadRequest)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive drop %s monitor command for %s", vars["type"], vars["host"]+":"+vars["port"])
		if node != nil {
			mycluster.RemoveServerMonitor(node.Host, node.Port)
		} else if prx != nil {
			mycluster.RemoveProxyMonitor(prx.GetType(), prx.GetHost(), prx.GetPort())
		}
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}

}

// handlerMuxClusterStatus handles the HTTP request to retrieve the status of a specified cluster.
// @Summary Retrieve status of a cluster
// @Description This endpoint retrieves the status of a specified cluster and returns it in JSON format.
// @Tags Cluster
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} map[string]string "Cluster status"
// @Failure 400 {string} string "No cluster found"
// @Router /api/clusters/{clusterName}/status [get]
func (repman *ReplicationManager) handlerMuxClusterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		if mycluster.GetStatus() {
			io.WriteString(w, `{"alive": "running"}`)
		} else {
			io.WriteString(w, `{"alive": "errors"}`)
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxClusterMasterPhysicalBackup handles the physical backup process for the master of a given cluster.
// @Summary Perform a physical backup for the master of a specific cluster
// @Description This endpoint triggers a physical backup for the master of the specified cluster.
// @Tags ClusterBackup
// @Accept json
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered physical backup"
// @Failure 403 {string} string "No valid ACL"
// @Failure 400 {string} string "No cluster found"
// @Router /api/clusters/{clusterName}/actions/master-physical-backup [post]
func (repman *ReplicationManager) handlerMuxClusterMasterPhysicalBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		mycluster.GetMaster().JobBackupPhysical()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxClusterOptimize handles the optimization process for a given cluster.
// @Summary Optimize a specific cluster
// @Description This endpoint triggers the optimization process for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered optimization"
// @Failure 403 {string} string "No valid ACL"
// @Failure 400 {string} string "No cluster found"
// @Router /api/clusters/{clusterName}/actions/optimize [post]
func (repman *ReplicationManager) handlerMuxClusterOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		mycluster.RollingOptimize()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxClusterSysbench handles the execution of sysbench for a given cluster.
// @Summary Run sysbench for a specific cluster
// @Description This endpoint runs sysbench for the specified cluster.
// @Tags ClusterTest
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param threads query string false "Number of threads"
// @Success 200 {string} string "Successfully triggered sysbench"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/sysbench [post]
func (repman *ReplicationManager) handlerMuxClusterSysbench(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		if r.URL.Query().Get("threads") != "" {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Sysbench threads to %s", r.URL.Query().Get("threads"))
			mycluster.SetSysbenchThreads(r.URL.Query().Get("threads"))
		}
		go mycluster.RunSysbench()
	}
}

// handlerMuxClusterApplyDynamicConfig handles the application of dynamic configuration for a given cluster.
// @Summary Apply dynamic configuration for a specific cluster
// @Description This endpoint applies dynamic configuration for the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully applied dynamic configuration"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/apply-dynamic-config [post]
func (repman *ReplicationManager) handlerMuxClusterApplyDynamicConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.SetDBDynamicConfig()
	}
}

// handlerMuxClusterReloadCertificates handles the reloading of client certificates for a given cluster.
// @Summary Reload client certificates for a specific cluster
// @Description This endpoint reloads the client certificates for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded client certificates"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/certificates-reload [post]
func (repman *ReplicationManager) handlerMuxClusterReloadCertificates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.ReloadCertificates()
	}
}

// handlerMuxClusterWaitDatabases handles the waiting for databases to be ready for a given cluster.
// @Summary Wait for databases to be ready for a specific cluster
// @Description This endpoint waits for the databases to be ready for the specified cluster.
// @Tags Cluster
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Databases are ready"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/waitdatabases [post]
func (repman *ReplicationManager) handlerMuxClusterWaitDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		err := mycluster.WaitDatabaseCanConn()
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}
}

// handlerMuxCluster handles the HTTP request to retrieve the details of a specified cluster.
// @Summary Retrieve details of a cluster
// @Description This endpoint retrieves the details of a specified cluster and returns it in JSON format.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.Cluster "Cluster details"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName} [get]
// buildClusterAPIPayload serialises mycluster into the JSON payload returned by
// GET /api/clusters/{clusterName}. It replaces the clusterS3Providers field
// with a safely-snapshotted copy (acquired under the grouped s3Providers state lock) so
// concurrent CRUD mutations cannot produce a racy or inconsistent response.
func buildClusterAPIPayload(mycluster *cluster.Cluster) ([]byte, error) {
	cl, err := json.Marshal(mycluster)
	if err != nil {
		return nil, fmt.Errorf("marshal cluster: %w", err)
	}

	for crkey := range mycluster.Conf.Secrets {
		cl, err = sjson.SetBytes(cl, "config."+strcase.ToLowerCamel(crkey), "*:*")
		if err != nil {
			return nil, fmt.Errorf("mask secret %s: %w", crkey, err)
		}
	}

	// Reduce the content of the cluster object
	cl, _ = sjson.DeleteBytes(cl, "config.apps")
	cl, _ = sjson.DeleteBytes(cl, "servers")
	cl, _ = sjson.DeleteBytes(cl, "proxies")
	cl, _ = sjson.DeleteBytes(cl, "apps")

	// Overwrite clusterS3Providers with a safe snapshot acquired under the mutex
	// to prevent races with concurrent CRUD mutations (AddS3Provider, etc.).
	s3snap := mycluster.GetS3ProvidersSnapshot()
	s3JSON, merr := json.Marshal(s3snap)
	if merr != nil {
		return nil, fmt.Errorf("marshal clusterS3Providers snapshot: %w", merr)
	}
	cl, err = sjson.SetRawBytes(cl, "clusterS3Providers", s3JSON)
	if err != nil {
		return nil, fmt.Errorf("set clusterS3Providers snapshot: %w", err)
	}

	return cl, nil
}

func (repman *ReplicationManager) handlerMuxCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		cl, err := buildClusterAPIPayload(mycluster)
		if err != nil {
			http.Error(w, "Error Marshal", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(cl)
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

}

// handlerMuxClusterSettings handles the retrieval of settings for a given cluster.
// @Summary Retrieve settings for a specific cluster
// @Description This endpoint retrieves the settings for the specified cluster.
// @Tags ClusterSettings
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} config.Config "Cluster settings"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings [get]
func (repman *ReplicationManager) handlerMuxClusterSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(*mycluster.Conf)
		if err != nil {
			http.Error(w, "Encoding error in settings", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

}

// handlerMuxClusterPlugins returns the list of active log-tailer plugins for
// the cluster together with their current resolved config and enabled state.
//
// @Summary  List active log-tailer plugins for a cluster
// @Tags     ClusterSettings
// @Produce  json
// @Param    clusterName path string true "Cluster Name"
// @Success  200 {array}  object
// @Router   /api/clusters/{clusterName}/plugins [get]
func (repman *ReplicationManager) handlerMuxClusterPlugins(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	type PluginInfo struct {
		Name    string            `json:"name"`
		Enabled bool              `json:"enabled"`
		Config  map[string]string `json:"config"`
	}

	plugins := mycluster.PluginRegistry().All()
	result := make([]PluginInfo, 0, len(plugins))
	for _, p := range plugins {
		var cfg map[string]string
		if mycluster.Conf.PluginConfig != nil {
			cfg = mycluster.Conf.PluginConfig[p.Name()]
		}
		if cfg == nil {
			cfg = map[string]string{}
		}
		enabled := mycluster.Conf.LogPlugin
		if v, ok := cfg["enabled"]; ok {
			enabled = v != "false"
		}
		result = append(result, PluginInfo{
			Name:    p.Name(),
			Enabled: enabled,
			Config:  cfg,
		})
	}

	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	if err := e.Encode(result); err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
	}
}

// handlerMuxClusterPluginsReload forces a rescan of the cluster's plugin directory
// and reloads the global registry.  Useful after deploying new plugin binaries
// without restarting the server or triggering a topology reset.
//
// @Summary  Reload external log plugins for a cluster
// @Tags     ClusterSettings
// @Param    clusterName path string true "Cluster Name"
// @Success  200
// @Router   /api/clusters/{clusterName}/plugins/reload [post]
func (repman *ReplicationManager) handlerMuxClusterPluginsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	mycluster.ReloadLogPlugins()
	w.WriteHeader(http.StatusOK)
}

// handlerMuxClusterSendVaultToken sends the Vault token to the specified cluster via email.
// @Summary Send Vault token to a specific cluster
// @Description This endpoint sends the Vault token to the specified cluster via email.
// @Tags ClusterVault
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Vault token sent successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/send-vault-token [post]
func (repman *ReplicationManager) handlerMuxClusterSendVaultToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.SendVaultTokenByMail(mycluster.Conf)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterMonitorSchemas triggers the monitoring of schemas for a given cluster.
// @Summary Monitor schemas for a specific cluster
// @Description This endpoint triggers the monitoring of schemas for the specified cluster.
// @Tags ClusterMonitor
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered schema monitoring"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/monitor-schemas [post]
func (repman *ReplicationManager) handlerMuxClusterMonitorSchemas(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.SetWaitMonitorSchema()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaChecksumAllTable handles the checksum calculation for all tables in a given cluster.
// @Summary Calculate checksum for all tables in a specific cluster
// @Description This endpoint triggers the checksum calculation for all tables in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for all tables"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/checksum-all-tables [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumAllTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		master := mycluster.GetMaster()
		if master == nil || len(master.Tables) == 0 {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Checksum all tables requested; schema cache empty. Triggered schema monitoring; re-run checksum after cache is ready.")
			mycluster.SetWaitMonitorSchema()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "schema cache empty; schema monitoring triggered; retry checksum after cache is populated",
			})
			return
		}
		go mycluster.CheckAllTableChecksum()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterChecksumSchema handles the checksum calculation for a specific table in a given cluster.
// @Summary Calculate checksum for a specific schema in a specific cluster
// @Description This endpoint triggers the checksum calculation for all tables in a schema in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for the schema"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/all/actions/checksum-schema [post]
func (repman *ReplicationManager) handlerMuxClusterChecksumSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.CheckAllTableChecksumSchema(vars["schemaName"])
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaChecksumTable handles the checksum calculation for a specific table in a given cluster.
// @Summary Calculate checksum for a specific table in a specific cluster
// @Description This endpoint triggers the checksum calculation for a specific table in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for the table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.CheckTableChecksum(vars["schemaName"], vars["tableName"])
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaChecksumRepairAllTable handles the repair checksum  for all tables in a given cluster.
// @Summary Compute Repair for all tables in a specific cluster
// @Description This endpoint triggers the checksum calculation for all tables in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for all tables"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/checksum-repair-all-tables [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumRepairAllTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		master := mycluster.GetMaster()
		if master == nil || len(master.Tables) == 0 {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Repair Checksum all tables requested; schema cache empty. Triggered schema monitoring; re-run checksum after cache is ready.")
			mycluster.SetWaitMonitorSchema()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"message": "schema cache empty; schema monitoring triggered; retry checksum after cache is populated",
			})
			return
		}
		go mycluster.RepairAllTableChecksum()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaChecksumRepairTable handles repair after checksum calculation for a specific table in a given cluster.
// @Summary Repair checksum error for a specific table in a specific cluster
// @Description This endpoint triggers the checksum calculation for a specific table in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for the table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-repair-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumRepairTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.RepairOneTableChecksum(vars["schemaName"], vars["tableName"])
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaAnalyzeAllTables handles the analyze calculation for all tables in a given cluster.
// @Summary Calculate analyze for all tables in a specific cluster
// @Description This endpoint triggers the analyze calculation for all tables in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered analyze calculation for all tables"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/analyze-all-tables/{persistent} [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaAnalyzeAllTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		persistent := mycluster.Conf.AnalyzeUsePersistent
		if strings.ToUpper(vars["persistent"]) == "TRUE" {
			persistent = true
		} else if strings.ToUpper(vars["persistent"]) == "FALSE" {
			persistent = false
		}

		go mycluster.JobAnalyzeSQL(persistent)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterAnalyzeSchema handles the analyze calculation for a specific table in a given cluster.
// @Summary Calculate analyze for a specific schema in a specific cluster
// @Description This endpoint triggers the analyze calculation for all tables in a schema in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Success 200 {string} string "Successfully triggered analyze calculation for the schema"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/all/actions/analyze-schema/{persistent} [post]
func (repman *ReplicationManager) handlerMuxClusterAnalyzeSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		persistent := mycluster.Conf.AnalyzeUsePersistent
		if strings.ToUpper(vars["persistent"]) == "TRUE" {
			persistent = true
		} else if strings.ToUpper(vars["persistent"]) == "FALSE" {
			persistent = false
		}
		if vars["schemaName"] == "" {
			http.Error(w, "No schema name provided", http.StatusBadRequest)
			return
		}
		go mycluster.JobAnalyzeSchema(vars["schemaName"], "", persistent)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaAnalyzeTable handles the analyze calculation for a specific table in a given cluster.
// @Summary Calculate analyze for a specific table in a specific cluster
// @Description This endpoint triggers the analyze calculation for a specific table in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Success 200 {string} string "Successfully triggered analyze calculation for the table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/analyze-table/{persistent} [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaAnalyzeTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if vars["schemaName"] == "" {
			http.Error(w, "No schema name provided", http.StatusBadRequest)
			return
		}

		if vars["tableName"] == "" {
			http.Error(w, "No table name provided", http.StatusBadRequest)
			return
		}

		persistent := mycluster.Conf.AnalyzeUsePersistent
		if strings.ToUpper(vars["persistent"]) == "TRUE" {
			persistent = true
		} else if strings.ToUpper(vars["persistent"]) == "FALSE" {
			persistent = false
		}
		go mycluster.JobAnalyzeSchema(vars["schemaName"], vars["tableName"], persistent)
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaUniversalTable handles the setting of a universal table for a given cluster.
// @Summary Set a universal table for a specific cluster
// @Description This endpoint sets a universal table for the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Success 200 {string} string "Successfully set universal table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/universal-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaUniversalTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		for _, pri := range mycluster.Proxies {
			if pr, ok := pri.(*cluster.MariadbShardProxy); ok {
				go mycluster.ShardSetUniversalTable(pr, vars["schemaName"], vars["tableName"])
			}
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaReshardTable handles the resharding of a table for a given cluster.
// @Summary Reshard a table for a specific cluster
// @Description This endpoint triggers the resharding of a table for the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Param clusterList path string false "Cluster List"
// @Success 200 {string} string "Successfully triggered resharding of the table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table/{clusterList} [post]
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaReshardTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		for _, pri := range mycluster.Proxies {
			if pr, ok := pri.(*cluster.MariadbShardProxy); ok {
				clusters := mycluster.GetClusterListFromShardProxy(mycluster.Conf.MdbsProxyHosts)
				if vars["clusterList"] == "" {
					mycluster.ShardProxyReshardTable(pr, vars["schemaName"], vars["tableName"], clusters)
				} else {
					var clustersFilter map[string]*cluster.Cluster
					for _, c := range clusters {
						if strings.Contains(vars["clusterList"], c.GetName()) {
							clustersFilter[c.GetName()] = c
						}
					}
					mycluster.ShardProxyReshardTable(pr, vars["schemaName"], vars["tableName"], clustersFilter)
				}
			}
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterSchemaMoveTable handles the movement of a table to a different shard cluster.
// @Summary Move a table to a different shard cluster
// @Description This endpoint moves a table to a different shard cluster for the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Param clusterShard path string true "Cluster Shard"
// @Success 200 {string} string "Successfully moved table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/move-table/{clusterShard} [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaMoveTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Source cluster not found", http.StatusNotFound)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	// Validate required parameters
	if vars["clusterShard"] == "" {
		http.Error(w, "Destination cluster name is required", http.StatusBadRequest)
		return
	}
	if vars["schemaName"] == "" || vars["tableName"] == "" {
		http.Error(w, "Schema name and table name are required", http.StatusBadRequest)
		return
	}

	// Get destination cluster
	destcluster := repman.getClusterByName(vars["clusterShard"])
	if destcluster == nil {
		http.Error(w, "Destination cluster not found", http.StatusNotFound)
		return
	}

	// Find shard proxy and move table
	for _, pri := range mycluster.Proxies {
		if pr, ok := pri.(*cluster.MariadbShardProxy); ok {
			mycluster.ShardProxyMoveTable(pr, vars["schemaName"], vars["tableName"], destcluster)
			return
		}
	}

	// No shard proxy found
	http.Error(w, "No MariaDB shard proxy found in cluster", http.StatusBadRequest)
}

// handlerMuxClusterSchema handles the retrieval of schema information for a given cluster.
// @Summary Retrieve schema information for a specific cluster
// @Description This endpoint retrieves the schema information for the specified cluster.
// @Tags ClusterSchema
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} map[string]interface{} "Schema information"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema [get]
func (repman *ReplicationManager) handlerMuxClusterSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		if mycluster.GetMaster() != nil {
			err := e.Encode(mycluster.GetMaster().GetDictTables())
			if err != nil {
				http.Error(w, "Encoding error in settings", http.StatusInternalServerError)
				return
			}
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerDiffVariables handles the retrieval of variable differences for a given cluster.
// @Summary Retrieve variable differences for a specific cluster
// @Description This endpoint retrieves the variable differences for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} cluster.VariableDiff "List of variable differences"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/diffvariables [get]
func (repman *ReplicationManager) handlerDiffVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		vars := mycluster.DiffVariables
		if vars == nil {
			vars = []cluster.VariableDiff{}
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(vars)
		if err != nil {
			http.Error(w, "Encoding error for DiffVariables", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerRotatePasswords rotates the passwords for a given cluster.
// @Summary Rotate passwords for a specific cluster
// @Description This endpoint rotates the passwords for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully rotated passwords"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/rotate-passwords [post]
func (repman *ReplicationManager) handlerRotatePasswords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.RotatePasswords()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterGraphiteFilterList handles the retrieval of Graphite filter list for a given cluster.
// @Summary Retrieve Graphite filter list for a specific cluster
// @Description This endpoint retrieves the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of Graphite filters"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/graphite-filterlist [get]
func (repman *ReplicationManager) handlerMuxClusterGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.Header().Set("Content-Type", "application/json")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		list := mycluster.GetGraphiteFilterList()
		err := e.Encode(list)
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}

	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterSetGraphiteFilterList sets the Graphite filter list for a given cluster.
// @Summary Set Graphite filter list for a specific cluster
// @Description This endpoint sets the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param filterType path string true "Filter Type"
// @Param body body cluster.GraphiteFilterList true "Graphite Filter List"
// @Success 200 {string} string "Filterlist updated"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/set-graphite-filterlist/{filterType} [post]
func (repman *ReplicationManager) handlerMuxClusterSetGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var gfilter cluster.GraphiteFilterList
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		err := json.NewDecoder(r.Body).Decode(&gfilter)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode error :%s", err.Error()), http.StatusInternalServerError)
			return
		}

		err = mycluster.SetGraphiteFilterList(vars["filterType"], gfilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write([]byte("Filterlist updated"))
		return
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterReloadGraphiteFilterList handles the reloading of Graphite filter list for a given cluster.
// @Summary Reload Graphite filter list for a specific cluster
// @Description This endpoint reloads the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded Graphite filter list"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/reload-graphite-filterlist [post]
func (repman *ReplicationManager) handlerMuxClusterReloadGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.ReloadGraphiteFilterList()
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterResetGraphiteFilterList handles the reset of Graphite filter list for a given cluster.
// @Summary Reset Graphite filter list for a specific cluster
// @Description This endpoint resets the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param template path string true "Template"
// @Success 200 {string} string "Successfully reset Graphite filter list"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/reset-graphite-filterlist/{template} [post]
func (repman *ReplicationManager) handlerMuxClusterResetGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetGraphiteWhitelistTemplate(vars["template"])
		if err := mycluster.ResetFilterListRegexp(); err != nil {
			http.Error(w, fmt.Sprintf("Error while reset filterlist: %s", err.Error()), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterGetJobEntries retrieves job entries for a specific cluster.
// @Summary Retrieve job entries for a specific cluster
// @Description This endpoint retrieves the job entries for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of job entries"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/jobs [get]
func (repman *ReplicationManager) handlerMuxClusterGetJobEntries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		entries, _ := mycluster.JobsGetEntries()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	} else {
		http.Error(w, "Cluster Not Found", http.StatusInternalServerError)
		return
	}
}

// handlerMuxAcceptSubscription handles the acceptance of a subscription for a given cluster.
// @Summary Accept a subscription for a specific cluster
// @Description This endpoint accepts a subscription for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body cluster.UserForm true "User Form"
// @Success 200 {string} string "Email sent to sponsor!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error accepting subscription"
// @Router /api/clusters/{clusterName}/sales/accept-subscription [post]
func (repman *ReplicationManager) handlerMuxAcceptSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	// if mycluster.Conf.Cloud18DatabaseReadSrvRecord == "" {
	// 	http.Error(w, "Empty Read Srv Record", http.StatusForbidden)
	// 	return
	// }

	// if mycluster.Conf.Cloud18DatabaseReadWriteSrvRecord == "" {
	// 	http.Error(w, "Empty Read-Write Srv Record", http.StatusForbidden)
	// 	return
	// }

	// if mycluster.Conf.Cloud18DatabaseReadWriteSplitSrvRecord == "" {
	// 	http.Error(w, "Empty Read-Write Split Srv Record", http.StatusForbidden)
	// 	return
	// }

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Processing sponsorship for %s with %s as sponsor", mycluster.Name, userform.Username)

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting up db credentials for sponsor of cluster %s", mycluster.Name)

	suser, spass := misc.SplitPair(mycluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"))
	if suser == "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No sponsor db credentials found. Generating sponsor db credentials")
		suser = "sponsor"
	}
	if spass == "" {
		spass, _ = mycluster.GeneratePassword()
	}

	err = repman.setClusterSetting(mycluster, "cloud18-sponsor-user-credentials", base64.StdEncoding.EncodeToString([]byte(suser+":"+spass)))
	// if err != nil {
	// 	http.Error(w, "Error setting sponsor db credentials :"+err.Error(), http.StatusInternalServerError)
	// 	return
	// }

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting up db credentials for dba of cluster %s", mycluster.Name)

	duser, dpass := misc.SplitPair(mycluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
	if duser == "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No dba database credentials found. Generating dba credentials")
		duser = "dba"
	}
	if dpass == "" {
		dpass, _ = mycluster.GeneratePassword()
	}

	err = repman.setClusterSetting(mycluster, "cloud18-dba-user-credentials", base64.StdEncoding.EncodeToString([]byte(duser+":"+dpass)))
	// if err != nil {
	// 	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "The sponsorship process for %s is proceeding without creating a DBA user, as it does not impact the sponsor's operations", mycluster.Name)
	// }

	err = repman.AcceptSubscription(userform, mycluster)
	if err != nil {
		// Reset sponsor credentials if failed
		repman.setClusterSetting(mycluster, "cloud18-sponsor-user-credentials", base64.StdEncoding.EncodeToString([]byte("")))
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error accepting subscription : %v", err)
		http.Error(w, "Error accepting subscription :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERTOK", "User %s registered as sponsor successfully", userform.Username)

	if repman.Conf.Cloud18SalesSubscriptionValidateScript != "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Executing script after sponsor validated")
		repman.BashScriptSalesSubscriptionValidate(mycluster, userform.Username, uinfomap["User"])
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No script to execute after sponsor validated")
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending sponsor activation email to user %s", userform.Username)

	err = repman.SendSponsorActivationMail(mycluster, userform)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Failed to send sponsor activation email to %s: %v", userform.Username, err)
		http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sponsor activation email sent to %s", userform.Username)

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending sponsor db credentials to user %s", userform.Username)

	err = repman.SendSponsorCredentialsMail(mycluster)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Failed to send sponsor db credentials to %s: %v", userform.Username, err)
		http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
		return
	}

	err = repman.SendDBACredentialsMail(mycluster, "dbops", "admin")
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send dba db credentials to dbops: %v", err)
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sponsor DB credentials sent!")

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent to sponsor!"))
}

// handlerMuxRejectSubscription handles the rejection of a subscription for a given cluster.
// @Summary Reject a subscription for a specific cluster
// @Description This endpoint rejects a subscription for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body cluster.UserForm true "User Form"
// @Success 200 {string} string "Subscription removed!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error removing subscription"
// @Router /api/clusters/{clusterName}/sales/refuse-subscription [post]
func (repman *ReplicationManager) handlerMuxRejectSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	// If user is not the submitter, check if he has the right to reject
	if uinfomap["User"] != userform.Username {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
	}

	err = repman.CancelSubscription(userform, mycluster)
	if err != nil {
		http.Error(w, "Error removing subscription :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Pending subscription for %s is rejected!")

	err = repman.SendPendingRejectionMail(mycluster, userform)
	if err != nil {
		http.Error(w, "Error sending rejection mail :"+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Subscription removed!"))
}

// handlerMuxRemoveSponsor handles the removal of a sponsor from a given cluster.
// @Summary Remove a sponsor from a specific cluster
// @Description This endpoint removes a sponsor from the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body cluster.UserForm true "User Form"
// @Success 200 {string} string "Sponsor subscription removed!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error removing sponsor subscription"
// @Router /api/clusters/{clusterName}/sales/end-subscription [post]
func (repman *ReplicationManager) handlerMuxRemoveSponsor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	// If user is not the submitter, check if he has the right to remove sponsor
	if uinfomap["User"] != userform.Username {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Ending subscription from sponsor %s for cluster %s by %s", userform.Username, mycluster.Name, uinfomap["User"])
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Ending subscription for cluster %s by %s", mycluster.Name, uinfomap["User"])
	}

	err = repman.EndSubscription(userform, mycluster)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error removing sponsor subscription: %s", err)
		http.Error(w, "Error removing sponsor subscription :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Revoking db privileges from sponsor %s for cluster %s", userform.Username, mycluster.Name)
	mycluster.RevokeDBUserGrants(mycluster.GetSponsorUser())

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Removing sponsor db credentials for cluster %s", mycluster.Name)
	repman.setClusterSetting(mycluster, "cloud18-sponsor-user-credentials", base64.StdEncoding.EncodeToString([]byte("")))

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Changing dba credentials for cluster %s", mycluster.Name)
	dpass, _ := mycluster.GeneratePassword()
	repman.setClusterSetting(mycluster, "cloud18-dba-user-credentials", base64.StdEncoding.EncodeToString([]byte("dba:"+dpass)))

	if repman.Conf.Cloud18SalesUnsubscribeScript != "" {
		repman.BashScriptSalesUnsubscribe(mycluster, userform.Username, uinfomap["User"])
	}

	err = repman.SendSponsorUnsubscribeMail(mycluster, userform)
	if err != nil {
		http.Error(w, "Error sending rejection mail :"+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Sponsor subscription removed!"))
}

type CredentialMailForm struct {
	Username       string `json:"username"`
	CredentialType string `json:"type"`
}

// handlerMuxSendCredentials sends the credentials to the specified user via email.
// @Summary Send credentials to a specific user
// @Description This endpoint sends the credentials to the specified user via email.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CredentialMailForm true "Credential Mail Form"
// @Success 200 {string} string "Credentials sent to user!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error sending email"
// @Router /api/clusters/{clusterName}/users/send-credentials [post]
func (repman *ReplicationManager) handlerMuxSendCredentials(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	valid, delegator := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	duser, ok := mycluster.APIUsers[delegator]
	if !ok {
		http.Error(w, "User does not exists", http.StatusBadRequest)
		return
	}

	var credForm CredentialMailForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&credForm)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	u, ok := mycluster.APIUsers[credForm.Username]
	if !ok {
		http.Error(w, "User does not exists", http.StatusBadRequest)
		return
	}

	to := u.User
	if to == "admin" {
		to = repman.Conf.Cloud18GitUser
	}

	switch credForm.CredentialType {
	case "db":
		if !duser.Roles[config.RoleDBOps] && !(duser.Roles[config.RoleExtDBOps] && duser.User == u.User) {
			http.Error(w, "Delegator has no ACL to send DBA Credentials", http.StatusForbidden)
			return
		}

		err = repman.SendDBACredentialsMail(mycluster, to, delegator)
		if err != nil {
			http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "DBA Credentials sent to %s. Delegator: %s", to, delegator)
	case "sys":
		if !duser.Roles[config.RoleSysOps] && !(duser.Roles[config.RoleExtSysOps] && duser.User == u.User) {
			http.Error(w, "Delegator has no ACL to send DBA Credentials", http.StatusForbidden)
			return
		}
		err = repman.SendSysAdmCredentialsMail(mycluster, to, delegator)
		if err != nil {
			http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SysAdm Credentials sent to %s. Delegator: %s", to, delegator)
	case "sponsor":
		if !duser.Roles[config.RoleSysOps] && !(duser.Roles[config.RoleSponsor] && duser.User == u.User) {
			http.Error(w, "Delegator has no ACL to send DBA Credentials", http.StatusForbidden)
			return
		}

		err = repman.SendSponsorCredentialsMail(mycluster)
		if err != nil {
			http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sponsor Credentials sent to %s. Delegator: %s", to, delegator)
	default:
		http.Error(w, "Invalid credential type :"+credForm.CredentialType, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Credentials sent to user!"))
}

// handlerMuxRefreshStagingCluster handles the HTTP request to refresh the staging cluster.
// @Summary Refresh Staging Cluster
// @Description Refreshes the staging cluster specified by the cluster name in the URL.
// @Tags ClusterActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Staging cluster refresh initiated"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/staging-refresh [post]
func (repman *ReplicationManager) handlerMuxRefreshStagingCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.RefreshStaging(mycluster, "") // "" means no GTID sync needed since restore from same cluster
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxReloadStagingScript handles the HTTP request to reload the staging script.
// @Summary Reload Staging Script
// @Description Reloads the staging script specified by the cluster name in the URL.
// @Tags ClusterActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Staging script reloaded"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/staging-reload-script [post]
func (repman *ReplicationManager) handlerMuxReloadStagingScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		err := mycluster.ReloadStagingScript()
		if err != nil {
			http.Error(w, "Error reloading staging script :"+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Staging script reloaded"))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxSubscribeExternalOps handles the registration of external operations for a given cluster.
// @Summary subscribe external operations for a specific cluster
// @Description This endpoint subscribes external operations for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CloudUserForm true "User Form"
// @Success 200 {string} string "Email sent to sponsor!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error subscribing external operations"
// @Router /api/clusters/{clusterName}/ext-role/subscribe [post]
func (repman *ReplicationManager) handlerMuxSubscribeExternalOps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var userform CloudUserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	partner, ok := repman.GetPartnerByMail(userform.Username)
	if !ok {
		http.Error(w, "Invalid partner", http.StatusInternalServerError)
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Registering external operations for %s with %s as %s", mycluster.Name, userform.Username, userform.Roles)

	err = repman.RegisterExternalOps(userform, mycluster, uinfomap["User"])
	if err != nil {
		http.Error(w, "Error subscribing external operations :"+err.Error(), http.StatusInternalServerError)
		return
	}

	err = repman.SendSponsorExternalOpsSubscriptionMail(mycluster, userform, partner)
	if err != nil {
		http.Error(w, "Error sending email to sponsor :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Partner %s requested as %s successfully", userform.Username, userform.Roles)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent to sponsor!"))
}

// handlerMuxQuoteExternalOps handles the quoting of external operations for a given cluster.
// @Summary Quote external operations for a specific cluster
// @Description This endpoint quotes external operations for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CloudUserForm true "User Form"
// @Success 200 {string} string "Email sent to sponsor!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error accepting external operations"
// @Router /api/clusters/{clusterName}/ext-role/quote [post]
func (repman *ReplicationManager) handlerMuxQuoteExternalOps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var userform CloudUserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Update external operations for %s with %s as %s", mycluster.Name, userform.Username, userform.Roles)

	err = repman.QuoteExternalOps(userform, mycluster, uinfomap["User"])
	if err != nil {
		http.Error(w, "Error accepting external operations :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending external ops quotation email to user %s", userform.Username)

	err = repman.SendExternalOpsSubscriptionMail(mycluster, userform)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send external ops quotation email to %s: %v", userform.Username, err)
		http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "External quotation email sent to %s", userform.Username)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent to partner!"))
}

// handlerMuxAcceptExternalOps handles the acceptance of external operations for a given cluster.
// @Summary Accept external operations for a specific cluster
// @Description This endpoint accepts external operations for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CloudUserForm true "User Form"
// @Success 200 {string} string "Email sent to sponsor!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error accepting subscription"
// @Router /api/clusters/{clusterName}/ext-role/accept [post]
func (repman *ReplicationManager) handlerMuxAcceptExternalOps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var userform CloudUserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	partner, ok := repman.GetPartnerByMail(userform.Username)
	if !ok {
		http.Error(w, "Invalid partner", http.StatusInternalServerError)
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Processing external operations for %s with %s as %s by user %s", mycluster.Name, userform.Username, userform.Roles, uinfomap["User"])

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting up db credentials for sponsor of cluster %s", mycluster.Name)

	if userform.Roles == "extdbops" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting up db credentials for dba of cluster %s", mycluster.Name)
		duser, dpass := misc.SplitPair(mycluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
		if duser == "" {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No dba database credentials found. Generating dba credentials")
			duser = "dba"
		}
		if dpass == "" {
			dpass, _ = mycluster.GeneratePassword()
		}

		// Set dba credentials, return error if failed
		err = repman.setClusterSetting(mycluster, "cloud18-dba-user-credentials", base64.StdEncoding.EncodeToString([]byte(duser+":"+dpass)))
		if err != nil {
			http.Error(w, "Error setting dba db credentials :"+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	err = repman.AcceptExternalOps(userform, mycluster, uinfomap["User"])
	if err != nil {
		http.Error(w, "Error accepting external operations :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "User %s registered as %s successfully", userform.Username, userform.Roles)

	if repman.Conf.Cloud18SalesExternalOpsValidateScript != "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Executing script after external ops validated")
		repman.BashScriptExternalOpsValidate(mycluster, userform.Username, userform.Roles, uinfomap["User"])
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No script to execute after external ops validated")
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending external ops activation email to user %s", userform.Username)

	err = repman.SendExternalOpsActivationMail(mycluster, userform)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send external ops activation email to %s: %v", userform.Username, err)
		http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "External activation email sent to %s", userform.Username)

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending external ops activation email to sponsor %s", mycluster.GetSponsorEmail())

	err = repman.SendSponsorExternalOpsActivationMail(mycluster, userform.Roles, partner)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send external ops activation email to %s: %v", mycluster.GetSponsorEmail(), err)
		http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "External activation email sent to %s", mycluster.GetSponsorEmail())

	if userform.Roles == "extdbops" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending dba db credentials to user %s", userform.Username)
		err = repman.SendDBACredentialsMail(mycluster, userform.Username, uinfomap["User"])
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send dba db credentials to %s: %v", userform.Username, err)
			http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
			return
		}
	} else if userform.Roles == "extsysops" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending sysadm db credentials to user %s", userform.Username)
		err = repman.SendSysAdmCredentialsMail(mycluster, userform.Username, uinfomap["User"])
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send sysadm db credentials to %s: %v", userform.Username, err)
			http.Error(w, "Error sending email :"+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "External ops credentials sent!")

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent to sponsor!"))
}

// handlerMuxRefuseExternalOps handles the rejection of external operations for a given cluster.
// @Summary Reject external operations for a specific cluster
// @Description This endpoint rejects external operations for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CloudUserForm true "User Form"
// @Success 200 {string} string "Subscription removed!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error removing subscription"
// @Router /api/clusters/{clusterName}/ext-role/refuse [post]
func (repman *ReplicationManager) handlerMuxRefuseExternalOps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	var userform CloudUserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	if userform.Reason == "" {
		http.Error(w, "A reason must be provided e.g. 'Subscription expired'", http.StatusInternalServerError)
		return
	}

	partner, ok := repman.GetPartnerByMail(userform.Username)
	if !ok {
		http.Error(w, "Invalid partner", http.StatusInternalServerError)
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	// If user is not the submitter, check if he has the right to reject
	if uinfomap["User"] != userform.Username {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
	}

	err = repman.CancelExternalOps(userform, mycluster)
	if err != nil {
		http.Error(w, "Error removing partnership :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pending partnership for %s is rejected!")

	err = repman.SendSponsorPendingRejectionExternalOpsMail(mycluster, userform.Roles, partner)
	if err != nil {
		http.Error(w, "Error sending rejection mail to sponsor:"+err.Error(), http.StatusInternalServerError)
		return
	}

	err = repman.SendPartnerPendingRejectionExternalOpsMail(mycluster, userform)
	if err != nil {
		http.Error(w, "Error sending rejection mail to partner:"+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Subscription removed!"))
}

// handlerMuxRemoveExternalOps handles the removal of external operations for a given cluster.
// @Summary Remove external operations for a specific cluster
// @Description This endpoint removes external operations for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CloudUserForm true "User Form"
// @Success 200 {string} string "Sponsor partnership removed!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error removing sponsor partnership"
// @Router /api/clusters/{clusterName}/sales/end-external-ops [post]
func (repman *ReplicationManager) handlerMuxRemoveExternalOps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", http.StatusInternalServerError)
		return
	}

	var userform CloudUserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	partner, ok := repman.GetPartnerByMail(userform.Username)
	if !ok {
		http.Error(w, "Invalid partner", http.StatusInternalServerError)
		return
	}

	if userform.Reason == "" {
		http.Error(w, "A reason must be provided e.g. 'Subscription expired'", http.StatusInternalServerError)
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	// If user is not the submitter, check if he has the right to remove sponsor
	if uinfomap["User"] != userform.Username {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Ending partnership with partner %s for cluster %s by %s", userform.Username, mycluster.Name, uinfomap["User"])
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Partner %s ending their partnership for cluster %s", uinfomap["User"], mycluster.Name)
	}

	err = repman.EndExternalOps(userform, mycluster)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error removing external partnership: %s", err)
		http.Error(w, "Error removing sponsor partnership :"+err.Error(), http.StatusInternalServerError)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Changing dba credentials for cluster %s", mycluster.Name)
	dpass, _ := mycluster.GeneratePassword()
	err = repman.setClusterSetting(mycluster, "cloud18-dba-user-credentials", base64.StdEncoding.EncodeToString([]byte("dba:"+dpass)))
	// Not fatal because not affecting removal of external ops
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error setting dba db credentials : %s", err)
		// Continue with the process
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "DBA credentials was not changed, please reset manually. Continuing with the process of removing external ops")
	}

	if repman.Conf.Cloud18SalesExternalOpsStopScript != "" {
		repman.BashScriptSalesUnsubscribe(mycluster, userform.Username, uinfomap["User"])
	}

	err = repman.SendSponsorExternalOpsEndMail(mycluster, userform.Roles, partner)
	if err != nil {
		http.Error(w, "Error sending partnership end mail for sponsor :"+err.Error(), http.StatusInternalServerError)
		return
	}

	err = repman.SendPartnerExternalOpsEndMail(mycluster, userform)
	if err != nil {
		http.Error(w, "Error sending partnership end mail for partner :"+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Sponsor partnership removed!"))
}

// handlerMuxClusterBackups handles the retrieval of backups for a given cluster.
// @Summary Retrieve backups for a specific cluster
// @Description This endpoint retrieves the backups for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param format query string false "Response format" Enums(default,legacy)
// @Success 200 {object} resticSnapshotsResponse "Snapshots with repository context"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/snapshots [get]
func (repman *ReplicationManager) handlerMuxClusterSnapshots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		// Get all snapshots
		snapshots := mycluster.GetSnapshots()
		metadataIndex := mycluster.BuildSnapshotMetadataIndex(snapshots)

		// Check for filter query parameter
		filterParam := r.URL.Query().Get("filter")
		if filterParam == "latest-per-session" {
			// Apply session-based deduplication filter
			snapshots = cluster.FilterMostRecentSnapshotsPerSessionWithIndex(mycluster, snapshots, metadataIndex)
		}

		repoPath := strings.TrimSpace(mycluster.Conf.BackupResticRepository)
		if mycluster.ResticManager != nil {
			managerPath := strings.TrimSpace(mycluster.ResticManager.GetRepoPath())
			if managerPath != "" {
				repoPath = managerPath
			}
		}

		responses := make([]resticSnapshotResponse, 0, len(snapshots))
		for i := range snapshots {
			snap := snapshots[i]
			statusString := mycluster.GetSnapshotMetadataStatusString(snap.Id)
			metadataReady := mycluster.IsSnapshotMetadataReady(snap.Id)
			metadata := metadataIndex[snap.Id]
			if metadata == nil {
				metadata = mycluster.SummarizeSnapshotMetadata(&snap)
			}
			responses = append(responses, resticSnapshotResponse{
				BackupSnapshot: snap,
				Metadata:       metadata,
				MetadataReady:  metadataReady,
				MetadataStatus: statusString,
				MetadataError:  mycluster.GetSnapshotMetadataError(snap.Id),
			})
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
		if format == "legacy" {
			err := e.Encode(responses)
			if err != nil {
				http.Error(w, "Encoding error", http.StatusInternalServerError)
				return
			}
			return
		}

		response := resticSnapshotsResponse{
			RepoPath:  repoPath,
			Stats:     mycluster.GetSnapshotStats(),
			Snapshots: responses,
		}

		err := e.Encode(response)
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterBackupStats handles the retrieval of backup stats for a given cluster.
// @Summary Retrieve backup stats for a specific cluster
// @Description This endpoint retrieves the backup stats for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} backupmgr.BackupStat "List of backups"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/stats [get]
func (repman *ReplicationManager) handlerMuxClusterSnapshotStat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetSnapshotStats())
		if err != nil {
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// withResticCluster is a helper function to handle requests that require a restic-enabled cluster.
// It checks for cluster existence, ACL validity, and restic backup enablement before invoking the provided handler.
// If any checks fail, it responds with the appropriate HTTP error.
// Parameters:
// - w: http.ResponseWriter to write the response.
// - r: *http.Request representing the incoming request.
// - mustEnabled: bool indicating if restic backup must be enabled.
// - handler: function to handle the request if all checks pass. It receives the cluster and URL variables as parameters.
func (repman *ReplicationManager) withResticCluster(
	w http.ResponseWriter,
	r *http.Request,
	mustEnabled bool,
	handler func(cluster *cluster.Cluster, vars map[string]string),
) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)

	cluster := repman.getClusterByName(vars["clusterName"])
	if cluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, cluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	if mustEnabled && !cluster.Conf.BackupRestic {
		http.Error(w, "Restic backup not enabled", http.StatusInternalServerError)
		return
	}

	handler(cluster, vars)
}

// handlerMuxResticRestoreConfig handles the HTTP request to restore the restic config for a given cluster.
// @Summary Restore Restic Config
// @Description Restores the restic config for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param force path string true "Force Restore" Enum(force, noforce) default(noforce)
// @Success 200 {string} string "Restic config restore done"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/restore-config/{force} [post]
func (repman *ReplicationManager) handlerMuxResticRestoreConfig(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, true, func(mycluster *cluster.Cluster, vars map[string]string) {
		var force bool
		if vars["force"] == "force" {
			force = true
		}

		err := mycluster.RestoreResticConfig(force)
		if err != nil {
			http.Error(w, "Error restoring restic config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Restic config restore done"))
	})
}

// handlerMuxResticFetch handles the HTTP request to fetch the restic snapshots for a given cluster.
// @Summary Fetch Restic Snapshots
// @Description Fetches the restic backup for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Restic snapshots fetch queued"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/fetch [post]
func (repman *ReplicationManager) handlerMuxResticFetch(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, true, func(mycluster *cluster.Cluster, vars map[string]string) {

		mycluster.ResticFetchRepo()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Restic snapshots fetch queued"))
	})
}

// handlerMuxResticPurge handles the HTTP request to purge the restic repo for a given cluster.
// @Summary Purge Restic Backup
// @Description Purges the restic backup for the specified cluster.
// @Description Required grant: db-backup
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param snapshotID path string true "Snapshot ID"
// @Param dry_run query bool false "Dry run restic purge"
// @Success 200 {string} string "Restic repository purged"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/purge/{snapshotID} [post]
func (repman *ReplicationManager) handlerMuxResticPurge(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, true, func(mycluster *cluster.Cluster, vars map[string]string) {
		dryRun := false
		if value := strings.TrimSpace(r.URL.Query().Get("dry_run")); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				http.Error(w, "Invalid dry_run value: "+value, http.StatusBadRequest)
				return
			}
			dryRun = parsed
		}

		if vars["snapshotID"] == "" {
			http.Error(w, "No snapshot ID provided, please provide one or use 'policy' to purge according to policy", http.StatusInternalServerError)
			return
		}
		if vars["snapshotID"] == "policy" {
			err := mycluster.ResticPurgeRepoWithOptions(true, dryRun)
			if err != nil {
				http.Error(w, "Error purging restic repo: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			err := mycluster.ResticPurgeSnapshotWithOptions(vars["snapshotID"], true, dryRun)
			if err != nil {
				http.Error(w, "Error adding purge task: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Restic repository purged"))
	})
}

// handlerMuxResticUnlock handles the HTTP request to unlock restic repo for a given cluster.
// @Summary Unlock Restic Repository
// @Description Unlocks the restic repository for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Restic repository unlocked"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/unlock [post]
func (repman *ReplicationManager) handlerMuxResticUnlock(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, true, func(mycluster *cluster.Cluster, vars map[string]string) {

		mycluster.ResticUnlockRepo()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Restic repository unlocked"))
	})
}

// handlerMuxResticInitRepo handles the HTTP request to init restic repo for a given cluster.
// @Summary Init Restic Repository
// @Description Inits the restic repository for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param force path string false "Force init" Enums(force)
// @Param body body backupmgr.ResticInitOption false "Init options"
// @Success 200 {string} string "Restic repository initialized"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/init [post]
// @Router /api/clusters/{clusterName}/restic/init/{force} [post]
func (repman *ReplicationManager) handlerMuxResticInitRepo(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, true, func(mycluster *cluster.Cluster, vars map[string]string) {
		var force bool
		if vars["force"] == "force" {
			force = true
		}

		var initOptions backupmgr.ResticInitOption
		if err := json.NewDecoder(r.Body).Decode(&initOptions); err != nil && err != io.EOF {
			http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		initOptions.Force = force

		err := mycluster.ResticInitRepoWithOptions(initOptions)
		if err != nil {
			http.Error(w, "Error initializing restic repository: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if effectivePrefix, ok := mycluster.ResticS3EffectivePrefixForInit(); ok {
			mycluster.Conf.BackupResticAwsPrefix = effectivePrefix
			mycluster.ReloadResticEnv()
			mycluster.ConfigManager.SaveConfig(mycluster, false)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Restic repository initialized"))
	})
}

// handlerMuxGetResticTaskQueue handles the HTTP request to get the restic task queue for a given cluster.
// @Summary Get Restic Task Queue
// @Description Gets the restic task queue for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} backupmgr.ResticTask "Task queue fetched"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/task-queue [get]
func (repman *ReplicationManager) handlerMuxGetResticTaskQueue(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {

		taskqueue, err := mycluster.ResticGetQueue()
		if err != nil {
			http.Error(w, "Error getting task queue :"+err.Error(), http.StatusInternalServerError)
			return
		}

		// Marshal provided interface into JSON structure
		taskqueueJSON, err := json.Marshal(taskqueue)
		if err != nil {
			http.Error(w, "Error marshalling task queue :"+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(taskqueueJSON)
	})
}

// ResticCurrentTaskResponse wraps current task state and queue.
type ResticCurrentTaskResponse struct {
	CurrentTask *backupmgr.ResticTaskState `json:"current_task,omitempty"`
	Queue       []*backupmgr.ResticTask    `json:"queue"`
}

// handlerMuxGetResticCurrentTask handles the HTTP request to get current restic task state and queue.
// @Summary Get Restic Current Task
// @Description Gets the current restic task state and queue for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} ResticCurrentTaskResponse "Current task and queue fetched. Progress fields are populated for backup tasks only."
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/task-current [get]
func (repman *ReplicationManager) handlerMuxGetResticCurrentTask(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		taskqueue, err := mycluster.ResticGetQueue()
		if err != nil {
			http.Error(w, "Error getting task queue :"+err.Error(), http.StatusInternalServerError)
			return
		}

		var currentTask *backupmgr.ResticTaskState
		if mycluster.ResticManager != nil {
			currentTask = mycluster.ResticManager.GetCurrentTaskState()
		}

		response := ResticCurrentTaskResponse{
			CurrentTask: currentTask,
			Queue:       taskqueue,
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "Error encoding current task response :"+err.Error(), http.StatusInternalServerError)
			return
		}
	})
}

// @Router /api/clusters/{clusterName}/restic/task-queue/resume [post]
func (repman *ReplicationManager) handlerMuxResticTaskQueueResume(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, true, func(mycluster *cluster.Cluster, vars map[string]string) {

		mycluster.ResticRunQueue()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Task queue resumed"))
	})
}

// @Router /api/clusters/{clusterName}/restic/task-queue/resume [post]
func (repman *ReplicationManager) handlerMuxResticTaskQueuePause(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {

		mycluster.ResticPauseQueue()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Task queue paused"))
	})
}

// handlerMuxModifyResticTaskQueue handles the HTTP request to modify the restic task queue for a given cluster.
// @Summary Modify Restic Task Queue
// @Description	Modify the restic task queue for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param moveType path backupmgr.MoveType true "Move Type"
// @Param taskID path string true "Task ID"
// @Param afterID path string false "After ID"
// @Success 200 {string} string "Task queue modified"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/task-queue/move/{moveType}/{taskID} [post]
// @Router /api/clusters/{clusterName}/restic/task-queue/move/{moveType}/{taskID}/{afterID} [post]
func (repman *ReplicationManager) handlerMuxResticTaskQueueMove(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		moveType := vars["moveType"]
		var taskID, afterID int

		// Parse ID to int
		taskID, err := strconv.Atoi(vars["taskID"])
		if err != nil {
			http.Error(w, "Invalid taskID", http.StatusInternalServerError)
			return
		}

		if moveType == "after" {
			afterID, err = strconv.Atoi(vars["afterID"])
			if err != nil {
				http.Error(w, "Invalid afterID", http.StatusInternalServerError)
				return
			}
		}

		switch moveType {
		case "after", "first", "last":
			err := mycluster.ResticModifyQueue(moveType, taskID, afterID)
			if err != nil {
				http.Error(w, "Error modifying task queue :"+err.Error(), http.StatusInternalServerError)
				return
			}
		default:
			http.Error(w, "Invalid moveType. Must be one of: after, first, last", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Task queue modified"))
	})
}

// handlerMuxCancelResticTask handles the HTTP request to cancel a restic task for a given cluster.
// @Summary Cancel Restic Task
// @Description	Cancel the specified restic task for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param taskID path string true "Task ID"
// @Success 200 {string} string "Task cancelled"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/task-queue/cancel/{taskID} [post]
func (repman *ReplicationManager) handlerMuxCancelResticTask(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {

		taskID, err := strconv.Atoi(vars["taskID"])
		if err != nil {
			http.Error(w, "Invalid taskID", http.StatusInternalServerError)
			return
		}

		err = mycluster.ResticCancelTask(taskID)
		if err != nil {
			http.Error(w, "Error cancelling task :"+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Task cancelled"))
	})
}

// handlerMuxResetResticTaskQueue handles the HTTP request to reset the restic task queue for a given cluster.
// @Summary Reset Restic Task Queue
// @Description	Empty the restic task queue for the specified cluster.
// @Tags ClusterRestic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Task queue reset"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/restic/task-queue/reset [get]
func (repman *ReplicationManager) handlerMuxResetResticTaskQueue(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {

		err := mycluster.ResticClearQueue()
		if err != nil {
			http.Error(w, "Error clearing task queue :"+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Task queue cleared"))
	})
}

type MeetAlertMessage struct {
	Fields  log.Fields `json:"fields"`
	Message string     `json:"message"`
}

// handlerMuxSendCloud18Alert handles the HTTP request to send a cloud18 alert for a given cluster.
// @Summary Send Cloud18 Alert
// @Description	Send a cloud18 alert for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param hooktype path string true "Hook Type" Enums(cloud18, slack)
// @Param body body MeetAlertMessage true "Alert Message"
// @Success 200 {string} string "Task queue reset"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/send-alert/{hooktype} [post]
func (repman *ReplicationManager) handlerMuxSendAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	hooktype := vars["hooktype"]

	if hooktype == "" {
		http.Error(w, "No hook type", http.StatusInternalServerError)
		return
	}

	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		var post MeetAlertMessage
		//decode request into post struct
		err := json.NewDecoder(r.Body).Decode(&post)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "Error in request")
			return
		}

		if mycluster.LogSlack.IsHookActive(hooktype) {
			mycluster.LogSlack.WithFields(post.Fields).Warn(post.Message)
		} else {
			http.Error(w, "No slack hook", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Message sent via logrus"))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterHealth handles the HTTP request to retrieve the status of a specified cluster.
// @Summary Get Cluster Health
// @Description	Get the health status of the specified cluster.
// @Tags ClusterHealth
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} peer.PeerHealth "Cluster health fetched"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/health [get]
func (repman *ReplicationManager) handlerMuxClusterHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mycluster.GetPeerHealth())
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxReseedFromParent handles the HTTP request to reseed a cluster from its parent cluster.
// @Summary Reseed from Parent Cluster
// @Description Reseed the specified cluster from its parent cluster.
// @Tags ClusterReplication
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Reseed from parent queued"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/reseed-from-parent [post]
func (repman *ReplicationManager) handlerMuxReseedFromParent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var strUser string
	var valid bool
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, strUser = repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if mycluster.Conf.ReplicationMultisourceHeadClusters == "" {
			http.Error(w, "No multisource cluster", http.StatusInternalServerError)
			return
		}

		pcluster := repman.GetParentClusterFromReplicationSource(mycluster.Conf.ReplicationMultisourceHeadClusters)
		if pcluster == nil {
			http.Error(w, "No parent cluster", http.StatusInternalServerError)
			return
		}

		if !pcluster.IsURLPassACL(strUser, strings.Replace(r.URL.Path, "/"+mycluster.Name+"/", "/"+pcluster.Name+"/", 1), true) {
			http.Error(w, "No valid ACL in parent cluster", http.StatusForbidden)
			return
		}

		go func() {
			cmaster := mycluster.GetMaster()
			if cmaster == nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel reseed from parent cluster. No master found", mycluster.Name)
				return
			}

			masterGTIDList, err := mycluster.ReseedFromParentCluster(pcluster, cmaster, "") // master will combine it's GTID list with the one from the parent cluster automatically
			if err == nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reseed from parent cluster %s done. Refreshing staging", pcluster.Name)

				slave := mycluster.GetSlaveByIndex(0)
				if slave == nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel refresh staging. No slave found for standalone candidate", mycluster.Name)
					return
				}
				starttime := time.Now()
				if slave.State == "SlaveLate" {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting for slave to sync replication")
					for slave.State == "SlaveLate" && time.Since(starttime) < 2*time.Minute {
						time.Sleep(1 * time.Second)
					}

					if slave.State == "SlaveLate" {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel refresh staging. Slave is still late after 2 minutes. Please refresh staging later or check the replication status")
						return
					}
				}

				if slave.State != "Slave" {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel refresh staging. Slave is not OK. Please check the replication status")
					return
				}

				err := mycluster.RefreshStaging(pcluster, masterGTIDList) // refresh staging and sync the GTID list from current master
				if err == nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reseed from parent cluster %s done", pcluster.Name)
					// Refresh staging is done. Now we can start the backup
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting backup for cluster %s", mycluster.Name)
					go cmaster.JobBackupLogical(context.Background())
				} else {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Refresh standalone failed: %s", err)
				}

			} else {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel refresh staging. Error reseeding from parent cluster: %s", err)
			}
		}()

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Reseed from parent queued"))

	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Reseed from parent queued"))
}

// handlerMuxServersPortRegenerateConfig handles the HTTP request to regenerate the configuration of a specific server port within a cluster.
// @Summary Get server port configuration
// @Description Retrieves the configuration of a specified server port within a cluster.
// @Tags Database
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Configuration regenerated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster" or "No server"
// @Router /api/clusters/{clusterName}/settings/actions/generate-configs/{servertype} [get]
func (repman *ReplicationManager) handlerMuxClusterRegenerateConfigs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		vars["servertype"] = strings.ToLower(vars["servertype"])
		if vars["servertype"] == "db" {
			if len(mycluster.Servers) > 0 {
				mycluster.SetConfigRefreshCookie()
			} else {
				http.Error(w, "No server", http.StatusInternalServerError)
				return
			}
		} else if vars["servertype"] == "proxy" {
			if len(mycluster.Proxies) > 0 {
				for _, prx := range mycluster.Proxies {
					if prx != nil {
						prx.GetProxyConfig()
					} else {
						http.Error(w, "No server", http.StatusInternalServerError)
						return
					}
				}
			}
		} else {
			http.Error(w, "No valid type", http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterVariablesPreserve handles the HTTP request to preserve or unpreserve a variable for a given cluster.
// @Summary Preserve or unpreserve a variable
// @Description Preserves or unpreserves a variable for the specified cluster.
// @Tags Database
// @Accept json
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Param variableName path string true "Variable Name"
// @Param preserve path string true "Preserve or unpreserve" Enums(true, false)
// @Success 200 {string} string "Variable preserved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/preserve-variable/{variableName}/{preserve} [get]
func (repman *ReplicationManager) handlerMuxClusterVariablesPreserve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		if vars["variableName"] == "" {
			http.Error(w, "Variable name can not be empty", http.StatusInternalServerError)
			return
		} else if strings.HasPrefix(vars["variableName"], "optimizer_switch") {
			http.Error(w, "Can not preserve 'optimizer_switch'. Use db-tags instead", http.StatusInternalServerError)
			return
		}

		var preserve bool
		var response string
		if strings.ToUpper(vars["preserve"]) == "TRUE" {
			preserve = true
			response = "Variable preserved successfully"
		} else if strings.ToUpper(vars["preserve"]) == "FALSE" {
			preserve = false
			response = "Variable unpreserved successfully"
		} else {
			http.Error(w, "No valid preserve key", http.StatusInternalServerError)
			return
		}

		mycluster.PreserveVariable(vars["variableName"], preserve)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))

	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

func (repman *ReplicationManager) handlerMuxApps(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		apps, err := json.MarshalIndent(mycluster.Apps, "", "\t")
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", http.StatusInternalServerError)
			return
		}

		for idx := range mycluster.Apps {
			apps, _ = sjson.DeleteBytes(apps, fmt.Sprintf("%d.appClusterSubstitute", idx))
			apps, _ = sjson.DeleteBytes(apps, fmt.Sprintf("%d.config.deployment", idx))
			apps, _ = sjson.SetBytes(apps, fmt.Sprintf("%d.config.appDbPass", idx), "*****")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(apps)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error writing response: ", err)
			http.Error(w, "Error writing response", http.StatusInternalServerError)
			return
		}
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

type DockerRegistryLoginForm struct {
	IsPrivate bool   `json:"private"`  // true if private registry, false if public
	Update    bool   `json:"update"`   // true if updating existing credentials, false if new credentials
	AuthType  string `json:"authType"` // "password" or "token"
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Template  string `json:"template"` // Optional template for the registry, e.g., "docker.io" or "quay.io"
}

// handlerDockerRegistryConnect handles the HTTP request to login to a Docker registry.
// @Summary Docker Registry Login
// @Description Logs in to a Docker registry using the provided credentials.
// @Tags Docker
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body DockerRegistryLoginForm true "Docker Registry Login Form"
// @Success 200 {string} string "Docker registry login successful"
// @Failure 400 {string} string "Error decoding request body"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error creating request" or "Error making request to Docker registry" or "Docker registry login failed"
// @Router /api/clusters/{clusterName}/docker/actions/registry-connect [post]
func (repman *ReplicationManager) handlerDockerRegistryConnect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		// URL parse registry get host and port
		var body DockerRegistryLoginForm
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil {
			http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		if body.AuthType != "password" && body.AuthType != "token" {
			http.Error(w, "Invalid auth type, must be 'password' or 'token'", http.StatusBadRequest)
			return
		}

		if body.URL == "" || body.Username == "" {
			http.Error(w, "URL and Username must be provided", http.StatusBadRequest)
			return
		}

		req, err := http.NewRequest("GET", body.URL, nil)
		if err != nil {
			http.Error(w, "Error creating request: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if body.AuthType == "password" {
			req.SetBasicAuth(body.Username, body.Password)
		} else if body.AuthType == "token" {
			req.Header.Set("Authorization", "Bearer "+body.Password)
		}

		client := &http.Client{Transport: &http.Transport{
			// Allow insecure connections for testing purposes
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Error making request to Docker registry: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			http.Error(w, fmt.Sprintf("Docker registry login failed: %s - %s", resp.Status, string(bodyBytes)), resp.StatusCode)
			return
		}

		// If we reach here, the login was successful
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Docker registry login successful for %s", body.URL)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Docker registry login successful"))
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerDockerImageFilesystemDir handles the HTTP request to list files in a directory of a Docker image.
// @Summary List Files in Docker Image Directory
// @Description Lists files in a specified directory of a Docker image.
// @Tags Docker
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param imageRef path string true "Docker Image Reference"
// @Success 200 {object} treehelper.FileTreeCache "List of files in the directory"
// @Failure 400 {string} string "Image reference or source directory not provided"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error listing files in image directory" or "Error encoding JSON"
// @Router /api/clusters/{clusterName}/docker/browse/{imageRef} [get]
func (repman *ReplicationManager) handlerDockerImageFilesystemDir(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		imageRef := strings.TrimSpace(vars["imageRef"])
		if imageRef == "" {
			http.Error(w, "Image reference not provided", http.StatusBadRequest)
			return
		}

		cacheDir := filepath.Join(mycluster.WorkingDir, ".cache", "docker", "images")
		results, err := dockerhelper.GetFileTreeCache(cacheDir, imageRef)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error listing files in image directory: ", err)
			http.Error(w, "Error listing files in image directory: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Write the JSON response
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "\t") // Pretty print
		if err := encoder.Encode(results); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error encoding JSON: ", err)
			http.Error(w, "Error encoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterGatewayServiceNodes handles the HTTP request to retrieve the gateway nodes of a cluster.
// @Summary Get Cluster Gateway Nodes
// @Description Retrieves the gateway nodes of the specified cluster.
// @Tags ClusterGateway
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of gateway nodes"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster" or "Error getting gateway nodes
// @Router /api/clusters/{clusterName}/opensvc-gateway [get]
func (repman *ReplicationManager) handlerMuxClusterGatewayServiceNodes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		svc := mycluster.OpenSVCConnect()
		nodes, err := svc.GetServiceNodeFromState(mycluster.Conf.Cloud18GatewayService)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error getting gateway nodes: ", err)
			http.Error(w, "Error getting gateway nodes: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Marshal provided interface into JSON structure
		nodesJSON, err := json.Marshal(nodes)
		if err != nil {
			http.Error(w, "Error marshalling nodes: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(nodesJSON)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error writing response: ", err)
			http.Error(w, "Error writing response: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterOpenSVCDaemonStatus handles the HTTP request to retrieve the OpenSVC daemon status of a cluster.
// @Summary Get OpenSVC Daemon Status
// @Description Retrieves the OpenSVC daemon status of the specified cluster.
// @Tags ClusterGateway
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} opensvc.DaemonNodeStats "OpenSVC daemon status fetched"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster" or "Error getting OpenSVC stats"
// @Router /api/clusters/{clusterName}/opensvc-stats [get]
func (repman *ReplicationManager) handlerMuxClusterOpenSVCDaemonStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		stats, err := mycluster.GetOpenSVCStats()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error getting OpenSVC stats: ", err)
			http.Error(w, "Error getting OpenSVC stats: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Marshal provided interface into JSON structure
		statsJSON, err := json.Marshal(stats)
		if err != nil {
			http.Error(w, "Error marshalling stats: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(statsJSON)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error writing response: ", err)
			http.Error(w, "Error writing response: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterIsInErrorState handles the HTTP request to check if a cluster is in an error state.
// @Summary Check if Cluster is in Error State
// @Description Checks if the specified cluster is in an error state.
// @Tags ClusterHealth
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param state path string true "State to check"
// @Success 200 {string} string "true" or "false"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/is-in-errstate/{errstate} [get]
func (repman *ReplicationManager) handlerMuxClusterIsInErrState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		isInErrorState := mycluster.IsInErrorState(vars["state"], "")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(fmt.Sprintf("%t", isInErrorState)))
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error writing response: ", err)
			http.Error(w, "Error writing response: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterAllowJobsUpgrade handles the HTTP request to allow jobs upgrade on all server within a cluster.
// @Summary Allow Jobs Upgrade on Cluster Servers
// @Description Flags all servers within the specified cluster to allow jobs upgrade.
// @Tags ClusterJobs
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Flagged for jobs upgrade"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster" or "No server"
// @Router /api/clusters/{clusterName}/actions/jobs-upgrade [get]
func (repman *ReplicationManager) handlerMuxClusterAllowJobsUpgrade(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		mycluster.SetRollingJobsUpgradeState()
		w.WriteHeader(200)
		w.Write([]byte("Cluster flagged for jobs upgrade"))
		return
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxClusterCheckLogLevel handles the HTTP request to check if a specific log level is enabled for a given task in a cluster.
// @Summary Check Cluster Log Level
// @Description Checks if a specific log level is enabled for a given task in the specified cluster.
// @Tags ClusterLogging
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Param task path config.TaskName true "Task Name"
// @Param level path LogLevelEnum true "Log Level"
// @Success 200 {string} string "true" or "false"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/jobs-log-level/{task}/{level} [get]
func (repman *ReplicationManager) handlerMuxClusterCheckJobLogLevel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var mod int
		level := vars["level"]
		task := vars["task"]
		switch task {
		case "auditlog":
			mod = config.ConstLogModDbAudit
		case "errorlog", "sqlerrorlog":
			mod = config.ConstLogModDbErrors
		case "slowquery":
			mod = config.ConstLogModDbSlowquery
		case "optimize":
			mod = config.ConstLogModDbOptimize
		case "mariabackup", "xtrabackup", "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariadbackup", "reseedmysqldump", "flashbackmysqldump":
			mod = config.ConstLogModBackupStream
		default:
			mod = config.ConstLogModTask
		}

		if mycluster.Conf.IsEligibleForPrinting(mod, level) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("true"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("false"))
		}
		return
	} else {
		http.Error(w, "No cluster", http.StatusInternalServerError)
	}
}

// handlerMuxGetPreservedVarsCnf retrieves the content of the preserved variables CNF file
// @Summary Get preserved variables CNF content
// @Description This endpoint retrieves the content of the preserved_variables.cnf file from the cluster's working directory
// @Tags ClusterSettings
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} map[string]string "CNF file content in JSON format with 'content' key"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "File not found"
// @Failure 500 {string} string "Error reading file"
// @Router /api/clusters/{clusterName}/settings/preserved-variables-cnf [get]
func (repman *ReplicationManager) handlerMuxGetPreservedVarsCnf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	// Read the preserved variables CNF content using cluster method
	content, err := mycluster.GetPreservedVarsCnfContent()
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Error reading preserved variables file: %v", err), http.StatusInternalServerError)
		return
	}

	// Return JSON response with content
	response := map[string]string{
		"content": content,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// PreservedVarsCnfRequest represents the request body for saving preserved variables CNF
type PreservedVarsCnfRequest struct {
	Content string `json:"content"`
}

// handlerMuxSavePreservedVarsCnf saves the updated content to the preserved variables CNF file
// @Summary Save preserved variables CNF content
// @Description This endpoint saves the updated content to the preserved_variables.cnf file in the cluster's working directory
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body PreservedVarsCnfRequest true "CNF file content"
// @Success 200 {string} string "Successfully saved preserved variables CNF"
// @Failure 403 {string} string "No valid ACL"
// @Failure 400 {string} string "Invalid request body"
// @Failure 500 {string} string "Error saving file"
// @Router /api/clusters/{clusterName}/settings/actions/save-preserved-variables-cnf [post]
func (repman *ReplicationManager) handlerMuxSavePreservedVarsCnf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	// Decode the request body
	var req PreservedVarsCnfRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Write the content using cluster method
	err = mycluster.WritePreservedVarsCnfContent(req.Content)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error saving preserved variables file: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("Successfully saved preserved variables CNF to %s", mycluster.GetPreservedVarsPath())))
}

// handlerMuxSecurityRemediationPlan returns the remediation plan for all open
// security findings in a cluster.
//
// Each RemediationEntry has one or more RemediationFix items. Fix types:
//
//	add_tag      — add a compliance module tag (deploys .cnf + runs mariadb_command SQL)
//	drop_tag     — remove a compliance module tag (runs mariadb_default SQL)
//	fix_db_user  — named action on a database account; no raw SQL from client
//	cnf_template — suggested .cnf content to add to the compliance module (no tag yet)
//
// @Summary Get security remediation plan
// @Tags ClusterSecurity
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param clusterName path string true "Cluster name"
// @Success 200 {object} cluster.RemediationPlan
// @Failure 404 {string} string "Cluster not found"
// @Router /api/clusters/{clusterName}/security/remediation-plan [get]
func (repman *ReplicationManager) handlerMuxSecurityRemediationPlan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}
	plan := mycluster.GetRemediationPlan()
	data, err := json.Marshal(plan)
	if err != nil {
		http.Error(w, "Encoding error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// handlerMuxSecurityApplyFix applies a tag-based security remediation fix
// (add_tag or drop_tag).  The cluster's AddDBTag / DropDBTag mechanism deploys
// the compliance .cnf file to all servers and executes the embedded
// mariadb_command / mariadb_default SQL automatically — no direct SQL execution.
//
// Per-account findings (SEC0100, SEC0107, SEC0108) must be handled manually.
//
// @Summary Apply a tag-based security remediation fix
// @Tags ClusterSecurity
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param clusterName path string true "Cluster name"
// @Param body body object true "{\"action\":\"add_tag\"|\"drop_tag\", \"tag\":\"<tag_name>\"}"
// @Success 200 {string} string "Fix applied"
// @Failure 400 {string} string "Bad request"
// @Failure 404 {string} string "Cluster not found"
// @Failure 500 {string} string "Execution error"
// @Router /api/clusters/{clusterName}/security/apply-fix [post]
func (repman *ReplicationManager) handlerMuxSecurityApplyFix(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}
	var req struct {
		Action string `json:"action"`
		Tag    string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Action == "" {
		http.Error(w, `Body must be JSON with non-empty "action" and "tag" fields`, http.StatusBadRequest)
		return
	}
	if req.Tag == "" {
		http.Error(w, `"tag" field is required`, http.StatusBadRequest)
		return
	}
	if err := mycluster.ApplyRemediationTag(req.Action, req.Tag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

// handlerMuxSecurityFixState applies the automated remediation for a single SEC
// error key across all servers in the cluster.  No request body is required —
// the server knows exactly what to do for each supported code.
//
// Supported codes (AutoFixable=true in the remediation plan):
//
//	SEC0100 — ACCOUNT LOCK all no-password, non-socket accounts
//	SEC0102 — drop compliance tag with_sec_localinfile (SET GLOBAL local_infile=0)
//	SEC0104 — drop compliance tag with_log_general    (SET GLOBAL general_log=0)
//	SEC0107 — DROP all anonymous user accounts
//	SEC0113 — INSTALL SONAME 'simple_password_check'   (MariaDB 10.1+)
//	SEC0114 — INSTALL SONAME 'cracklib_password_check' (MariaDB 10.1+, requires cracklib OS lib)
//	SEC0115 — INSTALL SONAME 'password_reuse_check'    (MariaDB 10.7+)
//
// @Summary Apply automated fix for a security finding
// @Tags ClusterSecurity
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param clusterName path string true "Cluster name"
// @Param errKey path string true "SEC error key (e.g. SEC0102)"
// @Success 200 {string} string "Fix applied"
// @Failure 400 {string} string "No automated fix for this code"
// @Failure 404 {string} string "Cluster not found"
// @Failure 500 {string} string "Execution error"
// @Router /api/clusters/{clusterName}/security/fix-state/{errKey} [post]
func (repman *ReplicationManager) handlerMuxSecurityFixState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}
	errKey := vars["errKey"]
	tag := r.URL.Query().Get("tag") // optional: selects among multiple fix options (e.g. SEC0113 pwdcheck)
	if err := mycluster.FixSecState(errKey, tag); err != nil {
		// FixSecState returns a specific error when the code has no automated fix.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

// s3ProviderRequest is the JSON request body for S3 provider add and modify operations.
// Unlike config.S3Provider, this struct exposes AccessKey and SecretKey so that
// handler code can accept credentials from the request without bypassing MarshalJSON.
type s3ProviderRequest struct {
	Name           string                  `json:"name"`
	ProviderSource config.S3ProviderSource `json:"providerSource"`
	ProviderApp    string                  `json:"providerApp,omitempty"`
	Endpoint       string                  `json:"endpoint,omitempty"`
	Region         string                  `json:"region,omitempty"`
	AccessKey      string                  `json:"accesskey,omitempty"`
	SecretKey      string                  `json:"secretkey,omitempty"`
}

const s3ProviderRequestBodyMaxBytes int64 = 1 << 20 // 1 MiB

func decodeS3ProviderRequestBody(w http.ResponseWriter, r *http.Request, req *s3ProviderRequest) (bool, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s3ProviderRequestBodyMaxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return false, fmt.Errorf("request body too large (max %d bytes)", s3ProviderRequestBodyMaxBytes)
		}
		return false, fmt.Errorf("invalid request body: %w", err)
	}

	strictDec := json.NewDecoder(bytes.NewReader(body))
	strictDec.DisallowUnknownFields()
	if err := strictDec.Decode(req); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			compatDec := json.NewDecoder(bytes.NewReader(body))
			if compatErr := compatDec.Decode(req); compatErr != nil {
				return false, fmt.Errorf("invalid request body: %w", err)
			}
			var compatExtra any
			if compatErr := compatDec.Decode(&compatExtra); compatErr != io.EOF {
				return false, fmt.Errorf("invalid request body: unexpected trailing content")
			}
			return true, nil
		}
		return false, fmt.Errorf("invalid request body: %w", err)
	}

	// Reject trailing content after a single JSON payload.
	var extra any
	if err := strictDec.Decode(&extra); err != io.EOF {
		return false, fmt.Errorf("invalid request body: unexpected trailing content")
	}
	return false, nil
}

// s3ProviderResponse is the JSON read response struct for provider GET/list APIs.
// Product contract: stored credentials are never returned to the UI.
//   - AccessKey is intentionally omitted (even though product classifies it as
//     config/env, not a secret).
//   - SecretKey is represented as "****" when present to indicate configuration
//     without exposing credential material.
type s3ProviderResponse struct {
	Name           string                  `json:"name"`
	ProviderSource config.S3ProviderSource `json:"providerSource"`
	ProviderApp    string                  `json:"providerApp,omitempty"`
	Endpoint       string                  `json:"endpoint,omitempty"`
	Region         string                  `json:"region,omitempty"`
	SecretKey      string                  `json:"secretkey,omitempty"`
}

// maskS3Provider builds a read-safe response from a provider:
// AccessKey omitted, SecretKey masked.
func maskS3Provider(p config.S3Provider) s3ProviderResponse {
	resp := s3ProviderResponse{
		Name:           p.Name,
		ProviderSource: p.ProviderSource,
		ProviderApp:    p.ProviderApp,
		Endpoint:       p.Endpoint,
		Region:         p.Region,
	}
	if p.SecretKey != "" {
		resp.SecretKey = "****"
	}
	return resp
}

// validateS3ProviderAPIRequest performs API-layer validation beyond model-level
// config.S3Provider.Validate():
//   - app mode: verifies ProviderApp host:port exists in cluster.AppS3Providers
//   - custom mode: requires non-empty AccessKey and SecretKey; validates Endpoint URL
func validateS3ProviderAPIRequest(p config.S3Provider, mycluster *cluster.Cluster) error {
	switch p.ProviderSource {
	case config.S3ProviderSourceApp:
		found := false
		for _, a := range mycluster.AppS3Providers {
			if a == p.ProviderApp {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("providerApp %q is not a known sibling app S3 provider", p.ProviderApp)
		}
	case config.S3ProviderSourceCustom:
		if p.AccessKey == "" {
			return fmt.Errorf("accesskey is required for custom mode")
		}
		if p.SecretKey == "" {
			return fmt.Errorf("secretkey is required for custom mode")
		}
		u, err := url.ParseRequestURI(p.Endpoint)
		if err != nil {
			return fmt.Errorf("endpoint is not a valid URL: %v", err)
		}
		scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("endpoint must use http or https scheme")
		}
		if strings.TrimSpace(u.Host) == "" {
			return fmt.Errorf("endpoint must include a host")
		}
	}
	return nil
}

func logLegacyS3ProviderMethodUsage(mycluster *cluster.Cluster, routePath, method, preferredMethod string) {
	if strings.EqualFold(method, preferredMethod) {
		return
	}
	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
		"Deprecated S3 provider API method %s on %s (preferred: %s)", method, routePath, preferredMethod)
}

// handlerMuxClusterS3ProvidersGet lists all S3 providers for a cluster with secrets masked.
func (repman *ReplicationManager) handlerMuxClusterS3ProvidersGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	providers := mycluster.GetS3ProvidersSnapshot()
	resp := make([]s3ProviderResponse, len(providers))
	for i, p := range providers {
		resp[i] = maskS3Provider(p)
	}
	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	if err := e.Encode(resp); err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}
}

type s3ProviderCRUDHTTPResponse struct {
	statusCode int
	message    string
}

func executeS3ProviderCRUDTransaction(mycluster *cluster.Cluster, txn func() s3ProviderCRUDHTTPResponse) s3ProviderCRUDHTTPResponse {
	resp := s3ProviderCRUDHTTPResponse{statusCode: http.StatusInternalServerError, message: "Unhandled S3 provider CRUD transaction state"}
	_ = mycluster.WithS3ProviderCRUDLock(func() error {
		if txn == nil {
			return nil
		}
		resp = txn()
		return nil
	})
	return resp
}

func writeS3ProviderCRUDResponse(w http.ResponseWriter, resp s3ProviderCRUDHTTPResponse) {
	if resp.statusCode == 0 {
		resp.statusCode = http.StatusOK
	}
	if resp.message != "" {
		http.Error(w, resp.message, resp.statusCode)
		return
	}
	w.WriteHeader(resp.statusCode)
}

// handlerMuxClusterS3ProviderAdd validates and adds a new S3 provider to the cluster library.
func (repman *ReplicationManager) handlerMuxClusterS3ProviderAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	resp := executeS3ProviderCRUDTransaction(mycluster, func() s3ProviderCRUDHTTPResponse {
		var req s3ProviderRequest
		legacyUnknownFieldAccepted, err := decodeS3ProviderRequestBody(w, r, &req)
		if err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusBadRequest, message: err.Error()}
		}
		if legacyUnknownFieldAccepted {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Deprecated S3 provider add payload with unknown fields accepted for compatibility")
		}
		p := config.S3Provider{
			Name:           req.Name,
			ProviderSource: req.ProviderSource,
			ProviderApp:    req.ProviderApp,
			Endpoint:       req.Endpoint,
			Region:         req.Region,
			AccessKey:      req.AccessKey,
			SecretKey:      req.SecretKey,
		}
		if err := p.Validate(); err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusBadRequest, message: fmt.Sprintf("Validation error: %v", err)}
		}
		if err := validateS3ProviderAPIRequest(p, mycluster); err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusBadRequest, message: fmt.Sprintf("Validation error: %v", err)}
		}
		if err := mycluster.AddS3Provider(p); err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusConflict, message: fmt.Sprintf("Failed to add S3 provider: %v", err)}
		}
		if err := mycluster.SaveS3Providers(); err != nil {
			// Rollback the in-memory add so state does not diverge from disk.
			if rbErr := mycluster.RemoveS3Provider(p.Name); rbErr != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
					"Rollback of in-memory add failed for %q: %v", p.Name, rbErr)
			}
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to persist S3 providers after add of %q: %v", p.Name, err)
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusInternalServerError, message: fmt.Sprintf("Failed to persist S3 provider: %v", err)}
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"S3 provider %q added to cluster library", p.Name)
		return s3ProviderCRUDHTTPResponse{statusCode: http.StatusCreated}
	})
	writeS3ProviderCRUDResponse(w, resp)
}

// prepareModifyProvider builds a fully-validated S3Provider for the modify path.
// It looks up the existing provider so it can (a) preserve blank credentials
// ("no change" convention from the UI edit form) and (b) return the old state
// for rollback on save failure. Validation runs after the credential merge so
// that a blank AccessKey/SecretKey does not trigger "required" errors.
// Returns the merged+validated provider, the pre-modification snapshot (may be
// nil when the provider is not found), and any validation error.
func prepareModifyProvider(req s3ProviderRequest, mycluster *cluster.Cluster) (config.S3Provider, *config.S3Provider, error) {
	p := config.S3Provider{
		Name:           req.Name,
		ProviderSource: req.ProviderSource,
		ProviderApp:    req.ProviderApp,
		Endpoint:       req.Endpoint,
		Region:         req.Region,
		AccessKey:      req.AccessKey,
		SecretKey:      req.SecretKey,
	}

	// Capture old state and merge blank credentials before validation.
	var oldProvider *config.S3Provider
	for _, sp := range mycluster.GetS3ProvidersSnapshot() {
		if sp.Name == p.Name {
			cp := sp
			oldProvider = &cp
			break
		}
	}
	if oldProvider != nil && p.ProviderSource == oldProvider.ProviderSource {
		if p.AccessKey == "" {
			p.AccessKey = oldProvider.AccessKey
		}
		if p.SecretKey == "" {
			p.SecretKey = oldProvider.SecretKey
		}
	}

	if err := p.Validate(); err != nil {
		return config.S3Provider{}, oldProvider, err
	}
	if err := validateS3ProviderAPIRequest(p, mycluster); err != nil {
		return config.S3Provider{}, oldProvider, err
	}
	return p, oldProvider, nil
}

// handlerMuxClusterS3ProviderModify validates and updates an existing S3 provider.
// The provider name is taken from the URL path; any name in the request body is ignored.
func (repman *ReplicationManager) handlerMuxClusterS3ProviderModify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	logLegacyS3ProviderMethodUsage(mycluster, "/api/clusters/{clusterName}/s3providers/{name}/modify", r.Method, http.MethodPut)
	resp := executeS3ProviderCRUDTransaction(mycluster, func() s3ProviderCRUDHTTPResponse {
		var req s3ProviderRequest
		legacyUnknownFieldAccepted, err := decodeS3ProviderRequestBody(w, r, &req)
		if err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusBadRequest, message: err.Error()}
		}
		if legacyUnknownFieldAccepted {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Deprecated S3 provider modify payload with unknown fields accepted for compatibility")
		}
		// URL path name is authoritative to prevent accidental rename.
		req.Name = vars["name"]
		p, oldProvider, err := prepareModifyProvider(req, mycluster)
		if err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusBadRequest, message: fmt.Sprintf("Validation error: %v", err)}
		}
		if err := mycluster.UpdateS3Provider(p); err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusNotFound, message: fmt.Sprintf("Failed to update S3 provider: %v", err)}
		}
		if err := mycluster.SaveS3Providers(); err != nil {
			// Rollback the in-memory update so state does not diverge from disk.
			if oldProvider != nil {
				if rbErr := mycluster.UpdateS3Provider(*oldProvider); rbErr != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
						"Rollback of in-memory modify failed for %q: %v", p.Name, rbErr)
				}
			}
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to persist S3 providers after modify of %q: %v", p.Name, err)
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusInternalServerError, message: fmt.Sprintf("Failed to persist S3 provider: %v", err)}
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"S3 provider %q modified in cluster library", p.Name)
		return s3ProviderCRUDHTTPResponse{statusCode: http.StatusOK}
	})
	writeS3ProviderCRUDResponse(w, resp)
}

// handlerMuxClusterS3ProviderDrop removes an S3 provider from the cluster library.
func (repman *ReplicationManager) handlerMuxClusterS3ProviderDrop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	logLegacyS3ProviderMethodUsage(mycluster, "/api/clusters/{clusterName}/s3providers/{name}/drop", r.Method, http.MethodDelete)
	resp := executeS3ProviderCRUDTransaction(mycluster, func() s3ProviderCRUDHTTPResponse {
		name := vars["name"]
		// Capture old state before removal so we can roll back if save fails.
		var oldProvider *config.S3Provider
		for _, sp := range mycluster.GetS3ProvidersSnapshot() {
			if sp.Name == name {
				cp := sp
				oldProvider = &cp
				break
			}
		}
		if err := mycluster.RemoveS3Provider(name); err != nil {
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusNotFound, message: fmt.Sprintf("Failed to remove S3 provider: %v", err)}
		}
		if err := mycluster.SaveS3Providers(); err != nil {
			// Rollback the in-memory removal so state does not diverge from disk.
			if oldProvider != nil {
				if rbErr := mycluster.AddS3Provider(*oldProvider); rbErr != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
						"Rollback of in-memory drop failed for %q: %v", name, rbErr)
				}
			}
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to persist S3 providers after drop of %q: %v", name, err)
			return s3ProviderCRUDHTTPResponse{statusCode: http.StatusInternalServerError, message: fmt.Sprintf("Failed to persist S3 provider: %v", err)}
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"S3 provider %q dropped from cluster library", name)
		return s3ProviderCRUDHTTPResponse{statusCode: http.StatusOK}
	})
	writeS3ProviderCRUDResponse(w, resp)
}

// s3ProviderReference represents a single mount reference to a provider.
type s3ProviderReference struct {
	AppID        string `json:"appId"`
	AppName      string `json:"appName"`
	MountName    string `json:"mountName"`
	ProviderName string `json:"providerName"`
	Status       string `json:"status"`
	Fields       struct {
		Endpoint string `json:"endpoint,omitempty"`
		Region   string `json:"region,omitempty"`
		Bucket   string `json:"bucket,omitempty"`
	} `json:"fields"`
}

// s3ProviderReferencesResponse is the API response for provider reference discovery.
type s3ProviderReferencesResponse struct {
	ProviderName   string                `json:"providerName"`
	ProviderFound  bool                  `json:"providerFound"`
	ReferenceCount int                   `json:"referenceCount"`
	References     []s3ProviderReference `json:"references"`
}

// buildS3ProviderReferencesResponse scans mycluster.Apps for S3 mounts that
// reference providerName and returns the populated response. provider may be nil
// when the named provider no longer exists in the library (stale references are
// still returned with status "provider_missing"). Extracted for testability.
func buildS3ProviderReferencesResponse(mycluster *cluster.Cluster, providerName string, provider *config.S3Provider) s3ProviderReferencesResponse {
	references := []s3ProviderReference{}
	providerEndpoint := ""
	providerRegion := ""
	providerSourceKnown := false
	if provider != nil {
		effectiveEndpoint, effectiveRegion, _, _ := mycluster.ResolveS3ProviderEffectiveValues(*provider)
		switch provider.ProviderSource {
		case config.S3ProviderSourceApp, config.S3ProviderSourceCustom:
			providerSourceKnown = true
			providerEndpoint = effectiveEndpoint
			providerRegion = effectiveRegion
		}
	}

	for _, snap := range mycluster.S3MountReferencesSnapshot(providerName) {
		ref := s3ProviderReference{
			AppID:        snap.AppID,
			AppName:      snap.AppName,
			MountName:    snap.MountName,
			ProviderName: snap.ProviderName,
			Status:       "unknown",
		}
		ref.Fields.Endpoint = snap.Endpoint
		ref.Fields.Region = snap.Region
		ref.Fields.Bucket = snap.Bucket

		if provider == nil {
			ref.Status = "provider_missing"
		} else if !providerSourceKnown {
			ref.Status = "unknown"
		} else if snap.Endpoint == providerEndpoint && snap.Region == providerRegion {
			ref.Status = "matches_provider"
		} else {
			ref.Status = "customized"
		}

		references = append(references, ref)
	}
	return s3ProviderReferencesResponse{
		ProviderName:   providerName,
		ProviderFound:  provider != nil,
		ReferenceCount: len(references),
		References:     references,
	}
}

// handlerMuxClusterS3ProviderReferences returns all app S3 mounts that reference a given provider.
func (repman *ReplicationManager) handlerMuxClusterS3ProviderReferences(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	providerName := strings.TrimSpace(vars["name"])
	if providerName == "" {
		http.Error(w, "Provider name is required", http.StatusBadRequest)
		return
	}
	if len(providerName) > 255 {
		http.Error(w, "Provider name too long", http.StatusBadRequest)
		return
	}

	providers := mycluster.GetS3ProvidersSnapshot()

	// Find the provider; providerFound distinguishes "not found" from "zero references".
	var provider *config.S3Provider
	for i := range providers {
		if providers[i].Name == providerName {
			p := providers[i]
			provider = &p
			break
		}
	}

	resp := buildS3ProviderReferencesResponse(mycluster, providerName, provider)

	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	if err := e.Encode(resp); err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
		return
	}
}

// s3SyncRequest is the shared JSON request body for the sync preview and apply endpoints.
type s3SyncRequest struct {
	Targets       []cluster.SyncTarget `json:"targets"`
	RevisionToken string               `json:"revisionToken,omitempty"`
}

const s3SyncRequestBodyMaxBytes int64 = 1 << 20 // 1 MiB

func decodeS3SyncRequestBody(w http.ResponseWriter, r *http.Request, req *s3SyncRequest) (int, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s3SyncRequestBodyMaxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return http.StatusRequestEntityTooLarge, fmt.Errorf("request body too large (max %d bytes)", s3SyncRequestBodyMaxBytes)
		}
		return http.StatusBadRequest, fmt.Errorf("Invalid request body: %w", err)
	}
	// Reject trailing garbage after the first JSON value.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return http.StatusBadRequest, fmt.Errorf("Invalid request body: unexpected trailing content")
	}
	return http.StatusOK, nil
}

func validateS3SyncApplyRevisionToken(revisionToken string) error {
	token := strings.TrimSpace(revisionToken)
	if token == "" {
		return fmt.Errorf("revisionToken is required; run preview again before apply")
	}
	if !cluster.IsValidS3SyncRevisionToken(token) {
		return fmt.Errorf("revisionToken is malformed; run preview again before apply")
	}
	return nil
}

// parseS3SyncRequest validates the cluster, ACL, provider name, and decodes the
// request body. It returns the cluster, provider name, and decoded targets, or
// writes an HTTP error and returns false.
func parseS3SyncRequest(repman *ReplicationManager, w http.ResponseWriter, r *http.Request) (*cluster.Cluster, string, s3SyncRequest, bool) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return nil, "", s3SyncRequest{}, false
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return nil, "", s3SyncRequest{}, false
	}

	providerName := vars["name"]
	if err := config.ValidateS3ProviderName(providerName); err != nil {
		http.Error(w, "Invalid provider name: "+err.Error(), http.StatusBadRequest)
		return nil, "", s3SyncRequest{}, false
	}

	var req s3SyncRequest
	if statusCode, err := decodeS3SyncRequestBody(w, r, &req); err != nil {
		http.Error(w, err.Error(), statusCode)
		return nil, "", s3SyncRequest{}, false
	}
	if len(req.Targets) == 0 {
		http.Error(w, "targets must not be empty", http.StatusBadRequest)
		return nil, "", s3SyncRequest{}, false
	}
	if len(req.Targets) > cluster.SyncMaxTargets {
		http.Error(w, fmt.Sprintf("too many targets: max %d", cluster.SyncMaxTargets), http.StatusBadRequest)
		return nil, "", s3SyncRequest{}, false
	}
	for i, t := range req.Targets {
		if strings.TrimSpace(t.AppId) == "" || strings.TrimSpace(t.MountName) == "" {
			http.Error(w, fmt.Sprintf("invalid target at index %d: appId and mountName are required", i), http.StatusBadRequest)
			return nil, "", s3SyncRequest{}, false
		}
	}
	return mycluster, providerName, req, true
}

// handlerMuxClusterS3ProviderSyncPreview performs a dry-run preview of syncing
// one or more mounts from a named provider. No state is mutated.
//
// POST /api/clusters/{clusterName}/s3providers/{name}/sync/preview
func (repman *ReplicationManager) handlerMuxClusterS3ProviderSyncPreview(w http.ResponseWriter, r *http.Request) {
	mycluster, providerName, req, ok := parseS3SyncRequest(repman, w, r)
	if !ok {
		return
	}

	resp := mycluster.PreviewS3ProviderSync(providerName, req.Targets)

	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	if err := e.Encode(resp); err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
	}
}

// handlerMuxClusterS3ProviderSyncApply applies provider-managed field values to
// one or more mounts. Only endpoint, region, accessKey, and secretKey are
// overwritten; all mount-specific fields are preserved.
//
// POST /api/clusters/{clusterName}/s3providers/{name}/sync/apply
func (repman *ReplicationManager) handlerMuxClusterS3ProviderSyncApply(w http.ResponseWriter, r *http.Request) {
	mycluster, providerName, req, ok := parseS3SyncRequest(repman, w, r)
	if !ok {
		return
	}
	if err := validateS3SyncApplyRevisionToken(req.RevisionToken); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := mycluster.ApplyS3ProviderSync(providerName, req.Targets, req.RevisionToken)

	w.Header().Set("Content-Type", "application/json")
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	if err := e.Encode(resp); err != nil {
		http.Error(w, "Encoding error", http.StatusInternalServerError)
	}
}