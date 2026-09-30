import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { sizeOf, convertObjectToArray, formatBytes, formatDate, getBackupMethod, getBackupStrategy } from '../../utility/common'
import AccordionComponent from '../../components/AccordionComponent'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { Box, Flex, HStack, Progress, Slider, SliderFilledTrack, SliderThumb, SliderTrack, Tooltip, useDisclosure, VStack } from '@chakra-ui/react'
import TableType3 from '../../components/TableType3'
import { useDispatch, useSelector } from 'react-redux'
import { TaskLogs } from '../Dashboard/components/Logs'
import DatabaseJobs from './DatabaseJobs'
import { deleteBackup, purgeResticSnapshot, resticQueueCancel, resticQueueMove, resticQueuePause, resticQueueResume } from '../../redux/clusterSlice'
import RMIconButton from '../../components/RMIconButton'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { HiCog, HiPause, HiPlay, HiTrash, HiArchive, HiOutlineArchive, HiLockClosed, HiOutlineLockOpen, HiCheckCircle, HiClock, HiQuestionMarkCircle } from 'react-icons/hi'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { showWarningToast } from '../../redux/toastSlice'
import PropTypes from 'prop-types'
import { changePlanUnits } from '../../redux/settingsSlice'
import { Text } from '@chakra-ui/react'
import { getUnitRatios } from '../../utility/unitRatios'

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
    case 8:
      return "copy"
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


// BKUSlider is the backup plan bar, the BKU twin of the configurator's DBUSlider: the
// per-cluster reservation prov-db-bku on a linear 1..BKU_MAX scale. What the user must read
// at a glance is the GB LIMIT the plan gives them and how much of it is used: the plan in
// GB and the usage bar in GB against it. No pricing here: the product is not only a cloud
// offer; what a unit costs, when it does, is the marketplace's business (Resource Manager).
const BKU_MAX = 128
const bkuHelp = (unitGB) => `**Backup storage plan (BKU)**

1 BKU = ${unitGB} GB of local backup storage: the replication-manager backups kept on the infrastructure and the extra physical disk replicated for failover applications.

Exceeding the plan is monitored as over-commit and raises the alert WARN0219.`
function BKUSlider({ value, isDisabled, onChange, unitGB, bku }) {
  const [draft, setDraft] = useState(null)
  const [showTooltip, setShowTooltip] = useState(false)
  const [isHelpOpen, setIsHelpOpen] = useState(false)
  const plan = draft !== null ? draft : value
  const planGB = plan * unitGB
  const usedBytes = bku ? (bku.localBytes || 0) + (bku.appDiskBytes || 0) : 0
  const usedGB = usedBytes / (1024 * 1024 * 1024)
  const pct = planGB > 0 ? (usedGB / planGB) * 100 : 0
  const fmt = (n) => `${n} BKU = ${n * unitGB} GB of local backup storage`
  return (
    <Box w='100%'>
      <Flex justify='space-between' mb={1} align='start'>
        <HStack spacing={1}>
          <Text fontSize='sm' fontWeight='bold' color='var(--text-color)'>Backup storage plan (BKU) — per cluster</Text>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => setIsHelpOpen(true)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
        </HStack>
        <Text fontSize='sm' fontWeight='semibold' color='var(--text-color)'>plan {plan} BKU = {planGB} GB</Text>
      </Flex>
      <Slider
        min={1}
        max={BKU_MAX}
        step={1}
        value={plan}
        isDisabled={isDisabled}
        onChange={(v) => setDraft(v)}
        onMouseEnter={() => setShowTooltip(true)}
        onMouseLeave={() => setShowTooltip(false)}
        onChangeEnd={(v) => {
          setDraft(null)
          if (v !== value && onChange) onChange(v)
        }}
      >
        <SliderTrack h='8px' borderRadius='full' bg='gray.200'>
          <SliderFilledTrack bg='blue.400' />
        </SliderTrack>
        <Tooltip label={fmt(plan)} placement='top' isOpen={showTooltip || draft !== null} hasArrow>
          <SliderThumb boxSize={5} bg='blue.500' />
        </Tooltip>
      </Slider>
      <Flex justify='space-between' mt={1}>
        <Text fontSize='9px' color='gray.500'>1 BKU = {unitGB} GB</Text>
        <Text fontSize='9px' color='gray.500'>{BKU_MAX} BKU = {BKU_MAX * unitGB} GB</Text>
      </Flex>
      {bku && (
        <Box mt={2}>
          <Flex justify='space-between' mb={1}>
            <Text fontSize='sm' color='var(--text-color)'>
              Used {usedGB.toFixed(1)} GB of {planGB} GB ({pct.toFixed(0)}%) — backups {formatBytes(bku.localBytes || 0)}{bku.appDiskBytes > 0 ? `, failover application disks ${formatBytes(bku.appDiskBytes)}` : ''}
            </Text>
            <Text fontSize='sm' fontWeight='semibold' color={bku.overPlanUnits > 0 ? 'red.500' : 'var(--text-color)'}>
              {bku.overPlanUnits > 0
                ? `${bku.overPlanUnits * unitGB} GB over the plan (${bku.overPlanUnits} BKU), WARN0219 open`
                : bku.underPlanUnits > 0
                  ? `${bku.underPlanUnits * unitGB} GB left in the plan (${bku.underPlanUnits} BKU)`
                  : 'at the plan'}
            </Text>
          </Flex>
          <Progress value={Math.min(pct, 100)} size='sm' borderRadius='full' colorScheme={pct >= 100 ? 'red' : pct >= 80 ? 'orange' : 'blue'} />
        </Box>
      )}
      <CommonModal isOpen={isHelpOpen} closeModal={() => setIsHelpOpen(false)} title='Backup storage plan (BKU)' body={<Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{bkuHelp(unitGB)}</Markdown></Box>} size='xl' />
    </Box>
  )
}
BKUSlider.propTypes = {
  value: PropTypes.number,
  isDisabled: PropTypes.bool,
  onChange: PropTypes.func,
  unitGB: PropTypes.number,
  bku: PropTypes.object,
}

// section: undefined = full page, 'backup' = backup accordions only, 'jobs' = jobs accordion only
function Maintenance({ selectedCluster, user, section, onOpenBackupSettings, onOpenArchiveSettings, onOpenSchedulerSettings, onOpenLogsSettings }) {
  const [data, setData] = useState([])
  const [snapshotData, setSnapshotData] = useState([])
  const [queueData, setQueueData] = useState([])
  const [confirmState, setConfirmState] = useState({ isOpen: false, title: '', payload: null })
  // BKU plan (prov-db-bku): the per-cluster backup storage reservation, a PLAN not a resource,
  // so it lives with the backups, not among the configurator's resource gauges. The measured
  // side (backupUnits, every 30 ticks) is shown next to it; over the plan = billed.
  const [bkuConfirm, setBkuConfirm] = useState({ isOpen: false, title: '', delta: 0 })
  const clusterData = useSelector((state) => state.cluster?.clusterData)
  const bkuGB = getUnitRatios(clusterData).storage.diskGBPerUnit || 20
  const bku = selectedCluster?.backupUnits
  const bkuPlan = parseInt(selectedCluster?.config?.provDbBku) || 0
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
      const arrData = convertObjectToArray(list).reverse()
      const dataWithColor = arrData.map((item) => {
        const endTime = item.endTime ? new Date(item.endTime) : null
        const hasEnded = endTime && !isNaN(endTime.getTime()) && endTime.getFullYear() > 1
        return {
          ...item,
          rowColor: !item.completed && hasEnded ? 'red' : ''
        }
      })
      setData(dataWithColor)
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

  const backupSlotsTotal = selectedCluster?.backupSlotsTotal || 0
  const backupSlotsInUse = selectedCluster?.backupSlotsInUse || 0

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
    },
    {
      key: 'Free Backup Slots',
      value: backupSlotsTotal > 0 ? backupSlotsTotal - backupSlotsInUse : 'Unlimited'
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
            <Box className={styles.cellValue}>{getBackupStrategy(row.backupStrategy)}</Box>
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Backup Method',
          id: 'backupMethod'
        }
      ),
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
      columnHelper.accessor(
        (row) => (
          <HStack className={styles.iconRow} spacing={2}>
            <Tooltip label={row.completed ? 'Completed' : 'Pending'} hasArrow>
              <Box className={`${styles.statusIcon} ${row.completed ? styles.statusIconActive : styles.statusIconPending}`}>
                {row.completed ? <HiCheckCircle /> : <HiClock />}
              </Box>
            </Tooltip>
            <Tooltip label={row.compressed ? 'Compressed' : 'Not compressed'} hasArrow>
              <Box className={`${styles.statusIcon} ${row.compressed ? styles.statusIconActive : styles.statusIconInactive}`}>
                {row.compressed ? <HiArchive /> : <HiOutlineArchive />}
              </Box>
            </Tooltip>
            <Tooltip
              label={row.encrypted ? `Encrypted${row.encryptionAlgo ? ` · ${row.encryptionAlgo}` : ''}` : 'Not encrypted'}
              hasArrow
            >
              <Box className={`${styles.statusIcon} ${row.encrypted ? styles.statusIconActive : styles.statusIconInactive}`}>
                {row.encrypted ? <HiLockClosed /> : <HiOutlineLockOpen />}
              </Box>
            </Tooltip>
          </HStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Status',
          id: 'status'
        }
      ),
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

    const isCopyTask = currentResticTask.task_type === 8
    const isBackupTask = currentResticTask.task_type === 2
    const phase = currentResticTask.phase

    const percentDone = (() => {
      const hasPercent = typeof currentResticTask.percent_done === 'number'

      if (phase === 'init_destination') {
        return (
          <HStack spacing={3} alignItems="center">
            <Progress size="sm" flex="1" isIndeterminate />
            <Box minWidth="3.5rem" textAlign="right">Preparing...</Box>
          </HStack>
        )
      }

      if ((isBackupTask || (isCopyTask && phase === 'copy')) && hasPercent) {
        const clamped = Math.min(Math.max(currentResticTask.percent_done, 0), 1)
        const percentValue = Math.round(clamped * 100)
        return (
          <HStack spacing={3} alignItems="center">
            <Progress value={percentValue} size="sm" flex="1" />
            <Box minWidth="3.5rem" textAlign="right">{percentValue}%</Box>
          </HStack>
        )
      }

      return 'Running'
    })()
    const bytesValue = currentResticTask.total_bytes
      ? `${formatBytes(currentResticTask.bytes_done || 0)} / ${formatBytes(currentResticTask.total_bytes)}`
      : '-'
    const filesValue = currentResticTask.total_files
      ? `${currentResticTask.files_done || 0} / ${currentResticTask.total_files}`
      : '-'
    const packsValue = currentResticTask.total_packs
      ? `${currentResticTask.packs_done || 0} / ${currentResticTask.total_packs}`
      : '-'
    const startedAt = currentResticTask.started_at ? formatDate(currentResticTask.started_at) : '-'
    const completedAt = currentResticTask.completed_at ? formatDate(currentResticTask.completed_at) : '-'
    const duration = currentResticTask.total_duration
      ? `${Math.round(currentResticTask.total_duration)}s`
      : currentResticTask.seconds_elapsed
        ? `${currentResticTask.seconds_elapsed}s`
        : '-'

    return [
      { key: 'Task ID', value: currentResticTask.task_id || '-' },
      { key: 'Task Type', value: resticTaskType(currentResticTask.task_type) },
      { key: 'Status', value: currentResticTask.status || '-' },
      ...(currentResticTask.phase ? [{ key: 'Phase', value: currentResticTask.phase }] : []),
      { key: 'Progress', value: percentDone },
      { key: 'Bytes', value: bytesValue },
      ...(isCopyTask
        ? [{ key: 'Packs', value: packsValue }]
        : [{ key: 'Files', value: filesValue }]),
      ...(isCopyTask && currentResticTask.total_snapshots > 0 ? [{
        key: 'Snapshots',
        value: `${currentResticTask.completed_snapshots || 0} / ${currentResticTask.total_snapshots}`
      }] : []),
      { key: 'Snapshot ID', value: currentResticTask.snapshot_id || '-' },
      { key: 'Duration', value: duration },
      { key: 'Started', value: startedAt },
      { key: 'Completed', value: completedAt }
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
            <BKUSlider
              isDisabled={user?.grants['cluster-settings'] == false}
              value={bkuPlan || 1}
              unitGB={bkuGB}
              bku={bku}
              onChange={(value) => {
                const delta = value - bkuPlan
                if (delta === 0) return
                setBkuConfirm({ isOpen: true, delta, title: `Confirm the backup plan at ${value} BKU = ${value * bkuGB} GB of local backup storage for the cluster` })
              }}
            />
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
        headerActions={settingsButton(onOpenArchiveSettings || onOpenBackupSettings, 'Open Archive Settings')}
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
      {bkuConfirm.isOpen && <ConfirmModal title={bkuConfirm.title} isOpen={bkuConfirm.isOpen}
        onConfirmClick={() => { dispatch(changePlanUnits({ clusterName: selectedCluster?.name, unit: 'BKU', delta: bkuConfirm.delta })); setBkuConfirm({ isOpen: false, title: '', delta: 0 }) }}
        closeModal={() => setBkuConfirm({ isOpen: false, title: '', delta: 0 })} />}
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
