import { useState } from 'react'
import PropTypes from 'prop-types'
import { useSelector } from 'react-redux'
import { HStack, Tooltip, Box } from '@chakra-ui/react'
import { HiCog } from 'react-icons/hi'
import AccordionComponent from '../../../../components/AccordionComponent/index.jsx'
import TabItems from '../../../../components/TabItems'
import { CountPill } from '../../../../components/AlertBadge'
import { clusterHealthColor, securityColor, workloadColor, schemaColor, configColor, JOBS_COLOR } from '../../../../utility/domainPills'
import RMIconButton from '../../../../components/RMIconButton'
import { GeneralLogs, TaskLogs, SecurityLogs, WorkloadLogs, DDLLogs, SchemaLogs, VariableChangeLogs } from '../Logs'
import styles from './styles.module.scss'

// The cluster's logs in one place: one tab per domain log, with the titles and in the
// order of the sections they replace, Cluster Logs (the HA log) first and the default.
// Each tab reuses the component that rendered its log as a separate section, fed by the
// one call the cluster tick already makes (getClusterLogs, /topology/http-logs), so no
// tab adds a request; TabItems mounts only the open tab. See
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
    pill: ({ clusterData }) => ({ colorScheme: configColor(clusterData), shade: 600 }),
    Component: VariableChangeLogs
  }
]

const STORAGE_KEY = 'clusterLogsTab'

function readStoredTab() {
  try {
    const index = CLUSTER_LOG_TABS.findIndex((tab) => tab.key === localStorage.getItem(STORAGE_KEY))
    return index >= 0 ? index : 0
  } catch {
    return 0
  }
}

// a tab label: the log's name, a "?" tooltip explaining its content, and the number of
// entries its buffer holds when it has any. Divs, not spans: the global stylesheet forces
// every span to --text-color, which would not follow the selected tab's colour.
function LogTabLabel({ title, description, count, pill }) {
  return (
    <HStack spacing={2}>
      <Box>{title}</Box>
      {count > 0 && (
        <Tooltip label='Entries in the live buffer (not counting older history or filters)' placement='top' hasArrow>
          <Box>
            <CountPill count={count} colorScheme={pill.colorScheme} shade={pill.shade} />
          </Box>
        </Tooltip>
      )}
      <Tooltip label={description} placement='top' hasArrow>
        <Box className={styles.help}>?</Box>
      </Tooltip>
    </HStack>
  )
}

function ClusterLogsTabs({ openSettings = {}, className }) {
  const clusterLogs = useSelector((state) => state.cluster.clusterLogs)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const clusterAlerts = useSelector((state) => state.cluster.clusterAlerts)
  const [selectedTab, setSelectedTabState] = useState(readStoredTab)
  const setSelectedTab = (index) => {
    setSelectedTabState(index)
    try {
      localStorage.setItem(STORAGE_KEY, CLUSTER_LOG_TABS[index].key)
    } catch {
      // the tab is only not remembered
    }
  }

  const current = CLUSTER_LOG_TABS[selectedTab]
  const onOpenSettings = openSettings[current.settings]

  return (
    <AccordionComponent
      className={className}
      heading={'Logs'}
      headerActions={
        onOpenSettings ? (
          <RMIconButton icon={HiCog} tooltip={current.settingsTooltip} onClick={onOpenSettings} size='xs' variant='ghost' />
        ) : null
      }
      body={
        <TabItems
          className={styles.fullWidthTabs}
          options={CLUSTER_LOG_TABS.map((tab) => (
            <LogTabLabel
              key={tab.key}
              title={tab.title}
              description={tab.description}
              count={clusterLogs?.[tab.key]?.buffer?.length || 0}
              pill={tab.pill({ clusterData, clusterAlerts })}
            />
          ))}
          tabIndex={selectedTab}
          onChange={setSelectedTab}
          tabContents={CLUSTER_LOG_TABS.map(({ key, Component }) => (
            <Component key={key} />
          ))}
        />
      }
    />
  )
}

LogTabLabel.propTypes = {
  title: PropTypes.string.isRequired,
  description: PropTypes.string,
  count: PropTypes.number,
  pill: PropTypes.shape({ colorScheme: PropTypes.string, shade: PropTypes.number })
}

ClusterLogsTabs.propTypes = {
  // the gear of each tab: logs, scheduler, plugins, monitoring (Home's openSettings)
  openSettings: PropTypes.object,
  className: PropTypes.string
}

export default ClusterLogsTabs
