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

// Static help text - hoisted to module scope (not derived from props/state) so the
// useMemo blocks below don't need them in their dependency arrays.
const hMonitor = `**ProxySQL Monitor**\n\nEnables replication-manager's ProxySQL integration.\nKeeps ProxySQL's server list, hostgroups, and routing rules in sync with the current cluster topology.\n\nConfig: \`proxysql\``
const hServers = `**ProxySQL Bootstrap Servers**\n\nPushes the current cluster server list into ProxySQL's \`mysql_servers\` table.\n\nConfig: \`proxysql-bootstrap-servers\``
const hUsers = `**ProxySQL Bootstrap Users**\n\nCopies MySQL user grants into ProxySQL's \`mysql_users\` table.\n\nConfig: \`proxysql-bootstrap-users\``
const hVars = `**ProxySQL Bootstrap Variables**\n\nApplies recommended ProxySQL global variables from the replication-manager configuration.\n\nConfig: \`proxysql-bootstrap-variables\``
const hHG = `**ProxySQL Bootstrap Hostgroups**\n\nConfigures ProxySQL hostgroups (writer, reader, backup-writer) based on the current cluster topology.\n\nConfig: \`proxysql-bootstrap-hostgroups\``
const hQR = `**ProxySQL Bootstrap Query Rules**\n\nLoads default query routing rules into ProxySQL. Existing rules are replaced.\n\nConfig: \`proxysql-bootstrap-query-rules\``

const hHaproxyMode = `**HAProxy Mode**\n\nControls how replication-manager drives HAProxy's backend membership and health:\n\n- **runtimeapi** (default): repman reconciles master/reader drain state via HAProxy's Runtime API every monitoring pass, and — when \`haproxy-api-bootstrap-servers\` is also enabled and HAProxy is >= 2.6 — dynamically adds/removes servers as cluster membership changes, without a reload. Deployed by the cluster's own orchestrator.\n- **standby**: repman always runs its own HAProxy instance co-located with repman itself — started/reloaded via a local PID — regardless of which orchestrator the cluster's databases use (OpenSVC, Kubernetes, etc.).\n- **externalcheck**: HAProxy's own external-check command polls repman's HTTP health endpoints to decide backend health/routing; newly provisioned proxies use \`/master-status\` for the write backend and \`/reader-status\` for the read backend. Older externalcheck proxies keep polling legacy \`/slave-status\` until they are reprovisioned after upgrade. Repman does not push state via the Runtime API in this mode. Deployed by the cluster's own orchestrator.\n- **dataplaneapi**: currently handled the same as externalcheck for config generation (servers named positionally, no Runtime API reconciliation) — treat as reserved for a future Data Plane API integration, not yet implemented in this codebase.\n\nChanging this takes effect on the next monitoring pass; a config reload/reprovision of the proxy may still be required for the new mode's config layout to fully apply. For \`externalcheck\`, reprovision the proxy after upgrading if you want it to pick up regenerated checkmaster/checkslave scripts.\n\nConfig: \`haproxy-mode\``
const hHaproxyDynServers = `**HAProxy Bootstrap Servers**\n\nFor HAProxy mode \`runtimeapi\` only. When enabled, repman drives every backend member (read and write) with its own resolved server IP over the Runtime API instead of HAProxy's own DNS resolution: generated server lines carry no \`resolvers\` clause, and adding/removing a cluster server updates the live backend at runtime instead of requiring a reload.\n\nOff (the default) keeps \`runtimeapi\` resolver-backed, identical to \`externalcheck\`/\`standby\` — HAProxy resolves each server's hostname itself and dynamic add/remove is not available.\n\nRequires HAProxy >= 2.6 (silently inactive otherwise). Existing ready/drain/maintenance state handling is unaffected either way.\n\nTakes effect on the next (re)provision of the proxy — toggling this live does not change an already-running proxy's config until it is reprovisioned.\n\nConfig: \`haproxy-api-bootstrap-servers\``
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
const hHaproxyUser = `**HAProxy Admin User**\n\nAdmin username configured for HAProxy's stats/admin interface at provisioning time.\n\nApplied at proxy provisioning time — reprovision the proxy for a change to take effect.\n\nConfig: \`haproxy-user\``
const hHaproxyPassword = `**HAProxy Admin Password**\n\nAdmin password configured for HAProxy's stats/admin interface at provisioning time.\n\nApplied at proxy provisioning time — reprovision the proxy for a change to take effect.\n\nConfig: \`haproxy-password\``

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

  const haproxyRows = useMemo(() => [
    { key: 'HAProxy Mode', help: h(hHaproxyMode, 'HAProxy Mode'), value: (<Dropdown options={[{ name: 'runtimeapi — Runtime API driven (default)', value: 'runtimeapi' }, { name: 'standby — repman-managed, co-located', value: 'standby' }, { name: 'externalcheck — HAProxy external-check', value: 'externalcheck' }, { name: 'dataplaneapi — Data Plane API driven', value: 'dataplaneapi' }]} selectedValue={config?.haproxyMode} confirmTitle={'Confirm haproxy-mode to'} isDisabled={isDisabled} onChange={(val) => dispatch(setSetting({ clusterName, setting: 'haproxy-mode', value: val }))} />) },
    { key: 'HAProxy Bootstrap Servers', help: h(hHaproxyDynServers, 'HAProxy Bootstrap Servers'), value: sw('haproxy-api-bootstrap-servers', 'haproxyAPIBootstrapServers') },
    { key: 'HAProxy Write Port', help: h(hHaproxyWritePort, 'HAProxy Write Port'), value: txt('haproxy-write-port', 'haproxyWritePort') },
    { key: 'HAProxy Read Port', help: h(hHaproxyReadPort, 'HAProxy Read Port'), value: txt('haproxy-read-port', 'haproxyReadPort') },
    { key: 'HAProxy Stat Port', help: h(hHaproxyStatPort, 'HAProxy Stat Port'), value: txt('haproxy-stat-port', 'haproxyStatPort') },
    { key: 'HAProxy Runtime API Port', help: h(hHaproxyAPIPort, 'HAProxy Runtime API Port'), value: txt('haproxy-api-port', 'haproxyAPIPort') },
    { key: 'HAProxy Write Bind IP', help: h(hHaproxyWriteBind, 'HAProxy Write Bind IP'), value: txt('haproxy-ip-write-bind', 'haproxyIpWriteBind') },
    { key: 'HAProxy Read Bind IP', help: h(hHaproxyReadBind, 'HAProxy Read Bind IP'), value: txt('haproxy-ip-read-bind', 'haproxyIpReadBind') },
    { key: 'HAProxy Binary Path', help: h(hHaproxyBinaryPath, 'HAProxy Binary Path'), value: txt('haproxy-binary-path', 'haproxyBinaryPath') },
    { key: 'HAProxy Read Backend Name', help: h(hHaproxyReadBackend, 'HAProxy Read Backend Name'), value: txt('haproxy-api-read-backend', 'haproxyAPIReadBackend') },
    { key: 'HAProxy Write Backend Name', help: h(hHaproxyWriteBackend, 'HAProxy Write Backend Name'), value: txt('haproxy-api-write-backend', 'haproxyAPIWriteBackend') },
    { key: 'HAProxy Staging Backend Name', help: h(hHaproxyStagingBackend, 'HAProxy Staging Backend Name'), value: txt('haproxy-staging-backend', 'haproxyStagingBackend') },
    { key: 'HAProxy Admin User', help: h(hHaproxyUser, 'HAProxy Admin User'), value: txt('haproxy-user', 'haproxyUser') },
    { key: 'HAProxy Admin Password', help: h(hHaproxyPassword, 'HAProxy Admin Password'), value: (<TextForm type='password' value={config?.haproxyPassword || ''} isDisabled={isDisabled} className={styles.textbox} size='sm' confirmTitle={'Confirm haproxy-password to '} onSave={(v) => dispatch(setSetting({ clusterName, setting: 'haproxy-password', value: btoa(v) }))} />) },
  ], [h, sw, txt, dispatch, clusterName, isDisabled, config])

  // Proxy-type-specific settings live behind a Tab strip (ProxySQL/HAProxy, styled
  // like Restic's repository-type tabs), nested as the value of one TableType2 row -
  // same as Restic nests its own repo-type tabs inside a row of its settings table.
  const proxyTypeTabs = useMemo(() => (
    <Tabs index={proxyTypeTabIndex} onChange={setProxyTypeTabIndex} className={styles.repoTabs} variant='enclosed'>
      <TabList className={styles.repoTabList}>
        <Tab className={styles.repoTab}>ProxySQL</Tab>
        <Tab className={styles.repoTab}>HAProxy</Tab>
      </TabList>
      <TabPanels>
        <TabPanel px='0' pt='3'>
          <TableType2 dataArray={proxysqlRows} className={styles.tableWithHelp} helpColumn={true} />
        </TabPanel>
        <TabPanel px='0' pt='3'>
          <TableType2 dataArray={haproxyRows} className={styles.tableWithHelp} helpColumn={true} />
        </TabPanel>
      </TabPanels>
    </Tabs>
  ), [proxyTypeTabIndex, proxysqlRows, haproxyRows])

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
    </>
  )
}

export default ProxySettings
