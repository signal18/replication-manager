import { useMemo } from 'react'
import { DataTable } from '../../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box } from '@chakra-ui/react'
import AppMenu from '../AppMenu'
import styles from './styles.module.scss'
import { Link } from 'react-router-dom'
import ServerName from '../../../../../components/ServerName'
import ServerStatus from '../../../../../components/ServerStatus'
import RMIconButton from '../../../../../components/RMIconButton'
import { HiViewGrid } from 'react-icons/hi'
import RouteSummary from '../RouteSummary'

const columnHelper = createColumnHelper()

function AppTable({ apps = [], isDesktop, clusterName, user, orchestrator, showGridView }) {
  const columns = useMemo(
    () => [
      columnHelper.accessor(
        (row) => row.name || row.id,
        {
          cell: ({row}) => (
            <AppMenu
              row={row.original}
              isDesktop={isDesktop}
              clusterName={clusterName}
              user={user}
              orchestrator={orchestrator}
            />
          ),
          id: 'options',
          header: () => <RMIconButton onClick={showGridView} icon={HiViewGrid} tooltip='Show grid view' />,
          width: '40px'
        }
      ),
      columnHelper.accessor((row) => (<Link to={`/clusters/${clusterName}/apps/${row?.id}`}>
        <ServerName name={`${row.host}`} />
      </Link>), {
        cell: (info) => info.getValue(),
        header: 'Apps'
      }),
      columnHelper.accessor((row) => (<ServerStatus state={row.state} />), {
        cell: (info) => info.getValue(),
        header: 'Status'
      }),
      columnHelper.accessor((row) => row.config?.provAppDockerImg, {
        cell: (info) => info.getValue(),
        header: 'Docker Image'
      }),
      columnHelper.accessor((row) => (<RouteSummary routeStatuses={row.routeStatus} configuredRouteCount={row.config?.deployment?.routes?.length ?? null} showIdentity />), {
        cell: (info) => info.getValue(),
        header: 'Routes'
      }),
    ],
    [clusterName, isDesktop, orchestrator, user, showGridView]
  )

  return (
    <Box className={styles.tableContainer}>
      <DataTable data={apps} columns={columns} />
    </Box>
  )
}

export default AppTable
