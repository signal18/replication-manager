import { createColumnHelper } from '@tanstack/react-table'
import React, { useEffect, useMemo, useState } from 'react'
import { convertObjectToArray, formatBytes, formatDate, getBackupMethod, getBackupStrategy } from '../../utility/common'
import AccordionComponent from '../../components/AccordionComponent'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { Box, HStack, useDisclosure, VStack } from '@chakra-ui/react'
import TableType3 from '../../components/TableType3'
import { useDispatch, useSelector } from 'react-redux'
import { getBackupSnapshot } from '../../redux/clusterSlice'
import BackupSettings from '../Settings/BackupSettings'
import SchedulerSettings from '../Settings/SchedulerSettings'
import Logs from '../Dashboard/components/Logs'
import DatabaseJobs from './DatabaseJobs'

function Maintenance({ selectedCluster, user }) {
  const dispatch = useDispatch()
  const [data, setData] = useState([])
  const [snapshotData, setSnapshotData] = useState([])
  const columnHelper = createColumnHelper()
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

  const {
    cluster: { backupSnapshots }
  } = useSelector((state) => state)

  useEffect(() => {
    dispatch(getBackupSnapshot({ clusterName: selectedCluster?.name }))
  }, [])

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
    if (selectedCluster?.backupList) {
      setData(convertObjectToArray(selectedCluster.backupList))
    }
  }, [selectedCluster?.backupList])
  useEffect(() => {
    if (backupSnapshots?.length > 0) {
      setSnapshotData(backupSnapshots)
    }
  }, [backupSnapshots])

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
          <VStack className={styles.cellStack}>
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
          <VStack className={styles.cellStack}>
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
      columnHelper.accessor((row) => (row.compressed ? 'Yes' : 'No'), {
        cell: (info) => info.getValue(),
        header: 'Compressed',
        id: 'compression'
      }),
      columnHelper.accessor(
        (row) => (
          <VStack>
            <div>{row.encrypted ? 'Yes' : 'No'}</div>
            {row.encrypted && (
              <VStack className={styles.cellStack}>
                <Box className={styles.cellValue}>{row.encryptionAlgo}</Box>
                <Box className={styles.cellValue}>{row.encryptionKey}</Box>
              </VStack>
            )}
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'Encryption Details',
          id: 'encryption'
        }
      ),
      columnHelper.accessor(
        (row) => (
          <VStack className={styles.cellStack}>
            <Box className={styles.cellValue}>{`File: ${row.binLogFileName}`}</Box>
            <Box className={styles.cellValue}>{`Pos: ${row.binLogFilePos}`}</Box>
            <Box className={styles.cellValue}>{`GTID: ${row.binLogUuid}`}</Box>
          </VStack>
        ),
        {
          cell: (info) => info.getValue(),
          header: 'BinLog Info',
          id: 'binLogInfo'
        }
      ),
      columnHelper.accessor((row) => row.retentionDays, {
        cell: (info) => info.getValue(),
        header: 'Retention (Days)',
        id: 'retention'
      }),
      columnHelper.accessor((row) => (row.completed ? 'Yes' : 'No'), {
        cell: (info) => info.getValue(),
        header: 'Completed',
        id: 'completed'
      })
    ],
    []
  )

  const snapshotDataStats = [
    {
      key: 'Total Size',
      value: selectedCluster?.backupStat?.total_size
    },
    {
      key: 'Total File Count',
      value: selectedCluster?.backupStat?.total_file_count
    },
    {
      key: 'Total Blob Count',
      value: selectedCluster?.backupStat?.total_blob_count
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
    })
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
        body={<DataTable data={data} columns={columns} className={styles.table} />}
      />
      <AccordionComponent
        heading={'Backup Snapshots'}
        isOpen={isBackupSnapshotOpen}
        onToggle={onBackupSnapshotToggle}
        className={styles.accordion}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={
          <VStack className={styles.snapshotContainer}>
            <TableType3 dataArray={snapshotDataStats} className={styles.statsTable} />
            <DataTable data={snapshotData} columns={snapshotColumns} className={styles.table} />
          </VStack>
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
        body={<Logs logs={selectedCluster?.logTask?.buffer} />}
      />
    </VStack>
  )
}

export default Maintenance
