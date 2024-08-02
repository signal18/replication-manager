import React, { useState, useEffect, useMemo } from 'react'
import { useSelector } from 'react-redux'
import { HiViewGrid } from 'react-icons/hi'
import { DataTable } from '../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import ServerMenu from './ServerMenu'
import DBServersGrid from './DBServerGrid'
import CompareModal from '../../../../components/Modals/CompareModal'
import { getCurrentGtid, getDelay, getFailCount, getSlaveGtid, getUsingGtid } from './utils'
import CheckOrCrossIcon from '../../../../components/Icons/CheckOrCrossIcon'
import DBFlavourIcon from '../../../../components/Icons/DBFlavourIcon'
import ServerName from './ServerName'
import GTID from '../../../../components/GTID'
import ServerStatus from '../../../../components/ServerStatus'
import RMIconButton from '../../../../components/RMIconButton'
import { useColorMode } from '@chakra-ui/react'

function DBServers({ selectedCluster, user }) {
  const {
    common: { isDesktop },
    cluster: { clusterServers, clusterMaster }
  } = useSelector((state) => state)
  const { colorMode } = useColorMode()
  const [data, setData] = useState([])
  const [viewType, setViewType] = useState('table')
  const [hasMariadbGtid, setHasMariadbGtid] = useState(false)
  const [hasMysqlGtid, setHasMysqlGtid] = useState(false)
  const [isCompareModalOpen, setIsCompareModalOpen] = useState(false)
  const [compareServer, setCompareServer] = useState(null)

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

  const showGridView = () => {
    setViewType('grid')
  }
  const showTableView = () => {
    setViewType('table')
  }

  const openCompareModal = (rowData) => {
    setIsCompareModalOpen(true)
    setCompareServer(rowData)
  }
  const closeCompareModal = () => {
    setIsCompareModalOpen(false)
    setCompareServer(null)
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
              openCompareModal={openCompareModal}
            />
          ) : null,
        {
          cell: (info) => info.getValue(),
          id: 'options',
          header: () => {
            return <RMIconButton onClick={showGridView} icon={HiViewGrid} tooltip='Show grid view' />
          }
        }
      ),
      columnHelper.accessor((row) => <DBFlavourIcon dbFlavor={row.dbVersion.flavor} />, {
        cell: (info) => info.getValue(),
        header: 'Db',
        maxWidth: 40,
        id: 'dbFlavor'
      }),
      columnHelper.accessor((row) => <ServerName rowData={row} />, {
        cell: (info) => info.getValue(),
        header: 'Server',
        maxWidth: 250,
        id: 'serverName'
      }),

      columnHelper.accessor((row) => <ServerStatus state={row.state} isBlinking={true} />, {
        cell: (info) => info.getValue(),
        header: 'Status',
        id: 'status'
      }),
      columnHelper.accessor((row) => getUsingGtid(row, hasMariadbGtid, hasMysqlGtid), {
        cell: (info) => info.getValue(),
        header: () => {
          return `${hasMariadbGtid && 'Using GTID'} ${hasMariadbGtid && hasMysqlGtid ? '/' : ''} ${hasMysqlGtid ? 'Executed GTID Set' : ''}`
        },
        id: 'usingGtid'
      }),
      columnHelper.accessor(
        (row) => {
          const gtids = getCurrentGtid(row, hasMariadbGtid, hasMysqlGtid)
          return <GTID text={gtids} />
        },
        {
          cell: (info) => info.getValue(),
          header: () => {
            return hasMariadbGtid ? 'Current GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'File' : ''
          },
          id: 'currentGtid'
        }
      ),
      columnHelper.accessor((row) => <GTID text={getSlaveGtid(row, hasMariadbGtid, hasMysqlGtid)} />, {
        cell: (info) => info.getValue(),
        header: () => {
          return hasMariadbGtid ? 'Slave GTID' : !hasMariadbGtid && !hasMysqlGtid ? 'Pos' : ''
        },
        id: 'slaveGtid'
      }),
      columnHelper.accessor((row) => getDelay(row), {
        cell: (info) => info.getValue(),
        header: 'Delay',
        id: 'delay'
      }),
      columnHelper.accessor((row) => getFailCount(row), {
        cell: (info) => info.getValue(),
        header: 'Fail Cnt',
        id: 'failCount'
      }),
      columnHelper.accessor(
        (row) => <CheckOrCrossIcon isValid={row.prefered} isInvalid={row.ignored} variant='thumb' />,
        {
          cell: (info) => info.getValue(),
          header: 'Prf Ign',
          maxWidth: 40,
          id: 'prfIgn'
        }
      ),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={row.isMaintenance} />, {
        cell: (info) => info.getValue(),
        header: 'In Mnt',
        id: 'inMaintenance'
      }),
      columnHelper.accessor(
        (row) => (
          <CheckOrCrossIcon
            isValid={row.replications?.length > 0 && row.replications[0].slaveIoRunning.String == 'Yes'}
          />
        ),
        {
          cell: (info) => info.getValue(),
          header: 'IO Thr',
          id: 'ioThr',
          maxWidth: 40
        }
      ),
      columnHelper.accessor(
        (row) => (
          <CheckOrCrossIcon
            isValid={row.replications?.length > 0 && row.replications[0].slaveSqlRunning.String == 'Yes'}
          />
        ),
        {
          cell: (info) => info.getValue(),
          header: 'SQL Thr',
          id: 'sqlThr',
          maxWidth: 40
        }
      ),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={row.readOnly == 'ON'} />, {
        cell: (info) => info.getValue(),
        header: 'Ro Sts',
        id: 'roSts',
        maxWidth: 40
      }),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={row.ignoredRO} />, {
        cell: (info) => info.getValue(),
        header: 'Ign RO',
        id: 'ignRO',
        maxWidth: 40
      }),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={row.eventScheduler} />, {
        cell: (info) => info.getValue(),
        header: 'Evt Sch',
        id: 'evtSch',
        maxWidth: 40
      }),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={row.semiSyncMasterStatus} />, {
        cell: (info) => info.getValue(),
        header: 'Mst Syn',
        id: 'mstSyn',
        maxWidth: 40
      }),
      columnHelper.accessor((row) => <CheckOrCrossIcon isValid={row.semiSyncSlaveStatus} />, {
        cell: (info) => info.getValue(),
        header: 'Rep Syn',
        id: 'repSyn',
        maxWidth: 40
      })
    ],
    [hasMariadbGtid, hasMysqlGtid, selectedCluster?.name]
  )

  return clusterServers?.length > 0 ? (
    <>
      {viewType === 'table' ? (
        <DataTable data={data} columns={columns} />
      ) : (
        <DBServersGrid
          allDBServers={data}
          clusterMasterId={clusterMaster?.id}
          clusterName={selectedCluster?.name}
          user={user}
          showTableView={showTableView}
          openCompareModal={openCompareModal}
          hasMariadbGtid={hasMariadbGtid}
          hasMysqlGtid={hasMysqlGtid}
        />
      )}

      {isCompareModalOpen && (
        <CompareModal
          isOpen={isCompareModalOpen}
          closeModal={closeCompareModal}
          allDBServers={data}
          compareServer={compareServer}
          hasMariadbGtid={hasMariadbGtid}
          hasMysqlGtid={hasMariadbGtid}
        />
      )}
    </>
  ) : null
}

export default DBServers
