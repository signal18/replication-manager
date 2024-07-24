import React, { useMemo, useEffect, useState } from 'react'
import { DataTable } from '../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, Heading, IconButton, Tooltip } from '@chakra-ui/react'
import ProxyMenu from './ProxyMenu'
import { HiExclamation, HiViewGrid } from 'react-icons/hi'
import CheckOrCrossIcon from '../../../../components/Icons/CheckOrCrossIcon'
import CustomIcon from '../../../../components/Icons/CustomIcon'
import TagPill from '../../../../components/TagPill'
import ServerStatus from '../../../../components/ServerStatus'
import ProxyLogo from './ProxyLogo'
import ProxyStatus from './ProxyStatus'

function ProxyTable({ proxies, isDesktop, clusterName, showGridView }) {
  const [tableData, setTableData] = useState([])
  useEffect(() => {
    if (proxies?.length > 0) {
      const data = []
      proxies.forEach((proxy) => {
        let isNewProxy = false
        proxy.backendsWrite?.forEach((writeData, index) => {
          isNewProxy = index === 0
          data.push(readWriteData(proxy, writeData, 'WRITE', isNewProxy))
        })
        proxy.backendsRead?.forEach((readData, index) => {
          isNewProxy = !isNewProxy && index === 0
          data.push(readWriteData(proxy, readData, 'READ', isNewProxy))
        })
      })
      setTableData(data)
    }
  }, [proxies])

  const readWriteData = (proxy, data, readWriteType, isNewProxy) => {
    return {
      logo: isNewProxy && <ProxyLogo proxyName={proxy.type} />,
      proxyId: proxy.id,
      showMenu: isNewProxy,
      server: isNewProxy ? `${proxy.host}:${proxy.port}` : '',
      status: isNewProxy ? <ProxyStatus status={proxy.state} /> : '',
      group: <TagPill text={readWriteType} />,
      dbName: `${data.prxName}:${data.port}`,
      dbStatus: <ServerStatus state={data.status} />,
      pxStatus: data.prxStatus,
      connections: data.prxConnections,
      bytesOut: data.prxByteOut,
      bytesIn: data.prxByteIn,
      sessTime: data.prxLatency,
      idGroup: data.prxHostgroup
    }
  }
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

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) => row.showMenu && <ProxyMenu row={row} isDesktop={isDesktop} clusterName={clusterName} />,
        {
          cell: (info) => info.getValue(),
          id: 'options',
          header: () => {
            return (
              <Tooltip label='Show grid view'>
                <IconButton onClick={showGridView} size='small' icon={<HiViewGrid />} />
              </Tooltip>
            )
          }
        }
      ),
      columnHelper.accessor((row) => row.logo, {
        cell: (info) => info.getValue(),
        id: 'logo',
        header: ''
      }),
      columnHelper.accessor((row) => row.proxyId, {
        cell: (info) => info.getValue(),
        header: 'Proxy Id',
        id: 'proxyId',
        enableHiding: true
      }),
      columnHelper.accessor((row) => row.server, {
        cell: (info) => info.getValue(),
        header: 'Server'
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
        header: 'DB Name'
      }),
      columnHelper.accessor((row) => row.dbStatus, {
        cell: (info) => info.getValue(),
        header: 'DB Status'
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
    <Box sx={styles.tableContainer}>
      <DataTable data={tableData} columns={columns} />
    </Box>
  )
}

export default ProxyTable
