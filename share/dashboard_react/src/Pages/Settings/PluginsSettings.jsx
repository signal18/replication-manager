import { Box, Flex, Spinner, Text } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import { useState, useEffect } from 'react'
import PropTypes from 'prop-types'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import TableType2 from '../../components/TableType2'
import RMIconButton from '../../components/RMIconButton'
import LogSlider from '../../components/Sliders/LogSlider'
import RMSwitch from '../../components/RMSwitch'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import { setGlobalSetting } from '../../redux/globalClustersSlice'
import styles from './styles.module.scss'
import { clusterService } from '../../services/clusterService'
import Dropdown from '../../components/Dropdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function PluginsSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const baseURL = useSelector((state) => state?.auth?.baseURL || '')
  const globalConfig = useSelector((state) => state?.globalClusters?.monitor?.config)
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      onClick={() => openInfoModal(title, content)}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const sw = (setting, configKey) => (
    <RMSwitch
      confirmTitle={`Confirm switch settings for ${setting}?`}
      onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))}
      isDisabled={user?.grants['cluster-settings'] == false}
      isChecked={selectedCluster?.config?.[configKey]}
    />
  )

  const hSigningKey = `**Plugin Signing Public Key**\n\nPath to the Ed25519 public key used to verify external log plugin binaries before they are executed.\n\nWhen set, every plugin binary pulled into the cluster plugins directory must have a matching \`.sig\` sidecar file in \`<share>/plugins/\`.\nPlugins that fail verification are rejected and logged as errors — they are never executed.\n\nLeave empty (or remove the key file) to skip verification on dev or unsigned builds.\n\nThe public key ships with the repman package at \`<ShareDir>/plugins/plugin-signing.pub\`.\n\nConfig: \`plugin-signing-public-key\``
  const hLogPlugin = `**Enable Log Plugins**\n\nGlobal switch that activates the external log plugin evaluation loop.\nWhen disabled no plugin binaries are executed regardless of individual plugin settings.\n\nConfig: \`log-plugin\``
  const hLogPluginLevel = `**Plugin Module Verbosity**\n\nLog verbosity level for the plugin evaluation subsystem.\n\n- **0** — disabled\n- **1** — errors only\n- **2** — errors + warnings\n- **3** — informational\n- **4** — debug\n\nConfig: \`log-level-plugin\``

  const [plugins, setPlugins] = useState([])
  const [pluginsLoading, setPluginsLoading] = useState(false)

  useEffect(() => {
    if (!selectedCluster?.name) return
    setPluginsLoading(true)
    clusterService
      .getClusterPlugins(selectedCluster.name, baseURL)
      .then(({ data }) => setPlugins(Array.isArray(data) ? data : []))
      .catch(() => setPlugins([]))
      .finally(() => setPluginsLoading(false))
  }, [selectedCluster?.name, selectedCluster?.config?.logPlugin, baseURL])

  const buildPluginRows = () => {
    const rows = []

    if (pluginsLoading) {
      rows.push({ key: 'Plugins', value: <Spinner size='sm' /> })
      return rows
    }

    if (plugins.length === 0) {
      rows.push({
        key: 'Plugins',
        value: <Text fontSize='sm' color='gray.400'>No plugins loaded — enable log-plugin and place plugin binaries in the cluster plugins directory</Text>
      })
      return rows
    }

    plugins.forEach((plugin) => {
      const isPluginEnabled = plugin.config?.['enabled'] !== 'false'
      const desc = pluginDescription(plugin.name)

      rows.push({
        key: (
          <Text fontWeight='semibold' fontSize='sm' color='blue.300'>
            {plugin.name}
          </Text>
        ),
        help: desc ? h(desc, plugin.name) : null,
        value: (
          <RMSwitch
            confirmTitle={`Confirm ${isPluginEnabled ? 'disable' : 'enable'} plugin '${plugin.name}'?`}
            onChange={() =>
              dispatch(setSetting({
                clusterName: selectedCluster?.name,
                setting: `plugin-config-${plugin.name}-enabled`,
                value: isPluginEnabled ? 'false' : 'true'
              }))
            }
            isDisabled={user?.grants['cluster-settings'] == false}
            isChecked={isPluginEnabled}
          />
        )
      })

      pluginKnownKeys(plugin.name).forEach((key) => {
        const currentRaw = plugin.config?.[key] ?? pluginKeyDefault(plugin.name, key)
        const type = pluginKeyType(plugin.name, key)
        const keyHelp = pluginKeyHelp(plugin.name, key)

        let control
        if (key === 'min-log-level') {
          const levelOptions = [
            { value: 'System',  label: 'System — startup/shutdown only' },
            { value: 'Note',    label: 'Note — informational+' },
            { value: 'Warning', label: 'Warning — warnings + errors (default)' },
            { value: 'ERROR',   label: 'ERROR — errors only' },
          ]
          control = (
            <Dropdown
              options={levelOptions}
              selectedValue={currentRaw}
              isDisabled={user?.grants['cluster-settings'] == false}
              confirmTitle={`Confirm min-log-level for '${plugin.name}': `}
              onChange={(opt) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-min-log-level`,
                  value: opt.value
                }))
              }
            />
          )
        } else if (type === 'text') {
          control = (
            <TextForm
              value={currentRaw}
              confirmTitle={`Confirm change '${key}' for '${plugin.name}' to: `}
              onSave={(value) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-${key}`,
                  value: value
                }))
              }
            />
          )
        } else if (type === 'bool') {
          const isOn = currentRaw === 'true' || currentRaw === true
          control = (
            <RMSwitch
              confirmTitle={`Confirm change '${key}' for '${plugin.name}'?`}
              onChange={() =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-${key}`,
                  value: isOn ? 'false' : 'true'
                }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={isOn}
            />
          )
        } else {
          const isFloat = type === 'float'
          const { min, max, step } = pluginKeyRange(plugin.name, key)
          control = (
            <NumberInput
              min={min}
              max={max}
              step={step}
              value={isFloat ? parseFloat(currentRaw) : parseInt(currentRaw, 10)}
              isDisabled={user?.grants['cluster-settings'] == false}
              showConfirmModal={true}
              confirmTitle={`Confirm change '${key}' for '${plugin.name}' to: `}
              onConfirm={(val) =>
                dispatch(setSetting({
                  clusterName: selectedCluster?.name,
                  setting: `plugin-config-${plugin.name}-${key}`,
                  value: String(val)
                }))
              }
            />
          )
        }

        rows.push({
          key: `  ${pluginKeyLabel(plugin.name, key)}`,
          help: keyHelp ? h(keyHelp, pluginKeyLabel(plugin.name, key)) : null,
          value: control
        })
      })
    })

    return rows
  }

  const dataObject = [
    {
      key: 'Signing',
      value: [
        {
          key: 'Plugin Signing Public Key',
          help: h(hSigningKey, 'Plugin Signing Public Key'),
          value: (
            <TextForm
              value={globalConfig?.pluginSigningPublicKey}
              confirmTitle={`Confirm change 'plugin-signing-public-key' to `}
              onSave={(value) =>
                dispatch(setGlobalSetting({ setting: 'plugin-signing-public-key', value: value.length === 0 ? '{undefined}' : value }))
              }
            />
          )
        }
      ]
    },
    {
      key: 'External Plugins',
      value: [
        {
          key: 'Enable Log Plugins',
          help: h(hLogPlugin, 'Enable Log Plugins'),
          value: sw('log-plugin', 'logPlugin')
        },
        {
          key: 'Plugin Module Verbosity',
          help: h(hLogPluginLevel, 'Plugin Module Verbosity'),
          value: (
            <LogSlider
              value={selectedCluster?.config?.logPluginLevel}
              confirmTitle={`Confirm change 'log-level-plugin' to: `}
              onChange={(val) =>
                dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'log-level-plugin', value: val }))
              }
            />
          )
        },
        ...buildPluginRows()
      ]
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.tableWithHelp} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

// ---- Plugin metadata --------------------------------------------------------

function pluginKnownKeys(pluginName) {
  switch (pluginName) {
    // Built-in in-process plugins
    case 'errorlog':
      return ['timeframe-hours', 'min-log-level', 'spike-sigma']
    case 'sqlerrorlog':
    case 'slowlog':
      return ['timeframe-hours', 'spike-sigma']
    case 'auditlog':
      return ['current-window-hours', 'baseline-window-hours', 'spike-sigma']
    // Workload plugins
    case 'plugin-connection-storm':
      return ['sleep-ratio-threshold', 'lock-wait-count', 'min-connections']
    case 'plugin-error-storm':
      return ['storm-threshold', 'storm-window-mins']
    case 'plugin-full-table-scan-spike':
      return ['scan-ratio-threshold', 'min-full-scan-count', 'min-exec-count']
    case 'plugin-metadata-lock-contention':
      return ['lock-wait-ms-threshold', 'lock-count-threshold']
    case 'plugin-replication-lag-predictor':
      return ['window-mins', 'write-rate-threshold']
    case 'plugin-slow-query-regression':
      return ['timeframe-hours', 'regression-factor', 'min-executions']
    case 'plugin-tmp-table-storm':
      return ['disk-tmp-threshold', 'ratio-threshold', 'min-exec-count']
    case 'plugin-off-hours-access':
      return ['allowed-hours-start', 'allowed-hours-end', 'always-allowed-users', 'allowed-operations', 'timeframe-hours']
    case 'plugin-privilege-escalation':
      return ['allowed-admin-users', 'timeframe-hours']
    case 'plugin-binlog-cleartext-password':
    case 'plugin-binlog-creditcard-leak':
      return ['timeframe-hours', 'max-findings']
    // Security plugins with user-list config
    case 'plugin-security-weak-auth':
      return ['ignored-users', 'include-empty']
    case 'plugin-security-no-password-user':
      return ['ignored-users']
    case 'plugin-security-hardening':
      return ['wildcard-priv-ignored-users']
    // Score plugins and plugins with no configurable params
    default:
      return []
  }
}

function pluginKeyType(pluginName, key) {
  const floatKeys = new Set(['sleep-ratio-threshold', 'scan-ratio-threshold', 'ratio-threshold', 'regression-factor', 'spike-sigma'])
  const textKeys  = new Set(['always-allowed-users', 'allowed-operations', 'allowed-admin-users', 'ignored-users', 'wildcard-priv-ignored-users'])
  const boolKeys  = new Set(['include-empty'])
  if (floatKeys.has(key)) return 'float'
  if (textKeys.has(key))  return 'text'
  if (boolKeys.has(key))  return 'bool'
  return 'int'
}

function pluginKeyRange(pluginName, key) {
  switch (key) {
    case 'sleep-ratio-threshold':
    case 'scan-ratio-threshold':
    case 'ratio-threshold':
      return { min: 0.05, max: 1.0, step: 0.05 }
    case 'regression-factor':
      return { min: 1.0, max: 20.0, step: 0.5 }
    case 'spike-sigma':
      return { min: 0.5, max: 10.0, step: 0.5 }
    case 'allowed-hours-start':
      return { min: 0, max: 23, step: 1 }
    case 'allowed-hours-end':
      return { min: 1, max: 24, step: 1 }
    case 'lock-wait-ms-threshold':
      return { min: 100, max: 300000, step: 100 }
    case 'timeframe-hours':
    case 'current-window-hours':
    case 'baseline-window-hours':
    case 'window-mins':
    case 'storm-window-mins':
      return { min: 1, max: 1440, step: 1 }
    case 'max-findings':
      return { min: 1, max: 100, step: 1 }
    default:
      return { min: 1, max: 10000, step: 1 }
  }
}

function pluginKeyLabel(_pluginName, key) {
  const labels = {
    'timeframe-hours':            'Timeframe (hours)',
    'current-window-hours':       'Current window (hours)',
    'baseline-window-hours':      'Baseline window (hours)',
    'spike-sigma':                'Spike threshold (σ)',
    'min-log-level':              'Min log level',
    'sleep-ratio-threshold':      'Sleep ratio threshold',
    'lock-wait-count':            'Lock-wait thread count',
    'min-connections':            'Min connections to evaluate',
    'storm-threshold':            'Storm occurrence threshold',
    'storm-window-mins':          'Storm window (minutes)',
    'scan-ratio-threshold':       'Full-scan ratio threshold',
    'min-full-scan-count':        'Min full-scan count',
    'min-exec-count':             'Min execution count',
    'lock-wait-ms-threshold':     'Lock wait threshold (ms)',
    'lock-count-threshold':       'Concurrent lock wait threshold',
    'window-mins':                'Observation window (minutes)',
    'write-rate-threshold':       'DML write rate threshold (q/min)',
    'regression-factor':          'Regression factor (×)',
    'min-executions':             'Min PFS executions for baseline',
    'disk-tmp-threshold':         'On-disk tmp table threshold',
    'ratio-threshold':            'Disk/total tmp ratio threshold',
    'allowed-hours-start':        'Business hours start (0–23)',
    'allowed-hours-end':          'Business hours end (1–24)',
    'always-allowed-users':       'Always-allowed users',
    'allowed-operations':         'Watched audit operations',
    'allowed-admin-users':        'Allowed admin users',
    'max-findings':               'Max findings per evaluation',
    'ignored-users':              'Ignored users',
    'include-empty':              'Flag empty-plugin accounts',
    'wildcard-priv-ignored-users':'Wildcard priv ignored users',
  }
  return labels[key] || key
}

function pluginKeyDefault(pluginName, key) {
  switch (key) {
    case 'spike-sigma':            return '2'
    case 'min-log-level':         return 'Warning'
    case 'timeframe-hours':
      if (pluginName === 'plugin-privilege-escalation') return '24'
      return '1'
    case 'current-window-hours':  return '1'
    case 'baseline-window-hours': return '24'
    case 'sleep-ratio-threshold': return '0.6'
    case 'lock-wait-count':       return '3'
    case 'min-connections':       return '10'
    case 'storm-threshold':       return '10'
    case 'storm-window-mins':     return '5'
    case 'scan-ratio-threshold':  return '0.3'
    case 'min-full-scan-count':   return '10'
    case 'min-exec-count':        return '3'
    case 'lock-wait-ms-threshold':return '5000'
    case 'lock-count-threshold':  return '3'
    case 'window-mins':           return '5'
    case 'write-rate-threshold':  return '50'
    case 'regression-factor':     return '3.0'
    case 'min-executions':        return '5'
    case 'disk-tmp-threshold':    return '20'
    case 'ratio-threshold':       return '0.2'
    case 'allowed-hours-start':   return '8'
    case 'allowed-hours-end':     return '20'
    case 'always-allowed-users':  return 'root,replication_manager'
    case 'allowed-operations':    return 'QUERY,QUERY_DML,QUERY_DDL,CONNECT'
    case 'allowed-admin-users':   return 'root,replication_manager'
    case 'max-findings':          return '10'
    case 'ignored-users':         return ''
    case 'include-empty':         return 'true'
    case 'wildcard-priv-ignored-users': return ''
    default:                      return '1'
  }
}

// ---- Per-key help content ---------------------------------------------------

function pluginKeyHelp(pluginName, key) {
  const helps = {
    'spike-sigma':            '**Spike threshold (σ)**\n\nNumber of standard deviations above the rolling mean before the metric is classified as a spike.\n\nHigher values = less sensitive, fewer false positives. Lower values = more sensitive, more noise.\n\nApplies only to built-in plugins that use Graphite-backed spike detection.',
    'min-log-level':          '**Min log level**\n\nOnly error log entries at or above this severity are fed to the plugin.\n\n- **System** — startup/shutdown messages only\n- **Note** — informational and above\n- **Warning** — warnings and errors *(default)*\n- **ERROR** — errors only',
    'timeframe-hours':        '**Timeframe (hours)**\n\nOnly log/binlog events within this rolling window are inspected.\n\nSmaller values react faster but may miss slow-building patterns. Larger values smooth noise but delay detection.',
    'current-window-hours':   '**Current window (hours)**\n\nThe recent audit log window used to compute the current activity baseline for drift detection.',
    'baseline-window-hours':  '**Baseline window (hours)**\n\nThe historical audit log window used to establish the expected activity level for drift comparison.',
    'sleep-ratio-threshold':  '**Sleep ratio threshold**\n\nFraction of total connections that must be in `Sleep` state to fire WARN0307.\n\nFormula: `sleeping / total ≥ threshold`\n\nA high ratio indicates a connection leak — clients opening connections and not closing them. Default 0.60 (60%).',
    'lock-wait-count':        '**Lock-wait thread count**\n\nNumber of threads simultaneously waiting on a lock (metadata lock, table lock, row lock) to fire WARN0307.\n\nDefault 3.',
    'min-connections':        '**Min connections to evaluate**\n\nSkip evaluation entirely when total active connections are below this value.\n\nPrevents false positives on idle or lightly loaded servers. Default 10.',
    'storm-threshold':        '**Storm occurrence threshold**\n\nNumber of occurrences of the same error template within the window to fire WARN0302.\n\nError messages are fingerprinted (numbers and quoted strings stripped) so that variant messages with different values are grouped. Default 10.',
    'storm-window-mins':      '**Storm window (minutes)**\n\nRolling time window for counting error occurrences. Default 5 minutes.',
    'scan-ratio-threshold':   '**Full-scan ratio threshold**\n\nFraction of total high-frequency query executions that must use a full table scan to fire WARN0304.\n\nFormula: `full_scan_exec / total_exec ≥ threshold`. Default 0.30 (30%).',
    'min-full-scan-count':    '**Min full-scan count**\n\nAbsolute minimum number of full-scan executions required before firing, even if the ratio is exceeded.\n\nPrevents alerts on servers with very low query volume. Default 10.',
    'min-exec-count':         '**Min execution count**\n\nQuery digests with fewer total executions than this threshold are ignored.\n\nFilters out one-off or infrequent queries that would skew ratio calculations. Default 3–5 depending on plugin.',
    'lock-wait-ms-threshold': '**Lock wait threshold (ms)**\n\nAny single metadata lock (MDL) wait longer than this value fires WARN0305.\n\nMDL waits block all concurrent DML on the affected table. Default 5000ms (5 seconds).\n\n*Requires MariaDB `METADATA_LOCK_INFO` plugin.*',
    'lock-count-threshold':   '**Concurrent lock wait threshold**\n\nIf no single wait exceeds the duration threshold but the total number of concurrent MDL waits exceeds this count, WARN0305 fires.\n\nIndicates broad DDL blocking even when individual waits are short. Default 3.',
    'window-mins':            '**Observation window (minutes)**\n\nSlow log entries older than this window are excluded from the DML rate calculation. Default 5 minutes.',
    'write-rate-threshold':   '**DML write rate threshold (queries/min)**\n\nFires WARN0303 when DML statements (INSERT, UPDATE, DELETE, REPLACE, LOAD DATA) appear in the slow log at this rate or higher.\n\nHigh DML rates in the slow log predict near-future replication lag. Default 50 queries/min.',
    'regression-factor':      '**Regression factor (×)**\n\nA query is flagged as regressed when its current slow-log average latency is this many times higher than its PFS historical average.\n\nFormula: `current_avg_ms / pfs_avg_ms ≥ factor`. Default 3× (300% slower).',
    'min-executions':         '**Min PFS executions for baseline**\n\nQuery digests with fewer historical executions in PFS are excluded from regression comparison — not enough data to establish a reliable baseline. Default 5.',
    'disk-tmp-threshold':     '**On-disk tmp table threshold**\n\nFires WARN0306 when the total count of PFS queries creating on-disk temporary tables reaches or exceeds this value. Default 20.',
    'ratio-threshold':        '**Disk/total tmp ratio threshold**\n\nFires WARN0306 when the fraction of temporary tables spilling to disk exceeds this ratio.\n\nFormula: `disk_tmp / (disk_tmp + mem_tmp) ≥ threshold`. Default 0.20 (20%).',
    'allowed-hours-start':    '**Business hours start**\n\nHour of day (local time, 0–23) from which database activity is considered normal. Access before this hour is flagged as off-hours. Default 8 (08:00).',
    'allowed-hours-end':      '**Business hours end**\n\nHour of day (local time, 1–24) after which database activity is considered off-hours. Default 20 (20:00).',
    'always-allowed-users':   '**Always-allowed users**\n\nComma-separated list of database accounts that are never flagged for off-hours access (e.g. monitoring, replication, backup accounts).\n\nDefault: `root,replication_manager`.',
    'allowed-operations':     '**Watched audit operations**\n\nComma-separated list of audit log operation types to inspect. Only these operation types can trigger WARN0309.\n\nDefault: `QUERY,QUERY_DML,QUERY_DDL,CONNECT`.',
    'allowed-admin-users':    '**Allowed admin users**\n\nComma-separated list of accounts permitted to execute privilege-changing DDL (GRANT, REVOKE, CREATE USER, etc.).\n\nAny other account performing these operations fires WARN0308. Default: `root,replication_manager`.',
    'max-findings':           '**Max findings per evaluation**\n\nCaps the number of findings emitted in a single monitoring tick to avoid flooding the log when many events match.\n\nDefault 10.',
    'ignored-users':          '**Ignored users**\n\nComma-separated list of `\'user\'@\'host\'` pairs (or bare usernames) to exclude from this plugin\'s checks.\n\nUseful for suppressing known-safe service accounts. Example: `monitoring,backup_user`.',
    'include-empty':          '**Flag empty-plugin accounts**\n\nWhen enabled, accounts with an empty `authentication_string` *and* no explicit plugin are also flagged as weak auth.\n\nAn empty plugin falls back to the server-default authentication method, which is often `mysql_native_password`. Default: enabled.',
    'wildcard-priv-ignored-users': '**Wildcard priv ignored users**\n\nComma-separated list of accounts to suppress from the wildcard-host privilege advisory (SEC0108).\n\nUse this to exempt known-safe accounts that legitimately need `%` host access (e.g. internal replication or HA accounts).',
  }
  return helps[key] || null
}

// ---- Plugin-level descriptions ----------------------------------------------

function pluginDescription(pluginName) {
  switch (pluginName) {
    case 'plugin-connection-storm':
      return `**Connection Storm Detector** — WARN0307\n\nMonitors the processlist for two signs of connection saturation:\n\n1. **Sleep ratio**: fires when \`sleeping / total ≥ sleep-ratio-threshold\` (default 60%). Indicates a connection leak.\n2. **Lock waits**: fires when threads stuck in lock-wait states ≥ \`lock-wait-count\` (default 3).\n\nSkips evaluation when total connections < \`min-connections\` (default 10) to avoid false positives on idle servers.`

    case 'plugin-error-storm':
      return `**Error Storm Detector** — WARN0302\n\nGroups error log entries by a template fingerprint (strips numbers and quoted strings so variant messages are counted together) then fires when any template appears ≥ \`storm-threshold\` times (default 10) within \`storm-window-mins\` minutes (default 5).\n\nBoth the MariaDB/MySQL error log and the SQL error log are scanned.`

    case 'plugin-full-table-scan-spike':
      return `**Full Table Scan Spike** — WARN0304\n\nReads \`performance_schema.events_statements_summary_by_digest\` and fires when **both** conditions hold:\n\n- \`full_scan_exec / total_exec ≥ scan-ratio-threshold\` (default 30%)\n- \`full_scan_exec ≥ min-full-scan-count\` (default 10)\n\nIgnores digests with fewer than \`min-exec-count\` (default 5) executions. Reports the top 3 offending query digests.`

    case 'plugin-metadata-lock-contention':
      return `**Metadata Lock Contention** — WARN0305\n\nRequires the MariaDB \`METADATA_LOCK_INFO\` plugin. Reads \`information_schema.METADATA_LOCK_INFO\` and fires when:\n\n- Any single MDL wait ≥ \`lock-wait-ms-threshold\` ms (default 5000ms), **or**\n- Total concurrent MDL waits ≥ \`lock-count-threshold\` (default 3)\n\nMDL waits mean a DDL statement (ALTER TABLE, DROP TABLE) is blocking concurrent DML on that table.`

    case 'plugin-replication-lag-predictor':
      return `**Replication Lag Predictor** — WARN0303\n\nDetects DML write bursts in the slow log *before* lag appears in \`seconds_behind_master\`. Fires when:\n\n\`DML count in window / window-mins ≥ write-rate-threshold queries/min\`\n\nDML verbs counted: INSERT, UPDATE, DELETE, REPLACE, LOAD DATA. Default: 50 DML/min over a 5-minute window.`

    case 'plugin-slow-query-regression':
      return `**Slow Query Regression** — WARN0301\n\nCompares current slow-log average latency against the PFS historical baseline. Fires for any query where:\n\n\`current_avg_ms / pfs_avg_ms ≥ regression-factor\` (default 3×)\n\nCurrent window = last \`timeframe-hours\` hours of slow log. Only considers digests with ≥ \`min-executions\` PFS executions to ensure a meaningful baseline. Up to 5 regressions reported per tick.`

    case 'plugin-tmp-table-storm':
      return `**Temp Table Disk Storm** — WARN0306\n\nReads PFS to detect queries creating on-disk temporary tables. Fires when **either**:\n\n- On-disk tmp count ≥ \`disk-tmp-threshold\` (default 20), **or**\n- \`disk_tmp / (disk_tmp + mem_tmp) ≥ ratio-threshold\` (default 20%)\n\nOnly digests with ≥ \`min-exec-count\` executions are included. Common causes: missing indexes on GROUP BY/ORDER BY columns, or \`tmp_table_size\` / \`max_heap_table_size\` too small.`

    case 'plugin-off-hours-access':
      return `**Off-Hours Access** — WARN0309\n\nScans the audit log for connections or DML outside business hours from non-exempt accounts. Fires when:\n\n- Access time is outside \`allowed-hours-start\`–\`allowed-hours-end\` (default 08:00–20:00 local time)\n- Account is not in \`always-allowed-users\`\n- Operation type is in \`allowed-operations\`\n- Event falls within the last \`timeframe-hours\` (default 1h)\n\nUseful for PCI-DSS and HIPAA compliance auditing.`

    case 'plugin-privilege-escalation':
      return `**Privilege Escalation** — WARN0308\n\nWatches the audit log for DDL that modifies user privileges: GRANT, REVOKE, CREATE USER, ALTER USER, DROP USER, RENAME USER, SET PASSWORD.\n\nFires when such an operation is performed by any account **not** in \`allowed-admin-users\` (default: root, replication_manager) within the last \`timeframe-hours\` (default 24h).`

    case 'plugin-binlog-cleartext-password':
      return `**Binlog Cleartext Password** — WARN0310\n\nScans binlog QUERY events for SQL statements containing a cleartext password literal:\n\n- \`CREATE USER … IDENTIFIED BY 'pwd'\`\n- \`ALTER USER … IDENTIFIED BY 'pwd'\`\n- \`GRANT … IDENTIFIED BY 'pwd'\`\n- \`SET PASSWORD … = 'pwd'\`\n\nThe password value is partially redacted in findings (first + last character shown). Scans events within \`timeframe-hours\` (default 1h), capped at \`max-findings\` per tick (default 10).`

    case 'plugin-binlog-creditcard-leak':
      return `**Binlog Credit Card Leak** — WARN0311\n\nScans binlog QUERY events for potential credit card PANs using two layers:\n\n1. **Regex**: matches 13–19 digit sequences with optional space/dash separators\n2. **Luhn algorithm**: validates candidates to filter phone numbers, order IDs, and timestamps\n\nSupports Visa, Mastercard, Amex, Discover, UnionPay. PANs are masked in findings (last 4 digits shown). Window: \`timeframe-hours\` (default 1h), cap: \`max-findings\` (default 10).`

    case 'plugin-innodb-corruption':
      return `**InnoDB Corruption Detector** — WARN0300\n\nScans the error log for InnoDB corruption indicators in the last 24 hours. Detected keywords: "corruption", "corrupted", "Database page corruption", "checksum mismatch", "is in the future", etc.\n\nNo configurable parameters — the 24-hour window is fixed.`

    case 'plugin-security-weak-auth':
      return `**Weak Authentication** — SEC0101\n\nAudits \`mysql.user\` for accounts using weak or deprecated authentication plugins.\n\n**Flagged plugins:** \`mysql_native_password\` (SHA-1, deprecated in MySQL 8.0, removed in 8.4), \`mysql_old_password\` (DES-based)\n\n**Safe plugins:** \`ed25519\`, \`caching_sha2_password\`, \`auth_gssapi\`, \`unix_socket\`, \`parsec\`\n\nUse \`ignored-users\` to suppress known-safe service accounts.`

    case 'plugin-security-no-password-user':
      return `**No-Password User** — SEC0100\n\nFlags accounts in \`mysql.user\` where \`authentication_string\` is empty and no socket-based authentication plugin is active.\n\nEmpty-password accounts with no socket auth allow connections from any host without any credential. Use \`ignored-users\` to exclude known-safe internal accounts.`

    case 'plugin-security-hardening':
      return `**Security Hardening** — SEC0103–SEC0118\n\nChecks a set of CIS MySQL/MariaDB Benchmark hardening controls:\n\n- **SEC0103** \`require_secure_transport=OFF\` — plaintext connections allowed\n- **SEC0104** \`general_log=ON\` — passwords logged in cleartext\n- **SEC0105** \`secure_file_priv=''\` — unrestricted filesystem access\n- **SEC0106** \`skip_name_resolve=OFF\` — DNS hostname spoofing possible\n- **SEC0107** Anonymous user account exists\n- **SEC0108** Wildcard-host account with SUPER/ADMIN/ALL privileges\n- **SEC0113–SEC0115** MariaDB password validation plugins\n- **SEC0116–SEC0117** MySQL password validation\n- **SEC0118** Hostname grants with skip_name_resolve=ON\n\nUse \`wildcard-priv-ignored-users\` to exempt known-safe wildcard-host accounts from SEC0108.`

    default:
      if (pluginName && pluginName.startsWith('plugin-score-')) {
        const scoreName = pluginName.replace('plugin-score-', '')
        return `**Security Score: ${scoreName.charAt(0).toUpperCase() + scoreName.slice(1)}**\n\nComputes binary pass/fail security score checks from the server snapshot. Outputs \`ScoreCheck\` entries that feed the SecurityScore gauge in the dashboard.\n\nNo configurable parameters — calculation is derived entirely from the server state (SHOW GLOBAL VARIABLES, mysql.user, etc.).`
      }
      return null
  }
}

PluginsSettings.propTypes = {
  selectedCluster: PropTypes.object,
  user: PropTypes.object
}

export default PluginsSettings
