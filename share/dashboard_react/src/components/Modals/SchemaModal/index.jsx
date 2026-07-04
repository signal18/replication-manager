import React, { useMemo } from 'react'
import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay,
  Badge, Wrap, WrapItem, Text
} from '@chakra-ui/react'
import { useSelector } from 'react-redux'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import NotFound from '../../NotFound'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'

const columnHelper = createColumnHelper()

// SchemaModal lists the cluster SchemaStateMachine content: inventory and
// lifecycle tags (SCHTAG / CINF info states) as pills, findings (SCH errors
// and warnings) as a table.
function SchemaModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const { isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)

  const allStates = clusterData?.schemaStates || []

  const tags = useMemo(
    () => allStates.filter((s) => (s.ErrKey || '').startsWith('SCHTAG') || (s.ErrKey || '').startsWith('CINF')),
    [allStates]
  )
  const findings = useMemo(
    () => allStates.filter((s) => !(s.ErrKey || '').startsWith('SCHTAG') && !(s.ErrKey || '').startsWith('CINF')),
    [allStates]
  )

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.ErrKey, { cell: (i) => i.getValue(), header: 'Key', id: 'key', maxWidth: 120 }),
      columnHelper.accessor(
        (row) => (
          <Text whiteSpace='pre-wrap' fontFamily='monospace' fontSize='sm'>
            {row.ErrDesc}
          </Text>
        ),
        { cell: (i) => i.getValue(), header: 'Description', id: 'desc' }
      ),
      columnHelper.accessor((row) => row.ServerUrl, { cell: (i) => i.getValue(), header: 'Server', id: 'server', maxWidth: 220 })
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
        <ModalHeader whiteSpace='pre-line'>
          {`Schema — Findings: ${findings.length} / Tags: ${tags.length}`}
        </ModalHeader>
        <ModalCloseButton />
        <ModalBody overflow='auto' p='0'>
          {tags.length > 0 && (
            <Wrap p='12px' justify='center'>
              {tags.map((t) => (
                <WrapItem key={t.ErrKey}>
                  <Badge colorScheme='cyan' variant='subtle' px='2' py='1' textTransform='none'>
                    {t.ErrDesc}
                  </Badge>
                </WrapItem>
              ))}
            </Wrap>
          )}
          {findings.length === 0 ? (
            <NotFound text='No schema findings' />
          ) : (
            <DataTable key='SchemaFindings' columns={columns} data={findings} cellValueAlign='start' />
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default SchemaModal
