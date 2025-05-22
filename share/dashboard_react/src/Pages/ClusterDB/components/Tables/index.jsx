import React, { useEffect, useMemo, useState, useRef } from 'react'
import styles from '../../styles.module.scss'
import { Flex, HStack, Input, Box, VStack, Checkbox, Text } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { shallowEqual, useDispatch, useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import { analyzeAllTables, analyzeSchema, analyzeTable, checksumAllTables, checksumSchema, checksumTable, getDatabaseService } from '../../../../redux/clusterSlice'
import { getTablePct } from '../../../../utility/common'
import Gauge from '../../../../components/Gauge'
import MenuOptions from '../../../../components/MenuOptions'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'
import RMIconButton from '../../../../components/RMIconButton'
import { HiChartBar, HiShieldCheck } from 'react-icons/hi'
import { use } from 'react'

const selectTables = (state) => ({ tables: state.cluster.database.tables, isDesktop: state.common.isDesktop })

const columnHelper = createColumnHelper()

function Tables({ clusterName, dbId, selectedDBServer, usePersistent, tableSize }) {
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
  
  const handleChecksumSchema = (schema) => {
    dispatch(checksumSchema({ clusterName, schema }))
  }

  const handleChecksumAll = () => {
    dispatch(checksumAllTables({ clusterName }))
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
            onClick: () => {
              openConfirmModal()
              setConfirmTitle(`Confirm run checksum for ${row.table_schema}.${row.table_name}?`)
              setConfirmHandler(() => () => handleChecksum(row.table_schema, row.table_name))
            }
          },
          {
            name: 'Analyze',
            subMenu: [
              {
                name: 'Persistent',
                onClick: () => {
                  setConfirmTitle(`Confirm run analyze for ${row.table_schema}.${row.table_name}?`)
                  setConfirmHandler(() => () => handleAnalyze(row.table_schema, row.table_name, true))
                  openConfirmModal()
                }
              },
              {
                name: 'Non-Persistent',
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
      columnHelper.accessor((row) => getTablePct(row.data_length, row.index_length, tableSize), {
        header: '%',
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
                    onClick: () => {
                      setConfirmTitle(`Confirm run analyze for ${row.table_schema}?`)
                      setConfirmHandler(() => () => handleAnalyzeSchema(row.table_schema, true))
                      openConfirmModal()
                    }
                  },
                  {
                    name: 'Non-Persistent',
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
            <RMIconButton icon={HiShieldCheck} tooltip={"Checksum All Tables"} onClick={() => {
              setConfirmTitle(`Confirm run checksum for all schemas and tables?`)
              setConfirmHandler(() => () => handleChecksumAll())
              openConfirmModal()
            }} />
            <Box className={styles.divider} />
            <label htmlFor='search'>Analyze</label>
            <RMIconButton icon={HiChartBar} tooltip={"Analyze All Tables"} onClick={() => {
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
        <DataTable key="tables" data={data} columns={columns} className={styles.table} initialGrouping={groupColumns} enableGrouping={true} enableExpanding={true} columnVisibility={{ "table_schema": false, groupHeader: false }} />
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
