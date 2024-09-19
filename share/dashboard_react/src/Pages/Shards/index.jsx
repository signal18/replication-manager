import React, { useEffect, useMemo, useRef, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { checksumAllTables, checksumTable, getShardSchema } from '../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { isEqual } from 'lodash'
import { Flex, VStack } from '@chakra-ui/react'
import RMButton from '../../components/RMButton'
import { getTablePct } from '../../utility/common'
import Gauge from '../../components/Gauge'

function Shards({ selectedCluster, user }) {
  const dispatch = useDispatch()

  const {
    cluster: { shardSchema }
  } = useSelector((state) => state)
  const [data, setData] = useState(shardSchema || [])
  const prevShardsRef = useRef(shardSchema)

  useEffect(() => {
    if (shardSchema?.length > 0) {
      if (!isEqual(shardSchema, prevShardsRef.current)) {
        setData(shardSchema)
        prevShardsRef.current = shardSchema
      }
    }
  }, [shardSchema])

  const handleChecksum = (schema, table) => {
    dispatch(checksumTable({ clusterName: selectedCluster?.name, schema, table }))
  }
  const handleChecksumAll = () => {
    dispatch(checksumAllTables({ clusterName: selectedCluster?.name }))
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.table_schema, {
        header: 'Schema',
        cell: (info) => (
          <Flex className={styles.tablesSchemaCol}>
            <RMButton onClick={() => handleChecksum(info.row.original.table_schema, info.row.original.table_name)}>
              Checksum
            </RMButton>
            <span>{info.getValue()}</span>
          </Flex>
        )
      }),
      columnHelper.accessor((row) => row.table_name, {
        header: 'Name'
      }),
      columnHelper.accessor((row) => row.engine, {
        header: 'Engine'
      }),
      columnHelper.accessor((row) => row.table_rows, {
        header: 'Rows'
      }),
      columnHelper.accessor((row) => row.data_length, {
        header: 'Data'
      }),
      columnHelper.accessor((row) => row.index_length, {
        header: 'Index'
      }),
      columnHelper.accessor((row) => row.table_clusters, {
        header: 'Shards'
      }),
      columnHelper.accessor(
        (row) => getTablePct(row.data_length, row.index_length, selectedCluster?.workLoad?.dbTableSize),
        {
          header: 'Sync %',
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
                width={210}
                height={90}
              />
            )
          }
        }
      )
    ],
    []
  )
  useEffect(() => {
    dispatch(getShardSchema({ clusterName: selectedCluster?.name }))
  }, [])
  return (
    <VStack className={styles.shardsContainer}>
      <RMButton className={`${styles.btnChecksumAll}`} onClick={handleChecksumAll}>
        Checksum All Tables
      </RMButton>
      <DataTable data={data} columns={columns} className={styles.table} />
    </VStack>
  )
}

export default Shards
