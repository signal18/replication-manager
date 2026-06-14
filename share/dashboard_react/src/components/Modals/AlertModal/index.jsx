import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay,
  Table, Thead, Tbody, Tr, Th, Td, Badge, Text, Box, Link
} from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import { useSelector } from 'react-redux'
import NotFound from '../../NotFound'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import styles from './styles.module.scss'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'

function VariableDiffTable({ diffVariables, theme }) {
  if (!diffVariables || diffVariables.length === 0) return <Text>No variable differences found</Text>
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'
  return (
    <Box maxH='500px' overflowY='auto'>
      <Table size='sm' variant='simple'>
        <Thead>
          <Tr>
            <Th>Variable</Th>
            <Th>Server</Th>
            <Th>Role</Th>
            <Th>Value</Th>
          </Tr>
        </Thead>
        <Tbody>
          {diffVariables.map((v, i) =>
            v.diffValues.map((d, j) => (
              <Tr key={`${i}-${j}`}>
                {j === 0 && <Td rowSpan={v.diffValues.length} fontWeight={600}>{v.variableName}</Td>}
                <Td fontSize='xs'>{d.serverName}</Td>
                <Td><Badge variant={badgeVariant} colorScheme={d.role === 'leader' ? 'blue' : 'green'} size='sm'>{d.role}</Badge></Td>
                <Td fontFamily='mono' fontSize='xs'>{d.variableValue}</Td>
              </Tr>
            ))
          )}
        </Tbody>
      </Table>
    </Box>
  )
}

function AlertModal({ type, isOpen, closeModal, alerts }) {
  const { theme } = useTheme()
  const {
    common: { isMobile, isTablet, isDesktop },
    cluster: { clusterAlerts, clusterData }
  } = useSelector((state) => state)

  const source = alerts ?? clusterAlerts
  const [data, setData] = useState([])
  const [showVarDiff, setShowVarDiff] = useState(false)

  useEffect(() => {
    if (type === 'error' && source?.errors?.length > 0) {
      setData(source.errors)
    } else if (type === 'warning' && source?.warnings?.length > 0) {
      setData(source.warnings)
    } else {
      setData([])
    }
    setShowVarDiff(false)
  }, [source, type])

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.desc, {
        id: 'desc',
        cell: (info) => {
          const row = info.row.original
          const value = info.getValue()
          if (row.number?.startsWith('WARN0084')) {
            return (
              <Link color='blue.400' onClick={() => setShowVarDiff(true)} cursor='pointer'>
                {value?.replace(/,(?!\s)/g, ', ') || ''}
              </Link>
            )
          }
          return value?.replace(/,(?!\s)/g, ', ') || ''
        },
        header: () => <span>Description</span>
      }),
      columnHelper.accessor((row) => row.from, {
        id: 'from',
        cell: (info) => info.getValue(),
        header: () => <span>From</span>,
        maxWidth: '120'
      }),
      columnHelper.accessor((row) => row.number, {
        id: 'number',
        cell: (info) => (info.getValue() || '').split('@')[0],
        header: () => <span>Number</span>,
        maxWidth: '200'
      })
    ],
    []
  )
  return (
    <>
      <Modal isOpen={isOpen} onClose={closeModal}>
        <ModalOverlay />
        <ModalContent
          className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
          width={isDesktop ? '80%' : isTablet ? '97%' : '99%'}
          maxWidth='none'
          minHeight={'300px'}
          maxH={'90%'}
          textAlign='center'
          overflow='hidden'>
          <ModalHeader
            whiteSpace='pre-line'
            className={`${styles.header} ${type === 'error' ? styles.red : styles.orange}`}>
            {type === 'error' ? `Errors: ${data.length}` : `Warnings: ${data.length}`}
          </ModalHeader>
          <ModalCloseButton />
          <ModalBody className={styles.body}>
            {data.length === 0 ? (
              <NotFound text={`No ${type} alerts found`} />
            ) : (
              <DataTable
                key="Alerts"
                className={`${styles.table}  ${type === 'error' ? styles.red : styles.orange}`}
                columns={columns}
                data={data}
                cellValueAlign='start'
              />
            )}
          </ModalBody>
        </ModalContent>
      </Modal>

      <Modal isOpen={showVarDiff} onClose={() => setShowVarDiff(false)} size='xl'>
        <ModalOverlay />
        <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent} maxW='800px'>
          <ModalHeader fontSize='md'>Variable Differences — Leader vs Replicas</ModalHeader>
          <ModalCloseButton />
          <ModalBody pb={6}>
            <VariableDiffTable diffVariables={clusterData?.diffVariables} theme={theme} />
          </ModalBody>
        </ModalContent>
      </Modal>
    </>
  )
}

export default AlertModal
