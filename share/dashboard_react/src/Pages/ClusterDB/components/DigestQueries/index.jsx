import React, { useEffect, useMemo, useState, useRef } from 'react'
import styles from '../../styles.module.scss'
import {
  Flex,
  HStack,
  VStack,
  Box,
  IconButton,
  Tooltip,
  Modal,
  ModalOverlay,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalCloseButton,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  useDisclosure,
  useColorModeValue,
  Spinner,
  Text,
  Code
} from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { useDispatch, useSelector } from 'react-redux'
import { DataTable } from '../../../../components/DataTable'
import { isEqual } from 'lodash'
import { getDatabaseService } from '../../../../redux/clusterSlice'
import Toolbar from '../Toolbar'
import ShowMoreText from '../../../../components/ShowMoreText'
import { HiSearchCircle } from 'react-icons/hi'
import { format as formatSQL } from 'sql-formatter'
import { clusterService } from '../../../../services/clusterService'
import { useTheme } from '../../../../ThemeProvider'
import parentStyles from '../../../../components/Modals/styles.module.scss'

function safeSQLFormat(sql) {
  try {
    return formatSQL(sql, { language: 'mysql' })
  } catch {
    return sql
  }
}

function DigestQueries({ clusterName, dbId, selectedDBServer, digestMode, toggleDigestMode }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()

  const {
    cluster: {
      database: { digestQueries }
    },
    auth: { baseURL }
  } = useSelector((state) => state)
  const [data, setData] = useState(digestQueries || [])
  const prevDigestQueries = useRef(digestQueries)

  const { isOpen, onOpen, onClose } = useDisclosure()
  const codeBg = useColorModeValue('gray.100', 'gray.700')
  const codeColor = useColorModeValue('gray.800', 'gray.50')
  const tableBorderColor = useColorModeValue('gray.200', 'gray.600')
  const iconColor = useColorModeValue('gray.600', 'gray.200')
  const [explainData, setExplainData] = useState(null)
  const [explainLoading, setExplainLoading] = useState(false)
  const [explainQuery, setExplainQuery] = useState('')

  useEffect(() => {
    if (digestMode === 'pfs') {
      dispatch(getDatabaseService({ clusterName, serviceName: 'digest-statements-pfs', dbId }))
    } else {
      dispatch(getDatabaseService({ clusterName, serviceName: 'digest-statements-slow', dbId }))
    }
  }, [])

  useEffect(() => {
    if (digestQueries?.length > 0) {
      if (!isEqual(digestQueries, prevDigestQueries.current)) {
        setData(digestQueries)
        prevDigestQueries.current = digestQueries
      }
    }
  }, [digestQueries])

  const handleExplain = async (digest, sampleQuery) => {
    setExplainQuery(sampleQuery || '')
    setExplainData(null)
    setExplainLoading(true)
    onOpen()
    try {
      const response = await clusterService.getQueryExplainPFS(clusterName, dbId, digest, baseURL)
      setExplainData(response.data)
    } catch (err) {
      setExplainData({ error: err?.response?.data || 'Failed to get explain plan' })
    } finally {
      setExplainLoading(false)
    }
  }

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row, rowIndex) => rowIndex + 1, {
        header: 'Id'
      }),
      columnHelper.accessor((row) => row.lastSeen, {
        header: 'Last seen at',
        id: 'lastseen'
      }),
      columnHelper.accessor((row) => row.shemaName || '-', {
        header: 'Schema',
        id: 'schema'
      }),
      columnHelper.accessor((row) => row.digestText, {
        header: 'Query',
        id: 'query',
        cell: (info) => <ShowMoreText text={info.getValue()} />,
        textAlign: 'start'
      }),
      columnHelper.accessor((row) => row.sampleQuery || '', {
        header: 'Sample',
        id: 'sample',
        cell: (info) => {
          const val = info.getValue()
          return val ? <ShowMoreText text={val} maxLength={40} /> : '-'
        },
        textAlign: 'start'
      }),
      columnHelper.accessor((row) => row.execCount, {
        header: 'Count',
        id: 'count'
      }),
      columnHelper.group({
        header: 'Times',
        columns: [
          columnHelper.accessor((row) => row.execTimeTotal, {
            header: 'Total',
            id: 'total'
          }),
          columnHelper.accessor((row) => row.execTimeMax.Float64, {
            header: 'Max',
            id: 'max'
          }),
          columnHelper.accessor((row) => row.execTimeAvgMs.Float64, {
            header: 'Avg',
            id: 'avg'
          })
        ]
      }),
      columnHelper.group({
        header: 'Rows',
        id: 'row',
        columns: [
          columnHelper.accessor((row) => row.rowsSent, {
            header: 'Sent',
            id: 'sent'
          }),
          columnHelper.accessor((row) => row.rowsSentAvg, {
            header: 'Avg',
            id: 'row_avg'
          }),
          columnHelper.accessor((row) => row.rowsScanned, {
            header: 'Scan',
            id: 'scan'
          })
        ]
      }),
      columnHelper.group({
        header: 'Plans',
        id: 'plans',
        columns: [
          columnHelper.accessor((row) => row.planFullScan, {
            header: 'Full',
            id: 'full'
          }),
          columnHelper.accessor((row) => row.planTmpDisk, {
            header: 'Disk',
            id: 'disk'
          }),
          columnHelper.accessor((row) => row.planTmpMem, {
            header: 'Mem',
            id: 'mem'
          })
        ]
      }),
      columnHelper.display({
        header: 'Explain',
        id: 'explain',
        cell: (info) => {
          const row = info.row.original
          const hasSample = row.sampleQuery && row.sampleQuery.length > 0
          return (
            <Tooltip label={hasSample ? 'Run EXPLAIN' : 'No sample query available'}>
              <IconButton
                size='xs'
                variant='ghost'
                color={iconColor}
                icon={<HiSearchCircle size='16' />}
                isDisabled={!hasSample}
                onClick={() => handleExplain(row.digest, row.sampleQuery)}
                aria-label='Explain query'
              />
            </Tooltip>
          )
        }
      })
    ],
    []
  )

  return (
    <VStack className={styles.contentContainer}>
      <Flex className={styles.actions}>
        <Toolbar
          clusterName={clusterName}
          tab='digestQueries'
          dbId={dbId}
          selectedDBServer={selectedDBServer}
          digestMode={digestMode}
          toggleDigestMode={toggleDigestMode}
        />
      </Flex>
      <Box className={styles.tableContainer}>
        <DataTable key='digest' data={data} columns={columns} className={styles.table} />
      </Box>

      <Modal isOpen={isOpen} onClose={onClose} size='xl'>
        <ModalOverlay />
        <ModalContent maxW='900px' className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader fontSize='sm'>EXPLAIN Plan</ModalHeader>
          <ModalCloseButton />
          <ModalBody pb={6}>
            {explainQuery && (
              <Box
                mb={4}
                p={3}
                bg={codeBg}
                borderRadius='md'
                maxH='150px'
                overflowY='auto'
              >
                <Code whiteSpace='pre-wrap' wordBreak='break-all' fontSize='xs' display='block' bg='transparent' color={codeColor}>
                  {safeSQLFormat(explainQuery)}
                </Code>
              </Box>
            )}
            {explainLoading && (
              <Flex justify='center' py={4}>
                <Spinner />
              </Flex>
            )}
            {explainData?.error && <Text color='red.500'>{String(explainData.error)}</Text>}
            {explainData && !explainData.error && Array.isArray(explainData) && (
              <Box overflowX='auto'>
                <Table size='sm' variant='simple' borderColor={tableBorderColor} sx={{ tableLayout: 'auto', minW: '800px' }}>
                  <Thead>
                    <Tr>
                      <Th borderColor={tableBorderColor}>id</Th>
                      <Th borderColor={tableBorderColor}>select_type</Th>
                      <Th borderColor={tableBorderColor}>table</Th>
                      <Th borderColor={tableBorderColor}>type</Th>
                      <Th borderColor={tableBorderColor}>possible_keys</Th>
                      <Th borderColor={tableBorderColor}>key</Th>
                      <Th borderColor={tableBorderColor}>key_len</Th>
                      <Th borderColor={tableBorderColor}>ref</Th>
                      <Th borderColor={tableBorderColor}>rows</Th>
                      <Th borderColor={tableBorderColor} minW='200px'>Extra</Th>
                    </Tr>
                  </Thead>
                  <Tbody>
                    {explainData.map((row, i) => (
                      <Tr key={i}>
                        <Td borderColor={tableBorderColor}>{row.id}</Td>
                        <Td borderColor={tableBorderColor}>{row.selectType?.String || row.selectType?.Valid === false ? '-' : row.selectType}</Td>
                        <Td borderColor={tableBorderColor}>{row.table?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor}>{row.type?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor}>{row.possibleKeys?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor}>{row.key?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor}>{row.keyLen?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor}>{row.ref?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor}>{row.rows?.String || '-'}</Td>
                        <Td borderColor={tableBorderColor} whiteSpace='normal' minW='200px'>{row.extra?.String || '-'}</Td>
                      </Tr>
                    ))}
                  </Tbody>
                </Table>
              </Box>
            )}
            {explainData && !explainData.error && Array.isArray(explainData) && explainData.length === 0 && (
              <Text>No explain data returned</Text>
            )}
          </ModalBody>
        </ModalContent>
      </Modal>
    </VStack>
  )
}

export default DigestQueries
