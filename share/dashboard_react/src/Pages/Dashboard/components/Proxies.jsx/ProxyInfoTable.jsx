import React, { useMemo } from 'react'
import { DataTable } from '../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, Heading, IconButton, Tooltip } from '@chakra-ui/react'
import ProxyMenu from './ProxyMenu'
import { HiViewGrid } from 'react-icons/hi'

function ProxyInfoTable({ proxies, isDesktop }) {
  const styles = {
    tableContainer: {
      width: '100%'
    },
    heading: {
      fontSize: '16px',
      textAlign: 'center',
      padding: '1',
      background: 'blue.200'
    }
  }

  const showGridView = () => {
    setViewType('grid')
  }
  const showTableView = () => {
    setViewType('table')
  }
  const columnHelper = createColumnHelper()
  const mainColumns = useMemo(
    () => [
      columnHelper.accessor((row) => <ProxyMenu row={row} isDesktop={isDesktop} />, {
        cell: (info) => info.getValue(),
        id: 'options',
        header: () => {
          return (
            <Tooltip label='Show grid view'>
              <IconButton onClick={showGridView} size='small' icon={<HiViewGrid />} />
            </Tooltip>
          )
        }
      }),
      columnHelper.accessor((row) => row.id, {
        cell: (info) => info.getValue(),
        header: 'ID'
      }),
      columnHelper.accessor((row) => row.host, {
        cell: (info) => info.getValue(),
        header: 'Server'
      }),
      columnHelper.accessor((row) => row.state, {
        cell: (info) => info.getValue(),
        header: 'Status'
      }),
      columnHelper.accessor((row) => row.port, {
        cell: (info) => info.getValue(),
        header: 'Port'
      }),
      columnHelper.accessor((row) => row.version, {
        cell: (info) => info.getValue(),
        header: 'Version'
      }),
      columnHelper.accessor((row) => row.writePort, {
        cell: (info) => info.getValue(),
        header: 'Write Port'
      }),
      columnHelper.accessor((row) => row.writerHostGroup, {
        cell: (info) => info.getValue(),
        header: 'Writer HG'
      }),
      columnHelper.accessor((row) => row.readPort, {
        cell: (info) => info.getValue(),
        header: 'Read Port'
      }),
      columnHelper.accessor((row) => row.readerHostGroup, {
        cell: (info) => info.getValue(),
        header: 'Reader HG'
      }),
      columnHelper.accessor((row) => row.readWritePort, {
        cell: (info) => info.getValue(),
        header: 'Read Write Port'
      })
    ],
    []
  )

  const nestedColumns = useMemo(
    () => [
      columnHelper.accessor((row) => row.prxName, {
        cell: (info) => info.getValue(),
        header: 'DB Name'
      }),
      columnHelper.accessor((row) => row.status, {
        cell: (info) => info.getValue(),
        header: 'DB Status'
      }),
      columnHelper.accessor((row) => row.prxStatus, {
        cell: (info) => info.getValue(),
        header: 'PX Status'
      }),
      columnHelper.accessor((row) => row.prxConnections, {
        cell: (info) => info.getValue(),
        header: 'Connections'
      }),
      columnHelper.accessor((row) => row.prxByteOut, {
        cell: (info) => info.getValue(),
        header: 'Bytes Out'
      }),
      columnHelper.accessor((row) => row.prxByteIn, {
        cell: (info) => info.getValue(),
        header: 'Bytes In'
      }),
      columnHelper.accessor((row) => row.prxLatency, {
        cell: (info) => info.getValue(),
        header: 'Sess Time'
      }),
      columnHelper.accessor((row) => row.prxHostgroup, {
        cell: (info) => info.getValue(),
        header: 'Id Group'
      })
    ],
    []
  )
  return (
    <Box sx={styles.tableContainer}>
      <DataTable data={proxies} columns={mainColumns} nestedColumns={nestedColumns} />
    </Box>
  )
}

export default ProxyInfoTable
