import { useMemo } from 'react'
import PropTypes from 'prop-types'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, HStack, VStack } from '@chakra-ui/react'
import { DataTable } from '../../../components/DataTable'
import TableType3 from '../../../components/TableType3'
import RMIconButton from '../../../components/RMIconButton'
import { HiDownload, HiPause, HiPlay, HiTrash } from 'react-icons/hi'
import styles from '../styles.module.scss'

const columnHelper = createColumnHelper()

const getResticTaskType = (rtt) => {
  switch (rtt) {
    case 0:
      return 'init'
    case 1:
      return 'fetch'
    case 2:
      return 'backup'
    case 3:
      return 'purge'
    case 4:
      return 'unlock'
    case 5:
      return 'changepass'
    default:
      return 'Unknown'
  }
}

const renderResticTaskDetail = (row) => {
  switch (row.task_type) {
    case 2:
      return (
        <VStack>
          <HStack>
            <Box>Path:</Box>
            <Box>{row.dir_path}</Box>
          </HStack>
          <HStack>
            <Box>Tags:</Box>
            <Box>{row.tags?.join(', ')}</Box>
          </HStack>
        </VStack>
      )
    case 3:
      return (
        <VStack>
          <HStack>
            <Box>Options:</Box>
            <Box>{JSON.stringify(row.opt)}</Box>
          </HStack>
        </VStack>
      )
    default:
      return <div>-</div>
  }
}

function SnapshotTables({
  snapshotData,
  snapshotStats,
  queueData,
  isQueuePaused,
  onConfirmAction = () => { }
}) {
  const snapshotColumns = useMemo(
    () => [
      columnHelper.accessor((row) => row.short_id, {
        header: 'ID',
        id: 'id'
      }),
      columnHelper.accessor((row) => row.time, {
        header: 'Time'
      }),
      columnHelper.accessor((row) => row.paths?.join(','), {
        header: 'Path'
      }),
      columnHelper.accessor((row) => row.hostname, {
        header: 'Hostname'
      }),
      columnHelper.accessor((row) => row.tags?.join(','), {
        header: 'Tags'
      }),
      columnHelper.accessor(
        (row) => {
          const restorePaths = Array.isArray(row.paths) ? row.paths : row.paths ? [row.paths] : []
          const basePath = restorePaths.length === 1 && typeof restorePaths[0] === 'string'
            ? restorePaths[0].trim()
            : ''
          const targetDir = basePath || '/tmp/restic-restore'

          return (
            <HStack spacing={2}>
              <RMIconButton
                icon={HiDownload}
                tooltip="Restore snapshot"
                onClick={() =>
                  onConfirmAction('Restore Snapshot', {
                    action: 'snapshotRestore',
                    data: {
                      snapshotId: row.id,
                      targetDir,
                      basePath,
                      basePaths: restorePaths,
                      paths: []
                    }
                  })
                }
              />
              <RMIconButton
                icon={HiTrash}
                tooltip="Purge snapshot"
                onClick={() =>
                  onConfirmAction('Purge Snapshot', {
                    action: 'snapshotPurge',
                    data: { snapshotId: row.id }
                  })
                }
              />
            </HStack>
          )
        },
        {
          cell: (info) => info.getValue(),
          header: 'Actions',
          id: 'actions'
        }
      )
    ],
    [onConfirmAction]
  )

  const queueColumns = useMemo(
    () => [
      columnHelper.accessor((row) => row.task_id, {
        header: 'ID',
        id: 'task_id'
      }),
      columnHelper.accessor((row) => getResticTaskType(row.task_type), {
        header: 'Task Type'
      }),
      columnHelper.accessor((row) => renderResticTaskDetail(row), {
        header: 'Details',
        cell: (info) => info.getValue(),
        id: 'details',
        minWidth: 200
      }),
      columnHelper.accessor(
        (row) => (
          <RMIconButton
            icon={HiTrash}
            onClick={() =>
              onConfirmAction('Cancel Queued Task', {
                action: 'queueCancel',
                data: { taskId: row.task_id }
              })
            }
          />
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Actions',
          id: 'actions'
        }
      )
    ],
    [onConfirmAction]
  )

  const snapshotDataStats = [
    {
      key: 'Total Size',
      value: snapshotStats?.total_size
    },
    {
      key: 'Total File Count',
      value: snapshotStats?.total_file_count
    },
    {
      key: 'Total Blob Count',
      value: snapshotStats?.total_blob_count
    }
  ]

  const queueLength = queueData?.length || 0
  const queueDataHeader = [
    {
      key: 'Total Pending Tasks',
      value: queueLength
    },
    {
      key: 'Queue Status',
      value: isQueuePaused ? 'Paused' : 'Running'
    },
    {
      key: 'Action',
      value: isQueuePaused ? (
        <RMIconButton icon={HiPlay} onClick={() => onConfirmAction('Resume Restic Queue', { action: 'queueResume' })} />
      ) : (
        <RMIconButton icon={HiPause} onClick={() => onConfirmAction('Pause Restic Queue', { action: 'queuePause' })} />
      )
    }
  ]

  return (
    <VStack className={styles.snapshotContainer}>
      <TableType3 dataArray={snapshotDataStats} className={styles.statsTable} />
      <DataTable key="snapshot" data={snapshotData} columns={snapshotColumns} className={styles.table} />
      <TableType3 dataArray={queueDataHeader} className={styles.statsTable} />
      <DataTable key="queue" data={queueData} columns={queueColumns} className={styles.table} />
    </VStack>
  )
}

export default SnapshotTables

SnapshotTables.propTypes = {
  snapshotData: PropTypes.arrayOf(
    PropTypes.shape({
      short_id: PropTypes.string,
      time: PropTypes.string,
      paths: PropTypes.oneOfType([PropTypes.arrayOf(PropTypes.string), PropTypes.string]),
      hostname: PropTypes.string,
      tags: PropTypes.arrayOf(PropTypes.string),
      id: PropTypes.string
    })
  ),
  snapshotStats: PropTypes.shape({
    total_size: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    total_file_count: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    total_blob_count: PropTypes.oneOfType([PropTypes.string, PropTypes.number])
  }),
  queueData: PropTypes.arrayOf(
    PropTypes.shape({
      task_id: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
      task_type: PropTypes.number,
      dir_path: PropTypes.string,
      tags: PropTypes.arrayOf(PropTypes.string),
      opt: PropTypes.object
    })
  ),
  isQueuePaused: PropTypes.bool,
  onConfirmAction: PropTypes.func
}
