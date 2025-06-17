import React, { useMemo, useEffect, useState } from 'react'
import { DataTable } from '../../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, VStack } from '@chakra-ui/react'
import AppMenu from '../AppMenu'
import styles from './styles.module.scss'
import { Link } from 'react-router-dom'
import ServerName from '../../../../../components/ServerName'
import TagPill from '../../../../../components/TagPill'
import AppStatus from '../AppStatus'

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
        (row) => row.name || row.id,
        {
          cell: ({row}) => <AppMenu row={row.original} isDesktop={isDesktop} clusterName={clusterName} user={user} />,
          id: 'options',
          header: '',
          width: '40px'
        }
      ),
      columnHelper.accessor((row) => (<Link to={`/clusters/${clusterName}/app/${row?.id}`}>
        <ServerName name={`${row.host}`} />
      </Link>), {
        cell: (info) => info.getValue(),
        header: 'Apps'
      }),
      columnHelper.accessor((row) => (<AppStatus state={row.state} />), {
        cell: (info) => info.getValue(),
        header: 'Status'
      }),
      columnHelper.accessor((row) => row.config?.provAppDockerImg, {
        cell: (info) => info.getValue(),
        header: 'Docker Image'
      }),
      columnHelper.accessor((row) => (<VStack>
        {row.routeStatus?.filter((route) => route.primary).map((route, idx) => (<TagPill key={idx} colorScheme="blue" text={route.cname} />))}
      </VStack>), {
        cell: (info) => info.getValue(),
        header: 'Routes'
      }),
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
