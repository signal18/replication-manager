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

const severityOf = (s) =>
  s.ErrType === 'ERROR' ? 'Blocker' : s.ErrType === 'INFO' ? 'Info' : 'Warning'

const severityRank = { Blocker: 0, Warning: 1, Info: 2 }

// SchemaModal lists the cluster SchemaStateMachine content: inventory tags
// (SCHTAG counts) as pills, every other state (SCH findings, CINF lifecycle
// infos) in a severity table matching the health AlertModal.
function SchemaModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const { isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)

  const allStates = clusterData?.schemaStates || []

  const tags = useMemo(
    () => allStates.filter((s) => (s.ErrKey || '').startsWith('SCHTAG')),
    [allStates]
  )
  const findings = useMemo(
    () =>
      allStates
        .filter((s) => !(s.ErrKey || '').startsWith('SCHTAG'))
        .map((s) => ({ ...s, severity: severityOf(s) }))
        .sort((a, b) => severityRank[a.severity] - severityRank[b.severity]),
    [allStates]
  )

  const blockerCount = findings.filter((s) => s.severity === 'Blocker').length
  const infoCount = findings.filter((s) => s.severity === 'Info').length
  const warningCount = findings.length - blockerCount - infoCount

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.severity, {
        id: 'severity',
        cell: (info) => (
          <span
            style={{
              color:
                info.getValue() === 'Blocker'
                  ? 'var(--danger-primary-color)'
                  : info.getValue() === 'Info'
                    ? 'var(--chakra-colors-blue-400)'
                    : 'var(--warning-primary-color)',
              fontWeight: 'bold'
            }}>
            {info.getValue()}
          </span>
        ),
        header: 'Severity',
        maxWidth: 120
      }),
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
          {`Schema — Blockers: ${blockerCount} / Warnings: ${warningCount} / Infos: ${infoCount} / Tags: ${tags.length}`}
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
