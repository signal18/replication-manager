import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay
} from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import { useSelector } from 'react-redux'
import NotFound from '../../NotFound'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import styles from './styles.module.scss'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'

function AlertModal({ type, isOpen, closeModal, alerts }) {
  const { theme } = useTheme()
  const {
    common: { isMobile, isTablet, isDesktop }
  } = useSelector((state) => state)

  const source = alerts ?? useSelector((state) => state.cluster.clusterAlerts)
  const [data, setData] = useState([])

  useEffect(() => {
    if (type === 'error' && source?.errors?.length > 0) {
      setData(source.errors)
    } else if (type === 'warning' && source?.warnings?.length > 0) {
      setData(source.warnings)
    } else {
      setData([])
    }
  }, [source, type])

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.desc, {
        id: 'desc',
        cell: (info) => (
          <span style={{ whiteSpace: 'pre-wrap', fontFamily: 'monospace' }}>
            {info.getValue()?.replace(/,(?!\s)/g, ', ') || ''}
          </span>
        ),
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
  )
}

export default AlertModal
