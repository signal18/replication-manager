import { Box, Flex, Tab, TabList, TabPanel, TabPanels, Tabs } from '@chakra-ui/react'
import React, { useCallback, useMemo, useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import TextForm from '../../components/TextForm'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import RMSlider from '../../components/Sliders/RMSlider'
import Dropdown from '../../components/Dropdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import RMButton from '../../components/RMButton'
import SetCredentialsModal from '../../components/Modals/SetCredentialsModal'

// Static help text - hoisted to module scope (not derived from props/state) so the
// useMemo blocks below don't need them in their dependency arrays.
const hMonitor = `**ProxySQL Monitor**\n\nEnables replication-manager's ProxySQL integration.\nKeeps ProxySQL's server list, hostgroups, and routing rules in sync with the current cluster topology.\n\nConfig: \`proxysql\``
const hServers = `**ProxySQL Bootstrap Servers**\n\nPushes the current cluster server list into ProxySQL's \`mysql_servers\` table.\n\nConfig: \`proxysql-bootstrap-servers\``
const hUsers = `**ProxySQL Bootstrap Users**\n\nCopies MySQL user grants into ProxySQL's \`mysql_users\` table.\n\nConfig: \`proxysql-bootstrap-users\``
const hVars = `**ProxySQL Bootstrap Variables**\n\nApplies recommended ProxySQL global variables from the replication-manager configuration.\n\nConfig: \`proxysql-bootstrap-variables\``
const hHG = `**ProxySQL Bootstrap Hostgroups**\n\nConfigures ProxySQL hostgroups (writer, reader, backup-writer) based on the current cluster topology.\n\nConfig: \`proxysql-bootstrap-hostgroups\``
const hQR = `**ProxySQL Bootstrap Query Rules**\n\nLoads default query routing rules into ProxySQL. Existing rules are replaced.\n\nConfig: \`proxysql-bootstrap-query-rules\``

const hHaproxyMode = `**HAProxy Mode**\n\nControls how replication-manager drives HAProxy's backend membership and health:\n\n- **runtimeapi** (default): repman reconciles master/reader drain state via HAProxy's Runtime API every monitoring pass, and — when \`haproxy-api-bootstrap-servers\` is also enabled and HAProxy is >= 2.6 — dynamically adds/removes servers as cluster membership changes, without a reload. Deployed by the cluster's own orchestrator.\n- **standby**: repman always runs its own HAProxy instance co-located with repman itself — started/reloaded via a local PID — regardless of which orchestrator the cluster's databases use (OpenSVC, Kubernetes, etc.).\n- **externalcheck**: HAProxy's own external-check command polls repman's HTTP health endpoints to decide backend health/routing; newly provisioned proxies use \`/master-status\` for the write backend and \`/reader-status\` for the read backend. Older externalcheck proxies keep polling legacy \`/slave-status\` until they are reprovisioned after upgrade. Repman does not push state via the Runtime API in this mode. Deployed by the cluster's own orchestrator.\n- **dataplaneapi**: currently handled the same as externalcheck for config generation (servers named positionally, no Runtime API reconciliation) — treat as reserved for a future Data Plane API integration, not yet implemented in this codebase.\n\nOnly takes effect on the next (re)provision of the proxy and raises the **NeedProxyReprov** badge as a reminder. Changing it while a proxy is already provisioned is refused — unprovision it first, then change this and provision again.\n\nConfig: \`haproxy-mode\``
const hHaproxyDynServers = `**HAProxy Bootstrap Servers**\n\nFor HAProxy mode \`runtimeapi\` only. When enabled, repman drives every backend member (read and write) with its own resolved server IP over the Runtime API instead of HAProxy's own DNS resolution: generated server lines carry no \`resolvers\` clause, and adding/removing a cluster server updates the live backend at runtime instead of requiring a reload.\n\nOff (the default) keeps \`runtimeapi\` resolver-backed, identical to \`externalcheck\`/\`standby\` — HAProxy resolves each server's hostname itself and dynamic add/remove is not available.\n\nRequires HAProxy >= 2.6 (silently inactive otherwise). Existing ready/drain/maintenance state handling is unaffected either way.\n\nCan be changed live at any time; each proxy keeps running its own last-provisioned value until reprovisioned, which raises the **NeedProxyReprov** badge as a reminder.\n\nConfig: \`haproxy-api-bootstrap-servers\``
const hHaproxyWritePort = `**HAProxy Write Port**\n\nFront-end port HAProxy listens on for read-write (leader) traffic.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-write-port\``
const hHaproxyReadPort = `**HAProxy Read Port**\n\nFront-end port HAProxy listens on for load-balanced read traffic across all nodes.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-read-port\``
const hHaproxyStatPort = `**HAProxy Stat Port**\n\nPort serving HAProxy's statistics page.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-stat-port\``
const hHaproxyAPIPort = `**HAProxy Runtime API Port**\n\nPort repman connects to for HAProxy's Runtime API (used in \`runtimeapi\` mode) and to resolve the proxy's own address at provisioning time.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-api-port\``
const hHaproxyWriteBind = `**HAProxy Write Bind IP**\n\nIP address HAProxy binds the write front-end to.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-ip-write-bind\``
const hHaproxyReadBind = `**HAProxy Read Bind IP**\n\nIP address HAProxy binds the read front-end to.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-ip-read-bind\``
const hHaproxyBinaryPath = `**HAProxy Binary Path**\n\nPath to the HAProxy executable repman uses (\`standby\`/\`externalcheck\` modes on the Localhost orchestrator).\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-binary-path\``
const hHaproxyReadBackend = `**HAProxy Read Backend Name**\n\nName of the HAProxy backend pool that holds read servers.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-api-read-backend\``
const hHaproxyWriteBackend = `**HAProxy Write Backend Name**\n\nName of the HAProxy backend pool that holds the write (leader) server.\n\nApplied at proxy provisioning/config-render time — reprovision or restart the proxy for a change to take effect.\n\nConfig: \`haproxy-api-write-backend\``
const hHaproxyStagingBackend = `**HAProxy Staging Backend Name**\n\nName of the HAProxy backend pool repman repoints to the staging server (via the Runtime API) when topology staging is active.\n\nConfig: \`haproxy-staging-backend\``
// Client-side pre-checks mirroring what the server actually accepts for these
// fields (ports: server enforces 1-65535 in setClusterSetting; bind IP/backend
// name: server accepts any string, these are just sane-input guards).
const HAPROXY_PORT_PATTERN = '^([1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])$'
const HAPROXY_BIND_IP_PATTERN = '^(\\*|(\\d{1,3}\\.){3}\\d{1,3})$'
const HAPROXY_BACKEND_NAME_PATTERN = '^[a-zA-Z0-9_-]+$'

const hHaproxyUser = `**HAProxy Admin User**\n\nAdmin username configured for HAProxy's stats/admin interface at provisioning time.\n\nApplied at proxy provisioning time — reprovision the proxy for a change to take effect.\n\nConfig: \`haproxy-user\``
const hHaproxyPassword = `**HAProxy Admin Password**\n\nAdmin password configured for HAProxy's stats/admin interface at provisioning time.\n\nApplied at proxy provisioning time — reprovision the proxy for a change to take effect.\n\nConfig: \`haproxy-password\``

const hMaxscaleMode = `**MaxScale Config Mode**\n\nSelects the config syntax repman generates when provisioning MaxScale:\n\n- **auto** (default): detected from the MaxScale image tag (\`prov-proxy-docker-maxscale-img\`) — MaxScale's own versioning went from semver (2.2, 2.4...) to calendar-based (21.06, 22.08...); anything \`>= 2.5\` (semver) or any calendar release is treated as pinloki. An unparseable tag falls back to legacy.\n- **legacy**: pre-2.5 config syntax (\`cli\`/\`maxinfo\` routers, \`MariaDBClient\` protocol).\n- **pinloki**: 2.5+ config syntax (pinloki binlogrouter, no \`cli\`/\`maxinfo\` routers — removed upstream).\n\nBoth syntaxes use \`password=\`, never \`passwd=\` — that's rejected outright as of MaxScale 2.4.10, the oldest image still pullable from Docker Hub.\n\nConfig: \`maxscale-mode\``
const hMaxscaleRestApi = `**MaxScale REST API**\n\nUse MaxScale's REST API to connect (MaxScale >= 2.2, introduced alongside the API). Off falls back to the legacy MaxAdmin TCP protocol, removed entirely in MaxScale 2.5 — only disable this for a MaxScale older than 2.2.\n\nIndependent of MaxScale Config Mode: a legacy-mode config still speaks to MaxScale over REST when this is on (the common case — REST predates the config-syntax split by three years).\n\nConfig: \`maxscale-rest-api\``
const hMaxscaleRestPort = `**MaxScale REST API Port**\n\nPort repman connects to for MaxScale's REST API (used when MaxScale REST API is on).\n\nConfig: \`maxscale-rest-port\``
const hMaxscalePort = `**MaxScale Admin Port**\n\nMaxScale's CLI/MaxAdmin listener port. Used for the legacy MaxAdmin TCP protocol (MaxScale REST API off) and rendered as the legacy config template's CLI listener port either way.\n\nConfig: \`maxscale-port\``
const hMaxscaleWritePort = `**MaxScale Write Port**\n\nMaxScale's read-write (leader) front-end port.\n\nConfig: \`maxscale-write-port\``
const hMaxscaleReadPort = `**MaxScale Read Port**\n\nMaxScale's load-balanced read front-end port, across all nodes.\n\nConfig: \`maxscale-read-port\``
const hMaxscaleReadWritePort = `**MaxScale Read-Write Split Port**\n\nMaxScale's combined read-write split front-end port (routes writes to the leader, reads across replicas).\n\nConfig: \`maxscale-read-write-port\``
const hMaxscaleCredentials = `**MaxScale Admin Credentials**\n\nAdmin user/password used for both the REST API and MaxAdmin. Opens the same credentials dialog as Cluster → Credentials → Set Maxscale Credentials.\n\nConfig: \`maxscale-servers-credential\` (combined \`user:password\`, encrypted at rest)`
const hMaxscaleGetInfoMethod = `**MaxScale Get Info Method**\n\nHow repman fetches backend server/monitor status from MaxScale:\n\n- **maxadmin** (default): via \`ListServers\`/\`ListMonitors\` — despite the name, this actually goes over whichever transport MaxScale REST API selects (REST or MaxAdmin), not literally the MaxAdmin protocol.\n- **maxinfo**: the older \`maxinfo\` HTTP plugin. Not available on pinloki-mode MaxScale (2.5+ dropped it, same as \`cli\`/\`debugcli\`) — automatically falls back to the method above with a warning (\`WARN0211\`) if selected there.\n\nConfig: \`maxscale-get-info-method\``
const hMaxscaleServerMatchPort = `**MaxScale Match Servers by Port**\n\nWhen multiple database servers run on the same host with different ports, match MaxScale backend servers by host **and** port instead of host alone.\n\nConfig: \`maxscale-server-match-port\``
const hMaxscaleDisableMonitor = `**MaxScale Disable Monitor**\n\nShuts down MaxScale's own monitor (\`mariadbmon\`/\`galeramon\`/\`mmmon\`) and has repman drive server state manually instead.\n\nOff (the default, and how MaxScale normally runs) leaves MaxScale's monitor in charge — it already tracks and drives master/slave/running state on its own, correctly and continuously; repman only pushes state manually when there's genuinely no monitor to conflict with (none found, or this is enabled).\n\nConfig: \`maxscale-disable-monitor\``
const hMaxscaleBinlogOn = `**MaxScale Binlog Server**\n\nTreats a configured MaxScale as a binlog relay server (\`MxsBinlogOn\`) — repman detects it via \`@@maxscale_version\` (legacy) or \`@@version_comment\` (pinloki) rather than as a regular topology member.\n\nConfig: \`maxscale-binlog\``
const hMaxscaleBinlogPort = `**MaxScale Binlog Port**\n\nPort MaxScale's binlog relay (\`replication\`/\`Replication\` service) listens on.\n\nConfig: \`maxscale-binlog-port\``
const hMaxscaleFalsePositive = `**MaxScale Failover False-Positive Check**\n\nBefore confirming a failover, also asks MaxScale whether it independently sees the same master as down — guards against a false positive from repman's own monitoring alone.\n\nConfig: \`failover-falsepositive-maxscale\``

const hCompress = `**Proxies Compression to Backends**\n\nEnables MySQL protocol compression on connections between ProxySQL and the backend servers.\nReduces network bandwidth at the cost of additional CPU.\n\nConfig: \`proxy-servers-backend-compression\``
const hReadsWriter = `**Proxies Reads on Writer**\n\nRoutes read queries to the writer (master) node as well.\nUseful when strong read consistency is required or replica lag is too high.\n\nConfig: \`proxy-servers-read-on-master\``
const hReadsNoSlave = `**Proxies Reads on Writer When No Slave**\n\nFallback: automatically routes reads to the writer when no healthy replica is available.\n\nConfig: \`proxy-servers-read-on-master-no-slave\``
const hMaxConn = `**Proxies Max Backend Connections**\n\nMaximum connections ProxySQL will open to each backend server.\nTune according to the \`max_connections\` setting on your MySQL servers.\n\nConfig: \`proxy-servers-backend-max-connections\``
const hMaxLag = `**Proxies Max Backend Replication Lag for Reads**\n\nMaximum replication lag (in seconds) a replica may have before ProxySQL stops routing reads to it.\n\nConfig: \`proxy-servers-backend-max-replication-lag\``
const hInjectTraffic = `**Inject Test Traffic**\n\nInjects a small stream of database traffic through the proxy (test / demo). Combined with the marker below, this seeds a write stream that repman can rewind.\n\nConfig: \`test-inject-traffic\``
const hInjectMode = `**Inject Traffic Mode**\n\nFormat of the pseudo-GTID / traffic marker repman injects through the proxy:\n\n- **ddl** (default): \`CREATE OR REPLACE VIEW\` — self-contained, idempotent DDL, greppable for positional rejoin. Battle-tested, but a divergence is **NOT flashback-able** (flashback cannot reverse DDL).\n- **dml** (EXPERIMENTAL): single-row \`REPLACE\` — **flashback-able**; the marker table is created once through the proxy. Choose this to enable / demonstrate flashback rejoin.\n\nConfig: \`inject-traffic-mode\``

function ProxySettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [proxyTypeTabIndex, setProxyTypeTabIndex] = useState(0)
  const [isMaxscaleCredModalOpen, setIsMaxscaleCredModalOpen] = useState(false)

  const clusterName = selectedCluster?.name
  const config = selectedCluster?.config
  const isDisabled = user?.grants['cluster-settings'] == false

  // Stable callback identities so the useMemo blocks below only recompute when
  // the values they actually read (config, clusterName, isDisabled) change,
  // not on every unrelated re-render (e.g. opening the help modal).
  const openInfoModal = useCallback((title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }, [])

  const h = useCallback((content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
  ), [openInfoModal])

  const sw = useCallback((setting, configKey) => (
    <RMSwitch confirmTitle={`Confirm switch settings for ${setting}?`} onChange={() => dispatch(switchSetting({ clusterName, setting }))} isDisabled={isDisabled} isChecked={config?.[configKey]} />
  ), [dispatch, clusterName, isDisabled, config])

  const txt = useCallback((setting, configKey, opts = {}) => {
    const raw = config?.[configKey]
    return (<TextForm value={raw === undefined || raw === null ? '' : String(raw)} isDisabled={isDisabled} confirmTitle={`Confirm ${setting} to `} className={styles.textbox} size='sm' onSave={(v) => dispatch(setSetting({ clusterName, setting, value: v }))} {...opts} />)
  }, [dispatch, clusterName, isDisabled, config])

  const proxysqlRows = useMemo(() => [
    { key: 'ProxySQL Monitor', help: h(hMonitor, 'ProxySQL Monitor'), value: sw('proxysql', 'proxysql') },
    { key: 'ProxySQL Bootstrap Servers', help: h(hServers, 'ProxySQL Bootstrap Servers'), value: sw('proxysql-bootstrap-servers', 'proxysqlBootstrap') },
    { key: 'ProxySQL Bootstrap Users', help: h(hUsers, 'ProxySQL Bootstrap Users'), value: sw('proxysql-bootstrap-users', 'proxysqlBootstrapUsers') },
    { key: 'ProxySQL Bootstrap Variables', help: h(hVars, 'ProxySQL Bootstrap Variables'), value: sw('proxysql-bootstrap-variables', 'proxysqlBootstrapVariables') },
    { key: 'ProxySQL Bootstrap Hostgroups', help: h(hHG, 'ProxySQL Bootstrap Hostgroups'), value: sw('proxysql-bootstrap-hostgroups', 'proxysqlBootstrapHostgroups') },
    { key: 'ProxySQL Bootstrap Query Rules', help: h(hQR, 'ProxySQL Bootstrap Query Rules'), value: sw('proxysql-bootstrap-query-rules', 'proxysqlBootstrapQueryRules') },
  ], [h, sw])

  const haproxyMode = config?.haproxyMode

  const haproxyRows = useMemo(() => {
    const rows = [
      { key: 'HAProxy Mode', help: h(hHaproxyMode, 'HAProxy Mode'), value: (<Dropdown options={[{ name: 'runtimeapi — Runtime API driven (default)', value: 'runtimeapi' }, { name: 'standby — repman-managed, co-located', value: 'standby' }, { name: 'externalcheck — HAProxy external-check', value: 'externalcheck' }, { name: 'dataplaneapi — Data Plane API driven', value: 'dataplaneapi' }]} selectedValue={config?.haproxyMode} confirmTitle={'Confirm haproxy-mode to'} isDisabled={isDisabled} onChange={(val) => dispatch(setSetting({ clusterName, setting: 'haproxy-mode', value: val }))} />) },
    ]
    // Bootstrap Servers only applies to runtimeapi (see hHaproxyDynServers).
    if (haproxyMode === 'runtimeapi') {
      rows.push({ key: 'HAProxy Bootstrap Servers', help: h(hHaproxyDynServers, 'HAProxy Bootstrap Servers'), value: sw('haproxy-api-bootstrap-servers', 'haproxyAPIBootstrapServers') })
    }
    rows.push(
      { key: 'HAProxy Write Port', help: h(hHaproxyWritePort, 'HAProxy Write Port'), value: txt('haproxy-write-port', 'haproxyWritePort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'HAProxy Read Port', help: h(hHaproxyReadPort, 'HAProxy Read Port'), value: txt('haproxy-read-port', 'haproxyReadPort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'HAProxy Stat Port', help: h(hHaproxyStatPort, 'HAProxy Stat Port'), value: txt('haproxy-stat-port', 'haproxyStatPort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'HAProxy Runtime API Port', help: h(hHaproxyAPIPort, 'HAProxy Runtime API Port'), value: txt('haproxy-api-port', 'haproxyAPIPort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'HAProxy Write Bind IP', help: h(hHaproxyWriteBind, 'HAProxy Write Bind IP'), value: txt('haproxy-ip-write-bind', 'haproxyIpWriteBind', { regexPattern: HAPROXY_BIND_IP_PATTERN }) },
      { key: 'HAProxy Read Bind IP', help: h(hHaproxyReadBind, 'HAProxy Read Bind IP'), value: txt('haproxy-ip-read-bind', 'haproxyIpReadBind', { regexPattern: HAPROXY_BIND_IP_PATTERN }) },
    )
    // Binary Path only applies to standby/externalcheck (see hHaproxyBinaryPath).
    if (haproxyMode === 'standby' || haproxyMode === 'externalcheck') {
      rows.push({ key: 'HAProxy Binary Path', help: h(hHaproxyBinaryPath, 'HAProxy Binary Path'), value: txt('haproxy-binary-path', 'haproxyBinaryPath') })
    }
    rows.push(
      { key: 'HAProxy Read Backend Name', help: h(hHaproxyReadBackend, 'HAProxy Read Backend Name'), value: txt('haproxy-api-read-backend', 'haproxyAPIReadBackend', { regexPattern: HAPROXY_BACKEND_NAME_PATTERN }) },
      { key: 'HAProxy Write Backend Name', help: h(hHaproxyWriteBackend, 'HAProxy Write Backend Name'), value: txt('haproxy-api-write-backend', 'haproxyAPIWriteBackend', { regexPattern: HAPROXY_BACKEND_NAME_PATTERN }) },
      { key: 'HAProxy Staging Backend Name', help: h(hHaproxyStagingBackend, 'HAProxy Staging Backend Name'), value: txt('haproxy-staging-backend', 'haproxyStagingBackend', { regexPattern: HAPROXY_BACKEND_NAME_PATTERN }) },
      { key: 'HAProxy Admin User', help: h(hHaproxyUser, 'HAProxy Admin User'), value: txt('haproxy-user', 'haproxyUser') },
      { key: 'HAProxy Admin Password', help: h(hHaproxyPassword, 'HAProxy Admin Password'), value: (<TextForm type='password' value={config?.haproxyPassword || ''} isDisabled={isDisabled} className={styles.textbox} size='sm' confirmTitle={'Confirm haproxy-password to '} onSave={(v) => dispatch(setSetting({ clusterName, setting: 'haproxy-password', value: btoa(v) }))} />) },
    )
    return rows
  }, [h, sw, txt, dispatch, clusterName, isDisabled, config, haproxyMode])

  const maxscaleBinlogOn = config?.maxscaleBinlog

  const maxscaleRows = useMemo(() => {
    const rows = [
      { key: 'MaxScale Config Mode', help: h(hMaxscaleMode, 'MaxScale Config Mode'), value: (<Dropdown options={[{ name: 'auto — detect from image tag (default)', value: 'auto' }, { name: 'legacy — pre-2.5 config syntax', value: 'legacy' }, { name: 'pinloki — 2.5+ config syntax', value: 'pinloki' }]} selectedValue={config?.maxscaleMode} confirmTitle={'Confirm maxscale-mode to'} isDisabled={isDisabled} onChange={(val) => dispatch(setSetting({ clusterName, setting: 'maxscale-mode', value: val }))} />) },
      { key: 'MaxScale REST API', help: h(hMaxscaleRestApi, 'MaxScale REST API'), value: sw('maxscale-rest-api', 'maxscaleRestApi') },
      { key: 'MaxScale REST API Port', help: h(hMaxscaleRestPort, 'MaxScale REST API Port'), value: txt('maxscale-rest-port', 'maxscaleRestPort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'MaxScale Admin Port', help: h(hMaxscalePort, 'MaxScale Admin Port'), value: txt('maxscale-port', 'maxscalePort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'MaxScale Write Port', help: h(hMaxscaleWritePort, 'MaxScale Write Port'), value: txt('maxscale-write-port', 'maxscaleWritePort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'MaxScale Read Port', help: h(hMaxscaleReadPort, 'MaxScale Read Port'), value: txt('maxscale-read-port', 'maxscaleReadPort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'MaxScale Read-Write Split Port', help: h(hMaxscaleReadWritePort, 'MaxScale Read-Write Split Port'), value: txt('maxscale-read-write-port', 'maxscaleReadWritePort', { regexPattern: HAPROXY_PORT_PATTERN }) },
      { key: 'MaxScale Admin Credentials', help: h(hMaxscaleCredentials, 'MaxScale Admin Credentials'), value: (<RMButton size='sm' variant='outline' isDisabled={isDisabled} onClick={() => setIsMaxscaleCredModalOpen(true)}>Set Credentials</RMButton>) },
      { key: 'MaxScale Get Info Method', help: h(hMaxscaleGetInfoMethod, 'MaxScale Get Info Method'), value: (<Dropdown options={[{ name: 'maxadmin (default)', value: 'maxadmin' }, { name: 'maxinfo — legacy only, dropped in pinloki', value: 'maxinfo' }]} selectedValue={config?.maxscaleGetInfoMethod} confirmTitle={'Confirm maxscale-get-info-method to'} isDisabled={isDisabled} onChange={(val) => dispatch(setSetting({ clusterName, setting: 'maxscale-get-info-method', value: val }))} />) },
      { key: 'MaxScale Match Servers by Port', help: h(hMaxscaleServerMatchPort, 'MaxScale Match Servers by Port'), value: sw('maxscale-server-match-port', 'maxscaleServerMatchPort') },
      { key: 'MaxScale Disable Monitor', help: h(hMaxscaleDisableMonitor, 'MaxScale Disable Monitor'), value: sw('maxscale-disable-monitor', 'maxscaleDisableMonitor') },
      { key: 'MaxScale Binlog Server', help: h(hMaxscaleBinlogOn, 'MaxScale Binlog Server'), value: sw('maxscale-binlog', 'maxscaleBinlog') },
    ]
    // Binlog Port only applies when this MaxScale is acting as a binlog relay.
    if (maxscaleBinlogOn) {
      rows.push({ key: 'MaxScale Binlog Port', help: h(hMaxscaleBinlogPort, 'MaxScale Binlog Port'), value: txt('maxscale-binlog-port', 'maxscaleBinlogPort', { regexPattern: HAPROXY_PORT_PATTERN }) })
    }
    rows.push({ key: 'MaxScale Failover False-Positive Check', help: h(hMaxscaleFalsePositive, 'MaxScale Failover False-Positive Check'), value: sw('failover-falsepositive-maxscale', 'failoverFalsePositiveMaxscale') })
    return rows
  }, [h, sw, txt, dispatch, clusterName, isDisabled, config, maxscaleBinlogOn])

  // Proxy-type-specific settings live behind a Tab strip (ProxySQL/HAProxy/MaxScale,
  // styled like Restic's repository-type tabs), nested as the value of one TableType2
  // row - same as Restic nests its own repo-type tabs inside a row of its settings table.
  const proxyTypeTabs = useMemo(() => (
    <Tabs index={proxyTypeTabIndex} onChange={setProxyTypeTabIndex} className={styles.repoTabs} variant='enclosed'>
      <TabList className={styles.repoTabList}>
        <Tab className={styles.repoTab}>ProxySQL</Tab>
        <Tab className={styles.repoTab}>HAProxy</Tab>
        <Tab className={styles.repoTab}>MaxScale</Tab>
      </TabList>
      <TabPanels>
        <TabPanel px='0' pt='3'>
          <TableType2 dataArray={proxysqlRows} className={styles.tableWithHelp} helpColumn={true} />
        </TabPanel>
        <TabPanel px='0' pt='3'>
          <TableType2 dataArray={haproxyRows} className={styles.tableWithHelp} helpColumn={true} />
        </TabPanel>
        <TabPanel px='0' pt='3'>
          <TableType2 dataArray={maxscaleRows} className={styles.tableWithHelp} helpColumn={true} />
        </TabPanel>
      </TabPanels>
    </Tabs>
  ), [proxyTypeTabIndex, proxysqlRows, haproxyRows, maxscaleRows])

  const commonRows = useMemo(() => [
    { key: 'Proxies Compression to Backends', help: h(hCompress, 'Proxies Compression to Backends'), value: sw('proxy-servers-backend-compression', 'proxyServersBackendCompression') },
    { key: 'Proxies Reads on Writer', help: h(hReadsWriter, 'Proxies Reads on Writer'), value: sw('proxy-servers-read-on-master', 'proxyServersReadOnMaster') },
    { key: 'Proxies Reads on Writer When No Slave', help: h(hReadsNoSlave, 'Proxies Reads on Writer When No Slave'), value: sw('proxy-servers-read-on-master-no-slave', 'proxyServersReadOnMasterNoSlave') },
    { key: 'Proxies Max Backend Connections', help: h(hMaxConn, 'Proxies Max Backend Connections'), value: (<RMSlider value={config?.proxyServersBackendMaxConnections} min={100} max={10000} step={100} showMarkAtInterval={2000} selectedMarkLabelCSS={styles.maxConnectMarkLabel} confirmTitle='Confirm change backends max connections : ' onChange={(val) => dispatch(setSetting({ clusterName, setting: 'proxy-servers-backend-max-connections', value: val }))} />) },
    { key: 'Proxies Max Backend Replication Lag for Reads', help: h(hMaxLag, 'Proxies Max Backend Replication Lag for Reads'), value: (<RMSlider value={config?.proxyServersBackendMaxReplicationLag} min={10} max={5000} step={1} showMarkAtInterval={1000} selectedMarkLabelCSS={styles.maxConnectMarkLabel} confirmTitle='Confirm change delay : ' onChange={(val) => dispatch(setSetting({ clusterName, setting: 'proxy-servers-backend-max-replication-lag', value: val }))} />) },
    { key: 'Inject Test Traffic', help: h(hInjectTraffic, 'Inject Test Traffic'), value: sw('test-inject-traffic', 'testInjectTraffic') },
    { key: 'Inject Traffic Mode', help: h(hInjectMode, 'Inject Traffic Mode'), value: (<Dropdown options={[{ name: 'ddl — DDL marker (default, not flashback-able)', value: 'ddl' }, { name: 'dml — DML marker (flashback-able, experimental)', value: 'dml' }]} selectedValue={config?.injectTrafficMode} confirmTitle={'Confirm inject-traffic-mode to'} isDisabled={isDisabled} onChange={(val) => dispatch(setSetting({ clusterName, setting: 'inject-traffic-mode', value: val }))} />) },
  ], [h, sw, dispatch, clusterName, isDisabled, config])

  // Outer table: proxy type (ProxySQL/HAProxy) tab strip is one row, Common
  // settings are plain rows in the same table.
  const dataObject = useMemo(() => [
    { key: 'Proxy Type', value: proxyTypeTabs },
    ...commonRows,
  ], [proxyTypeTabs, commonRows])

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
      {isMaxscaleCredModalOpen && (
        <SetCredentialsModal
          clusterName={clusterName}
          isOpen={isMaxscaleCredModalOpen}
          closeModal={() => setIsMaxscaleCredModalOpen(false)}
          type='maxscale-servers-credential'
        />
      )}
    </>
  )
}

export default ProxySettings
