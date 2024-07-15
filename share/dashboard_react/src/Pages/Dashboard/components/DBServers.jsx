import { createColumnHelper } from '@tanstack/react-table'
import React, { useState, useEffect } from 'react'
import { DataTable } from '../../../components/DataTable'
import { useSelector } from 'react-redux'

function DBServers({ props }) {
  const {
    common: { theme, isDesktop },
    cluster: { clusterServers }
  } = useSelector((state) => state)
  const [data, setData] = useState([])

  useEffect(() => {
    if (clusterServers?.length > 0) {
      setData(clusterServers)
    }
  }, [clusterServers])

  const columnHelper = createColumnHelper()

  const columns = [
    columnHelper.accessor('Server', {
      cell: (info) => info.getValue(),
      header: 'Server'
    }),
    columnHelper.accessor('Status', {
      cell: (info) => info.getValue(),
      header: 'Status'
    }),
    columnHelper.accessor('In Mnt', {
      cell: (info) => info.getValue(),
      header: 'In Mnt'
    }),
    columnHelper.accessor('Using GTID', {
      cell: (info) => info.getValue(),
      header: 'Using GTID'
    }),
    columnHelper.accessor('Current GTID', {
      cell: (info) => info.getValue(),
      header: 'Current GTID'
    }),
    columnHelper.accessor('Slave GTID', {
      cell: (info) => info.getValue(),
      header: 'Slave GTID'
    }),
    columnHelper.accessor('Delay', {
      cell: (info) => info.getValue(),
      header: 'Delay'
    }),
    columnHelper.accessor('Fail Cnt', {
      cell: (info) => info.getValue(),
      header: 'Fail Cnt'
    }),
    columnHelper.accessor('Prf Ign', {
      cell: (info) => info.getValue(),
      header: 'Prf Ign'
    }),
    columnHelper.accessor('IO Thr', {
      cell: (info) => info.getValue(),
      header: 'IO Thr'
    }),
    columnHelper.accessor('SQL Thr', {
      cell: (info) => info.getValue(),
      header: 'SQL Thr'
    }),
    columnHelper.accessor('Ro Sts', {
      cell: (info) => info.getValue(),
      header: 'Ro Sts'
    }),
    columnHelper.accessor('Ign RO', {
      cell: (info) => info.getValue(),
      header: 'Ign RO'
    }),
    columnHelper.accessor('Evt Sch', {
      cell: (info) => info.getValue(),
      header: 'Evt Sch'
    }),
    columnHelper.accessor('Mst Syn', {
      cell: (info) => info.getValue(),
      header: 'Mst Syn'
    }),
    columnHelper.accessor('Rep Syn', {
      cell: (info) => info.getValue(),
      header: 'Rep Syn'
    })
  ]
  return <DataTable columns={columns} data={data} />
}

export default DBServers
