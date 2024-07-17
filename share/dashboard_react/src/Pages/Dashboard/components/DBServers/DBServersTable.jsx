import { createColumnHelper } from '@tanstack/react-table'
import React, { useState, useEffect, useMemo } from 'react'
import { DataTable } from '../../../../components/DataTable'
import { useDispatch, useSelector } from 'react-redux'
import CustomIcon from '../../../../components/CustomIcon'
import { HiCheck, HiTable, HiThumbDown, HiThumbUp, HiViewGrid, HiX } from 'react-icons/hi'
import { gtidstring } from '../../../../utility/common'
import MenuOptions from '../../../../components/MenuOptions'

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
import { Box, IconButton } from '@chakra-ui/react'
import TagPill from '../../../../components/TagPill'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'

function DBServersTable({ selectedCluster }) {
  const {
    common: { theme, isDesktop },
    cluster: { clusterServers, clusterMaster }
  } = useSelector((state) => state)
  const [data, setData] = useState([])
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [user, setUser] = useState(null)
  const [viewType, setViewType] = useState('table')

  const dispatch = useDispatch()
  useEffect(() => {
    if (clusterServers?.length > 0) {
      setData(clusterServers)
    }
  }, [clusterServers])

  useEffect(() => {
    const loggedUser = localStorage.getItem('username')
    if (loggedUser && selectedCluster?.apiUsers[loggedUser]) {
      const apiUser = selectedCluster.apiUsers[loggedUser]
      setUser(apiUser)
    }
  }, [selectedCluster])

  const styles = {
    gtid: {
      fontSize: '14px',
      width: '100px',
      display: 'block'
    }
  }

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }

  const showGridView = () => {
    setViewType('grid')
  }
  const showTableView = () => {
    setViewType('table')
  }

  const renderOptions = (row) => {
    return (
      <MenuOptions
        placement='right-end'
        subMenuPlacement={isDesktop ? 'right-end' : 'bottom'}
        options={[
          {
            name: 'Maintenance Mode',
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm maintenance for server-id: ${row.id}?`)
              setConfirmHandler(
                () => () => dispatch(setMaintenanceMode({ clusterName: selectedCluster?.name, serverId: row.id }))
              )
            }
          },
          ...(user?.grants['cluster-switchover'] && row.isSlave
            ? [
                {
                  name: 'Promote To Leader',
                  onClick: () => {
                    openConfirmModal()
                    setConfirmTitle(`Confirm promotion for server-id: ${row.id}?`)
                    setConfirmHandler(
                      () => () => dispatch(promoteToLeader({ clusterName: selectedCluster?.name, serverId: row.id }))
                    )
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
                        setConfirmTitle(`Confirm set as unrated for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(setAsPreferred({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Set as Ignored',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm set as unrated for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(setAsIgnored({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm set as unrated for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(setAsUnrated({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    }
                  ]
                : [])
            ]
          },
          {
            name: 'Backup',
            subMenu: [
              ...(clusterMaster?.id === row.id && user?.grants['db-backup']
                ? [
                    {
                      name: 'Physical Backup',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm master physical (xtrabackup compressed) backup?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(physicalBackupMaster({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Logical Backup',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm sending logical backup (mysqldump) for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(logicalBackup({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    }
                  ]
                : user?.grants['db-restore']
                  ? [
                      {
                        name: 'Reseed Logical From Backup',
                        onClick: () => {
                          openConfirmModal()
                          setConfirmTitle(`Confirm reseed with logical backup (mysqldump) for servr-id: ${row.id}?`)
                          setConfirmHandler(
                            () => () =>
                              dispatch(
                                reseedLogicalFromBackup({ clusterName: selectedCluster?.name, serverId: row.id })
                              )
                          )
                        }
                      },
                      {
                        name: 'Reseed Logical From Master',
                        onClick: () => {
                          openConfirmModal()
                          setConfirmTitle(`Confirm reseed with mysqldump for server-id: ${row.id}?`)
                          setConfirmHandler(
                            () => () =>
                              dispatch(
                                reseedLogicalFromMaster({ clusterName: selectedCluster?.name, serverId: row.id })
                              )
                          )
                        }
                      },
                      {
                        name: 'Reseed Physical From Backup',
                        onClick: () => {
                          openConfirmModal()
                          setConfirmTitle(
                            `Confirm reseed with physical backup (xtrabackup compressed) for server-id: ${row.id}?`
                          )
                          setConfirmHandler(
                            () => () =>
                              dispatch(
                                reseedPhysicalFromBackup({ clusterName: selectedCluster?.name, serverId: row.id })
                              )
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
                        setConfirmTitle(`Confirm flush logs for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(flushLogs({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm stop for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(stopDatabase({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm start for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(startDatabase({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm provision server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(provisionDatabase({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm unprovision for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(unprovisionDatabase({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Run Remote Jobs',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm running remote jobs for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(runRemoteJobs({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm optimize for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(optimizeServer({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm skip replication event for server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(skip1ReplicationEvent({ clusterName: selectedCluster?.name, serverId: row.id }))
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
                        setConfirmTitle(`Confirm toggle innodb monitor server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(toggleInnodbMonitor({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm toggle slow query capture server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () =>
                            dispatch(toggleSlowQueryCapture({ clusterName: selectedCluster?.name, serverId: row.id }))
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
                        setConfirmTitle(`Confirm start slave on server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(startSlave({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Stop Slave',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm stop slave on server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(stopSlave({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Reset Master',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(
                          `Confirm reset master this may break replication when done on master, server-id: ${row.id}?`
                        )
                        setConfirmHandler(
                          () => () => dispatch(resetMaster({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    },
                    {
                      name: 'Reset Slave',
                      onClick: () => {
                        openConfirmModal()
                        setConfirmTitle(`Confirm reset slave this will break replication on, server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(resetSlave({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
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
                        setConfirmTitle(`Confirm toggle read only on server-id: ${row.id}?`)
                        setConfirmHandler(
                          () => () => dispatch(toggleReadOnly({ clusterName: selectedCluster?.name, serverId: row.id }))
                        )
                      }
                    }
                  ]
                : [])
            ]
          }
        ]}
      />
    )
  }

  const getServerName = (rowData) => {
    return (
      <button type='button'>
        <span>{`${rowData.host}:${rowData.port}`}</span>
      </button>
    )
  }

  const getStatusValue = (rowData) => {
    const isVirtual = rowData.isVirtualMaster ? '-VMaster' : ''
    let colorScheme = ''
    let stateValue = rowData.state
    switch (rowData.state) {
      case 'SlaveErr':
        stateValue = 'Slave Error'
        colorScheme = 'orange'
        break
      case 'StandAlone':
        stateValue = 'Standalone'
        colorScheme = 'gray'
        break
      case 'Master':
        colorScheme = 'blue'
        break
      case 'Slave':
        colorScheme = 'gray'
        break
      case 'Suspect':
        colorScheme = 'orange'
        break
      case 'Failed':
        colorScheme = 'red'
        break
      default:
        stateValue = rowData.state
        break
    }
    return <TagPill colorScheme={colorScheme} text={`${stateValue}${isVirtual}`} />
  }

  const getMaintenanceValue = (rowData) => {
    return rowData.isMaintenance ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />
  }

  const getUsingGtid = (rowData) => {
    if (rowData.haveMariadbGtid) {
      return rowData.replications?.length > 0 && rowData.replications[0].usingGtid.String
    } else if (rowData.haveMysqlGtid) {
      return rowData.gtidExecuted
    }
  }

  const getCurrentGtid = (rowData) => {
    let result = ''
    if (rowData.haveMariadbGtid) {
      result = gtidstring(rowData.currentGtid)
    }

    if (!rowData.haveMariadbGtid && !rowData.haveMysqlGtid) {
      if (rowData.isSlave && rowData.replications?.length > 0) {
        result += rowData.replications[0].masterLogFile.String
      } else {
        result += rowData.binaryLogFile
      }
    }

    return result
  }

  const getSlaveGtid = (rowData) => {
    let result = ''
    if (rowData.haveMariadbGtid) {
      result = gtidstring(rowData.slaveGtid)
    }
    if (!rowData.haveMariadbGtid && !rowData.haveMysqlGtid) {
      if (rowData.isSlave && rowData.replications?.length > 0) {
        result += rowData.replications[0].execMasterLogPos.String
      } else {
        result += rowData.binaryLogPos
      }
    }
    return result
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => renderOptions(row), {
        cell: (info) => info.getValue(),
        id: 'options',
        header: () => {
          return <IconButton onClick={showGridView} size='small' icon={<HiViewGrid />} />
        },
        enableSorting: false,
        width: 20
      }),
      columnHelper.accessor((row) => getServerName(row), {
        cell: (info) => info.getValue(),
        header: 'Server',
        width: 20
      }),
      columnHelper.accessor((row) => getStatusValue(row), {
        cell: (info) => info.getValue(),
        header: 'Status'
      }),
      columnHelper.accessor((row) => getMaintenanceValue(row), {
        cell: (info) => info.getValue(),
        header: 'In Mnt'
      }),
      columnHelper.accessor((row) => getUsingGtid(row), {
        cell: (info) => info.getValue(),
        header: 'Using GTID'
      }),
      columnHelper.accessor(
        (row) => (
          <Box as='span' sx={styles.gtid}>
            {getCurrentGtid(row)}
          </Box>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Current GTID'
        }
      ),
      columnHelper.accessor(
        (row) => (
          <Box as='span' sx={styles.gtid}>
            {getSlaveGtid(row)}
          </Box>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Slave GTID'
        }
      ),
      columnHelper.accessor((row) => row.replications?.length > 0 && row.replications[0].secondsBehindMaster.Int64, {
        cell: (info) => info.getValue(),
        header: 'Delay'
      }),
      columnHelper.accessor((row) => `${row.failCount}/${row.failSuspectHeartbeat}`, {
        cell: (info) => info.getValue(),
        header: 'Fail Cnt'
      }),
      columnHelper.accessor(
        (row) =>
          row.ignored ? (
            <CustomIcon icon={HiThumbDown} color='red' />
          ) : row.prefered ? (
            <CustomIcon icon={HiThumbUp} color='green' />
          ) : null,
        {
          cell: (info) => info.getValue(),
          header: 'Prf Ign'
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.replications?.length > 0 && row.replications[0].slaveIoRunning.String == 'Yes' ? (
            <CustomIcon icon={HiCheck} color='green' />
          ) : (
            <CustomIcon icon={HiX} color='red' />
          ),
        {
          cell: (info) => info.getValue(),
          header: 'IO Thr'
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.replications?.length > 0 && row.replications[0].slaveSqlRunning.String == 'Yes' ? (
            <CustomIcon icon={HiCheck} color='green' />
          ) : (
            <CustomIcon icon={HiX} color='red' />
          ),
        {
          cell: (info) => info.getValue(),
          header: 'SQL Thr'
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.readOnly == 'ON' ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />,
        {
          cell: (info) => info.getValue(),
          header: 'Ro Sts'
        }
      ),
      columnHelper.accessor(
        (row) => (row.ignoredRO ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />),
        {
          cell: (info) => info.getValue(),
          header: 'Ign RO'
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.eventScheduler ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />,
        {
          cell: (info) => info.getValue(),
          header: 'Evt Sch'
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.semiSyncMasterStatus ? (
            <CustomIcon icon={HiCheck} color='green' />
          ) : (
            <CustomIcon icon={HiX} color='red' />
          ),
        {
          cell: (info) => info.getValue(),
          header: 'Mst Syn'
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.semiSyncSlaveStatus ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />,
        {
          cell: (info) => info.getValue(),
          header: 'Rep Syn'
        }
      )
    ],
    []
  )
  return (
    <>
      {viewType === 'table' ? (
        <DataTable columns={columns} data={data} fixedColumnIndex={1} />
      ) : (
        <div>
          grid view goes here <IconButton size='small' icon={<HiTable onClick={showTableView} />} />
        </div>
      )}

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

export default DBServersTable
