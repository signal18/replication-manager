import { useDispatch, useSelector } from 'react-redux'
import {
  FormControl,
  FormLabel,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  NumberInput,
  NumberInputField,
  Radio,
  RadioGroup,
  Select,
  Stack,
  Switch,
  Text
} from '@chakra-ui/react'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import RMButton from '../../../../components/RMButton'
import {
  dropServer,
  flushLogs,
  jobsUpgrade,
  logicalBackup,
  optimizeServer,
  physicalBackupMaster,
  pitrRestore,
  promoteToLeader,
  provisionDatabase,
  reseedLogicalFromBackup,
  reseedLogicalFromMaster,
  reseedPhysicalFromBackup,
  resetMaster,
  resetSlaveAll,
  runRemoteJobs,
  setAsIgnored,
  setAsPreferred,
  setAsUnrated,
  setMaintenanceMode,
  skipReplicationEvent,
  startDatabase,
  restartDatabase,
  startSlave,
  stopDatabase,
  stopSlave,
  toggleInnodbMonitor,
  toggleReadOnly,
  toggleSlowQueryCapture,
  unprovisionDatabase
} from '../../../../redux/clusterSlice'
import { useState, useEffect, useCallback } from 'react'
import { useHref } from 'react-router-dom'
import { generateConfig } from '../../../../redux/configSlice'
import { useTheme } from '../../../../ThemeProvider'
import parentStyles from '../../../../components/Modals/styles.module.scss'

// Constants
const JOBS_CONTAINER_RID = 'container#jobs'

/**
 * ServerMenu - Context menu for database server operations
 * 
 * Provides a hierarchical menu of database operations organized by category (Maintenance, 
 * Backup, Provision, DB Utils). All operations are permission-based and require user confirmation
 * for destructive actions.
 * 
 * Configuration-Based Database Management:
 * - Uses configuration files (01_preserved.cnf, 02_delta.cnf, 03_agreed.cnf) for database startup
 * - 01_preserved.cnf: User-accepted differences (highest precedence)
 * - 02_delta.cnf: Detected differences between deployed and config
 * - 03_agreed.cnf: Variables that should match between systems
 * 
 * @param {Object} props - Component properties
 * @param {string} props.clusterName - Name of the database cluster
 * @param {string} props.clusterMasterId - ID of the master server in the cluster
 * @param {string} props.backupPhysicalType - Physical backup type (e.g., 'xtrabackup', 'mariabackup')
 * @param {string} props.backupLogicalType - Logical backup type (e.g., 'mysqldump', 'mydumper')
 * @param {boolean} props.backupRestic - Whether restic backups are enabled
 * @param {string} props.orchestrator - Orchestrator type (e.g., 'opensvc', 'kubernetes')
 * @param {Object} props.row - Server data object
 * @param {string} props.row.id - Server ID
 * @param {string} props.row.host - Server hostname/IP
 * @param {number} props.row.port - Server port
 * @param {boolean} props.row.isSlave - Whether server is a slave
 * @param {boolean} props.row.prefered - Whether server is marked as preferred for failover
 * @param {boolean} props.row.preferedBackup - Whether server is the preferred backup server
 * @param {boolean} props.row.ignored - Whether server is ignored for failover
 * @param {Object} props.user - User object with permission grants
 * @param {Object} props.user.grants - User permission grants object
 * @param {boolean} props.isDesktop - Whether rendering on desktop (affects menu placement)
 * @param {string} [props.from='tableView'] - Origin context ('tableView' or other)
 * @param {Function} props.openCompareModal - Callback to open server comparison modal
 * @param {string} [props.colorScheme] - Color scheme for the menu
 * @param {string} [props.className] - Additional CSS classes
 * @param {boolean} [props.showCompareWithOption=true] - Whether to show compare option
 * @param {boolean} [props.showTerminal=false] - Whether to show terminal options
 * 
 * @returns {JSX.Element} ServerMenu component with context menu and confirmation modal
 */
function ServerMenu({
  clusterName,
  clusterMasterId,
  backupPhysicalType,
  backupLogicalType,
  backupRestic,
  orchestrator,
  row,
  user,
  isDesktop,
  from = 'tableView',
  openCompareModal,
  colorScheme,
  className,
  showCompareWithOption = true,
  showTerminal = false
}) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const backupsList = useSelector((state) => state.cluster?.backups?.list)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [serverName, setServerName] = useState('')
  const [isAdvancedBackupOpen, setIsAdvancedBackupOpen] = useState(false)
  const [advancedBackupType, setAdvancedBackupType] = useState('logical')
  const [advancedBackupLine, setAdvancedBackupLine] = useState('default')
  const [advancedRetentionDays, setAdvancedRetentionDays] = useState(0)
  const [advancedResticEnabled, setAdvancedResticEnabled] = useState(Boolean(backupRestic))
  const [isAdvancedReseedOpen, setIsAdvancedReseedOpen] = useState(false)
  const [advancedReseedType, setAdvancedReseedType] = useState('logical')
  const [advancedReseedSource, setAdvancedReseedSource] = useState('backup')
  const [advancedReseedLine, setAdvancedReseedLine] = useState('default')
  const [isPitrOpen, setIsPitrOpen] = useState(false)
  const [pitrBackupId, setPitrBackupId] = useState('')
  const [pitrRestoreTime, setPitrRestoreTime] = useState('')
  const [pitrUseBinlog, setPitrUseBinlog] = useState(true)

  const getHref = useHref('/').replace(/\/+$/, '');

  const openTerminalPage = useCallback((clusterName, srvId, commandType = '') => {
    const terminalURL = getHref.concat(`/terminal/clusters/${clusterName}/servers/${srvId}/${commandType}`).replace(/\/+$/, '')
    window.open(terminalURL, '_blank')
  }, [getHref])

  useEffect(() => {
    if (row?.id) {
      setServerName(`server ${row.host}:${row.port} (${row.id})`)
    }
  }, [row])

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  const openAdvancedBackupModal = () => {
    const canUseDefaultLine = row?.preferedBackup || clusterMasterId === row?.id
    setAdvancedBackupType('logical')
    setAdvancedBackupLine(canUseDefaultLine ? 'default' : 'adhoc')
    setAdvancedRetentionDays(0)
    setAdvancedResticEnabled(Boolean(backupRestic))
    setIsAdvancedBackupOpen(true)
  }

  const closeAdvancedBackupModal = () => {
    setIsAdvancedBackupOpen(false)
  }

  const handleAdvancedBackup = () => {
    const options = { line: advancedBackupLine }
    if (advancedBackupLine === 'adhoc' && advancedRetentionDays > 0) {
      options.retentionDays = advancedRetentionDays
    }
    if (advancedBackupLine === 'adhoc' && backupRestic) {
      options.restic = advancedResticEnabled
    }

    if (advancedBackupType === 'physical') {
      dispatch(physicalBackupMaster({ clusterName, serverId: row.id, options }))
    } else {
      dispatch(logicalBackup({ clusterName, serverId: row.id, options }))
    }
    closeAdvancedBackupModal()
  }

  const openAdvancedReseedModal = () => {
    setAdvancedReseedType('logical')
    setAdvancedReseedSource('backup')
    setAdvancedReseedLine('default')
    setIsAdvancedReseedOpen(true)
  }

  const closeAdvancedReseedModal = () => {
    setIsAdvancedReseedOpen(false)
  }

  const handleAdvancedReseed = () => {
    const options = { line: advancedReseedLine }

    if (advancedReseedSource === 'master') {
      // Reseed from master (only for logical)
      dispatch(reseedLogicalFromMaster({ clusterName, serverId: row.id }))
    } else {
      // Reseed from backup
      if (advancedReseedType === 'physical') {
        dispatch(reseedPhysicalFromBackup({ clusterName, serverId: row.id, options }))
      } else {
        dispatch(reseedLogicalFromBackup({ clusterName, serverId: row.id, options }))
      }
    }
    closeAdvancedReseedModal()
  }

  const openPitrModal = () => {
    // Get the most recent backup ID if available
    if (backupsList && backupsList.length > 0) {
      const serverBackups = backupsList.filter(b => b.server_id === row.id)
      if (serverBackups.length > 0) {
        setPitrBackupId(serverBackups[0].id?.toString() || '')
      }
    }
    setPitrRestoreTime('')
    setPitrUseBinlog(true)
    setIsPitrOpen(true)
  }

  const closePitrModal = () => {
    setIsPitrOpen(false)
  }

  const handlePitrRestore = () => {
    const pitrData = {
      Backup: parseInt(pitrBackupId, 10) || 0,
      RestoreTime: pitrRestoreTime ? new Date(pitrRestoreTime).getTime() / 1000 : 0,
      UseBinlog: pitrUseBinlog
    }
    dispatch(pitrRestore({ clusterName, serverId: row.id, pitrData }))
    closePitrModal()
  }

  return (
    <>
      <MenuOptions
        className={className}
        colorScheme={colorScheme}
        placement={from === 'tableView' ? 'right-end' : 'left-end'}
        subMenuPlacement={isDesktop ? (from === 'tableView' ? 'right-end' : 'left-end') : 'bottom'}
        options={[
          ...(showCompareWithOption
            ? [
              {
                name: 'Compare With',
                onClick: () => openCompareModal(row)
              }
            ]
            : []),
          {
            name: 'Maintenance Mode',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm maintenance for ${serverName}?`)
              setConfirmHandler(() => () => dispatch(setMaintenanceMode({ clusterName, serverId: row.id })))
            }
          },
          ...(user?.grants['terminal-db'] && showTerminal ? [
            {
              name: 'Web Terminal',
              subMenu: [
                {
                  name: 'MySQL Terminal',
                  onClick: () => openTerminalPage(clusterName, row.id, 'mysql')
                },
                {
                  name: 'MyTop Terminal',
                  onClick: () => openTerminalPage(clusterName, row.id, 'mytop')
                },
                ...(user?.grants['terminal-global'] ? [
                  {
                    name: 'Shell Terminal',
                    onClick: () => openTerminalPage(clusterName, row.id)
                  }
                ] : []),
              ]
            }
          ] : []),
          ...(user?.grants['cluster-switchover'] && row.isSlave
            ? [
              {
                name: 'Promote To Leader',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm promotion for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(promoteToLeader({ clusterName, serverId: row.id })))
                }
              }
            ]
            : []),
          {
            name: 'Failover Candidate',
            subMenu: [
              ...(user?.grants['cluster-failover'] && !row.prefered && !row.ignored
                ? [
                  {
                    name: 'Set as Preferred',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm set as preferred for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(setAsPreferred({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Set as Ignored',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm set as ignored for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(setAsIgnored({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['cluster-failover'] && (row.prefered || row.ignored)
                ? [
                  {
                    name: 'Set as unrated',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm set as unrated for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(setAsUnrated({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : [])
            ]
          },
          {
            name: 'Backup',
            subMenu: [
              ...(clusterMasterId === row.id && user?.grants['db-backup']
                ? [
                  {
                    name: 'Physical Backup',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm master physical (${backupPhysicalType}) backup?`)
                      setConfirmHandler(() => () => dispatch(physicalBackupMaster({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Logical Backup',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm sending logical backup (${backupLogicalType}) for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(logicalBackup({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(clusterMasterId !== row.id && user?.grants['db-restore']
                ? [
                  {
                    name: 'Advanced Reseed',
                    onClick: () => openAdvancedReseedModal()
                  },
                  {
                    name: 'Point-In-Time Recovery',
                    onClick: () => openPitrModal()
                  }
                ]
                : []),
              ...(user?.grants['db-backup']
                ? [
                  {
                    name: 'Advanced Backup',
                    onClick: () => openAdvancedBackupModal()
                  },
                  {
                    name: 'Flush logs',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm flush logs for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(flushLogs({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              {
                name: 'Run Remote Jobs',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm running remote jobs for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(runRemoteJobs({ clusterName, serverId: row.id })))
                }
              }
            ]
          },
          {
            name: 'Provision',
            subMenu: [
              ...(user?.grants['db-maintenance']
                ? [
                  {
                    name: 'Jobs Upgrade',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm jobs upgrade for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(jobsUpgrade({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-stop']
                ? [
                  {
                    name: 'Stop Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm stop for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(stopDatabase({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-start']
                ? [
                  {
                    name: 'Start Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm start for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(startDatabase({ clusterName, serverId: row.id })))
                    }
                  },
                  ...(orchestrator === 'opensvc' ? [
                    {
                      name: 'Restart Jobs Container',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm restart jobs container for ${serverName}?`)
                        setConfirmHandler(() => () => dispatch(restartDatabase({ clusterName, serverId: row.id, rid: JOBS_CONTAINER_RID })))
                      }
                    }
                  ] : [])
                ]
                : []),
              ...(user?.grants['prov-db-provision']
                ? [
                  {
                    name: 'Provision Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm provision ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(provisionDatabase({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['prov-db-unprovision']
                ? [
                  {
                    name: 'Unprovision Database',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm unprovision for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(unprovisionDatabase({ clusterName, serverId: row.id })))
                    }
                  },
                ]
                : []),
              ...(user?.grants['db-config-flag']
                ? [
                  {
                    name: 'Refresh Variables and Generate Config',
                    onClick: () => {
                      setConfirmTitle(`Confirm generate db config for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(generateConfig({ clusterName, host: row.host, port: row.port })))
                      openConfirmModal()
                    }
                  }
                ]
                : []),
              {
                name: 'Remove Monitor',
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm removing monitor for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(dropServer({ clusterName, host: row.host, port: row.port })))
                }
              },
            ]
          },
          {
            name: 'DB Utils',
            subMenu: [
              ...(user?.grants['db-optimize']
                ? [
                  {
                    name: 'Optimize',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm optimize for ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(optimizeServer({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-replication']
                ? [
                  {
                    name: 'Skip 1 Replication Event',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm skip replication event for ${serverName}?`)
                      setConfirmHandler(
                        () => () => dispatch(skipReplicationEvent({ clusterName, serverId: row.id }))
                      )
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-logs']
                ? [
                  {
                    name: 'Toggle InnoDB Monitor',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm toggle innodb monitor ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(toggleInnodbMonitor({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),

              ...(user?.grants['db-capture']
                ? [
                  {
                    name: 'Toggle Slow Query Capture',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm toggle slow query capture ${serverName}?`)
                      setConfirmHandler(
                        () => () => dispatch(toggleSlowQueryCapture({ clusterName, serverId: row.id }))
                      )
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-replication']
                ? [
                  {
                    name: 'Start Slave',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm start slave on ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(startSlave({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Stop Slave',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm stop slave on ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(stopSlave({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Reset Master',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(
                        `Confirm reset master this may break replication when done on master, ${serverName}?`
                      )
                      setConfirmHandler(() => () => dispatch(resetMaster({ clusterName, serverId: row.id })))
                    }
                  },
                  {
                    name: 'Reset Slave',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm reset slave this will break replication on, ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(resetSlaveAll({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : []),
              ...(user?.grants['db-readonly']
                ? [
                  {
                    name: 'Toggle Readonly',
                    onClick: () => {
                      openConfirmModal()
                      setConfirmTitle(`Confirm toggle read only on ${serverName}?`)
                      setConfirmHandler(() => () => dispatch(toggleReadOnly({ clusterName, serverId: row.id })))
                    }
                  }
                ]
                : [])
            ]
          }
        ]}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}
      <Modal isOpen={isAdvancedBackupOpen} onClose={closeAdvancedBackupModal} size='lg'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader>Advanced Backup</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Stack spacing={4}>
              <FormControl>
                <FormLabel>Backup type</FormLabel>
                <RadioGroup value={advancedBackupType} onChange={setAdvancedBackupType}>
                  <HStack spacing={4}>
                    <Radio value='logical'>Logical ({backupLogicalType})</Radio>
                    <Radio value='physical'>Physical ({backupPhysicalType})</Radio>
                  </HStack>
                </RadioGroup>
              </FormControl>
              <FormControl>
                <FormLabel>Backup line</FormLabel>
                {(row?.preferedBackup || clusterMasterId === row?.id) ? (
                  <>
                    <RadioGroup value={advancedBackupLine} onChange={setAdvancedBackupLine}>
                      <HStack spacing={4}>
                        <Radio value='default'>Default line</Radio>
                        <Radio value='adhoc'>Ad-hoc</Radio>
                      </HStack>
                    </RadioGroup>
                    {advancedBackupLine === 'default' ? (
                      <Text fontSize='sm' color='gray.500'>
                        Default line backup replaces the previous default backup file.
                      </Text>
                    ) : (
                      <Text fontSize='sm' color='blue.500'>
                        Ad-hoc backups are stored independently with timestamped filenames and can use per-backup retention.
                      </Text>
                    )}
                  </>
                ) : (
                  <>
                    <RadioGroup value='adhoc' isDisabled>
                      <HStack spacing={4}>
                        <Radio value='adhoc' isChecked>Ad-hoc (forced)</Radio>
                      </HStack>
                    </RadioGroup>
                    <Text fontSize='sm' color='orange.500' mt={2}>
                      Only preferred backup server or master can use default line. Ad-hoc backups are stored independently with timestamped filenames.
                    </Text>
                  </>
                )}
              </FormControl>
              {advancedBackupLine === 'adhoc' && (
                <>
                  <FormControl>
                    <FormLabel>Retention days</FormLabel>
                    <NumberInput
                      min={0}
                      step={1}
                      precision={0}
                      value={advancedRetentionDays}
                      onChange={(_, valueNumber) => setAdvancedRetentionDays(Number.isNaN(valueNumber) ? 0 : valueNumber)}
                    >
                      <NumberInputField placeholder='0 = keep indefinitely' />
                    </NumberInput>
                  </FormControl>
                  {backupRestic && (
                    <FormControl display='flex' alignItems='center' justifyContent='space-between'>
                      <FormLabel mb='0'>Backup to Restic</FormLabel>
                      <Switch isChecked={advancedResticEnabled} onChange={(event) => setAdvancedResticEnabled(event.target.checked)} />
                    </FormControl>
                  )}
                </>
              )}
            </Stack>
          </ModalBody>
          <ModalFooter gap={3}>
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeAdvancedBackupModal}>
              Cancel
            </RMButton>
            <RMButton colorScheme='blue' size='medium' onClick={handleAdvancedBackup}>
              Start Backup
            </RMButton>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal isOpen={isAdvancedReseedOpen} onClose={closeAdvancedReseedModal} size='lg'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader>Advanced Reseed</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Stack spacing={4}>
              <FormControl>
                <FormLabel>Reseed type</FormLabel>
                <RadioGroup value={advancedReseedType} onChange={(value) => {
                  setAdvancedReseedType(value)
                  // Reset source to backup when switching to physical (physical only supports backup source)
                  if (value === 'physical') {
                    setAdvancedReseedSource('backup')
                  }
                }}>
                  <HStack spacing={4}>
                    <Radio value='logical'>Logical ({backupLogicalType})</Radio>
                    <Radio value='physical'>Physical ({backupPhysicalType})</Radio>
                  </HStack>
                </RadioGroup>
              </FormControl>
              <FormControl>
                <FormLabel>Reseed source</FormLabel>
                <RadioGroup value={advancedReseedSource} onChange={setAdvancedReseedSource}>
                  <Stack spacing={2}>
                    <Radio value='backup'>From Backup</Radio>
                    <Radio value='master' isDisabled={advancedReseedType === 'physical'}>
                      From Master {advancedReseedType === 'physical' && '(not available for physical)'}
                    </Radio>
                  </Stack>
                </RadioGroup>
                {advancedReseedSource === 'backup' && (
                  <Text fontSize='sm' color='gray.500' mt={2}>
                    Restore from a previously created backup.
                  </Text>
                )}
                {advancedReseedSource === 'master' && (
                  <Text fontSize='sm' color='gray.500' mt={2}>
                    Create a fresh dump from the current master and restore it.
                  </Text>
                )}
              </FormControl>
              {advancedReseedSource === 'backup' && (
                <FormControl>
                  <FormLabel>Backup line</FormLabel>
                  <RadioGroup value={advancedReseedLine} onChange={setAdvancedReseedLine}>
                    <HStack spacing={4}>
                      <Radio value='default'>Default line</Radio>
                      <Radio value='adhoc'>Ad-hoc</Radio>
                    </HStack>
                  </RadioGroup>
                  {advancedReseedLine === 'default' ? (
                    <Text fontSize='sm' color='gray.500' mt={2}>
                      Restore from the default backup line (latest default backup).
                    </Text>
                  ) : (
                    <Text fontSize='sm' color='blue.500' mt={2}>
                      Restore from the most recent ad-hoc backup.
                    </Text>
                  )}
                </FormControl>
              )}
            </Stack>
          </ModalBody>
          <ModalFooter gap={3}>
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closeAdvancedReseedModal}>
              Cancel
            </RMButton>
            <RMButton colorScheme='blue' size='medium' onClick={handleAdvancedReseed}>
              Start Reseed
            </RMButton>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal isOpen={isPitrOpen} onClose={closePitrModal} size='lg'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader>Point-In-Time Recovery (PITR)</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <Stack spacing={4}>
              <FormControl isRequired>
                <FormLabel>Select Backup</FormLabel>
                <Select 
                  placeholder='Select a backup to restore from' 
                  value={pitrBackupId}
                  onChange={(e) => setPitrBackupId(e.target.value)}
                >
                  {backupsList && backupsList
                    .filter(backup => backup.server_id === row.id)
                    .map(backup => (
                      <option key={backup.id} value={backup.id}>
                        {backup.backup_tool} - {new Date(backup.start_time * 1000).toLocaleString()} 
                        {backup.backup_line ? ` (${backup.backup_line})` : ''}
                      </option>
                    ))
                  }
                </Select>
                <Text fontSize='sm' color='gray.500' mt={2}>
                  Select the backup to use as the base for point-in-time recovery.
                </Text>
              </FormControl>
              <FormControl>
                <FormLabel>Restore to Time (optional)</FormLabel>
                <Input
                  type='datetime-local'
                  value={pitrRestoreTime}
                  onChange={(e) => setPitrRestoreTime(e.target.value)}
                  placeholder='Leave empty to restore to latest'
                />
                <Text fontSize='sm' color='gray.500' mt={2}>
                  Specify a point in time to restore to. Leave empty to restore to the latest point available.
                </Text>
              </FormControl>
              <FormControl display='flex' alignItems='center' justifyContent='space-between'>
                <FormLabel mb='0'>Use Binary Logs</FormLabel>
                <Switch 
                  isChecked={pitrUseBinlog} 
                  onChange={(e) => setPitrUseBinlog(e.target.checked)} 
                />
              </FormControl>
              <Text fontSize='sm' color='blue.500'>
                PITR will restore the selected backup and optionally apply binary logs up to the specified point in time.
              </Text>
            </Stack>
          </ModalBody>
          <ModalFooter gap={3}>
            <RMButton variant='outline' colorScheme='white' size='medium' onClick={closePitrModal}>
              Cancel
            </RMButton>
            <RMButton 
              colorScheme='blue' 
              size='medium' 
              onClick={handlePitrRestore}
              isDisabled={!pitrBackupId}
            >
              Start PITR
            </RMButton>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}

export default ServerMenu
