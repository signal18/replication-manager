import { Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay } from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import { useSelector } from 'react-redux'
import NotFound from '../../NotFound'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import styles from './styles.module.scss'

function AlertModal({ type, isOpen, closeModal }) {
  const {
    common: { isMobile, isTablet, isDesktop },
    cluster: { clusterAlerts }
  } = useSelector((state) => state)

  const [data, setData] = useState([])

  useEffect(() => {
    if (type === 'error' && clusterAlerts?.errors?.length > 0) {
      setData(clusterAlerts.errors)
    } else if (type === 'warning' && clusterAlerts?.warnings?.length > 0) {
      setData(clusterAlerts.warnings)
    }
  }, [clusterAlerts])

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.desc, {
        id: 'desc',
        cell: (info) => info.getValue(),
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
        cell: (info) => info.getValue(),
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
            <DataTable columns={columns} data={data} cellValueAlign='start' />
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default AlertModal
