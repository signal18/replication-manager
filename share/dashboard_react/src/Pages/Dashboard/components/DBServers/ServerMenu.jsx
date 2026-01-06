import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import {
  dropServer,
  flushLogs,
  jobsUpgrade,
  logicalBackup,
  optimizeServer,
  physicalBackupMaster,
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
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [serverName, setServerName] = useState('')

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
                : user?.grants['db-restore']
                  ? [
                    {
                      name: 'Reseed Logical From Backup',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(
                          `Confirm reseed with logical backup (${backupLogicalType}) for ${serverName}?`
                        )
                        setConfirmHandler(
                          () => () => dispatch(reseedLogicalFromBackup({ clusterName, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Reseed Logical From Master',
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
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(
                          `Confirm reseed with physical backup (${backupPhysicalType}) for ${serverName}?`
                        )
                        setConfirmHandler(
                          () => () => dispatch(reseedPhysicalFromBackup({ clusterName, serverId: row.id }))
                        )
                      }
                    }
                  ]
                  : []),
              ...(user?.grants['db-backup']
                ? [
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
    </>
  )
}

export default ServerMenu
