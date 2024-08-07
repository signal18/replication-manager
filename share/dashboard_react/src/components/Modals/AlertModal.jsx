import {
  background,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  Table,
  useColorMode
} from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import { useSelector } from 'react-redux'
import NotFound from '../NotFound'
import { DataTable } from '../DataTable'
import { createColumnHelper } from '@tanstack/react-table'

function AlertModal({ type, isOpen, closeModal }) {
  const { colorMode } = useColorMode()
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

  const styles = {
    header: {
      background:
        type === 'error'
          ? colorMode === 'light'
            ? 'red.200'
            : 'red.700'
          : colorMode === 'light'
            ? 'orange.200'
            : 'orange.700'
    },
    body: {
      overflow: 'auto',
      padding: '2'
    }
  }

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
        minHeight={isMobile ? '300px' : '450px'}
        textAlign='center'
        overflow='hidden'>
        <ModalHeader whiteSpace='pre-line' sx={styles.header}>
          {type === 'error' ? `Errors: ${data.length}` : `Warnings: ${data.length}`}
        </ModalHeader>
        <ModalCloseButton />
        <ModalBody sx={styles.body}>
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
