import { useMemo } from 'react'
import { DataTable } from '../../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box } from '@chakra-ui/react'
import ProxyMenu from '../ProxyMenu'
import { HiViewGrid } from 'react-icons/hi'
import TagPill from '../../../../../components/TagPill'
import ServerStatus from '../../../../../components/ServerStatus'
import ProxyLogo from '../ProxyLogo'
import ProxyStatus from '../ProxyStatus'
import RMIconButton from '../../../../../components/RMIconButton'
import styles from './styles.module.scss'
import ServerName from '../../../../../components/ServerName'

const columnHelper = createColumnHelper()

const buildReadWriteRow = (proxy, data, readWriteType, isNewProxy) => ({
  logo: isNewProxy && <ProxyLogo proxyName={proxy.type} />,
  proxyId: proxy.id,
  isStaging: proxy.isStaging,
  showMenu: isNewProxy,
  server: `${proxy.host}:${data.port}`,
  status: <ProxyStatus status={proxy.state} />,
  group: <TagPill text={readWriteType} colorScheme={readWriteType === 'WRITE' ? 'blue' : 'gray'} />,
  dbName: `${data.prxName}`,
  dbStatus: <ServerStatus state={data.status} />,
  pxStatus: data.prxStatus,
  connections: data.prxConnections,
  bytesOut: data.prxByteOut,
  bytesIn: data.prxByteIn,
  sessTime: data.prxLatency,
  idGroup: data.prxHostgroup
})

function ProxyTable({
  proxies = [],
  isDesktop,
  clusterName,
  showGridView,
  user,
  isMenuOptionsVisible,
  showTerminal,
  topoStaging,
  orchestrator
}) {
  const tableData = useMemo(() => {
    const data = []
    proxies.forEach((proxy) => {
      // menuShown tracks whether the menu/logo row has been emitted for this proxy.
      // The menu appears exactly once — on the very first row regardless of whether
      // that row is a write or read backend.
      let menuShown = false
      const isFirst = () => {
        if (menuShown) return false
        menuShown = true
        return true
      }
      proxy.backendsWrite?.forEach((writeData) => {
        data.push(buildReadWriteRow(proxy, writeData, 'WRITE', isFirst()))
      })
      proxy.backendsRead?.forEach((readData) => {
        data.push(buildReadWriteRow(proxy, readData, 'READ', isFirst()))
      })
      if (!proxy.backendsRead?.length && !proxy.backendsWrite?.length) {
        data.push({
          logo: <ProxyLogo proxyName={proxy.type} />,
          isStaging: proxy.isStaging,
          proxyId: proxy.id,
          showMenu: true,
          server: `${proxy.host}:${proxy.port}`,
          status: <ProxyStatus status={proxy.state} />
        })
      }
    })
    return data
  }, [proxies])

  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) =>
          row.showMenu && (
            <ProxyMenu
              row={row}
              isDesktop={isDesktop}
              clusterName={clusterName}
              user={user}
              isMenuOptionsVisible={isMenuOptionsVisible}
              showTerminal={showTerminal}
              topoStaging={topoStaging}
              orchestrator={orchestrator}
            />
          ),
        {
          cell: (info) => info.getValue(),
          id: 'options',
          header: () => {
            return <RMIconButton onClick={showGridView} icon={HiViewGrid} tooltip='Show grid view' />
          },
          width: '40px'
        }
      ),
      columnHelper.accessor((row) => row.logo, {
        cell: (info) => info.getValue(),
        id: 'logo',
        header: '',
        width: '40px'
      }),
      columnHelper.accessor((row) => row.proxyId, {
        cell: (info) => info.getValue(),
        header: 'Proxy Id',
        id: 'proxyId',
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
        header: 'Proxy Status'
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
    [clusterName, isDesktop, isMenuOptionsVisible, orchestrator, showGridView, showTerminal, topoStaging, user]
  )

  return (
    <Box className={styles.tableContainer}>
      <DataTable data={tableData} columns={columns} />
    </Box>
  )
}

export default ProxyTable
