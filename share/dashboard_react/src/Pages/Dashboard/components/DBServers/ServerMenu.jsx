import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import AdvancedReseedModal from '../../../../components/Modals/AdvancedReseedModal'
import LostEventsModal from '../../../../components/Modals/LostEventsModal'
import {
  abortDatabase,
  clearDatabase,
  dropServer,
  flushLogs,
  jobsUpgrade,
  logicalBackup,
  optimizeServer,
  physicalBackupMaster,
  promoteToLeader,
  provisionDatabase,
  updateOpensvcTemplate,
  reseedLogicalFromBackup,
  reseedLogicalFromMaster,
  reseedPhysicalFromBackup,
  reseedFromResticSnapshot,
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
  upgradeDatabase,
  startSlave,
  stopDatabase,
  stopSlave,
  toggleInnodbMonitor,
  toggleReadOnly,
  toggleSlowQueryCapture,
  unprovisionDatabase
} from '../../../../redux/clusterSlice'
import { useState, useCallback, useMemo } from 'react'
import { useHref } from 'react-router-dom'
import { generateConfig } from '../../../../redux/configSlice'
import { OpenSVCTerminalRID } from '../../../Terminal/ridUtils'

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
 * @param {boolean} props.backupRestic - Whether restic backup is enabled
 * @param {string} props.orchestrator - Orchestrator type (e.g., 'opensvc', 'kubernetes')
 * @param {Object} props.row - Server data object
 * @param {string} props.row.id - Server ID
 * @param {string} props.row.host - Server hostname/IP
 * @param {number} props.row.port - Server port
 * @param {boolean} props.row.isSlave - Whether server is a slave
 * @param {boolean} props.row.prefered - Whether server is marked as preferred for failover
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
  backupRestic = false,
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

  // State for basic ConfirmModal (used for all non-reseed operations)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)

  // State for AdvancedReseedModal (used for reseed operations)
  const [isReseedModalOpen, setIsReseedModalOpen] = useState(false)
  const [reseedOperationType, setReseedOperationType] = useState('')

  // State for LostEventsModal (last divergence viewer)
  const [isLostEventsModalOpen, setIsLostEventsModalOpen] = useState(false)

  const serverName = row?.id ? `server ${row.host}:${row.port} (${row.id})` : ''

  const rawHref = useHref('/')
  const getHref = useMemo(() => rawHref.replace(/\/+$/, ''), [rawHref])

  const openTerminalPage = useCallback(
    (clusterName, srvId, commandType = '') => {
      const terminalURL = getHref
        .concat(`/terminal/clusters/${clusterName}/servers/${srvId}/${commandType}`)
        .replace(/\/+$/, '')
      window.open(terminalURL, '_blank')
    },
    [getHref]
  )

  // Handlers for basic ConfirmModal
  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  // Handlers for AdvancedReseedModal
  const openReseedModal = (operationType) => {
    setReseedOperationType(operationType)
    setIsReseedModalOpen(true)
  }
  const closeReseedModal = () => {
    setIsReseedModalOpen(false)
    setReseedOperationType('')
  }

  const handleReseedConfirm = ({ useRestic, snapshotId, strategy, cleanup, tempDir } = {}) => {
    switch (reseedOperationType) {
      case 'logical-backup':
      case 'physical-backup':
        if (useRestic) {
          if (!snapshotId) {
            return
          }
          dispatch(
            reseedFromResticSnapshot({
              clusterName,
              serverId: row.id,
              snapshotId,
              method: reseedOperationType === 'physical-backup' ? 'physical' : 'logical',
              strategy,
              cleanup,
              tempDir
            })
          )
        } else if (reseedOperationType === 'physical-backup') {
          dispatch(reseedPhysicalFromBackup({ clusterName, serverId: row.id }))
        } else {
          dispatch(reseedLogicalFromBackup({ clusterName, serverId: row.id }))
        }
        break
      case 'logical-master':
        dispatch(reseedLogicalFromMaster({ clusterName, serverId: row.id }))
        break
      default:
        break
    }
    closeReseedModal()
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
            isDisabled: !user?.grants['db-maintenance'],
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm maintenance for ${serverName}?`)
              setConfirmHandler(() => () => dispatch(setMaintenanceMode({ clusterName, serverId: row.id })))
            }
          },
          {
            name: 'Last Divergence',
            onClick: () => setIsLostEventsModalOpen(true)
          },
          ...(showTerminal
            ? [
                {
                  name: 'Web Terminal',
                  subMenu: [
                    {
                      name: 'MySQL Terminal',
                      isDisabled: !user?.grants['terminal-db'],
                      onClick: () => openTerminalPage(clusterName, row.id, 'mysql')
                    },
                    {
                      name: 'MyTop Terminal',
                      isDisabled: !user?.grants['terminal-db'],
                      onClick: () => openTerminalPage(clusterName, row.id, 'mytop')
                    },
                    {
                      name: 'Shell Terminal',
                      isDisabled: !user?.grants['terminal-global'],
                      onClick: () => openTerminalPage(clusterName, row.id)
                    }
                  ]
                }
              ]
            : []),
          ...(row.isSlave && user?.grants['cluster-switchover']
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
              {
                name: 'Set as Preferred',
                isDisabled: !user?.grants['cluster-failover'] || row.prefered || row.ignored,
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm set as preferred for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(setAsPreferred({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Set as Ignored',
                isDisabled: !user?.grants['cluster-failover'] || row.prefered || row.ignored,
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm set as ignored for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(setAsIgnored({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Set as unrated',
                isDisabled: !user?.grants['cluster-failover'] || (!row.prefered && !row.ignored),
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm set as unrated for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(setAsUnrated({ clusterName, serverId: row.id })))
                }
              }
            ]
          },
          {
            name: 'Backup',
            subMenu: [
              {
                name: 'Physical Backup',
                isDisabled: !user?.grants['db-backup'] || clusterMasterId !== row.id,
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm master physical (${backupPhysicalType}) backup?`)
                  setConfirmHandler(() => () => dispatch(physicalBackupMaster({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Logical Backup',
                isDisabled: !user?.grants['db-backup'] || clusterMasterId !== row.id,
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm sending logical backup (${backupLogicalType}) for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(logicalBackup({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Reseed Logical From Backup',
                isDisabled: !user?.grants['db-restore'] || clusterMasterId === row.id,
                onClick: () => {
                  if (backupRestic) {
                    openReseedModal('logical-backup')
                  } else {
                    openConfirmModal()
                    setConfirmTitle(`Confirm reseed with logical backup (${backupLogicalType}) for ${serverName}?`)
                    setConfirmHandler(
                      () => () => dispatch(reseedLogicalFromBackup({ clusterName, serverId: row.id }))
                    )
                  }
                }
              },
              {
                name: 'Reseed Logical From Master',
                isDisabled: !user?.grants['db-restore'] || clusterMasterId === row.id,
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm reseed with ${backupLogicalType} for ${serverName}?`)
                  setConfirmHandler(
                    () => () => dispatch(reseedLogicalFromMaster({ clusterName, serverId: row.id }))
                  )
                }
              },
              {
                name: 'Reseed Physical From Backup',
                isDisabled: !user?.grants['db-restore'] || clusterMasterId === row.id,
                onClick: () => {
                  if (backupRestic) {
                    openReseedModal('physical-backup')
                  } else {
                    openConfirmModal()
                    setConfirmTitle(`Confirm reseed with physical backup (${backupPhysicalType}) for ${serverName}?`)
                    setConfirmHandler(
                      () => () => dispatch(reseedPhysicalFromBackup({ clusterName, serverId: row.id }))
                    )
                  }
                }
              },
              {
                name: 'Flush logs',
                isDisabled: !user?.grants['db-backup'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm flush logs for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(flushLogs({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Run Remote Jobs',
                isDisabled: !user?.grants['cluster-process'],
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
              {
                name: 'Jobs Upgrade',
                isDisabled: !user?.grants['db-maintenance'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm jobs upgrade for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(jobsUpgrade({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Stop Database',
                isDisabled: !user?.grants['db-stop'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm stop for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(stopDatabase({ clusterName, serverId: row.id })))
                }
              },
              ...(orchestrator === 'opensvc'
                ? [
                    {
                      name: 'Abort Orchestration',
                      isDisabled: !user?.grants['db-stop'],
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm orchestration abort for ${serverName}?`)
                        setConfirmHandler(() => () => dispatch(abortDatabase({ clusterName, serverId: row.id })))
                      }
                    }
                  ]
                : []),
              {
                name: 'Start Database',
                isDisabled: !user?.grants['db-start'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm start for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(startDatabase({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Restart Database',
                isDisabled: !user?.grants['db-start'] || !user?.grants['db-stop'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm restart for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(restartDatabase({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Upgrade Database',
                isDisabled: !user?.grants['db-start'] || !user?.grants['db-stop'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm upgrade for ${serverName}? This will stop the database, upgrade to the target version, and restart.`)
                  setConfirmHandler(() => () => dispatch(upgradeDatabase({ clusterName, serverId: row.id })))
                }
              },
              ...(orchestrator === 'opensvc'
                ? [
                    {
                      name: 'Clear Instance State',
                      isDisabled: !user?.grants['db-start'],
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm clear instance state for ${serverName}?`)
                        setConfirmHandler(() => () => dispatch(clearDatabase({ clusterName, serverId: row.id })))
                      }
                    },
                    {
                      name: 'Restart Jobs Container',
                      isDisabled: !user?.grants['db-start'],
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm restart jobs container for ${serverName}?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(restartDatabase({ clusterName, serverId: row.id, rid: OpenSVCTerminalRID.Jobs }))
                        )
                      }
                    },
                    {
                      name: 'Update OpenSVC Template',
                      isDisabled: !user?.grants['prov-db-provision'],
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm update OpenSVC template for ${serverName}?`)
                        setConfirmHandler(
                          () => () => dispatch(updateOpensvcTemplate({ clusterName, serverId: row.id }))
                        )
                      }
                    }
                  ]
                : []),
              {
                name: 'Provision Database',
                isDisabled: !user?.grants['prov-db-provision'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm provision ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(provisionDatabase({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Unprovision Database',
                isDisabled: !user?.grants['prov-db-unprovision'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm unprovision for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(unprovisionDatabase({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Refresh Variables and Generate Config',
                isDisabled: !user?.grants['db-config-flag'],
                onClick: () => {
                  setConfirmTitle(`Confirm generate db config for ${serverName}?`)
                  setConfirmHandler(
                    () => () => dispatch(generateConfig({ clusterName, host: row.host, port: row.port }))
                  )
                  openConfirmModal()
                }
              },
              {
                name: 'Remove Monitor',
                isDisabled: !user?.grants['cluster-drop-monitor'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm removing monitor for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(dropServer({ clusterName, host: row.host, port: row.port })))
                }
              }
            ]
          },
          {
            name: 'DB Utils',
            subMenu: [
              {
                name: 'Optimize',
                isDisabled: !user?.grants['db-optimize'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm optimize for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(optimizeServer({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Skip 1 Replication Event',
                isDisabled: !user?.grants['db-replication'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm skip replication event for ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(skipReplicationEvent({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Toggle InnoDB Monitor',
                isDisabled: !user?.grants['db-logs'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm toggle innodb monitor ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(toggleInnodbMonitor({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Toggle Slow Query Capture',
                isDisabled: !user?.grants['db-capture'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm toggle slow query capture ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(toggleSlowQueryCapture({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Start Slave',
                isDisabled: !user?.grants['db-replication'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm start slave on ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(startSlave({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Stop Slave',
                isDisabled: !user?.grants['db-replication'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm stop slave on ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(stopSlave({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Reset Master',
                isDisabled: !user?.grants['db-replication'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm reset master this may break replication when done on master, ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(resetMaster({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Reset Slave',
                isDisabled: !user?.grants['db-replication'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm reset slave this will break replication on, ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(resetSlaveAll({ clusterName, serverId: row.id })))
                }
              },
              {
                name: 'Toggle Readonly',
                isDisabled: !user?.grants['db-readonly'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm toggle read only on ${serverName}?`)
                  setConfirmHandler(() => () => dispatch(toggleReadOnly({ clusterName, serverId: row.id })))
                }
              }
            ]
          }
        ]}
      />

      {/* Basic ConfirmModal for all non-reseed operations */}
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler?.()
            closeConfirmModal()
          }}
        />
      )}

      {/* AdvancedReseedModal for reseed operations (only when restic is enabled) */}
      {isReseedModalOpen && backupRestic && (
        <AdvancedReseedModal
          isOpen={isReseedModalOpen}
          closeModal={closeReseedModal}
          onConfirm={handleReseedConfirm}
          operationType={reseedOperationType}
          clusterName={clusterName}
          serverInfo={{
            id: row.id,
            host: row.host,
            port: row.port
          }}
          backupType={reseedOperationType.includes('physical') ? backupPhysicalType : backupLogicalType}
        />
      )}

      {isLostEventsModalOpen && (
        <LostEventsModal
          isOpen={isLostEventsModalOpen}
          closeModal={() => setIsLostEventsModalOpen(false)}
          clusterName={clusterName}
          server={row}
        />
      )}
    </>
  )
}

export default ServerMenu
