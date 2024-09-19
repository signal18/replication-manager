import React, { useEffect, useMemo, useRef, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { getQueryRules } from '../../redux/clusterSlice'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import styles from './styles.module.scss'
import { isEqual } from 'lodash'

function QueryRules({ selectedCluster }) {
  const dispatch = useDispatch()

  const {
    cluster: { queryRules }
  } = useSelector((state) => state)
  const [data, setData] = useState(queryRules || [])
  const prevQueryRulesRef = useRef(queryRules)

  useEffect(() => {
    if (queryRules?.length > 0) {
      if (!isEqual(queryRules, prevQueryRulesRef.current)) {
        setData(queryRules)
        prevQueryRulesRef.current = queryRules
      }
    }
  }, [queryRules])

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.ruleId, {
        header: 'Id'
      }),
      columnHelper.accessor((row) => row.digest.String, {
        header: 'Digest'
      }),
      columnHelper.accessor((row) => row.userName.String, {
        header: 'Match User'
      }),
      columnHelper.accessor((row) => row.schemaName.String, {
        header: 'Match Schema'
      }),
      columnHelper.accessor((row) => row.matchDigest.String, {
        header: 'Match Digest'
      }),
      columnHelper.accessor((row) => row.matchPattern.String, {
        header: 'Match Pattern'
      }),
      columnHelper.accessor((row) => row.destinationHostgroup.Int64, {
        header: 'Host Group'
      }),
      columnHelper.accessor((row) => row.proxies, {
        header: 'Proxies'
      })
    ],
    []
  )
  useEffect(() => {
    dispatch(getQueryRules({ clusterName: selectedCluster?.name }))
  }, [])
  return <DataTable data={data} columns={columns} className={styles.table} />
}

export default QueryRules
