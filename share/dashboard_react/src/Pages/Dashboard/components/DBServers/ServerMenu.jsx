import { useDispatch } from 'react-redux'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import {
  flushLogs,
  logicalBackup,
  optimizeServer,
  physicalBackupMaster,
  promoteToLeader,
  provisionDatabase,
  reseedLogicalFromBackup,
  reseedLogicalFromMaster,
  reseedPhysicalFromBackup,
  resetMaster,
  resetSlave,
  runRemoteJobs,
  setAsIgnored,
  setAsPreferred,
  setAsUnrated,
  setMaintenanceMode,
  skip1ReplicationEvent,
  startDatabase,
  startSlave,
  stopDatabase,
  stopSlave,
  toggleInnodbMonitor,
  toggleReadOnly,
  toggleSlowQueryCapture,
  unprovisionDatabase
} from '../../../../redux/clusterSlice'
import { useState, useEffect } from 'react'

function ServerMenu({ clusterName, clusterMasterId, row, user, isDesktop, from = 'tableView', openCompareModal }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [serverName, setServerName] = useState('')

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
        placement={from === 'tableView' ? 'right-end' : 'left-end'}
        subMenuPlacement={isDesktop ? (from === 'tableView' ? 'right-end' : 'left-end') : 'bottom'}
        options={[
          {
            name: 'Compare With',
            onClick: () => openCompareModal(row)
          },
          {
            name: 'Maintenance Mode',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm maintenance for ${serverName}?`)
              setConfirmHandler(() => () => dispatch(setMaintenanceMode({ clusterName, serverId: row.id })))
            }
          },
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
                        setConfirmTitle(`Confirm set as unrated for ${serverName}?`)
                        setConfirmHandler(() => () => dispatch(setAsPreferred({ clusterName, serverId: row.id })))
                      }
                    },
                    {
                      name: 'Set as Ignored',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm set as unrated for ${serverName}?`)
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
                        setConfirmTitle(`Confirm master physical (xtrabackup compressed) backup?`)
                        setConfirmHandler(() => () => dispatch(physicalBackupMaster({ clusterName, serverId: row.id })))
                      }
                    },
                    {
                      name: 'Logical Backup',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm sending logical backup (mysqldump) for ${serverName}?`)
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
                          setConfirmTitle(`Confirm reseed with logical backup (mysqldump) for ${serverName}?`)
                          setConfirmHandler(
                            () => () => dispatch(reseedLogicalFromBackup({ clusterName, serverId: row.id }))
                          )
                        }
                      },
                      {
                        name: 'Reseed Logical From Master',
                        onClick: () => {
                          openConfirmModal()
                          setConfirmTitle(`Confirm reseed with mysqldump for ${serverName}?`)
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
                            `Confirm reseed with physical backup (xtrabackup compressed) for ${serverName}?`
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
                : [])
            ]
          },
          {
            name: 'Provision',
            subMenu: [
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
                    }
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
                    {
                      name: 'Run Remote Jobs',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm running remote jobs for ${serverName}?`)
                        setConfirmHandler(() => () => dispatch(runRemoteJobs({ clusterName, serverId: row.id })))
                      }
                    }
                  ]
                : [])
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
                          () => () => dispatch(skip1ReplicationEvent({ clusterName, serverId: row.id }))
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
                        setConfirmHandler(() => () => dispatch(resetSlave({ clusterName, serverId: row.id })))
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
