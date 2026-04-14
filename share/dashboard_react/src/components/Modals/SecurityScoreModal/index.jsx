import React, { useMemo, useState } from 'react'
import {
  Modal, ModalBody, ModalCloseButton, ModalContent, ModalHeader, ModalOverlay,
  Box, Grid, GridItem, Text, Badge, HStack, VStack, Divider,
  Button, Tooltip, Menu, MenuButton, MenuList, MenuItem
} from '@chakra-ui/react'
import { useSelector, useDispatch } from 'react-redux'
import { DataTable } from '../../DataTable'
import { createColumnHelper } from '@tanstack/react-table'
import parentStyles from '../styles.module.scss'
import { useTheme } from '../../../ThemeProvider'
import { clusterService } from '../../../services/clusterService'
import { showSuccessToast, showErrorToast } from '../../../redux/toastSlice'

// Label map for security check tags — order controls display order in the grid.
const CHECK_LABELS = {
  hasSSL:              'SSL/TLS transport encryption',
  hasZeroSSL:          'Require secure transport (zero plain-text)',
  hasTableEncryption:  'At-rest table encryption',
  hasBinlogEncryption: 'Binary log encryption',
  hasTmpEncryption:    'Temporary files encryption',
  hasBackupEncryption: 'Backup encryption',
  hasAuditPlugins:     'Audit plugin active',
  noEmptyPassword:     'No empty-password accounts',
  noWeakAuthPlugin:    'No weak auth plugin (mysql_native_password / old_password)',
  hasStrongPwd:        'Strong password validation plugin',
  hasPasswordRotation: 'Password rotation (default_password_lifetime > 0)',
  hasParsecPlugins:    'PARSEC authentication (MariaDB 11.6+)',
  hasPrepareStatement: 'Prepared statements supported',
  hasProxies:          'Proxy layer configured',
  noHostnameGrants:    'No DNS hostname grants (IP-based grants only)',
  noClearPwdConfigs:   'No clear-text passwords in DB server config files',
  noClearPwdHistory:   'No clear-text passwords in shell/MySQL history',
  noClearPwdBinlogs:   'No clear-text passwords in binary logs',
  hasLastLTS:          'Running a supported LTS release',
}

const GRADE_COLOR = { A: 'green', B: 'teal', C: 'yellow', D: 'orange', F: 'red' }

function SecurityScoreModal({ isOpen, closeModal }) {
  const { theme } = useTheme()
  const dispatch = useDispatch()
  const { isMobile, isTablet, isDesktop } = useSelector((state) => state.common)
  const clusterData = useSelector((state) => state.cluster.clusterData)
  const baseURL = useSelector((state) => state.auth?.baseURL || '')
  const [fixing, setFixing] = useState({})

  const score = clusterData?.securityScore
  const statesArray = clusterData?.securityStates || []
  const clusterName = clusterData?.name

  // Build a lookup from err_key → RemediationEntry using the server-provided plan.
  // This is the single source of truth — no AUTO_FIXABLE / FIX_RISK duplication here.
  const remediationByKey = useMemo(() => {
    const map = {}
    for (const entry of clusterData?.securityRemediations?.remediations || []) {
      if (!map[entry.err_key]) map[entry.err_key] = entry
    }
    return map
  }, [clusterData?.securityRemediations])

  const handleFix = async (errKey, tag) => {
    const fixKey = tag ? `${errKey}:${tag}` : errKey
    setFixing((prev) => ({ ...prev, [fixKey]: true }))
    try {
      const entry = remediationByKey[errKey]
      const fix = tag ? entry?.fixes?.find((f) => f.tag === tag) : entry?.fixes?.[0]

      let status, data
      if (fix?.type === 'settings_switch' && fix?.url) {
        ;({ status, data } = await clusterService.switchClusterSetting(fix.url, baseURL))
      } else {
        ;({ status, data } = await clusterService.fixSecState(clusterName, errKey, baseURL, tag))
      }

      if (status === 200) {
        const risk = fix?.risk ?? ''
        dispatch(showSuccessToast({
          title: `Fix applied: ${errKey}`,
          description: risk === 'disruptive'
            ? 'Config deployed. Rolling restart in progress…'
            : 'Fix applied successfully.',
        }))
      } else {
        dispatch(showErrorToast({
          title: `Fix failed: ${errKey}`,
          description: typeof data === 'string' ? data : 'Unexpected error',
        }))
      }
    } catch (err) {
      dispatch(showErrorToast({ title: `Fix failed: ${errKey}`, description: String(err) }))
    } finally {
      setFixing((prev) => ({ ...prev, [fixKey]: false }))
    }
  }

  const columnHelper = createColumnHelper()
  const stateColumns = useMemo(() => [
    columnHelper.accessor((row) => row, {
      id: 'desc',
      header: () => <span>Finding</span>,
      cell: (info) => {
        const row = info.getValue()
        // ErrKey is stored as composite "SEC0109@server:3306" — extract the code
        const errKey = (row.ErrKey || '').split('@')[0]
        const desc = row.ErrDesc?.replace(/,(?!\s)/g, ', ') || ''
        const entry = remediationByKey[errKey]
        if (!entry?.auto_fixable) return <span>{desc}</span>

        // Fixes with type=add_tag or settings_switch are actionable; collect for the menu.
        const tagFixes = (entry.fixes || []).filter((f) => f.type === 'add_tag' || f.type === 'settings_switch')
        const isMulti = tagFixes.length > 1
        const primaryRisk = tagFixes[0]?.risk ?? entry.fixes?.[0]?.risk ?? 'safe'
        const isDisruptive = primaryRisk === 'disruptive'

        return (
          <HStack spacing={2} align='start'>
            {isMulti ? (
              // Multiple tag options — render a dropdown so the operator can choose.
              <Menu size='xs'>
                <Tooltip label={`Risk: ${primaryRisk}`} placement='top'>
                  <MenuButton
                    as={Button}
                    size='xs'
                    colorScheme={isDisruptive ? 'orange' : 'green'}
                    flexShrink={0}
                    rightIcon={<span>▾</span>}>
                    Fix
                  </MenuButton>
                </Tooltip>
                <MenuList fontSize='sm'>
                  {tagFixes.map((fix) => (
                    <MenuItem
                      key={fix.tag}
                      isDisabled={!!fixing[`${errKey}:${fix.tag}`]}
                      onClick={() => handleFix(errKey, fix.tag)}>
                      {fix.tag}
                      {fix.risk === 'disruptive' && <Badge ml={2} colorScheme='orange' fontSize='xs'>restart</Badge>}
                    </MenuItem>
                  ))}
                </MenuList>
              </Menu>
            ) : (
              <Tooltip label={`Risk: ${primaryRisk}`} placement='top'>
                <Button
                  size='xs'
                  colorScheme={isDisruptive ? 'orange' : 'green'}
                  flexShrink={0}
                  isLoading={!!fixing[errKey]}
                  onClick={() => handleFix(errKey)}>
                  Fix
                </Button>
              </Tooltip>
            )}
            <span>{desc}</span>
          </HStack>
        )
      },
    }),
    columnHelper.accessor((row) => row.ServerUrl, {
      id: 'server',
      cell: (info) => info.getValue() || '',
      header: () => <span>Server</span>,
      maxWidth: '200',
    }),
    columnHelper.accessor((row) => (row.ErrKey || '').split('@')[0], {
      id: 'code',
      cell: (info) => info.getValue() || '',
      header: () => <span>Code</span>,
      maxWidth: '120',
    }),
  ], [fixing, clusterName, baseURL, remediationByKey])

  if (!score) return null

  const gradeColor = GRADE_COLOR[score.grade] || 'gray'
  const checks = Object.entries(CHECK_LABELS).map(([key, label]) => ({
    label,
    pass: score[key] === true,
  }))

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent
        className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}
        width={isDesktop ? '75%' : isTablet ? '97%' : '99%'}
        maxWidth='none'
        maxH='90%'
        overflow='hidden'>
        <ModalHeader style={{ background: `var(--chakra-colors-${gradeColor}-500)`, color: 'white' }}>
          Security Score — {score.score}/100 &nbsp;
          <Badge fontSize='1.2em' colorScheme={gradeColor} variant='solid'>{score.grade}</Badge>
        </ModalHeader>
        <ModalCloseButton color='white' />
        <ModalBody overflowY='auto' pb={6}>
          {/* security check grid */}
          <Grid templateColumns={isDesktop ? 'repeat(2, 1fr)' : '1fr'} gap={2} mb={4}>
            {checks.map(({ label, pass }) => (
              <GridItem key={label}>
                <HStack spacing={2}>
                  <Badge colorScheme={pass ? 'green' : 'red'} minW='40px' textAlign='center'>
                    {pass ? 'PASS' : 'FAIL'}
                  </Badge>
                  <Text fontSize='sm'>{label}</Text>
                </HStack>
              </GridItem>
            ))}
          </Grid>

          {/* Open security states */}
          {statesArray.length > 0 && (
            <>
              <Divider my={3} />
              <Text fontWeight='bold' mb={2}>Open Security Findings ({statesArray.length})</Text>
              <DataTable
                key='SecurityStates'
                columns={stateColumns}
                data={statesArray}
                cellValueAlign='start'
              />
            </>
          )}
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default SecurityScoreModal
