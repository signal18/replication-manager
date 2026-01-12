import React, { useEffect, useState } from 'react'
import { convertObjectToArray } from '../../utility/common'
import AccordionComponent from '../../components/AccordionComponent'
import styles from './styles.module.scss'
import { useDisclosure, VStack } from '@chakra-ui/react'
import { useDispatch, useSelector } from 'react-redux'
import BackupSettings from '../Settings/BackupSettings'
import SchedulerSettings from '../Settings/SchedulerSettings'
import { TaskLogs } from '../Dashboard/components/Logs'
import DatabaseJobs from './DatabaseJobs'
import { purgeResticSnapshot, resticQueueCancel, resticQueueMove, resticQueuePause, resticQueueResume } from '../../redux/clusterSlice'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { showWarningToast } from '../../redux/toastSlice'
import BackupTables from './components/BackupTables'
import SnapshotTables from './components/SnapshotTables'
import ConfirmActionForm from './components/ConfirmActionForm'


function Maintenance({ selectedCluster, user }) {
  const [data, setData] = useState([])
  const [snapshotData, setSnapshotData] = useState([])
  const [queueData, setQueueData] = useState([])
  const [confirmState, setConfirmState] = useState({ isOpen: false, title: '', payload: null })
  const { isOpen: isConfirmModalOpen, title, payload } = confirmState

  const dispatch = useDispatch()
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

  const handleConfirm = () => {
    if (payload && payload.action) {
      switch (payload.action) {
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
