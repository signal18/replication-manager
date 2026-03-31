import { Box, Flex, HStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch, useSelector } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import NumberInput from '../../components/NumberInput'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function MonitoringSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: renderInfoModalBody(content) })
    setIsCommonModalOpen(true)
  }
  const closeInfoModal = () => setIsCommonModalOpen(false)

  const renderInfoModalBody = (content) => (
    <Box className={modalStyles.infoTooltip}>
      <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>
    </Box>
  )

  // ── Help content ────────────────────────────────────────────────────────────

  const helpSaveConfig = `**Monitoring Save Config**

When enabled, any setting changed through the API or GUI is immediately persisted to the cluster configuration file on disk.  
When disabled, changes apply at runtime only and are lost on the next restart.`

  const helpPause = `**Monitoring Pause**

Temporarily suspends all monitoring cycles for this cluster.  
The replication-manager process keeps running but stops polling servers, evaluating failover conditions, and updating state.  
Useful during planned maintenance windows to avoid spurious alerts or unintended failovers.`

  const helpMonitoringCapture = `**Monitoring Capture On Error**

When enabled, replication-manager captures a full diagnostic snapshot every time an error state is detected.  
The snapshot includes:
- \`SHOW FULL PROCESSLIST\`
- \`SHOW ENGINE INNODB STATUS\`
- Slave and master replication status

Snapshots are written to JSON files named \`capture_<server>_<timestamp>.json\` in the cluster working directory.  
Use the **Monitoring Capture On Error Trigger** setting to restrict capture to specific error codes.`

  const helpCaptureTrigger = `**Monitoring Capture On Error Trigger**

A comma-separated list of error codes that restrict when a capture snapshot is taken.  
Leave empty to capture on every error state transition.  

Example: \`ERR00001,ERR00002\``

  const helpIgnoreErrors = `**Monitoring Ignore Error List**

A comma-separated list of error or warning codes that replication-manager will silently suppress.  
Useful to avoid alert noise from known, accepted conditions in your environment.  

Example: \`WARN0001,ERR00042\``

  const helpSchema = `**Monitoring Schema**

When enabled, replication-manager tracks DDL changes (CREATE, ALTER, DROP) on all schemas.  
A diff is computed at each monitoring cycle and exposed via the schema-change API endpoint and GUI tab.  
Requires **Monitoring Schema Columns** and/or **Monitoring Schema Indexes** to also be enabled for full coverage.`

  const helpSchemaColumns = `**Monitoring Schema Columns**

When enabled, column-level changes (ADD COLUMN, DROP COLUMN, MODIFY COLUMN) are included in the schema diff.  
Requires **Monitoring Schema** to be enabled.`

  const helpSchemaIndexes = `**Monitoring Schema Indexes**

When enabled, index-level changes (ADD INDEX, DROP INDEX, CREATE UNIQUE INDEX) are included in the schema diff.  
Requires **Monitoring Schema** to be enabled.`

  const helpSchemaIgnoreTables = `**Monitoring Schema Ignore Tables**

A comma-separated list of tables (in \`schema.table\` format) to exclude from schema change monitoring.  
Changes on these tables will never appear in the schema diff, regardless of the other schema settings.  

Example: \`mydb.audit_log,mydb.temp_import\``

  const helpSchemaScanTimeout = `**Monitoring Schema Scan Timeout**

Maximum time in seconds that replication-manager will wait for a schema metadata query to complete  
(\`INFORMATION_SCHEMA.TABLES\`, \`INFORMATION_SCHEMA.COLUMNS\`, \`INFORMATION_SCHEMA.STATISTICS\`).  
Default: **30 seconds**.  
Increase this value on servers with very large numbers of tables or slow I_S performance.`

  const helpVariableDiff = `**Monitoring Variable Diff**

Tracks changes in MySQL/MariaDB global variables between monitoring cycles.  
When a variable value changes unexpectedly (e.g. after a \`SET GLOBAL\`), the diff is logged and surfaced in the GUI.  
Useful for detecting configuration drift across a cluster.`

  const helpProcesslist = `**Monitoring Processlist**

Enables collection of \`SHOW FULL PROCESSLIST\` on every monitoring cycle.  
Collected data is exposed in the GUI processlist tab and included in capture snapshots.

Related settings:
- **Monitoring Processlist Inactive** — include idle connections
- **Monitoring Processlist Transactions** — include InnoDB transaction details
- **Monitoring Processlist Information Schema** — use \`information_schema.processlist\` instead of \`SHOW FULL PROCESSLIST\``

  const helpProcesslistInfoSchema = `**Monitoring Processlist Information Schema**

When enabled, processlist data is collected from \`information_schema.processlist\` instead of \`SHOW FULL PROCESSLIST\`.  
The I_S table includes additional columns and is not subject to the \`PROCESS\` privilege restriction on MySQL 8.0+.  
On MariaDB, \`SHOW FULL PROCESSLIST\` is generally preferred.`

  const helpProcesslistInactive = `**Monitoring Processlist Inactive**

When enabled, idle connections (Command = \`Sleep\`) are included in the collected processlist.  
Disabled by default to reduce noise. Enable when tracking connection pool behaviour or idle connection leaks.`

  const helpProcesslistTransactions = `**Monitoring Processlist Transactions**

When enabled, active InnoDB transaction details from \`information_schema.innodb_trx\` are joined into the processlist view.  
Useful for identifying long-running transactions, lock waits, and deadlock candidates.`

  const helpInnoDBStatus = `**Monitoring InnoDB Status**

Enables periodic collection of \`SHOW ENGINE INNODB STATUS\` output.  
The result is parsed and exposed in the GUI and included in capture snapshots.  
Useful for diagnosing lock contention, deadlocks, buffer pool pressure, and I/O bottlenecks.`

  const helpInnoDBMutex = `**Monitoring InnoDB Mutex**

Enables collection of mutex wait metrics from \`performance_schema.events_waits_summary_global_by_event_name\`  
for InnoDB mutex instruments (names matching \`wait/synch/mutex/innodb/%\`).  
Exposes per-mutex spin, wait, and signal counts in the GUI graphs.  
Requires Performance Schema to be enabled on the monitored servers.`

  const helpInnoDBLatch = `**Monitoring InnoDB Latch**

Enables collection of read-write lock (latch) wait metrics from Performance Schema  
for InnoDB rw-lock instruments (names matching \`wait/synch/rwlock/innodb/%\`).  
Latches protect InnoDB internal structures; high latch waits indicate concurrency bottlenecks.  
Requires Performance Schema to be enabled on the monitored servers.`

  const helpPFSMemory = `**Monitoring Performance Schema Memory**

Enables collection of memory instrument metrics from \`performance_schema.memory_summary_global_by_event_name\`.  
Exposes per-subsystem memory consumption (InnoDB buffer pool, temp tables, etc.) in the GUI graphs.  
Requires Performance Schema to be enabled on the monitored servers.`

  const helpPFSInstruments = `**Monitoring Performance Schema Instruments**

Enables collection of instrument enable/disable state from \`performance_schema.setup_instruments\`.  
Useful to audit which Performance Schema instruments are active on each server.`

  const helpPFSQueries = `**Monitoring Performance Schema Queries**

Enables periodic snapshot capture of \`performance_schema.events_statements_summary_by_digest\`.

At each period boundary the digest table is:
1. **Read** — all digest rows with stats and a concrete sample SQL are written to a timestamped JSON-lines file:  
   \`<datadir>/log/log_pfs_queries_<YYYYMMDD_HH>.jsonl\`
2. **Truncated** — counters reset so the next period reflects only queries that ran in that window.

Each line in the snapshot contains: \`digest\`, \`digestText\` (normalised template), \`sampleQuery\` (concrete SQL for EXPLAIN), \`execCount\`, \`execTimeAvgMs\`, \`rowsScanned\`, \`planFullScan\`, etc.

> **Note:** Requires \`performance_schema = ON\` on the monitored servers.`

  const helpPFSQueriesPeriod = `**Monitoring Performance Schema Queries Period (hours)**

How many hours between consecutive PFS digest snapshot flushes.  
Default: **1 hour**.

Shorter periods give finer-grained workload windows but produce more snapshot files.  
The truncate at the end of each period resets all digest counters, so stats always reflect only the queries seen in that window.`

  const helpPFSExplain = `**Monitoring Performance Schema Queries Explain**

When enabled, replication-manager runs \`EXPLAIN\` for each query template captured during a PFS snapshot and persists the query plan to disk:  
\`<datadir>/log/pfs_explain_cache.jsonl\`

**Priority order for EXPLAIN execution:**
1. Templates never yet explained (highest priority)
2. Templates with the oldest cached plan (most likely stale after schema or statistics changes)

Plans are cached per digest hash. Once a plan exists it is refreshed in-memory each period but only appended to disk when new. The cache file is deduplicated on restart (last-write wins per digest).

Use the cached plans API endpoints:
- \`GET .../queries/{digest}/actions/explain-pfs-cached\` — single plan
- \`GET .../queries/explain-pfs-cached\` — all plans for a server`

  const helpPFSExplainDelay = `**Monitoring Performance Schema Queries Explain Delay (ms)**

Milliseconds to sleep between consecutive \`EXPLAIN\` calls during a snapshot.  
Default: **200 ms**.

Since EXPLAIN can trigger optimizer work (especially on complex queries with subqueries or derived tables), spreading calls over time prevents a burst of optimizer load at snapshot time.  
The delay uses a cancellable timer — if a new snapshot fires before all EXPLAINs finish, the in-flight run is interrupted immediately rather than waiting for the next delay to expire.  
Set to **0** to disable throttling entirely (not recommended on production).`

  const helpPFSExplainPurge = `**Monitoring Performance Schema Queries Explain Purge Period (days)**

Age in days after which a cached explain plan is evicted from both the in-memory map and the on-disk cache file.  
Default: **30 days**.

The purge runs once per day inside the monitoring cycle. When entries are evicted the cache file is atomically rewritten (temp file + rename) so it is never left in a partial state.  
Set to **0** to keep plans forever (no automatic purge).`

  // ── Settings ────────────────────────────────────────────────────────────────

  const {
    settings: {
      monSaveConfigLoading,
      monPauseLoading,
      monCaptureLoading,
      monSchemaChangeLoading,
      monInnoDBLoading,
      monVarDiffLoading,
      monProcessListLoading,
      monProcessListLoadingInactive,
      monProcessListLoadingTransactions,
      monProcessListLoadingInformationSchema,
      captureTriggerLoading,
      monIgnoreErrLoading
    }
  } = useSelector((state) => state)

  const helpKey = (label, helpContent, title) => (
    <HStack spacing={1} align="center">
      <span>{label}</span>
      <RMIconButton
        icon={HiQuestionMarkCircle}
        onClick={() => openInfoModal(title || label, helpContent)}
      />
    </HStack>
  )

  const dataObject = [
    {
      key: 'Monitoring Save Config',
      value: [
        {
          key: helpKey('Monitoring Save Config', helpSaveConfig),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-save-config?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-save-config' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringSaveConfig}
              loading={monSaveConfigLoading}
            />
          )
        },
        {
          key: helpKey('Monitoring Pause', helpPause),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-pause?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-pause' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringPause}
              loading={monPauseLoading}
            />
          )
        }
      ]
    },
    {
      key: helpKey('Monitoring Capture On Error', helpMonitoringCapture),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-capture?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-capture' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringCapture}
          loading={monCaptureLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Capture On Error Trigger', helpCaptureTrigger),
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringCaptureTrigger}
          confirmTitle={`Confirm change 'monitoring-capture-trigger' to `}
          onSave={(captureTriggerValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-capture-trigger',
                value: captureTriggerValue.length === 0 ? '{undefined}' : captureTriggerValue
              })
            )
          }
        />
      )
    },
    {
      key: helpKey('Monitoring Ignore Error List', helpIgnoreErrors),
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringIgnoreErrors}
          confirmTitle={`Confirm change 'monitoring-ignore-errors' to: `}
          onSave={(errorListValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-ignore-errors',
                value: errorListValue.length === 0 ? '{undefined}' : errorListValue
              })
            )
          }
        />
      )
    },
    {
      key: helpKey('Monitoring Schema', helpSchema),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-schema-change?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-change' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringSchemaChange}
          loading={monSchemaChangeLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Schema Columns', helpSchemaColumns),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-schema-columns?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-columns' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringSchemaColumns}
          loading={monSchemaChangeLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Schema Indexes', helpSchemaIndexes),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-schema-indexes?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-schema-indexes' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringSchemaIndexes}
          loading={monSchemaChangeLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Schema Ignore Tables', helpSchemaIgnoreTables),
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringSchemaIgnoreTables}
          confirmTitle={`Confirm change 'monitoring-schema-ignore-tables' to: `}
          onSave={(errorListValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-schema-ignore-tables',
                value: errorListValue.length === 0 ? '&nbsp;' : errorListValue
              })
            )
          }
        />
      )
    },
    {
      key: helpKey('Monitoring Schema Scan Timeout', helpSchemaScanTimeout),
      value: (
        <NumberInput
          value={selectedCluster?.config?.monitoringSchemaScanTimeout}
          showEditButton={true}
          showConfirmModal={true}
          confirmTitle={`Confirm change 'monitoring-schema-scan-timeout' to: `}
          onConfirm={(timeoutValue) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-schema-scan-timeout',
                value: timeoutValue.length === 0 ? '30' : timeoutValue
              })
            )
          }
        />
      )
    },
    {
      key: helpKey('Monitoring Variable Diff', helpVariableDiff),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-variable-diff?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-variable-diff' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringVariableDiff}
          loading={monVarDiffLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Processlist', helpProcesslist),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslist}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Processlist Information Schema', helpProcesslistInfoSchema),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist-information-schema?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist-information-schema' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslistInformationSchema}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Processlist Inactive', helpProcesslistInactive),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist-inactive?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist-inactive' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslistInactive}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring Processlist Transactions', helpProcesslistTransactions),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-processlist-transactions?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-processlist-transactions' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringProcesslistTransactions}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring InnoDB Status', helpInnoDBStatus),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-innodb-status?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-innodb-status' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringInnoDBStatus}
          loading={monInnoDBLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring InnoDB Mutex', helpInnoDBMutex),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-performance-schema-mutex?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-mutex' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringPerformanceSchemaMutex}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: helpKey('Monitoring InnoDB Latch', helpInnoDBLatch),
      value: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for monitoring-performance-schema-latch?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-latch' }))
          }
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={selectedCluster?.config?.monitoringPerformanceSchemaLatch}
          loading={monProcessListLoading}
        />
      )
    },
    {
      key: 'Monitoring Performance Schema Queries',
      value: [
        {
          key: helpKey('Monitoring Performance Schema Memory', helpPFSMemory),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-performance-schema-memory?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-memory' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringPerformanceSchemaMemory}
            />
          )
        },
        {
          key: helpKey('Monitoring Performance Schema Instruments', helpPFSInstruments),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-performance-schema-instruments?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-instruments' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringPerformanceIntruments}
            />
          )
        },
        {
          key: helpKey('Monitoring Performance Schema Queries', helpPFSQueries),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-performance-schema-queries?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-queries' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringPerformanceSchemaQueries}
            />
          )
        },
        {
          key: helpKey('Monitoring Performance Schema Queries Period (hours)', helpPFSQueriesPeriod),
          value: (
            <NumberInput
              value={selectedCluster?.config?.monitoringPerformanceSchemaQueriesPeriod}
              min={1}
              max={168}
              showEditButton={true}
              showConfirmModal={true}
              confirmTitle={`Confirm change 'monitoring-performance-schema-queries-period' to: `}
              onConfirm={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'monitoring-performance-schema-queries-period',
                    value: val.length === 0 ? '1' : val
                  })
                )
              }
            />
          )
        },
        {
          key: helpKey('Monitoring Performance Schema Queries Explain', helpPFSExplain),
          value: (
            <RMSwitch
              confirmTitle={'Confirm switch settings for monitoring-performance-schema-queries-explain?'}
              onChange={() =>
                dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-performance-schema-queries-explain' }))
              }
              isDisabled={user?.grants['cluster-settings'] == false}
              isChecked={selectedCluster?.config?.monitoringPerformanceSchemaQueriesExplain}
            />
          )
        },
        {
          key: helpKey('Monitoring Performance Schema Queries Explain Delay (ms)', helpPFSExplainDelay),
          value: (
            <NumberInput
              value={selectedCluster?.config?.monitoringPerformanceSchemaQueriesExplainDelay}
              min={0}
              max={5000}
              step={50}
              showEditButton={true}
              showConfirmModal={true}
              confirmTitle={`Confirm change 'monitoring-performance-schema-queries-explain-delay' to: `}
              onConfirm={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'monitoring-performance-schema-queries-explain-delay',
                    value: val.length === 0 ? '200' : val
                  })
                )
              }
            />
          )
        },
        {
          key: helpKey('Monitoring Performance Schema Queries Explain Purge Period (days)', helpPFSExplainPurge),
          value: (
            <NumberInput
              value={selectedCluster?.config?.monitoringPerformanceSchemaQueriesExplainPurgePeriod}
              min={0}
              max={365}
              showEditButton={true}
              showConfirmModal={true}
              confirmTitle={`Confirm change 'monitoring-performance-schema-queries-explain-purge-period' to: `}
              onConfirm={(val) =>
                dispatch(
                  setSetting({
                    clusterName: selectedCluster?.name,
                    setting: 'monitoring-performance-schema-queries-explain-purge-period',
                    value: val.length === 0 ? '30' : val
                  })
                )
              }
            />
          )
        }
      ]
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} labelClassName={styles.labelWithHelp} />
      </Flex>
      <CommonModal
        isOpen={isCommonModalOpen}
        closeModal={closeInfoModal}
        title={action.title}
        body={action.body}
        size='xl'
      />
    </>
  )
}

export default MonitoringSettings
