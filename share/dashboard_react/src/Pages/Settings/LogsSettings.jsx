import { Box, HStack, Stack, Text } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TableType2 from '../../components/TableType2'
import RMSwitch from '../../components/RMSwitch'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { HiChevronDown, HiChevronUp } from 'react-icons/hi'
import clsx from 'clsx'

const LOG_LEVEL_OPTIONS = [
  { value: 0, label: 'Off' },
  { value: 1, label: 'Error' },
  { value: 2, label: 'Warn' },
  { value: 3, label: 'Info' },
  { value: 4, label: 'Debug' }
]

const VERBOSE_LOG_LEVEL_OPTIONS = LOG_LEVEL_OPTIONS.filter((item) => item.value > 0)

const PROXY_LOG_SETTINGS = [
  { label: 'Log Proxy', configKey: 'logProxyLevel', settingKey: 'log-level-proxy' },
  { label: 'Log HAProxy', configKey: 'haproxyLogLevel', settingKey: 'log-level-haproxy' },
  { label: 'Log ProxySQL', configKey: 'proxysqlLogLevel', settingKey: 'log-level-proxysql' },
  { label: 'Log Proxy Janitor', configKey: 'proxyjanitorLogLevel', settingKey: 'log-level-proxyjanitor' },
  { label: 'Log Maxscale', configKey: 'maxscaleLogLevel', settingKey: 'log-level-maxscale' }
]

const INTERNAL_OVERRIDE_GROUPS = [
  {
    id: 'cluster-orchestration',
    title: 'Cluster & Orchestration',
    description: 'Controls orchestration, topology, and cluster lifecycle logging.',
    items: [
      { label: 'Log Orchestrator', configKey: 'logOrchestratorLevel', settingKey: 'log-level-orchestrator' },
      { label: 'Log Topology Detection', configKey: 'logTopologyLevel', settingKey: 'log-level-topology' },
      { label: 'Log writer election', configKey: 'logWriterElectionLevel', settingKey: 'log-level-writer-election' },
      { label: 'Log HeartBeat', configKey: 'logHeartbeatLevel', settingKey: 'log-level-heartbeat' },
      { label: 'Log Config Load', configKey: 'logConfigLoadLevel', settingKey: 'log-level-config-load' },
      { label: 'Log DB Jobs', configKey: 'logTaskLevel', settingKey: 'log-level-task' },
      { label: 'Log SST', configKey: 'logSstLevel', settingKey: 'log-level-sst' }
    ]
  },
  {
    id: 'backup-restore',
    title: 'Backup & Restore',
    description: 'Adjust logging for backup, restore, and archive workflows.',
    items: [
      { label: 'Log Backup Stream', configKey: 'logBackupStreamLevel', settingKey: 'log-level-backup-stream' },
      { label: 'Log Restic', configKey: 'logResticLevel', settingKey: 'log-level-restic' },
      { label: 'Log Binlog Purge', configKey: 'logBinlogPurgeLevel', settingKey: 'log-level-binlog-purge' }
    ]
  },
  {
    id: 'database-log-collection',
    title: 'Database Log Collection',
    description: 'Fine-tune collection of database-side logs and optimization output.',
    items: [
      { label: 'Log Fetch Auditlog Level', configKey: 'logLevelDatabaseAudit', settingKey: 'log-level-database-audit' },
      { label: 'Log Fetch Errorlog Level', configKey: 'logLevelDatabaseErrors', settingKey: 'log-level-database-errors' },
      {
        label: 'Log Fetch SQL Error log Level',
        configKey: 'logLevelDatabaseSqlErrors',
        settingKey: 'log-level-database-sql-errors'
      },
      { label: 'Log Fetch Slowquery Level', configKey: 'logLevelDatabaseSlowquery', settingKey: 'log-level-database-slowquery' },
      { label: 'Log DB Optimize Level', configKey: 'logLevelDatabaseOptimize', settingKey: 'log-level-database-optimize' }
    ]
  },
  {
    id: 'external-integrations',
    title: 'External Integrations',
    description: 'Logging for integrations, notifications, and external scripts.',
    items: [
      { label: 'Log Vault', configKey: 'logVaultLevel', settingKey: 'log-level-vault' },
      { label: 'Log Graphite', configKey: 'logGraphiteLevel', settingKey: 'log-level-graphite' },
      { label: 'Log Mailer Level', configKey: 'logMailerLevel', settingKey: 'log-level-mailer' },
      { label: 'Log Support Level', configKey: 'logSupportLevel', settingKey: 'log-level-support' },
      {
        label: 'Log External Script Level',
        configKey: 'logExternalScriptLevel',
        settingKey: 'log-level-external-script'
      }
    ]
  }
]

const INTERNAL_SECTION_IDS = INTERNAL_OVERRIDE_GROUPS.map((group) => group.id)

const LEVEL_LABEL_BY_VALUE = LOG_LEVEL_OPTIONS.reduce((acc, item) => {
  acc[item.value] = item.label
  return acc
}, {})

const normalizeLogLevel = (value) => {
  const parsedValue = Number(value)
  if (Number.isNaN(parsedValue)) {
    return 0
  }
  return Math.min(4, Math.max(0, Math.round(parsedValue)))
}

function ConfirmableLogLevelControl({
  value,
  confirmTitle,
  onConfirm,
  isDisabled,
  compact = false
}) {
  const [currentValue, setCurrentValue] = useState(normalizeLogLevel(value))
  const [pendingValue, setPendingValue] = useState(null)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    setCurrentValue(normalizeLogLevel(value))
    if (!isConfirmModalOpen) {
      setPendingValue(null)
    }
  }, [value, isConfirmModalOpen])

  const selectedValue = pendingValue ?? currentValue

  const requestChange = (nextValue) => {
    if (nextValue === currentValue || isDisabled) {
      return
    }
    setPendingValue(nextValue)
    setIsConfirmModalOpen(true)
  }

  const closeModal = () => {
    setPendingValue(null)
    setIsConfirmModalOpen(false)
  }

  const confirmChange = () => {
    if (pendingValue === null || pendingValue === currentValue) {
      closeModal()
      return
    }

    setCurrentValue(pendingValue)
    onConfirm(pendingValue)
    closeModal()
  }

  return (
    <>
      <HStack spacing={2} align='center' className={compact ? undefined : styles.logLevelInlineControl}>
        <Box className={clsx(styles.logLevelControl, compact && styles.logLevelControlCompact)}>
          <button
            type='button'
            className={`${styles.logLevelOffButton} ${selectedValue === 0 ? styles.logLevelOffButtonSelected : ''}`}
            onClick={() => requestChange(0)}
            disabled={isDisabled}
            aria-pressed={selectedValue === 0}>
            Off
          </button>

          <Box className={styles.logLevelGauge}>
            {VERBOSE_LOG_LEVEL_OPTIONS.map((option) => {
              const isSelected = selectedValue === option.value
              const isFilled = selectedValue !== 0 && selectedValue >= option.value
              const gaugeButtonClassName = clsx(
                styles.logLevelGaugeButton,
                styles[`logLevelGaugeButtonLevel${option.value}`],
                isFilled ? styles[`logLevelGaugeButtonFilled${option.value}`] : styles.logLevelGaugeButtonUnfilled,
                compact && styles.logLevelGaugeButtonCompact,
                isSelected && styles.logLevelGaugeButtonSelected,
                isSelected && styles[`logLevelGaugeButtonSelectedLevel${option.value}`]
              )

              return (
                <button
                  key={option.value}
                  type='button'
                  className={gaugeButtonClassName}
                  onClick={() => requestChange(option.value)}
                  disabled={isDisabled}
                  aria-pressed={isSelected}>
                  {option.label}
                </button>
              )
            })}
          </Box>
        </Box>
        {!compact && <Text className={styles.logLevelCurrentLabel}>Current: {LEVEL_LABEL_BY_VALUE[currentValue]}</Text>}
      </HStack>

      {isConfirmModalOpen && (
        <ConfirmModal
          closeModal={closeModal}
          title={`${confirmTitle.trimEnd()} ${LEVEL_LABEL_BY_VALUE[pendingValue]}`}
          onConfirmClick={confirmChange}
        />
      )}
    </>
  )
}

function CollapsibleLogSection({
  title,
  description,
  isOpen,
  onToggle,
  rows,
  controlsId
}) {
  const headerId = `${controlsId}-header`

  return (
    <Box className={`${styles.panel} ${styles.logsPanel}`} w='full'>
      <HStack
        as='button'
        id={headerId}
        type='button'
        spacing={2}
        onClick={onToggle}
        aria-label={`Toggle ${title}`}
        aria-expanded={isOpen}
        aria-controls={controlsId}
        className={`${styles.panelHeader} ${styles.logsPanelHeader}`}>
        <Stack spacing={1} className={styles.panelHeaderContent}>
          <Text className={styles.panelTitle}>{title}</Text>
          {description && <Text className={styles.panelDescription}>{description}</Text>}
        </Stack>
        <Box className={styles.panelChevron}>{isOpen ? <HiChevronUp /> : <HiChevronDown />}</Box>
      </HStack>

      <Box
        id={controlsId}
        role='region'
        aria-labelledby={headerId}
        className={`${styles.panelBody} ${styles.logsPanelBody}`}
        display={isOpen ? 'block' : 'none'}>
        <TableType2
          dataArray={rows.map((row) => ({ key: row.label, value: row.control }))}
          className={`${styles.table} ${styles.logCompactTable} ${styles.logsInnerTable}`}
          labelClassName={styles.logsInnerLabel}
          valueClassName={styles.logsInnerValue}
          rowClassName={styles.logsInnerRow}
        />
      </Box>
    </Box>
  )
}

function LogsSettings({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const isSettingsDisabled = user?.grants['cluster-settings'] === false
  const [sectionOpenState, setSectionOpenState] = useState(() => {
    const initialState = {}
    INTERNAL_OVERRIDE_GROUPS.forEach((group) => {
      initialState[group.id] = false
    })
    initialState['proxy-overrides'] = false
    return initialState
  })

  const buildSetSettingHandler = (setting, clusterName) => (value) =>
    dispatch(
      setSetting({
        clusterName,
        setting,
        value
      })
    )

  const topLevelRows = [
    {
      label: 'Verbose Mode',
      description: 'Overrides all scoped log settings and prints all log levels while enabled.',
      control: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for verbose?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'verbose' }))}
          isDisabled={isSettingsDisabled}
          isChecked={selectedCluster?.config?.verbose}
        />
      )
    },
    {
      label: 'Log to SysLog',
      description: 'Forward replication-manager logs to your system logger.',
      control: (
        <RMSwitch
          confirmTitle={'Confirm switch settings for log-syslog?'}
          onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'log-syslog' }))}
          isDisabled={isSettingsDisabled}
          isChecked={selectedCluster?.config?.logSyslog}
        />
      )
    },
    {
      label: 'Log SQL in Monitoring',
      description: 'Include monitored SQL statements in logs.',
      control: (
        <ConfirmableLogLevelControl
          value={selectedCluster?.config?.logSqlLevel}
          confirmTitle={`Confirm change 'log-level-sql' to:`}
          isDisabled={isSettingsDisabled}
          onConfirm={buildSetSettingHandler('log-level-sql', selectedCluster?.name)}
        />
      )
    },
    {
      label: 'Log Level',
      description: 'General log scope for cluster-wide logging behavior.',
      control: (
        <ConfirmableLogLevelControl
          value={selectedCluster?.config?.logLevel}
          confirmTitle={`Confirm change 'log-level' to:`}
          isDisabled={isSettingsDisabled}
          onConfirm={buildSetSettingHandler('log-level', selectedCluster?.name)}
        />
      )
    }
  ]

  const mapSettingsToRows = (items) => items.map((item) => ({
    label: item.label,
    control: (
      <ConfirmableLogLevelControl
        compact={true}
        value={selectedCluster?.config?.[item.configKey]}
        confirmTitle={`Confirm change '${item.settingKey}' to:`}
        isDisabled={isSettingsDisabled}
        onConfirm={buildSetSettingHandler(item.settingKey, selectedCluster?.name)}
      />
    )
  }))

  const toggleSection = (sectionId) => {
    setSectionOpenState((prev) => ({
      ...prev,
      [sectionId]: !prev[sectionId]
    }))
  }

  const setAllInternalSections = (isOpen) => {
    setSectionOpenState((prev) => {
      const nextState = { ...prev }
      INTERNAL_SECTION_IDS.forEach((sectionId) => {
        nextState[sectionId] = isOpen
      })
      return nextState
    })
  }

  const defaultLoggingContent = (
    <Stack spacing={2} className={styles.logsSectionContent}>
      <TableType2
        dataArray={topLevelRows.map((row) => ({
          key: (
            <Stack spacing={0} className={styles.settingLabelGroup}>
              <Text className={styles.settingLabel}>{row.label}</Text>
              <Text className={styles.settingDescription}>{row.description}</Text>
            </Stack>
          ),
          value: row.control
        }))}
        className={`${styles.table} ${styles.logCompactTable} ${styles.logsInnerTable}`}
        labelClassName={styles.logsInnerLabel}
        valueClassName={styles.logsInnerValue}
        rowClassName={styles.logsInnerRow}
      />
    </Stack>
  )

  const internalOverridesContent = (
    <Stack spacing={3} className={styles.logsSectionContent}>
      <HStack justify='space-between' className={styles.sectionHeader} flexWrap='wrap'>
        <HStack spacing={2}>
          <button type='button' className={styles.sectionToggle} onClick={() => setAllInternalSections(true)}>
            Show all
          </button>
          <button type='button' className={styles.sectionToggle} onClick={() => setAllInternalSections(false)}>
            Hide all
          </button>
        </HStack>
      </HStack>

      <Stack spacing={3}>
        {INTERNAL_OVERRIDE_GROUPS.map((group) => (
          <CollapsibleLogSection
            key={group.id}
            title={group.title}
            description={group.description}
            isOpen={sectionOpenState[group.id]}
            onToggle={() => toggleSection(group.id)}
            rows={mapSettingsToRows(group.items)}
            controlsId={`log-settings-${group.id}`}
          />
        ))}

      </Stack>
    </Stack>
  )

  const proxyOverridesContent = (
    <Stack spacing={3} className={styles.logsSectionContent}>
      <CollapsibleLogSection
        title='Proxy Types'
        isOpen={sectionOpenState['proxy-overrides']}
        onToggle={() => toggleSection('proxy-overrides')}
        rows={mapSettingsToRows(PROXY_LOG_SETTINGS)}
        controlsId='log-settings-proxy-overrides'
      />
    </Stack>
  )

  const tableSections = [
    { key: 'General Log Level', value: defaultLoggingContent },
    { key: 'Internal Log Scopes', value: internalOverridesContent },
    { key: 'Proxy Log Scopes', value: proxyOverridesContent }
  ]

  return (
    <Stack spacing={3} w='full' className={styles.logsPage}>
      <TableType2
        dataArray={tableSections}
        className={`${styles.table} ${styles.logsOuterTable}`}
        labelClassName={styles.logsOuterLabel}
        valueClassName={styles.logsOuterValue}
        rowClassName={styles.logsOuterRow}
      />
    </Stack>
  )
}

export default LogsSettings
