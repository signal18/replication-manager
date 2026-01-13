import { useCallback, useEffect, useMemo, useState } from 'react'
import PropTypes from 'prop-types'
import { convertObjectToArray } from '../../utility/common'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'
import { useDisclosure, VStack } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import BackupSettings from '../Settings/BackupSettings'
import SchedulerSettings from '../Settings/SchedulerSettings'
import { TaskLogs } from '../Dashboard/components/Logs'
import DatabaseJobs from './DatabaseJobs'
import { purgeResticSnapshot, resticListSnapshot, resticQueueCancel, resticQueueMove, resticQueuePause, resticQueueResume, resticRestoreSnapshot } from '../../redux/clusterSlice'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { showWarningToast } from '../../redux/toastSlice'
import BackupTables from './components/BackupTables'
import SnapshotTables from './components/SnapshotTables'
import ConfirmActionForm from './components/ConfirmActionForm'

const normalizePath = (value) => {
  if (!value || typeof value !== 'string') {
    return ''
  }
  let trimmed = value.trim()
  if (!trimmed) {
    return ''
  }
  if (!trimmed.startsWith('/')) {
    trimmed = `/${trimmed}`
  }
  const cleaned = trimmed.replace(/\/+$/, '')
  return cleaned === '' ? '/' : cleaned
}

const filterEntriesByPaths = (items, basePaths) => {
  if (!Array.isArray(basePaths) || basePaths.length === 0) {
    return items
  }
  const normalizedBases = basePaths.map((base) => normalizePath(base)).filter(Boolean)
  if (normalizedBases.length === 0) {
    return items
  }
  return items.filter((entry) => {
    const entryPath = normalizePath(entry?.path)
    if (!entryPath) {
      return false
    }
    return normalizedBases.some((base) => {
      if (base === '/') {
        return true
      }
      return entryPath === base || entryPath.startsWith(`${base}/`)
    })
  })
}


function Maintenance({ selectedCluster, user }) {
  const [data, setData] = useState([])
  const [snapshotData, setSnapshotData] = useState([])
  const [queueData, setQueueData] = useState([])
  const [restoreListState, setRestoreListState] = useState({ snapshotId: null, pathsKey: '', items: [], isLoading: false, error: null })
  const [confirmState, setConfirmState] = useState({ isOpen: false, title: '', payload: null })
  const { isOpen: isConfirmModalOpen, title, payload } = confirmState

  const dispatch = useDispatch()
  const clusterName = selectedCluster?.name
  const { isOpen: isBackupSettingsOpen, onToggle: onBackupSettingsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupSettingsOpen')) || false
  })
  const { isOpen: isSchedulerOpen, onToggle: onSchedulerToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isSchedulerOpen')) || false
  })
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

  const openConfirmModal = (title, payload) => {
    setConfirmState({ isOpen: true, title, payload })
  }

  const closeConfirmModal = () => {
    setConfirmState({ isOpen: false, title: '', payload: null })
  }

  useEffect(() => {
    console.log('Confirm Modal State Changed:', confirmState)
  }, [confirmState])

  const handleConfirm = () => {
    if (payload && payload.action) {
      switch (payload.action) {
        case 'snapshotPurge':
          dispatch(purgeResticSnapshot({ clusterName: selectedCluster.name, snapshotId: payload.data.snapshotId }))
          break
        case 'snapshotRestore': {
          if (!payload.data?.targetDir) {
            dispatch(showWarningToast({ title: 'Missing target directory', description: 'Please specify a target directory to restore the snapshot.' }))
            return
          }
          const basePath = payload.data?.basePath?.trim()
          const sourcePath = basePath || ''
          const sourcePathType = basePath
            ? restoreListState.items.find((entry) => entry.path === basePath)?.type
            : undefined
          dispatch(
            resticRestoreSnapshot({
              clusterName: selectedCluster.name,
              snapshotId: payload.data.snapshotId,
              targetDir: payload.data.targetDir,
              paths: payload.data.paths,
              sourcePath,
              sourcePathType
            })
          )
          break
        }
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

  // Replace the handleRestorePaths function with this:

  const handleRestorePaths = useCallback((paths) => {
    setConfirmState((prevState) => {
      // Always use the previous state to avoid stale closures
      return {
        ...prevState,
        payload: {
          ...prevState.payload,
          data: {
            ...prevState.payload.data,
            paths: Array.isArray(paths) ? [...paths] : []
          }
        }
      }
    })
  }, []) // Empty dependencies since we use functional update

  // Also wrap the other handlers with useCallback if not already:

  const handleMove = useCallback((direction, afterId) => {
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
  }, [])

  const handleRestoreBasePath = useCallback((basePath) => {
    setConfirmState((prevState) => ({
      ...prevState,
      payload: {
        ...prevState.payload,
        data: {
          ...prevState.payload.data,
          basePath
        }
      }
    }))
  }, [])

  const handleRestoreTarget = useCallback((targetDir) => {
    setConfirmState((prevState) => ({
      ...prevState,
      payload: {
        ...prevState.payload,
        data: {
          ...prevState.payload.data,
          targetDir
        }
      }
    }))
  }, [])

  const loadRestoreList = useCallback(async (snapshotId, listPaths, pathsKey) => {
    if (!snapshotId || !clusterName) {
      return
    }
    const listEntries = Array.isArray(listPaths) ? listPaths : listPaths ? [listPaths] : []
    const nextPathsKey = typeof pathsKey === 'string' ? pathsKey : listEntries.join('|')
    setRestoreListState({ snapshotId, pathsKey: nextPathsKey, items: [], isLoading: true, error: null })
    try {
      const result = await dispatch(
        resticListSnapshot({
          clusterName,
          snapshotId,
          paths: listEntries.length > 0 ? listEntries : undefined,
          recursive: listEntries.length > 0
        })
      ).unwrap()
      const items = Array.isArray(result?.data) ? result.data : []
      const seen = new Set()
      const uniqueItems = items.filter((entry) => {
        const path = entry?.path
        if (!path || seen.has(path)) {
          return false
        }
        seen.add(path)
        return true
      })
      const filteredItems = filterEntriesByPaths(uniqueItems, listEntries)
      setRestoreListState({ snapshotId, pathsKey: nextPathsKey, items: filteredItems, isLoading: false, error: null })
    } catch (error) {
      setRestoreListState({
        snapshotId,
        pathsKey: nextPathsKey,
        items: [],
        isLoading: false,
        error: error?.message || 'Failed to load restic file list'
      })
    }
  }, [clusterName, dispatch])

  useEffect(() => {
    localStorage.setItem('isBackupSettingsOpen', JSON.stringify(isBackupSettingsOpen))
  }, [isBackupSettingsOpen])
  useEffect(() => {
    localStorage.setItem('isSchedulerOpen', JSON.stringify(isSchedulerOpen))
  }, [isSchedulerOpen])
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

  const restoreSnapshotId = payload?.data?.snapshotId
  const restoreBasePath = payload?.data?.basePath
  const restoreBasePaths = payload?.data?.basePaths
  const restoreListPaths = useMemo(() => {
    if (restoreBasePath) {
      return [restoreBasePath]
    }
    if (Array.isArray(restoreBasePaths)) {
      return restoreBasePaths
    }
    return restoreBasePaths ? [restoreBasePaths] : []
  }, [restoreBasePath, restoreBasePaths])
  const restorePathsKey = useMemo(() => restoreListPaths.join('|'), [restoreListPaths])

  useEffect(() => {
    if (!isConfirmModalOpen || payload?.action !== 'snapshotRestore') {
      if (restoreListState.snapshotId) {
        setRestoreListState({ snapshotId: null, pathsKey: '', items: [], isLoading: false, error: null })
      }
      return
    }

    if (
      restoreSnapshotId
      && !restoreListState.isLoading
      && (restoreSnapshotId !== restoreListState.snapshotId || restorePathsKey !== restoreListState.pathsKey)
    ) {
      loadRestoreList(restoreSnapshotId, restoreListPaths, restorePathsKey)
    }
  }, [
    isConfirmModalOpen,
    payload?.action,
    restoreSnapshotId,
    restorePathsKey,
    restoreListPaths,
    restoreListState.snapshotId,
    restoreListState.pathsKey,
    restoreListState.isLoading,
    loadRestoreList
  ])

  return (
    <VStack className={styles.backupContainer}>
      <AccordionComponent
        heading={'Scheduler Settings'}
        isOpen={isSchedulerOpen}
        onToggle={onSchedulerToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<SchedulerSettings selectedCluster={selectedCluster} user={user} />}
      />
      <AccordionComponent
        heading={'Backups Settings'}
        isOpen={isBackupSettingsOpen}
        onToggle={onBackupSettingsToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<BackupSettings selectedCluster={selectedCluster} user={user} />}
      />
      <AccordionComponent
        heading={'Current Backups'}
        isOpen={isBackupsOpen}
        onToggle={onBackupsToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={
          <BackupTables data={data} backupStats={backupStats} />
        }
      />
      <AccordionComponent
        heading={'Backup Snapshots'}
        isOpen={isBackupSnapshotOpen}
        onToggle={onBackupSnapshotToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={
          <SnapshotTables
            snapshotData={snapshotData}
            snapshotStats={stats}
            queueData={queueData}
            tagCategories={selectedCluster?.config?.backupResticTagCategories}
            isQueuePaused={selectedCluster?.isResticQueuePaused}
            onConfirmAction={openConfirmModal}
          />
        }
      />
      <AccordionComponent
        heading={'Database Jobs'}
        isOpen={isDBJobsOpen}
        onToggle={onDBJobsToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<DatabaseJobs clusterName={selectedCluster?.name} />}
      />
      <AccordionComponent
        className={styles.accordion}
        isOpen={isLogsOpen}
        onToggle={onLogsToggle}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        heading={'Job Logs'}
        body={<TaskLogs />}
      />
      {isConfirmModalOpen && (
        <ConfirmModal
          title={title}
          isOpen={isConfirmModalOpen}
          body={
            <ConfirmActionForm
              payload={payload}
              queueData={queueData}
              onMove={handleMove}
              onRestoreBasePath={handleRestoreBasePath}
              onRestoreTarget={handleRestoreTarget}
              onRestorePaths={handleRestorePaths}
              restorePaths={restoreListState.items}
              restorePathsLoading={restoreListState.isLoading}
              restorePathsError={restoreListState.error}
            />
          }
          onConfirmClick={handleConfirm}
          closeModal={closeConfirmModal}
        />
      )}
    </VStack>
  )
}

export default Maintenance

Maintenance.propTypes = {
  selectedCluster: PropTypes.shape({
    name: PropTypes.string,
    isResticQueuePaused: PropTypes.bool
  }),
  user: PropTypes.object
}
