import React, { useMemo } from 'react'
import { createColumnHelper } from '@tanstack/react-table'
import { Box, HStack, VStack } from '@chakra-ui/react'
import { DataTable } from '../../../components/DataTable'
import TableType3 from '../../../components/TableType3'
import RMIconButton from '../../../components/RMIconButton'
import { HiPause, HiPlay, HiTrash } from 'react-icons/hi'
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
        (row) => (
          <RMIconButton
            icon={HiTrash}
            onClick={() =>
              onConfirmAction('Purge Snapshot', {
                action: 'snapshotPurge',
                data: { snapshotId: row.id }
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
