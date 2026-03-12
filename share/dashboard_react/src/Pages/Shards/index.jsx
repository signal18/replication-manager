import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { checksumRepairAllTables, checksumRepairTable, checksumAllTables, checksumTable, getShardSchema, monitorAllSchemas } from '../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { isEqual } from 'lodash'
import { Flex, VStack } from '@chakra-ui/react'
import RMButton from '../../components/RMButton'
import { getTablePct } from '../../utility/common'
import Gauge from '../../components/Gauge'
import AccordionComponent from '../../components/AccordionComponent'
import  { GeneralLogs, TaskLogs } from '../Dashboard/components/Logs'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import { sizeOf } from '../../utility/common'

function Shards({ selectedCluster, user, onOpenSchedulerSettings }) {
  const dispatch = useDispatch()

  const {
    cluster: { shardSchema },
  } = useSelector((state) => state)
  const [data, setData] = useState(shardSchema || [])
  const prevShardsRef = useRef(shardSchema)
  const [isChecksumAllRunning, setIsChecksumAllRunning] = useState(false)
  const [isChecksumRepairAllRunning, setIsChecksumRepairAllRunning] = useState(false)
  const [isSchemaConfirmOpen, setIsSchemaConfirmOpen] = useState(false)
  const [pendingChecksumAll, setPendingChecksumAll] = useState(false)
  const [pendingChecksumRepairAll, setPendingChecksumRepairAll] = useState(false)
  const [checksumTimeout, setChecksumTimeout] = useState(false)
  const [checksumRepairTimeout, setChecksumRepairTimeout] = useState(false)
  const mountedRef = useRef(true)

  useEffect(() => {
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    if (shardSchema?.length > 0) {
      if (!isEqual(shardSchema, prevShardsRef.current)) {
        setData(shardSchema)
        prevShardsRef.current = shardSchema
      }
    }
  }, [shardSchema])

  const handleChecksum = useCallback(
    (schema, table) => {
      dispatch(checksumTable({ clusterName: selectedCluster?.name, schema, table }))
    },
    [dispatch, selectedCluster?.name]
  )

  const handleChecksumRepair = useCallback(
    (schema, table) => {
      dispatch(checksumRepairTable({ clusterName: selectedCluster?.name, schema, table }))
    },
    [dispatch, selectedCluster?.name]
  )

  const handleChecksumAll = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) {
      return
    }
    if (!shardSchema || shardSchema.length === 0) {
      setPendingChecksumAll(true)
      setIsSchemaConfirmOpen(true)
      return
    }
    await runChecksumAllFlow()
  }

  const handleChecksumRepairAll = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) {
      return
    }
    if (!shardSchema || shardSchema.length === 0) {
      setPendingChecksumRepairAll(true)
      setIsSchemaConfirmOpen(true)
      return
    }
    await runChecksumRepairAllFlow()
  }


  const runChecksumAllFlow = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) {
      return
    }
    setChecksumTimeout(false)
    setIsChecksumAllRunning(true)
    try {
      if (!shardSchema || shardSchema.length === 0) {
        await dispatch(monitorAllSchemas({ clusterName: selectedCluster?.name }))
        const schemaReady = await waitForSchemaCache()
        if (!mountedRef.current) {
          return
        }
        if (!schemaReady) {
          setChecksumTimeout(true)
          return
        }
      }
      await dispatch(checksumAllTables({ clusterName: selectedCluster?.name }))
    } finally {
      if (mountedRef.current) {
        setIsChecksumAllRunning(false)
      }
    }
  }

  const runChecksumRepairAllFlow = async () => {
    if (!selectedCluster?.name || isChecksumAllRunning) {
      return
    }
    setChecksumRepairTimeout(false)
    setIsChecksumRepairAllRunning(true)
    try {
      if (!shardSchema || shardSchema.length === 0) {
        await dispatch(monitorAllSchemas({ clusterName: selectedCluster?.name }))
        const schemaReady = await waitForSchemaCache()
        if (!mountedRef.current) {
          return
        }
        if (!schemaReady) {
          setChecksumRepairTimeout(true)
          return
        }
      }
      await dispatch(checksumRepairAllTables({ clusterName: selectedCluster?.name }))
    } finally {
      if (mountedRef.current) {
        setIsChecksumRepairAllRunning(false)
      }
    }
  }

  const waitForSchemaCache = async () => {
    if (!selectedCluster?.name) {
      return false
    }
    const maxAttempts = 12
    const intervalMs = 15000 // 15 seconds
    let attempts = 0
    while (attempts < maxAttempts) {
      if (!mountedRef.current) {
        return false
      }
      const action = await dispatch(getShardSchema({ clusterName: selectedCluster?.name }))
      const payload = action?.payload?.data
      if (Array.isArray(payload) && payload.length > 0) {
        return true
      }
      attempts++
      await new Promise((resolve) => setTimeout(resolve, intervalMs))
    }
    return false
  }
  const columnHelper = createColumnHelper()
  const compareText = useCallback((left, right) => {
    const a = left == null ? '' : String(left)
    const b = right == null ? '' : String(right)
    if (a === b) {
      return 0
    }
    return a > b ? 1 : -1
  }, [])

  const compareTextDesc = useCallback((left, right) => compareText(right, left), [compareText])

  const sizePctSorting = useCallback((rowA, rowB, columnId) => {
    const a = Number(rowA.getValue(columnId))
    const b = Number(rowB.getValue(columnId))
    if (a !== b) {
      return a > b ? 1 : -1
    }
    const schemaCompare = compareTextDesc(rowA.original.table_schema, rowB.original.table_schema)
    if (schemaCompare !== 0) {
      return schemaCompare
    }
    return compareTextDesc(rowA.original.table_name, rowB.original.table_name)
  }, [compareTextDesc])

  const localSizeTotals = useMemo(() => {
    const rows = Array.isArray(data) ? data : []
    return rows.reduce(
      (acc, row) => {
        acc.table += Number(row?.data_length || 0)
        acc.index += Number(row?.index_length || 0)
        return acc
      },
      { table: 0, index: 0 }
    )
  }, [data])

  const sizeTotalsInfo = useMemo(() => {
    const workloadTable = Number(selectedCluster?.workLoad?.dbTableSize || 0)
    const workloadIndex = Number(selectedCluster?.workLoad?.dbIndexSize || 0)
    const workloadTotal = workloadTable + workloadIndex
    const localTotal = localSizeTotals.table + localSizeTotals.index
    const useLocalTotals = (!Number.isFinite(workloadTotal) || workloadTotal === 0) && localTotal > 0
    return {
      useLocalTotals,
      tableTotal: useLocalTotals ? localSizeTotals.table : workloadTable,
      indexTotal: useLocalTotals ? localSizeTotals.index : workloadIndex
    }
  }, [localSizeTotals, selectedCluster?.workLoad?.dbTableSize, selectedCluster?.workLoad?.dbIndexSize])

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.table_schema, {
        id: 'schema',
        header: 'Schema',
        enableSorting: true,
        cell: (info) => (
          <Flex className={styles.tablesSchemaCol}>
            <RMButton onClick={() => handleChecksum(info.row.original.table_schema, info.row.original.table_name)}>
              Checksum
            </RMButton>
            <RMButton onClick={() => handleChecksumRepair(info.row.original.table_schema, info.row.original.table_name)}>
              Repair
            </RMButton>
            <span>{info.getValue()}</span>
          </Flex>
        )
      }),
      columnHelper.accessor((row) => row.table_name, {
        id: 'tableName',
        header: 'Name',
        enableSorting: true
      }),
      columnHelper.accessor((row) => row.engine, {
        header: 'Engine'
      }),
      columnHelper.accessor((row) => row.table_rows, {
        header: 'Rows'
      }),
      columnHelper.accessor((row) => sizeOf(row.data_length), {
        header: 'Data'
      }),
      columnHelper.accessor((row) => sizeOf(row.index_length), {
        header: 'Index'
      }),
      columnHelper.accessor((row) => row.table_clusters, {
        header: 'Shards'
      }),
      columnHelper.accessor((row) => row.table_sync, {
        header: 'Sync'
      }),
      columnHelper.accessor(
        (row) => getTablePct(row.data_length, row.index_length, sizeTotalsInfo.tableTotal, sizeTotalsInfo.indexTotal),
        {
          id: 'sizePct',
          header: '% Size',
          enableSorting: true,
          sortingFn: sizePctSorting,
          cell: (info) => {
            if (isNaN(info.getValue())) {
              return ''
            }
            return (
              <Gauge
                className={styles.gauge}
                minValue={0}
                maxValue={100}
                value={info.getValue()}
                width={100}
                height={50}
              />
            )
          }
        }
      )
    ],
    [handleChecksum, handleChecksumRepair, sizeTotalsInfo, sizePctSorting]
  )
  useEffect(() => {
    if (selectedCluster?.name) {
      dispatch(getShardSchema({ clusterName: selectedCluster?.name }))
    }
  }, [dispatch, selectedCluster?.name])
  return (
    <VStack className={styles.shardsContainer}>
      <Flex className={styles.section} direction='column'>
        <Flex className={styles.sectionHeader} direction='column'>
          <span className={styles.sectionTitle}>Schema Actions</span>
          <span className={styles.sectionDescription}>
            Run one-off checks or refresh schema metadata for the shard list.
          </span>
        </Flex>
        <Flex className={styles.sectionBody} direction='column'>
          <Flex className={styles.actionsRow}>
            <RMButton
              className={styles.btnChecksumAll}
              onClick={handleChecksumAll}
              isDisabled={!selectedCluster?.name || isChecksumAllRunning}
              isLoading={isChecksumAllRunning}>
              {isChecksumAllRunning ? 'Preparing schema cache...' : 'Checksum All Tables'}
            </RMButton>
            <RMButton
              className={styles.btnChecksumAll}
              onClick={handleChecksumRepairAll}
              isDisabled={!selectedCluster?.name || isChecksumRepairAllRunning}
              isLoading={isChecksumRepairAllRunning}>
              {isChecksumRepairAllRunning ? 'Preparing schema cache...' : 'Repair All Tables'}
            </RMButton>
            <RMButton
              variant='outline'
              onClick={onOpenSchedulerSettings}
              isDisabled={!onOpenSchedulerSettings || user?.grants['cluster-show-backups'] == false}>
              Open Scheduler Settings
            </RMButton>
          </Flex>
          {checksumTimeout && (
            <Flex className={styles.timeoutMessage}>
              <span>Schema monitoring timed out. Check server logs or retry later.</span>
            </Flex>
          )}
          {checksumRepairTimeout && (
            <Flex className={styles.timeoutMessage}>
              <span>Schema monitoring timed out. Check server logs or retry later.</span>
            </Flex>
          )}
          {sizeTotalsInfo.useLocalTotals && (
            <Flex className={styles.timeoutMessage}>
              <span>Size percentage uses table list totals (cluster totals missing).</span>
            </Flex>
          )}
        </Flex>
      </Flex>
      <DataTable
        key="shards"
        data={data}
        columns={columns}
        className={styles.table}
        enableSorting={true}
        lockSorting={true}
        initialSorting={[
          { id: 'sizePct', desc: true }
        ]}
      />
      <AccordionComponent
        className={styles.accordion}
        heading={'Cluster Logs'}
        body={<GeneralLogs />}
      />
      <AccordionComponent
        className={styles.accordion}
        heading={'Job Logs'}
        body={<TaskLogs />}
      />
      {isSchemaConfirmOpen && (
        <ConfirmModal
          isOpen={isSchemaConfirmOpen}
          closeModal={() => {
            setIsSchemaConfirmOpen(false)
            setPendingChecksumAll(false)
            setPendingChecksumRepairAll(false)
          }}
          title='Schema cache required'
          body='Schema cache is empty. Run a schema scan now and wait for it to complete before checksumming all tables?'
          onConfirmClick={async () => {
            setIsSchemaConfirmOpen(false)
            if (pendingChecksumAll) {
              setPendingChecksumAll(false)
              setPendingChecksumRepairAll(false)
              await runChecksumAllFlow()
            }
          }}
        />
      )}
    </VStack>
  )
}

export default Shards
