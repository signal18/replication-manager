import React, { useEffect, useMemo, useState, useRef } from 'react'
import styles from '../../styles.module.scss'
import { Flex, HStack, Input, Box, VStack, Checkbox, Text } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { shallowEqual, useDispatch, useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import { analyzeAllTables, analyzeSchema, analyzeTable, checksumAllTables, checksumRepairAllTables, checksumSchema, checksumRepairSchema, checksumTable, checksumRepairTable, getDatabaseService } from '../../../../redux/clusterSlice'
import { getTablePct } from '../../../../utility/common'
import Gauge from '../../../../components/Gauge'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import RMIconButton from '../../../../components/RMIconButton'
import { HiChartBar, HiShieldCheck } from 'react-icons/hi'
import { use } from 'react'

const selectTables = (state) => ({ tables: state.cluster.database.tables, isDesktop: state.common.isDesktop })

const columnHelper = createColumnHelper()

function Tables({ clusterName, dbId, selectedDBServer, usePersistent, tableSize, user }) {
  const dispatch = useDispatch()
  const { tables, isDesktop } = useSelector(selectTables, shallowEqual)
  const [search, setSearch] = useState('')
  const [persistent, setPersistent] = useState(usePersistent || false)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmHandler, setConfirmHandler] = useState(() => () => { })

  const groupColumns = ['table_schema']
  const [data, setData] = useState(tables || [])
  const [allData, setAllData] = useState(tables || [])
  const prevTables = useRef(tables)

  const placement = 'right-end'
  const subMenuPlacement = isDesktop ? placement : 'bottom'

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmTitle('')
    setConfirmHandler(() => () => { })
  }

  const searchData = (serverData) => {
    const searchedData = serverData?.filter((x) => {
      const searchValue = search.toLowerCase()
      if (
        x.table_schema.toLowerCase().includes(searchValue) ||
        x.table_name.toLowerCase().includes(searchValue) ||
        x.engine.toLowerCase().includes(searchValue)
      ) {
        return x
      }
    }) || []
    return searchedData
  }

  useEffect(() => {
    dispatch(getDatabaseService({ clusterName, serviceName: 'tables', dbId }))
  }, [])

  useEffect(() => {
    if (tables?.length > 0) {
      if (!isEqual(tables, prevTables.current)) {
        setAllData(tables)
        setData(searchData(tables))
        prevTables.current = tables
      }
    }
  }, [tables])

  useEffect(() => {
    setData(searchData(allData))
  }, [search])

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  const handleChecksum = (schema, table) => {
    dispatch(checksumTable({ clusterName, schema, table }))
  }

  const handleRepairChecksum = (schema, table) => {
    dispatch(checksumRepairTable({ clusterName, schema, table }))
  }

  const handleChecksumSchema = (schema) => {
    dispatch(checksumSchema({ clusterName, schema }))
  }

  const handleChecksumRepairSchema = (schema) => {
    dispatch(checksumSchema({ clusterName, schema }))
  }

  const handleChecksumAll = () => {
    dispatch(checksumAllTables({ clusterName }))
  }

  const handleChecksumRepairAll = () => {
    dispatch(checksumRepairAllTables({ clusterName }))
  }

  const handleAnalyze = (schema, table, persistent) => {
    dispatch(analyzeTable({ clusterName, schema, table, persistent }))
  }

  const handleAnalyzeSchema = (schema, persistent) => {
    dispatch(analyzeSchema({ clusterName, schema, persistent }))
  }

  const handleAnalyzeAll = (persistent) => {
    dispatch(analyzeAllTables({ clusterName, persistent }))
  }


  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => <MenuOptions key={row.id} placement={placement} subMenuPlacement={subMenuPlacement}
        options={[
          {
            name: 'Checksum',
            isDisabled: !user?.grants['cluster-checksum'],
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm run checksum for ${row.table_schema}.${row.table_name}?`)
              setConfirmHandler(() => () => handleChecksum(row.table_schema, row.table_name))
            }
          },
          {
            name: 'ChecksumRepair',
            isDisabled: !user?.grants['cluster-checksum-repair'],
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm run repair for ${row.table_schema}.${row.table_name}?`)
              setConfirmHandler(() => () => handleChecksumRepair(row.table_schema, row.table_name))
            }
          },
          {
            name: 'Analyze',
            subMenu: [
              {
                name: 'Persistent',
                isDisabled: !user?.grants['cluster-analyze'],
                onClick: () => {
                  setConfirmTitle(`Confirm run analyze for ${row.table_schema}.${row.table_name}?`)
                  setConfirmHandler(() => () => handleAnalyze(row.table_schema, row.table_name, true))
                  openConfirmModal()
                }
              },
              {
                name: 'Non-Persistent',
                isDisabled: !user?.grants['cluster-analyze'],
                onClick: () => {
                  setConfirmTitle(`Confirm run analyze for ${row.table_schema}.${row.table_name}?`)
                  setConfirmHandler(() => () => handleAnalyze(row.table_schema, row.table_name, false))
                  openConfirmModal()
                }
              }
            ]
          }
        ]}
      />, {
        id: 'id',
        header: '#',
        width: "30px",
        cell: (info) => {
          return (info.getValue())
        }
      }),
      columnHelper.accessor((row) => row.table_schema, {
        id: 'table_schema',
        header: 'Schema',
        enableGrouping: true,
        enableHiding: true,
        cell: () => null,
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
      // Update the syncStatus column accessor to add sortingFn:
      columnHelper.accessor((row) => row.table_sync, {
        id: 'syncStatus',
        header: 'Sync',
        enableSorting: true,
        sortingFn: (rowA, rowB) => {
          const order = { PR: 0, ER: 1, '': 2, NA: 3, OK: 4 }
          const a = (rowA.original.table_sync || '').toUpperCase()
          const b = (rowB.original.table_sync || '').toUpperCase()
          const diff = (order[a] ?? 99) - (order[b] ?? 99)
          if (diff !== 0) return diff
          if (a === 'PR' && b === 'PR') {
            const countA = rowA.original.table_chunks_count   || 0
            const currA  = rowA.original.table_chunks_current || 0
            const countB = rowB.original.table_chunks_count   || 0
            const currB  = rowB.original.table_chunks_current || 0
            const pctA = countA > 0 ? currA / countA : 0
            const pctB = countB > 0 ? currB / countB : 0
            return pctB - pctA
          }
          return 0
        },
        cell: (info) => {
          const val = (info.getValue() || '').toUpperCase()
          const count   = info.row.original.table_chunks_count   || 0
          const current = info.row.original.table_chunks_current || 0
          const pct = val === 'PR' && count > 0 ? Math.round((current / count) * 100) : null
          const color = val === 'OK' ? '#27500A' : val === 'ER' ? '#A32D2D' : val === 'PR' ? '#1A55A3' : '#5F5E5A'
          const bg    = val === 'OK' ? '#EAF3DE' : val === 'ER' ? '#FCEBEB' : val === 'PR' ? '#EBF4FF' : '#F1EFE8'
          const label = val === 'OK' ? '✓ OK' : val === 'ER' ? '✕ ERROR' : val === 'PR' ? `↻ ${pct ?? 0}%` : val === 'NA' ? '⊘ N/A' : '—'
          return (
            <span style={{
              display: 'inline-flex', flexDirection: 'column', alignItems: 'center', gap: 2,
              padding: '2px 8px', borderRadius: 5, fontSize: 11, fontWeight: 600,
              background: bg, color: color, whiteSpace: 'nowrap',
              minWidth: pct !== null ? 70 : undefined,
            }}>
              {label}
              {pct !== null && (
                <span style={{ width: '100%', height: 4, borderRadius: 2, background: '#c3def9', overflow: 'hidden' }}>
                  <span style={{ display: 'block', height: '100%', width: `${pct}%`, borderRadius: 2, background: '#1A55A3', transition: 'width 0.3s ease' }} />
                </span>
              )}
            </span>
          )
        }
      }),
      columnHelper.accessor((row) => getTablePct(row.data_length, row.index_length, tableSize), {
        header: '%',
        cell: (info) => {
          if (isNaN(info.getValue())) {
            return ''
          }
          return ( info.getValue())
        }
      }),
      {
        id: 'groupHeader',
        header: '',
        meta: {
          renderGroupHeader: (row) => {
            return (<Text fontWeight={"bold"}>{row.original.table_schema}</Text>)
          },
          groupHeaderMenuColumnRef: 'id',
          renderGroupHeaderMenu: (row) => (<MenuOptions key={row.id} placement={placement} subMenuPlacement={subMenuPlacement}
            options={[
              {
                name: 'Checksum',
                isDisabled: !user?.grants['cluster-checksum'],
                onClick: () => {
                  openConfirmModal()
                  setConfirmTitle(`Confirm run checksum for ${row.original.table_schema}?`)
                  setConfirmHandler(() => () => handleChecksumSchema(row.table_schema))
                }
              },
              {
                name: 'Analyze',
                subMenu: [
                  {
                    name: 'Persistent',
                    isDisabled: !user?.grants['cluster-analyze'],
                    onClick: () => {
                      setConfirmTitle(`Confirm run analyze for ${row.table_schema}?`)
                      setConfirmHandler(() => () => handleAnalyzeSchema(row.table_schema, true))
                      openConfirmModal()
                    }
                  },
                  {
                    name: 'Non-Persistent',
                    isDisabled: !user?.grants['cluster-analyze'],
                    onClick: () => {
                      setConfirmTitle(`Confirm run analyze for ${row.table_schema}?`)
                      setConfirmHandler(() => () => handleAnalyzeSchema(row.table_schema, false))
                      openConfirmModal()
                    }
                  }
                ]
              }
            ]}
          />)
        },
        cell: () => null,
      }
    ],
    []
  )

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        <HStack gap='4'>
          <HStack className={styles.search}>
            <label htmlFor='search'>Search</label>
            <Input id='search' type='search' onChange={handleSearch} />
            <Box className={styles.divider} />
            <label htmlFor='search'>Checksum</label>
            <RMIconButton icon={HiShieldCheck} tooltip={"Checksum All Tables"} isDisabled={!user?.grants['cluster-checksum']} onClick={() => {
              setConfirmTitle(`Confirm run checksum for all schemas and tables?`)
              setConfirmHandler(() => () => handleChecksumAll())
              openConfirmModal()
            }} />
            <Box className={styles.divider} />
            <label htmlFor='search'>Analyze</label>
            <RMIconButton icon={HiChartBar} tooltip={"Analyze All Tables"} isDisabled={!user?.grants['cluster-analyze']} onClick={() => {
              let title = persistent ? 'with PERSISTENT FOR ALL' : ''
              setConfirmTitle(`Confirm run checksum for all schemas and tables ${title}?`)
              setConfirmHandler(() => () => handleAnalyzeAll(persistent))
              openConfirmModal()
            }} />
            <Checkbox size='lg' isChecked={persistent} onChange={(e) => { setPersistent(e.target.checked) }} className={styles.checkbox}>
              Persistent
            </Checkbox>
          </HStack>
        </HStack>
      </Flex>
      <Box className={styles.tableContainer}>
      <DataTable
        key="tables"
        data={data}
        columns={columns}
        className={styles.table}
        initialGrouping={groupColumns}
        enableGrouping={true}
        enableExpanding={true}
        enableSorting={true}
        initialSorting={[{ id: 'syncStatus', desc: false }]}
        columnVisibility={{ "table_schema": false, groupHeader: false }}
      />
        {isConfirmModalOpen && (
          <ConfirmModal
            isOpen={isConfirmModalOpen}
            closeModal={closeConfirmModal}
            title={confirmTitle}
            onConfirmClick={() => {
              confirmHandler()
              closeConfirmModal()
            }}
          />
        )}
      </Box>
    </VStack>
  )
}

export default Tables
