import React, { useMemo, useEffect, useState } from 'react'
import { DataTable } from '../../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box } from '@chakra-ui/react'
import AppMenu from '../AppMenu'
import { HiViewGrid } from 'react-icons/hi'
import RMIconButton from '../../../../../components/RMIconButton'
import styles from './styles.module.scss'
import ServerName from '../../../../../components/ServerName'
import { Link } from 'react-router-dom'

function AppTable({ apps = [], isDesktop, clusterName, showGridView, user }) {
  const [tableData, setTableData] = useState([])
  useEffect(() => {
    if (apps?.length > 0) {
      setTableData(apps)
    }
    console.log(apps)
  }, [apps])

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) => row.isMenuOptionVisible && <AppMenu row={row} isDesktop={isDesktop} clusterName={clusterName} user={user} />,
        {
          cell: (info) => info.getValue(),
          id: 'options',
          header: () => {
            return <RMIconButton onClick={showGridView} icon={HiViewGrid} tooltip='Show grid view' />
          },
          width: '40px'
        }
      ),
      columnHelper.accessor((row) => (
          <Link to={`/clusters/${clusterName}/app/${row?.id}`}>
            <ServerName name={`${row.host}:${row.port}`} />
          </Link>
        ), {
        cell: (info) => info.getValue(),
        header: 'App Id',
        id: 'appId',
        enableHiding: true
      }),
      columnHelper.accessor((row) => row.status, {
        cell: (info) => info.getValue(),
        header: 'Status'
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
