import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { sizeOf, convertObjectToArray, formatBytes, formatDate, getBackupMethod, getBackupStrategy } from '../../utility/common'
import AccordionComponent from '../../components/AccordionComponent'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { Box, HStack, Progress, useDisclosure, VStack } from '@chakra-ui/react'
import TableType3 from '../../components/TableType3'
import { useDispatch, useSelector } from 'react-redux'
import { TaskLogs } from '../Dashboard/components/Logs'
import DatabaseJobs from './DatabaseJobs'
import { deleteBackup, purgeResticSnapshot, resticQueueCancel, resticQueueMove, resticQueuePause, resticQueueResume } from '../../redux/clusterSlice'
import RMIconButton from '../../components/RMIconButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { HiCog, HiPause, HiPlay, HiTrash } from 'react-icons/hi'
import { showWarningToast } from '../../redux/toastSlice'

const QueueMoveForm = React.memo(({ list = [], currentId, onChange = (dir, afterId) => { } }) => {
  const [direction, setDirection] = useState('first');

  const handleDirectionChange = (e) => {
    setDirection(e.target.value);
    if (e.target.value !== 'after') {
      onChange(e.target.value, null);
    }
  };

  const handleAfterChange = (e) => {
    onChange('after', e.target.value);
  };
  return (
    <VStack>
      <HStack>
        <Box>Move {direction}:</Box>
        <Select onChange={handleDirectionChange} value={direction}>
          <option value="first">First</option>
          <option value="before">After</option>
          <option value="last">Last</option>
        </Select>
      </HStack>
      {direction === 'after' && (
        <HStack>
          <Box>Move After:</Box>
          <Select onChange={handleAfterChange}>
            {list.filter(item => item.task_id !== currentId).map(item => (
              <option key={item.task_id} value={item.task_id}>Task ID #{item.task_id}</option>
            ))}
          </Select>
        </HStack>
      )}
    </VStack>
  )
})

const formConfig = {
  queueMove: {
    component: QueueMoveForm,
    getProps: (ctx) => ({
      list: ctx.queueData,
      currentId: ctx.payload.data.taskId,
      onChange: ctx.handleMove
    })
  }
};

const DynamicForm = (ctx) => {
  const { action } = ctx.payload;

  const entry = formConfig[action];
  if (!entry) return null;

  const Component = entry.component;
  const props = entry.getProps(ctx);

  return <Component {...props} />;
};

const resticTaskType = (rtt) => {
  switch (rtt) {
    case 0:
      return "init"
    case 1:
      return "fetch"
    case 2:
      return "backup"
    case 3:
      return "purge"
    case 4:
      return "unlock"
    case 5:
      return "changepass"
    case 6:
      return "restore"
    case 7:
      return "check"
    default:
      return "Unknown"
  }
}

const resticTaskDetail = (row) => {
  switch (row.task_type) {
    case 2:
      return (<VStack>
        <HStack>
          <Box>Path:</Box>
          <Box>{row.dir_path}</Box>
        </HStack>
        <HStack>
          <Box>Tags:</Box>
          <Box>{row.tags?.join(', ')}</Box>
        </HStack>
      </VStack>)
    case 3:
      return (<VStack>
        <HStack>
          <Box>Options:</Box>
          <Box>{JSON.stringify(row.opt)}</Box>
        </HStack>
      </VStack>)
    default:
      return (<div>-</div>)
  }
}


// section: undefined = full page, 'backup' = backup accordions only, 'jobs' = jobs accordion only
function Maintenance({ selectedCluster, user, section, onOpenBackupSettings, onOpenSchedulerSettings, onOpenLogsSettings }) {
  const [data, setData] = useState([])
  const [snapshotData, setSnapshotData] = useState([])
  const [queueData, setQueueData] = useState([])
  const [confirmState, setConfirmState] = useState({ isOpen: false, title: '', payload: null })
  const { isOpen: isConfirmModalOpen, title, payload } = confirmState

  const dispatch = useDispatch()
  const columnHelper = createColumnHelper()
  const { isOpen: isBackupsOpen, onToggle: onBackupsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupsOpen')) === false ? false : true
  })
  const { isOpen: isBackupSnapshotOpen, onToggle: onBackupSnapshotToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupSnapshotOpen')) || false
  })
  const { isOpen: isDBJobsOpen, onToggle: onDBJobsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isDBJobsOpen')) || false
  })
  const { isOpen: isLogsOpen, onToggle: onLogsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isLogsInBackupOpen')) || false
  })

  const list = useSelector((state) => state.cluster.backups.list)
  const backupStats = useSelector((state) => state.cluster.backups.stats)

  const snapshots = useSelector((state) => state.cluster.restic.snapshots)
  const stats = useSelector((state) => state.cluster.restic.stats)
  const resticQueue = useSelector((state) => state.cluster.restic.queue)
  const currentResticTask = useSelector((state) => state.cluster.restic.currentTask)
  const resticRepoPath = useSelector((state) => state.cluster.restic.repoPath)

  const openConfirmModal = (title, payload) => {
    setConfirmState({ isOpen: true, title, payload })
  }

  const closeConfirmModal = () => {
    setConfirmState({ isOpen: false, title: '', payload: null })
  }

  const handleConfirm = () => {
    if (payload && payload.action) {
      switch (payload.action) {
        case 'backupDelete':
          dispatch(deleteBackup({ clusterName: selectedCluster.name, backupId: payload.data.backupId }))
          break
        case 'snapshotPurge':
          dispatch(purgeResticSnapshot({ clusterName: selectedCluster.name, snapshotId: payload.data.snapshotId }))
          break
        case 'queueCancel':
          dispatch(resticQueueCancel({ clusterName: selectedCluster.name, taskId: payload.data.taskId }))
          break
        case 'queueMove':
          dispatch(resticQueueMove({ clusterName: selectedCluster.name, taskId: payload.data.taskId, direction: payload.data.direction, afterId: payload.data.afterId }))
          break
        case 'queuePause':
          dispatch(resticQueuePause({ clusterName: selectedCluster.name }));
          break
        case 'queueResume':
          dispatch(resticQueueResume({ clusterName: selectedCluster.name }));
          break
        default:
          dispatch(showWarningToast({ title: 'Unknown action', description: `The action ${payload.action} is not recognized.` }))
          break
      }
    }

    closeConfirmModal()
  }

  const handleMove = (direction, afterId) => {
    setConfirmState((prevState) => ({
      ...prevState,
      payload: {
        ...prevState.payload,
        data: {
          ...prevState.payload.data,
          direction,
          afterId
        }
      }
    }))
  }

  useEffect(() => {
    localStorage.setItem('isBackupSnapshotOpen', JSON.stringify(isBackupSnapshotOpen))
  }, [isBackupSnapshotOpen])
  useEffect(() => {
    localStorage.setItem('isDBJobsOpen', JSON.stringify(isDBJobsOpen))
  }, [isDBJobsOpen])

  useEffect(() => {
    localStorage.setItem('isLogsInBackupOpen', JSON.stringify(isLogsOpen))
  }, [isLogsOpen])
  useEffect(() => {
    localStorage.setItem('isBackupsOpen', JSON.stringify(isBackupsOpen))
  }, [isBackupsOpen])

  useEffect(() => {
    if (list) {
      const arrData = convertObjectToArray(list)
      setData(arrData.reverse())
    } else {
      setData([])
    }
  }, [selectedCluster?.name, list])

  useEffect(() => {
    if (snapshots?.length > 0) {
      setSnapshotData(snapshots)
    } else {
      setSnapshotData([])
    }
  }, [selectedCluster?.name, snapshots])

  useEffect(() => {
    if (resticQueue?.length > 0) {
      const arrData = convertObjectToArray(resticQueue)
      setQueueData(arrData.reverse())
    } else {
      setQueueData([])
    }
  }, [selectedCluster?.name, resticQueue])

  const backupDataStats = [
    {
      key: 'Total Size',
      value: sizeOf(backupStats?.total_size)
    },
    {
      key: 'Total File Count',
      value: backupStats?.total_file_count
    },
    {
      key: 'Total Blob Count',
      value: backupStats?.total_blob_count
    }
  ]

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.id, {
        cell: (info) => info.getValue(),
        header: 'ID',
        id: 'id'
      }),
      columnHelper.accessor(
        (row) => (
          <>
            {formatDate(row.startTime)} <br />
            {formatDate(row.endTime)}
          </>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Start - End Time',
          id: 'startendTime',
          minWidth: 160
        }
      ),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5}>
            <Box className={styles.cellValue}>{getBackupMethod(row.backupMethod)}</Box>
            <Box className={styles.cellValue}>{row.backupTool}</Box>
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Backup Method / Tool',
          id: 'backupMethod'
        }
      ),
      columnHelper.accessor((row) => getBackupStrategy(row.backupStrategy), {
        cell: (info) => info.getValue(),
        header: 'Strategy',
        id: 'strategy'
      }),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5}>
            <Box className={styles.cellValue}>{row.source}</Box>
            <Box className={styles.cellValue}>{row.dest}</Box>
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Source - Dest',
          id: 'srcDest'
        }
      ),
      columnHelper.accessor((row) => formatBytes(row.size), {
        cell: (info) => info.getValue(),
        header: 'Backup Size',
        id: 'backupSize',
        minWidth: 100
      }),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5}>
            <Box className={styles.cellValue}>{`Compressed: ${row.compressed ? 'Yes' : 'No'}`}</Box>
            <Box className={styles.cellValue}>{`Encrypted: ${row.encrypted ? 'Yes' : 'No'}`}</Box>
            {row.encrypted && (
              <>
                <Box className={styles.cellValue}>{row.encryptionAlgo}</Box>
                <Box className={styles.cellValue}>{row.encryptionKey}</Box>
              </>
            )}
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Compression / Encryption',
          id: 'compressionEncryption'
        }
      ),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack} spacing={0.5}>
            <Box className={styles.cellValue}>{`File: ${row.binLogFileName} / Pos: ${row.binLogFilePos}`}</Box>
            <Box className={styles.cellValue}>{`GTID: ${row.binLogUuid}`}</Box>
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'BinLog Info',
          id: 'binLogInfo'
        }
      ),
      columnHelper.accessor((row) => (row.completed ? 'Completed' : 'Pending'), {
        cell: (info) => info.getValue(),
        header: 'Status',
        id: 'status'
      }),
      columnHelper.display({
        id: 'actions',
        header: 'Actions',
        cell: (info) => (
          <RMIconButton
            icon={HiTrash}
            colorScheme='red'
            tooltip='Delete backup'
            onClick={() => openConfirmModal('Do you want to delete this backup?', { action: 'backupDelete', data: { backupId: info.row.original.id } })}
            isDisabled={!user?.grants['db-backup']}
          />
        )
      })
    ]
  )

  const snapshotDataStats = [
    {
      key: 'Total Size',
      value: sizeOf(stats?.total_size)
    },
    {
      key: 'Total File Count',
      value: stats?.total_file_count
    },
    {
      key: 'Total Blob Count',
      value: stats?.total_blob_count
    }
  ]

  const snapshotColumns = useMemo(() => [
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
    // Added Purge action column
    columnHelper.display({
      id: 'actions',
      header: 'Actions',
      cell: (info) => (
        <RMIconButton
          icon={HiTrash}
          tooltip='Purge snapshot'
          onClick={() => openConfirmModal('Do you want to purge this snapshot?', { action: 'snapshotPurge', data: { snapshotId: info.row.original.id } })}
          isDisabled={!user?.grants['cluster-process']}
        />
      )
    })
  ])

  const queuelength = queueData?.length || 0;

  const queueDataHeader = useMemo(() =>[
    {
      key: 'Total Pending Tasks',
      value: queuelength
    },
    {
      key: 'Queue Status',
      value: selectedCluster?.isResticQueuePaused ? 'Paused' : 'Running'
    },
    {
      key: 'Action',
      value: (selectedCluster?.isResticQueuePaused ?
        <RMIconButton icon={HiPlay} tooltip='Resume queue' isDisabled={!user?.grants['cluster-process']} onClick={() => openConfirmModal('Resume Restic Queue', { action: 'queueResume' })} /> :
        <RMIconButton icon={HiPause} tooltip='Pause queue' isDisabled={!user?.grants['cluster-process']} onClick={() => openConfirmModal('Pause Restic Queue', { action: 'queuePause' })} />
      )
    }
  ], [selectedCluster?.isResticQueuePaused, queuelength])

  const currentTaskHeader = useMemo(() => {
    if (!currentResticTask) {
      return []
    }

    const isBackupTask = currentResticTask.task_type === 2
    const percentDone = (() => {
      if (!isBackupTask || typeof currentResticTask.percent_done !== 'number') {
        return 'Running'
      }

      const clamped = Math.min(Math.max(currentResticTask.percent_done, 0), 1)
      const percentValue = Math.round(clamped * 100)

      return (
        <HStack spacing={3} alignItems="center">
          <Progress value={percentValue} size="sm" flex="1" />
          <Box minWidth="3.5rem" textAlign="right">{percentValue}%</Box>
        </HStack>
      )
    })()
    const bytesValue = currentResticTask.total_bytes
      ? `${formatBytes(currentResticTask.bytes_done || 0)} / ${formatBytes(currentResticTask.total_bytes)}`
      : '-'
    const filesValue = currentResticTask.total_files
      ? `${currentResticTask.files_done || 0} / ${currentResticTask.total_files}`
      : '-'
    const startedAt = currentResticTask.started_at ? formatDate(currentResticTask.started_at) : '-'
    const completedAt = currentResticTask.completed_at ? formatDate(currentResticTask.completed_at) : '-'
    const duration = currentResticTask.total_duration
      ? `${Math.round(currentResticTask.total_duration)}s`
      : currentResticTask.seconds_elapsed
        ? `${currentResticTask.seconds_elapsed}s`
        : '-'

    return [
      {
        key: 'Task ID',
        value: currentResticTask.task_id || '-'
      },
      {
        key: 'Task Type',
        value: resticTaskType(currentResticTask.task_type)
      },
      {
        key: 'Status',
        value: currentResticTask.status || '-'
      },
      {
        key: 'Progress',
        value: percentDone
      },
      {
        key: 'Bytes',
        value: bytesValue
      },
      {
        key: 'Files',
        value: filesValue
      },
      {
        key: 'Snapshot ID',
        value: currentResticTask.snapshot_id || '-'
      },
      {
        key: 'Duration',
        value: duration
      },
      {
        key: 'Started',
        value: startedAt
      },
      {
        key: 'Completed',
        value: completedAt
      }
    ]
  }, [currentResticTask])

  const queueColumns = useMemo(() => [
    columnHelper.accessor((row) => row.task_id, {
      header: 'ID',
      id: 'task_id'
    }),
    columnHelper.accessor((row) => resticTaskType(row.task_type), {
      header: 'Task Type'
    }),
    columnHelper.accessor((row) => resticTaskDetail(row), {
      header: 'Details',
      cell: (info) => info.getValue(),
      id: 'details',
      minWidth: 200
    }),
    // Added cancel action column
    columnHelper.display({
      id: 'actions',
      header: 'Actions',
      cell: (info) => (
        <RMIconButton
          icon={HiTrash}
          tooltip='Cancel queued task'
          onClick={() => openConfirmModal('Cancel Queued Task', { action: 'queueCancel', data: { taskId: info.row.original.task_id } })}
          isDisabled={!user?.grants['cluster-process']}
        />
      )
    })
  ])

  const settingsButton = (onClick, tooltip) =>
    onClick ? (
      <RMIconButton
        icon={HiCog}
        tooltip={tooltip}
        onClick={onClick}
        size='xs'
        variant='ghost'
      />
    ) : null

  const backupSection = (
    <>
      <AccordionComponent
        heading={'Current Backups'}
        isOpen={isBackupsOpen}
        onToggle={onBackupsToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        headerActions={settingsButton(onOpenBackupSettings, 'Open Backup Settings')}
        body={
          <VStack className={styles.snapshotContainer}>
            <TableType3 dataArray={backupDataStats} className={styles.statsTable} />
            <DataTable key="backups" data={data} columns={columns} className={styles.table} />
          </VStack>
        }
      />
      <AccordionComponent
        heading={'Backup Snapshots'}
        isOpen={isBackupSnapshotOpen}
        onToggle={onBackupSnapshotToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        headerActions={settingsButton(onOpenBackupSettings, 'Open Backup Settings')}
        body={
          <VStack className={styles.snapshotContainer}>
            <Box className={styles.repoRow}>
              <Box className={styles.repoRowLabel}>Repository:</Box>
              <Box className={styles.repoRowValue}>{resticRepoPath || '-'}</Box>
            </Box>
            <TableType3 dataArray={snapshotDataStats} className={styles.statsTable} />
            <DataTable key="snapshot" data={snapshotData} columns={snapshotColumns} className={styles.table} />
            <Box className={styles.sectionTitle}>Current Restic Task</Box>
            {currentResticTask ? (
              <TableType3 dataArray={currentTaskHeader} className={`${styles.statsTable} ${styles.currentTaskTable}`} />
            ) : (
              <Box className={styles.emptyState}>No active task</Box>
            )}
            <TableType3 dataArray={queueDataHeader} className={styles.statsTable} />
            <DataTable key="queue" data={queueData} columns={queueColumns} className={styles.table} />
          </VStack>
        }
      />
    </>
  )

  const jobsSection = (
    <AccordionComponent
      heading={'Database Jobs'}
      isOpen={isDBJobsOpen}
      onToggle={onDBJobsToggle}
      className={styles.accordion}
      headerClassName={styles.accordionHeader}
      panelClassName={styles.accordionPanel}
      headerActions={settingsButton(onOpenSchedulerSettings, 'Open Scheduler Settings')}
      body={<DatabaseJobs clusterName={selectedCluster?.name} user={user} />}
    />
  )

  const logsSection = (
    <AccordionComponent
      className={styles.accordion}
      isOpen={isLogsOpen}
      onToggle={onLogsToggle}
      headerClassName={styles.accordionHeader}
      panelClassName={styles.accordionPanel}
      heading={'Job Logs'}
      headerActions={settingsButton(onOpenLogsSettings, 'Open Log Settings')}
      body={<TaskLogs />}
    />
  )

  return (
    <VStack className={styles.backupContainer}>
      {(!section || section === 'backup') && backupSection}
      {(!section || section === 'jobs') && jobsSection}
      {!section && logsSection}
      {isConfirmModalOpen && <ConfirmModal title={title} isOpen={isConfirmModalOpen} body={<DynamicForm
        payload={payload}
        queueData={queueData}
        selectedCluster={selectedCluster}
        handleMove={handleMove}
      />} onConfirmClick={handleConfirm} closeModal={closeConfirmModal} />}
    </VStack>
  )
}

export default Maintenance
