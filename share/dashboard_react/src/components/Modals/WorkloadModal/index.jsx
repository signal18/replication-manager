import React, { useMemo } from 'react'
import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay, Text
} from '@chakra-ui/react'
import { useSelector } from 'react-redux'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import NotFound from '../../NotFound'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'

function WorkloadModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const { isMobile, isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)

  const workloadStates = clusterData?.workloadStateMachine?.CurState || {}
  const statesArray = useMemo(() => Object.values(workloadStates), [workloadStates])

  const columnHelper = createColumnHelper()
  const columns = useMemo(() => [
    columnHelper.accessor((row) => row.ErrDesc, {
      id: 'desc',
      cell: (info) => info.getValue()?.replace(/,(?!\s)/g, ', ') || '',
      header: () => <span>Description</span>,
    }),
    columnHelper.accessor((row) => row.ServerUrl, {
      id: 'server',
      cell: (info) => info.getValue() || '',
      header: () => <span>Server</span>,
      maxWidth: '200',
    }),
    columnHelper.accessor((row) => row.ErrKey, {
      id: 'code',
      cell: (info) => info.getValue() || '',
      header: () => <span>Code</span>,
      maxWidth: '120',
    }),
  ], [])

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent
        className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
        width={isDesktop ? '80%' : isTablet ? '97%' : '99%'}
        maxWidth='none'
        minHeight='300px'
        maxH='90%'
        overflow='hidden'>
        <ModalHeader style={{ background: 'var(--chakra-colors-purple-500)', color: 'white' }}>
          Workload Spikes ({statesArray.length})
        </ModalHeader>
        <ModalCloseButton color='white' />
        <ModalBody overflowY='auto' pb={6}>
          {statesArray.length === 0 ? (
            <NotFound text='No active workload findings' />
          ) : (
            <DataTable
              key='WorkloadStates'
              columns={columns}
              data={statesArray}
              cellValueAlign='start'
            />
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default WorkloadModal
