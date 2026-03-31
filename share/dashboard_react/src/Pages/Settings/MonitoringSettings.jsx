import { Box, Flex, HStack, Text } from '@chakra-ui/react'
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
Requires \`monitoring-schema-columns\` and/or \`monitoring-schema-indexes\` to also be enabled for full coverage.`

  const helpVariableDiff = `**Monitoring Variable Diff**

Tracks changes in MySQL/MariaDB global variables between monitoring cycles.  
When a variable value changes unexpectedly (e.g. after a \`SET GLOBAL\`), the diff is logged and surfaced in the GUI.  
Useful for detecting configuration drift across a cluster.`

  const helpProcesslist = `**Monitoring Processlist**

Enables collection of \`SHOW FULL PROCESSLIST\` on every monitoring cycle.  
Collected data is exposed in the GUI processlist tab and included in capture snapshots.

Related settings:
- **Monitoring Processlist Inactive** — include idle connections (Command = Sleep)
- **Monitoring Processlist Transactions** — include InnoDB transaction details from \`information_schema.innodb_trx\`
- **Monitoring Processlist Information Schema** — use \`information_schema.processlist\` instead of \`SHOW FULL PROCESSLIST\``

  const helpPFSMemory = `**Monitoring Performance Schema Memory**

Enables collection of memory instrument metrics from \`performance_schema.memory_summary_global_by_event_name\`.  
Exposes per-subsystem memory consumption (InnoDB buffer pool, temp tables, etc.) in the GUI graphs.`

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

> **Note:** This feature requires \`performance_schema = ON\` on the monitored servers.`

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

  const dataObject = [
    {
      key: 'Monitoring Save Config',
      value: [
        {
          key: 'Monitoring Save Config',
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
          key: 'Monitoring Pause',
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
      key: (
        <HStack>
          <Text>Monitoring Capture On Error</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Capture On Error', helpMonitoringCapture)} />
        </HStack>
      ),
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
      key: (
        <HStack>
          <Text>Monitoring Capture On Error Trigger</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Capture On Error Trigger', helpCaptureTrigger)} />
        </HStack>
      ),
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
      key: (
        <HStack>
          <Text>Monitoring Ignore Error List</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Ignore Error List', helpIgnoreErrors)} />
        </HStack>
      ),
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
      key: (
        <HStack>
          <Text>Monitoring Schema</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Schema', helpSchema)} />
        </HStack>
      ),
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
      key: 'Monitoring Schema Columns',
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
      key: 'Monitoring Schema Indexes',
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
      key: 'Monitoring Schema Ignore Tables',
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
      key: 'Monitoring Schema Scan Timeout',
      value: (
        <Flex className={styles.valueWithInfo}>
          <Text className={styles.info}>
            Timeout in seconds for schema metadata scans (TABLES, COLUMNS, STATISTICS queries)
          </Text>
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
        </Flex>
      )
    },
    {
      key: (
        <HStack>
          <Text>Monitoring Variable Diff</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Variable Diff', helpVariableDiff)} />
        </HStack>
      ),
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
      key: (
        <HStack>
          <Text>Monitoring Processlist</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Processlist', helpProcesslist)} />
        </HStack>
      ),
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
      key: 'Monitoring Processlist Information Schema',
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
      key: 'Monitoring Processlist Inactive',
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
      key: 'Monitoring Processlist Transactions',
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
      key: 'Monitoring InnoDB Status',
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
      key: 'Monitoring InnoDB Mutex',
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
      key: 'Monitoring InnoDB Latch',
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
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Memory</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Memory', helpPFSMemory)} />
            </HStack>
          ),
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
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Instruments</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Instruments', helpPFSInstruments)} />
            </HStack>
          ),
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
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Queries</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Queries', helpPFSQueries)} />
            </HStack>
          ),
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
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Queries Period (hours)</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Queries Period', helpPFSQueriesPeriod)} />
            </HStack>
          ),
          value: (
            <Flex className={styles.valueWithInfo}>
              <Text className={styles.info}>
                How often (in hours) the performance schema digest table is snapshotted and reset. Default: 1.
              </Text>
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
            </Flex>
          )
        },
        {
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Queries Explain</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Queries Explain', helpPFSExplain)} />
            </HStack>
          ),
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
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Queries Explain Delay (ms)</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Queries Explain Delay', helpPFSExplainDelay)} />
            </HStack>
          ),
          value: (
            <Flex className={styles.valueWithInfo}>
              <Text className={styles.info}>
                Milliseconds to sleep between consecutive EXPLAIN calls during a snapshot to spread optimizer load.
                Set to 0 to disable. Default: 200.
              </Text>
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
            </Flex>
          )
        },
        {
          key: (
            <HStack>
              <Text>Monitoring Performance Schema Queries Explain Purge Period (days)</Text>
              <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal('Monitoring Performance Schema Queries Explain Purge Period', helpPFSExplainPurge)} />
            </HStack>
          ),
          value: (
            <Flex className={styles.valueWithInfo}>
              <Text className={styles.info}>
                Age in days after which a cached explain plan is evicted from memory and disk.
                Set to 0 to keep plans forever. Default: 30.
              </Text>
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
            </Flex>
          )
        }
      ]
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} />
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
