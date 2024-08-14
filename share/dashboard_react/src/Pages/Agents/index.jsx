import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { DataTable } from '../../components/DataTable'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'

function Agents({ selectedCluster, user }) {
  const [data, setData] = useState([])
  const columnHelper = createColumnHelper()

  useEffect(() => {
    if (selectedCluster?.agents?.length > 0) {
      setData(selectedCluster.agents)
    }
  }, [selectedCluster?.agents])

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.hostName, {
        cell: (info) => info.getValue(),
        header: 'Name',
        id: 'name'
      }),
      columnHelper.accessor((row) => row.cpuCores, {
        cell: (info) => info.getValue(),
        header: 'CPU Cores',
        id: 'cpuCores'
      }),
      columnHelper.accessor((row) => row.cpuFreq, {
        cell: (info) => info.getValue(),
        header: 'CPU Freq',
        id: 'cpuFreq'
      }),
      columnHelper.accessor((row) => row.memBytes, {
        cell: (info) => info.getValue(),
        header: 'Memory',
        id: 'memBytes'
      }),
      columnHelper.accessor((row) => row.osName, {
        cell: (info) => info.getValue(),
        header: 'OS Name',
        id: 'osName'
      }),
      columnHelper.accessor((row) => row.osKernel, {
        cell: (info) => info.getValue(),
        header: 'Kernel',
        id: 'osKernel'
      })
    ],
    []
  )
  return (
    <AccordionComponent
      heading={'HYPERVISORS'}
      allowToggle={false}
      className={styles.accordion}
      panelSX={{ overflowX: 'auto', p: 0 }}
      body={<DataTable data={data} columns={columns} className={styles.table} />}
    />
  )
}

export default Agents
