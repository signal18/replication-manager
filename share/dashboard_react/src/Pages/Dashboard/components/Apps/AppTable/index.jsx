import { useMemo } from 'react'
import PropTypes from 'prop-types'
import { DataTable } from '../../../../../components/DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, Text, Tooltip } from '@chakra-ui/react'
import AppMenu from '../AppMenu'
import styles from './styles.module.scss'
import { Link } from 'react-router-dom'
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
      columnHelper.accessor((row) => row.host, {
        cell: ({ row }) => {
          const name = row.original.host || row.original.name || row.original.id || '';

          return (
            <Tooltip label={name} placement="top" hasArrow openDelay={400}>
              <Link to={`/clusters/${clusterName}/apps/${row.original?.id}`} className={styles.appLink}>
                <Box className={styles.appCell}>
                  <Text className={styles.appName}>
                    {name}
                  </Text>
                </Box>
              </Link>
            </Tooltip>
          );
        },
        header: 'Apps',
        minWidth: '280px'
      }),
      columnHelper.accessor((row) => row.state, {
        cell: ({ row }) => <ServerStatus state={row.original.state} />,
        header: 'Status',
        width: '100px'
      }),
      columnHelper.accessor((row) => row.config?.provAppDockerImg, {
        cell: (info) => {
          const val = info.getValue() || '';
          if (!val) return null;
          return (
            <Tooltip label={val} placement="top" hasArrow openDelay={400}>
              <Text fontSize="xs" color="gray.600" whiteSpace="nowrap" overflow="hidden" textOverflow="ellipsis" maxW="200px">
                {val}
              </Text>
            </Tooltip>
          );
        },
        header: 'Docker Image',
        minWidth: '220px'
      }),
      columnHelper.accessor((row) => row.routeStatus, {
        cell: ({ row }) => (
          <RouteSummary
            routeStatuses={row.original.routeStatus}
            configuredRouteCount={row.original.config?.deployment?.routes?.length ?? null}
            compact
          />
        ),
        header: 'Routes',
        minWidth: '190px'
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

AppTable.propTypes = {
  apps: PropTypes.array,
  isDesktop: PropTypes.bool,
  clusterName: PropTypes.string,
  user: PropTypes.object,
  orchestrator: PropTypes.string,
  showGridView: PropTypes.func,
}
