import React, { useMemo, useEffect, useState } from 'react'
import { DataTable } from '../../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box } from '@chakra-ui/react'
import AppMenu from '../AppMenu'
import { HiViewGrid } from 'react-icons/hi'
import TagPill from '../../../../../components/TagPill'
import ServerStatus from '../../../../../components/ServerStatus'
import AppStatus from '../AppStatus'
import RMIconButton from '../../../../../components/RMIconButton'
import styles from './styles.module.scss'
import ServerName from '../../../../../components/ServerName'

function AppTable({ apps = [], isDesktop, clusterName, showGridView, user, isMenuOptionsVisible }) {
  const [tableData, setTableData] = useState([])
  useEffect(() => {
    if (apps?.length > 0) {
      const data = []
      apps.forEach((app) => {
        let isNewApp = false
        app.backendsWrite?.forEach((writeData, index) => {
          isNewApp = index === 0
          data.push(readWriteData(app, writeData, 'WRITE', isNewApp))
        })
        app.backendsRead?.forEach((readData, index) => {
          isNewApp = !isNewApp && index === 0
          data.push(readWriteData(app, readData, 'READ', isNewApp))
        })
        if (!app.backendsRead && !app.backendsWrite) {
          data.push({
            appId: app.id,
            showMenu: isMenuOptionsVisible,
            server: `${app.host}:${app.port}`,
            status: <AppStatus status={app.state} />
          })
        }
      })
      setTableData(data)
    }
  }, [apps])

  const readWriteData = (app, data, readWriteType, isNewApp) => {
    return {
      appId: app.id,
      showMenu: isNewApp && isMenuOptionsVisible,
      server: `${app.host}:${data.port}`,
      status: <AppStatus status={app.state} />,
      group: <TagPill text={readWriteType} colorScheme={readWriteType === 'WRITE' ? 'blue' : 'gray'} />,
      dbName: `${data.prxName}`,
      dbStatus: <ServerStatus state={data.status} />,
      pxStatus: data.prxStatus,
      connections: data.prxConnections,
      bytesOut: data.prxByteOut,
      bytesIn: data.prxByteIn,
      sessTime: data.prxLatency,
      idGroup: data.prxHostgroup
    }
  }

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) => row.showMenu && <AppMenu row={row} isDesktop={isDesktop} clusterName={clusterName} user={user} />,
        {
          cell: (info) => info.getValue(),
          id: 'options',
          header: () => {
            return <RMIconButton onClick={showGridView} icon={HiViewGrid} tooltip='Show grid view' />
          },
          width: '40px'
        }
      ),
      columnHelper.accessor((row) => row.appId, {
        cell: (info) => info.getValue(),
        header: 'App Id',
        id: 'appId',
        enableHiding: true
      }),
      columnHelper.accessor((row) => <ServerName name={row.server} />, {
        cell: (info) => info.getValue(),
        header: 'Frontend',
        width: 280
      }),
      columnHelper.accessor((row) => row.status, {
        cell: (info) => info.getValue(),
        header: 'Status'
      }),
      columnHelper.accessor((row) => row.group, {
        cell: (info) => info.getValue(),
        header: 'Group'
      }),
      columnHelper.accessor((row) => row.dbName, {
        cell: (info) => info.getValue(),
        header: 'Backend',
        textAlign: 'left'
      }),
      columnHelper.accessor((row) => row.dbStatus, {
        cell: (info) => info.getValue(),
        header: 'DB Status'
      }),
      columnHelper.accessor((row) => row.pxStatus, {
        cell: (info) => info.getValue(),
        header: 'App Status'
      }),
      columnHelper.accessor((row) => row.connections, {
        cell: (info) => info.getValue(),
        header: 'Connections'
      }),
      columnHelper.accessor((row) => row.bytesOut, {
        cell: (info) => info.getValue(),
        header: 'Bytes Out'
      }),
      columnHelper.accessor((row) => row.bytesIn, {
        cell: (info) => info.getValue(),
        header: 'Bytes In'
      }),
      columnHelper.accessor((row) => row.sessTime, {
        cell: (info) => info.getValue(),
        header: 'Sess Time'
      }),
      columnHelper.accessor((row) => row.idGroup, {
        cell: (info) => info.getValue(),
        header: 'ID Group'
      })
    ],
    []
  )

  return (
    <Box className={styles.tableContainer}>
      <DataTable data={tableData} columns={columns} />
    </Box>
  )
}

export default AppTable
