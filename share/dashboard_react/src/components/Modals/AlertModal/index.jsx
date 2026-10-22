import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay, Button, HStack, Stack, Tooltip
} from '@chakra-ui/react'
import React, { useState, useEffect, useMemo } from 'react'
import { useSelector, useDispatch } from 'react-redux'
import NotFound from '../../NotFound'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import styles from './styles.module.scss'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import { showSuccessToast, showErrorToast } from '../../../redux/toastSlice'
import ConfirmModal from '../ConfirmModal'

// the label of a remediation fix's button, from its API action
const fixLabel = (fix) =>
  fix?.url?.includes('/snapback-release/') ? 'Release' : fix?.url?.includes('/snapback/') ? 'Snapback to latest' : 'Remediate'

function AlertModal({ type, isOpen, closeModal, alerts, title = 'Health' }) {
  const { theme } = useTheme()
  const {
    common: { isMobile, isTablet, isDesktop }
  } = useSelector((state) => state)

  const source = alerts ?? useSelector((state) => state.cluster.clusterAlerts)
  const [data, setData] = useState([])
  const dispatch = useDispatch()
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const baseURL = useSelector((state) => state.auth?.baseURL || '')
  const [running, setRunning] = useState({})
  const [confirmFix, setConfirmFix] = useState(null)

  // Operational states with a remediation (stateRemediations, e.g. WARN0255 a snapback): a
  // button per fix, keyed by the state's number (CODE@server). Only for this cluster's own
  // alerts: alerts handed in by another view are not the selected cluster's.
  const remediationByNumber = useMemo(() => {
    const map = {}
    if (alerts) return map
    for (const entry of clusterData?.stateRemediations?.remediations || []) {
      if (entry.auto_fixable) map[entry.server ? `${entry.err_key}@${entry.server}` : entry.err_key] = entry
    }
    return map
  }, [alerts, clusterData?.stateRemediations])

  const runFix = async (number, fix) => {
    const key = `${number}|${fix.url}`
    setRunning((prev) => ({ ...prev, [key]: true }))
    try {
      const { status, data } = await clusterService.runServerAction(fix.url, baseURL)
      if (status >= 200 && status < 300) {
        dispatch(showSuccessToast({ title: `${fixLabel(fix)}: ${number.split('@')[0]}`, description: fix.description }))
      } else {
        dispatch(showErrorToast({ title: `${fixLabel(fix)} failed`, description: typeof data === 'string' ? data : 'Unexpected error' }))
      }
    } catch (err) {
      dispatch(showErrorToast({ title: `${fixLabel(fix)} failed`, description: String(err) }))
    } finally {
      setRunning((prev) => ({ ...prev, [key]: false }))
    }
  }

  useEffect(() => {
    if (type === 'health') {
      setData([
        ...(source?.errors || []).map((row) => ({ ...row, severity: 'Blocker' })),
        ...(source?.warnings || []).map((row) => ({ ...row, severity: 'Warning' })),
        ...(source?.infos || []).map((row) => ({ ...row, severity: 'Info' }))
      ])
    } else if (type === 'info' && source?.infos?.length > 0) {
      setData(source.infos)
    } else if (type === 'error' && source?.errors?.length > 0) {
      setData(source.errors)
    } else if (type === 'warning' && source?.warnings?.length > 0) {
      setData(source.warnings)
    } else {
      setData([])
    }
  }, [source, type])

  const blockerCount = source?.errors?.length || 0
  const warningCount = source?.warnings?.length || 0
  const infoCount = source?.infos?.length || 0

  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      ...(type === 'health'
        ? [
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
              header: () => <span>Severity</span>,
              maxWidth: '120'
            })
          ]
        : []),
      columnHelper.accessor((row) => row.desc, {
        id: 'desc',
        cell: (info) => {
          const desc = (
            <span style={{ whiteSpace: 'pre-wrap', fontFamily: 'monospace' }}>
              {info.getValue()?.replace(/,(?!\s)/g, ', ') || ''}
            </span>
          )
          const number = info.row.original.number || ''
          const entry = remediationByNumber[number]
          if (!entry?.fixes?.length) return desc
          return (
            <Stack spacing={1} align='start'>
              {desc}
              <HStack spacing={2}>
                {entry.fixes.map((fix) => (
                  <Tooltip key={fix.url} label={`${fix.description} (risk: ${fix.risk || 'safe'})`} placement='top'>
                    <Button size='xs' colorScheme={fix.risk === 'safe' ? 'green' : 'orange'} isLoading={!!running[`${number}|${fix.url}`]}
                      onClick={() => setConfirmFix({ number, fix })}>
                      {fixLabel(fix)}
                    </Button>
                  </Tooltip>
                ))}
              </HStack>
            </Stack>
          )
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
    [type, remediationByNumber, running]
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
          className={`${styles.header} ${
            type === 'error' || (type === 'health' && blockerCount > 0)
              ? styles.red
              : type === 'info'
                ? styles.blue
                : styles.orange
          }`}>
          {type === 'health'
            ? `${title} — Blockers: ${blockerCount} / Warnings: ${warningCount} / Infos: ${infoCount}`
            : type === 'error'
              ? `Errors: ${data.length}`
              : type === 'info'
                ? `Infos: ${data.length}`
                : `Warnings: ${data.length}`}
        </ModalHeader>
        <ModalCloseButton />
        <ModalBody className={styles.body}>
          {data.length === 0 ? (
            <NotFound text={type === 'health' ? 'No open alerts' : `No ${type} alerts found`} />
          ) : (
            <DataTable
              key="Alerts"
              className={`${styles.table}  ${
                type === 'error' || (type === 'health' && blockerCount > 0)
                  ? styles.red
                  : type === 'info'
                    ? styles.blue
                    : styles.orange
              }`}
              columns={columns}
              data={data}
              cellValueAlign='start'
            />
          )}
        </ModalBody>
      </ModalContent>
      {confirmFix && (
        <ConfirmModal
          isOpen={!!confirmFix}
          title={`${fixLabel(confirmFix.fix)}?`}
          body={confirmFix.fix.description}
          onConfirmClick={() => {
            const { number, fix } = confirmFix
            setConfirmFix(null)
            runFix(number, fix)
          }}
          closeModal={() => setConfirmFix(null)}
        />
      )}
    </Modal>
  )
}

export default AlertModal
