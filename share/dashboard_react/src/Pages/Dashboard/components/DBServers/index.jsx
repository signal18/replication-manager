import React, { useState, useEffect, useMemo } from 'react'
import { useSelector } from 'react-redux'
import CustomIcon from '../../../../components/CustomIcon'
import { HiCheck, HiThumbDown, HiThumbUp, HiViewGrid, HiX } from 'react-icons/hi'
import { gtidstring } from '../../../../utility/common'
import { Box, Button, Icon, IconButton, Tooltip } from '@chakra-ui/react'
import TagPill from '../../../../components/TagPill'
import { SiMariadbfoundation } from 'react-icons/si'
import { GrMysql } from 'react-icons/gr'
import { DataTable } from '../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import ServerMenu from './ServerMenu'
import DBServersGrid from './DBServersGrid'

function DBServers({ selectedCluster }) {
  const {
    common: { theme, isDesktop },
    cluster: { clusterServers, clusterMaster }
  } = useSelector((state) => state)
  const [data, setData] = useState([])
  const [user, setUser] = useState(null)
  const [viewType, setViewType] = useState('table')
  const [hasMariadbGtid, setHasMariadbGtid] = useState(false)
  const [hasMysqlGtid, setHasMysqlGtid] = useState(false)

  useEffect(() => {
    if (clusterServers?.length > 0) {
      setData(clusterServers)

      setHasMariadbGtid(
        clusterServers.some(function (currentServer) {
          return currentServer.haveMariadbGtid
        })
      )
      setHasMysqlGtid(
        clusterServers.some(function (currentServer) {
          return currentServer.haveMysqlGtid
        })
      )
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
    gtid: {},
    serverName: {
      backgroundColor: 'transparent',
      display: 'flex',
      padding: '0',
      width: '100%',
      _hover: {}
    }
  }

  const showGridView = () => {
    setViewType('grid')
  }
  const showTableView = () => {
    setViewType('table')
  }

  const getDbFlavor = (rowData) => {
    const dbFlavor = rowData.dbVersion.flavor
    return (
      <Tooltip label={dbFlavor}>
        {dbFlavor === 'MariaDB' ? (
          <Icon as={SiMariadbfoundation} fill={'blue.400'} fontSize={'2rem'} />
        ) : dbFlavor === 'MySQL' ? (
          <Icon as={GrMysql} fill={'blue.400'} fontSize={'2rem'} />
        ) : null}
      </Tooltip>
    )
  }

  const getServerName = (rowData) => {
    return (
      <Button type='button' sx={styles.serverName}>
        <Box
          as='span'
          maxWidth='100%'
          whiteSpace='break-spaces'
          textAlign='start'
          overflowWrap='break-word'>{`${rowData.host}:${rowData.port}`}</Box>
      </Button>
    )
  }

  const getStatusValue = (rowData) => {
    const isVirtual = rowData.isVirtualMaster ? '-VMaster' : ''
    let colorScheme = 'gray'
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
    if (hasMariadbGtid) {
      return rowData.replications?.length > 0 && rowData.replications[0].usingGtid.String
    } else if (hasMysqlGtid) {
      return rowData.gtidExecuted
    }
  }

  const getCurrentGtid = (rowData) => {
    let result = ''
    if (hasMariadbGtid) {
      result = gtidstring(rowData.currentGtid)
    }

    if (!hasMariadbGtid && !hasMysqlGtid) {
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
    if (hasMariadbGtid) {
      result = gtidstring(rowData.slaveGtid)
    }
    if (!hasMariadbGtid && !hasMysqlGtid) {
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
      columnHelper.accessor(
        (row) =>
          selectedCluster?.name ? (
            <ServerMenu
              clusterName={selectedCluster?.name}
              clusterMasterId={clusterMaster?.id}
              row={row}
              user={user}
              isDesktop={isDesktop}
            />
          ) : null,
        {
          cell: (info) => info.getValue(),
          id: 'options',
          header: () => {
            return (
              <Tooltip label='Show grid view'>
                <IconButton onClick={showGridView} size='small' icon={<HiViewGrid />} />
              </Tooltip>
            )
          },
          enableSorting: false
        }
      ),
      columnHelper.accessor((row) => getDbFlavor(row), {
        cell: (info) => info.getValue(),
        header: 'Db',
        maxWidth: 40,
        id: 'dbFlavor'
      }),
      columnHelper.accessor((row) => getServerName(row), {
        cell: (info) => info.getValue(),
        header: 'Server',
        maxWidth: 250,
        id: 'serverName'
      }),

      columnHelper.accessor((row) => getStatusValue(row), {
        cell: (info) => info.getValue(),
        header: 'Status',
        id: 'status'
      }),
      columnHelper.accessor((row) => getMaintenanceValue(row), {
        cell: (info) => info.getValue(),
        header: 'In Mnt',
        id: 'inMaintenance'
      }),
      columnHelper.accessor((row) => getUsingGtid(row), {
        cell: (info) => info.getValue(),
        header: () => {
          return `${hasMariadbGtid && 'Using GTID'} ${hasMariadbGtid && hasMysqlGtid ? '/' : ''} ${hasMysqlGtid ? 'Executed GTID Set' : ''}`
        },
        id: 'usingGtid'
      }),
      columnHelper.accessor(
        (row) => (
          <Box as='span' sx={styles.gtid}>
            {getCurrentGtid(row)}
          </Box>
        ),
        {
          cell: (info) => info.getValue(),
          header: () => {
            return hasMariadbGtid ? 'Current GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'File' : ''
          },
          id: 'currentGtid'
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
          header: () => {
            return hasMariadbGtid ? 'Slave GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'Pos' : ''
          },
          id: 'slaveGtid'
        }
      ),
      columnHelper.accessor((row) => row.replications?.length > 0 && row.replications[0].secondsBehindMaster.Int64, {
        cell: (info) => info.getValue(),
        header: 'Delay',
        id: 'delay'
      }),
      columnHelper.accessor((row) => `${row.failCount}/${row.failSuspectHeartbeat}`, {
        cell: (info) => info.getValue(),
        header: 'Fail Cnt',
        id: 'failCount'
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
          header: 'Prf Ign',
          maxWidth: 40,
          id: 'prfIgn'
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
          header: 'IO Thr',
          id: 'ioThr',
          maxWidth: 40
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
          header: 'SQL Thr',
          id: 'sqlThr',
          maxWidth: 40
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.readOnly == 'ON' ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />,
        {
          cell: (info) => info.getValue(),
          header: 'Ro Sts',
          id: 'roSts',
          maxWidth: 40
        }
      ),
      columnHelper.accessor(
        (row) => (row.ignoredRO ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />),
        {
          cell: (info) => info.getValue(),
          header: 'Ign RO',
          id: 'ignRO',
          maxWidth: 40
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.eventScheduler ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />,
        {
          cell: (info) => info.getValue(),
          header: 'Evt Sch',
          id: 'evtSch',
          maxWidth: 40
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
          header: 'Mst Syn',
          id: 'mstSyn',
          maxWidth: 40
        }
      ),
      columnHelper.accessor(
        (row) =>
          row.semiSyncSlaveStatus ? <CustomIcon icon={HiCheck} color='green' /> : <CustomIcon icon={HiX} color='red' />,
        {
          cell: (info) => info.getValue(),
          header: 'Rep Syn',
          id: 'repSyn',
          maxWidth: 40
        }
      )
    ],
    [hasMariadbGtid, hasMysqlGtid, selectedCluster?.name]
  )

  return clusterServers?.length > 0 ? (
    <>
      {viewType === 'table' ? (
        <DataTable data={data} columns={columns} />
      ) : (
        <DBServersGrid
          data={data}
          columns={columns}
          clusterMasterId={clusterMaster?.id}
          clusterName={selectedCluster?.name}
          user={user}
          showTableView={showTableView}
        />
      )}
    </>
  ) : null
}

export default DBServers
