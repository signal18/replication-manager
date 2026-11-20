import PropTypes from 'prop-types'
import { useSelector } from 'react-redux'
import LogsTabs from '../../../../components/LogsTabs'
import { clusterHealthColor, securityColor, workloadColor, schemaColor, configColor, JOBS_COLOR } from '../../../../utility/domainPills'
import { GeneralLogs, TaskLogs, SecurityLogs, WorkloadLogs, DDLLogs, SchemaLogs, VariableChangeLogs } from '../Logs'

// The cluster's logs in one place: one tab per domain log, with the titles and in the
// order of the sections they replace, Cluster Logs (the HA log) first and the default.
// Each tab reuses the component that rendered its log as a separate section, fed by the
// one call the cluster tick already makes (getClusterLogs, /topology/http-logs), so no
// tab adds a request; the shared LogsTabs mounts only the open tab. See
// doc/implementation/ui-components/CLUSTER_LOGS_TABS.md.
// key: the log type in state.cluster.clusterLogs (the API's logType); settings: the
// openSettings entry its gear opens; pill: the colour of the count badge, the one of the
// header pill of the log's domain (utility/domainPills): HA the Cluster health pill, Jobs
// the running-backup pill, DDL changes the Schema pill, variable changes the Config pill.
const CLUSTER_LOG_TABS = [
  {
    key: 'general',
    title: 'Cluster Logs',
    description: 'The cluster log: monitoring, failover, switchover, rejoin and topology events.',
    settings: 'logs',
    settingsTooltip: 'Log Settings',
    pill: ({ clusterAlerts }) => ({ colorScheme: clusterHealthColor(clusterAlerts?.errors?.length || 0, clusterAlerts?.warnings?.length || 0) }),
    Component: GeneralLogs
  },
  {
    key: 'task',
    title: 'Job Logs',
    description: 'Job logs: backups, restores, reseeds and the other database jobs of the scheduler.',
    settings: 'scheduler',
    settingsTooltip: 'Scheduler Settings',
    pill: () => ({ colorScheme: JOBS_COLOR }),
    Component: TaskLogs
  },
  {
    key: 'security',
    title: 'Security Logs',
    description:
      'Security compliance findings from audit plugins (SEC0xxx): empty passwords, weak auth, no SSL, missing audit plugin, etc. Also includes API login failures and account lockouts. Does not appear in the main Cluster Logs.',
    settings: 'plugins',
    settingsTooltip: 'Plugin Settings',
    pill: ({ clusterData }) => ({ colorScheme: securityColor(clusterData) }),
    Component: SecurityLogs
  },
  {
    key: 'workload',
    title: 'Workload Logs',
    description:
      'Performance spike detections based on Graphite time-series analysis: slow query regressions, connection storms, tmp-table storms, full table scan spikes, metadata lock contention, replication lag predictions, error storms. Does not appear in the main Cluster Logs.',
    settings: 'plugins',
    settingsTooltip: 'Plugin Settings',
    pill: ({ clusterData }) => ({ colorScheme: workloadColor(clusterData) }),
    Component: WorkloadLogs
  },
  {
    key: 'ddl',
    title: 'DDL Change Logs',
    description:
      'Schema changes detected by the monitoring loop: CREATE TABLE, ALTER TABLE (column additions, modifications, drops), DROP TABLE. Each entry includes the column diff showing before/after definitions.',
    settings: 'monitoring',
    settingsTooltip: 'Monitoring Settings',
    pill: ({ clusterData }) => ({ colorScheme: schemaColor(clusterData), shade: 600 }),
    Component: DDLLogs
  },
  {
    key: 'schema',
    title: 'Schema Logs',
    description:
      'Schema advisory findings from schema plugins (SCH0xxx): row-size risks, LOB compression, engine/row-format tags. Does not appear in the main Cluster Logs.',
    settings: 'plugins',
    settingsTooltip: 'Plugin Settings',
    pill: ({ clusterData }) => ({ colorScheme: schemaColor(clusterData), shade: 600 }),
    Component: SchemaLogs
  },
  {
    key: 'variable-change',
    title: 'Variable Change Logs',
    description:
      'Runtime variable changes detected on any server (SET GLOBAL). Shows before/after values. Requires monitoring-variable-change = true.',
    settings: 'monitoring',
    settingsTooltip: 'Monitoring Settings',
    // blinks on a config ERROR state, like the Config pill of the header
    pill: ({ clusterData }) => ({ colorScheme: configColor(clusterData), shade: 600, blink: configColor(clusterData) === 'red' }),
    Component: VariableChangeLogs
  }
]

function ClusterLogsTabs({ openSettings = {}, className }) {
  const clusterLogs = useSelector((state) => state.cluster.clusterLogs)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const clusterAlerts = useSelector((state) => state.cluster.clusterAlerts)
  return (
    <LogsTabs
      tabs={CLUSTER_LOG_TABS}
      logs={clusterLogs}
      pillContext={{ clusterData, clusterAlerts }}
      storageKey='clusterLogsTab'
      openSettings={openSettings}
      className={className}
    />
  )
}

ClusterLogsTabs.propTypes = {
  // the gear of each tab: logs, scheduler, plugins, monitoring (Home's openSettings)
  openSettings: PropTypes.object,
  className: PropTypes.string
}

export default ClusterLogsTabs
