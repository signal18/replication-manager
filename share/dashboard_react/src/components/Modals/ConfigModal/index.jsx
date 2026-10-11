import React, { useMemo, useState } from 'react'
import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay, Text, HStack, Button, Tooltip
} from '@chakra-ui/react'
import { useSelector, useDispatch } from 'react-redux'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import NotFound from '../../NotFound'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import { showSuccessToast, showErrorToast } from '../../../redux/toastSlice'

const columnHelper = createColumnHelper()

const severityOf = (s) =>
  s.ErrType === 'ERROR' ? 'Blocker' : s.ErrType === 'INFO' ? 'Info' : 'Warning'

const severityRank = { Blocker: 0, Warning: 1, Info: 2 }

// ConfigModal lists the cluster ConfigStateMachine content (deprecated
// variables, pending compliance modulesets, unapplied config changes) in a
// severity table matching the health AlertModal. A state with a remediation
// (configRemediations, e.g. WARN0234) gets an Enable button that calls it.
function ConfigModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const dispatch = useDispatch()
  const { isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const baseURL = useSelector((state) => state.auth?.baseURL || '')
  const [fixing, setFixing] = useState({})

  const remediationByKey = useMemo(() => {
    const map = {}
    for (const entry of clusterData?.configRemediations?.remediations || []) {
      if (!map[entry.err_key]) map[entry.err_key] = entry
    }
    return map
  }, [clusterData?.configRemediations])

  const handleFix = async (errKey) => {
    const fix = remediationByKey[errKey]?.fixes?.[0]
    // settings_set: an explicit value, so a stale page or a double click never undoes it.
    if (!['settings_set', 'settings_switch'].includes(fix?.type) || !fix?.url) return
    setFixing((prev) => ({ ...prev, [errKey]: true }))
    try {
      const { status, data } = await clusterService.switchClusterSetting(fix.url, baseURL)
      if (status === 200) {
        // The setting is saved; the work it triggers (live resize) runs in the background.
        dispatch(showSuccessToast({ title: `Fix scheduled: ${errKey}`, description: fix.description }))
      } else {
        dispatch(showErrorToast({ title: `Fix failed: ${errKey}`, description: typeof data === 'string' ? data : 'Unexpected error' }))
      }
    } catch (err) {
      dispatch(showErrorToast({ title: `Fix failed: ${errKey}`, description: String(err) }))
    } finally {
      setFixing((prev) => ({ ...prev, [errKey]: false }))
    }
  }

  const findings = useMemo(
    () =>
      (clusterData?.configStates || [])
        .map((s) => ({ ...s, severity: severityOf(s) }))
        .sort((a, b) => severityRank[a.severity] - severityRank[b.severity]),
    [clusterData?.configStates]
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
        (row) => {
          const desc = (
            <Text whiteSpace='pre-wrap' fontFamily='monospace' fontSize='sm'>
              {row.ErrDesc}
            </Text>
          )
          const entry = remediationByKey[row.ErrKey]
          if (!entry?.auto_fixable) return desc
          const fix = entry.fixes?.[0]
          return (
            <HStack spacing={2} align='start'>
              <Tooltip label={`${fix?.description || ''} (risk: ${fix?.risk || 'safe'})`} placement='top'>
                <Button size='xs' colorScheme='green' flexShrink={0} isLoading={!!fixing[row.ErrKey]} onClick={() => handleFix(row.ErrKey)}>
                  Enable
                </Button>
              </Tooltip>
              {desc}
            </HStack>
          )
        },
        { cell: (i) => i.getValue(), header: 'Description', id: 'desc' }
      ),
      columnHelper.accessor((row) => row.ServerUrl, { cell: (i) => i.getValue(), header: 'Server', id: 'server', maxWidth: 220 })
    ],
    [fixing, remediationByKey, baseURL]
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
          {`Config — Blockers: ${blockerCount} / Warnings: ${warningCount} / Infos: ${infoCount}`}
        </ModalHeader>
        <ModalCloseButton />
        <ModalBody overflow='auto' p='0'>
          {findings.length === 0 ? (
            <NotFound text='No config findings' />
          ) : (
            <DataTable key='ConfigFindings' columns={columns} data={findings} cellValueAlign='start' />
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default ConfigModal
