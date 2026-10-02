import {
  Modal, ModalOverlay, ModalContent, ModalHeader, ModalBody, ModalCloseButton,
  Box, VStack, HStack, Text, Badge, Divider, Table, Thead, Tbody, Tr, Th, Td,
  Tabs, TabList, TabPanels, Tab, TabPanel, Spinner
} from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import { globalClustersService } from '../../../services/globalClustersService'
import { HiMoon, HiSun } from 'react-icons/hi'
import { FaUserPlus } from 'react-icons/fa'
import { TbKey } from 'react-icons/tb'
import RMButton from '../../RMButton'
import { useTheme } from '../../../ThemeProvider'
import parentStyles from '../styles.module.scss'

// Static checkmark character for grant/role indicators
const CHECK_MARK = '\u2713'

// ConsumedUnits: what the logged user consumed this month, in units (debit = plan +
// over-commit, credit = under-commit, unit-months so far), per cluster and unit, with a
// total per unit kind. No amount here: the money is the provider's.
function ConsumedUnits({ theme }) {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let alive = true
    globalClustersService.getMyUnits(undefined).then((res) => { if (alive) setData(res.data) }).catch((e) => { if (alive) setError(e?.message || 'failed') })
    return () => { alive = false }
  }, [])
  const f = (v) => (v === undefined || v === null ? '-' : Number(v).toFixed(3))
  const muted = theme === 'light' ? 'gray.600' : 'gray.400'
  if (error) return <Text fontSize='sm' color='red.400'>{error}</Text>
  if (!data) return <Spinner size='sm' />
  const rows = data.rows || []
  if (rows.length === 0) return <Text fontSize='sm' color={muted}>No cluster consumed units for {data.user} in {data.month}.</Text>
  const clusters = [...new Set(rows.map((r) => r.cluster))]
  return (
    <VStack align='stretch' spacing={3}>
      <Text fontSize='sm' color={muted}>Month {data.month}, {Number(data.elapsedPct || 0).toFixed(1)}% elapsed. Debit = plan + over-commit, credit = under-commit, in unit-months so far; net = debit - credit; projected = end of month at the current rate.</Text>
      <Table size='sm' variant='simple'>
        <Thead><Tr><Th>Cluster</Th><Th>Unit</Th><Th isNumeric>Declared</Th><Th isNumeric>Debit</Th><Th isNumeric>Credit</Th><Th isNumeric>Net</Th><Th isNumeric>Projected net</Th></Tr></Thead>
        <Tbody>
          {clusters.map((c) => {
            const cr = rows.filter((r) => r.cluster === c && (r.plan > 0 || r.debit > 0 || r.credit > 0))
            const sub = cr.reduce((a, r) => ({ debit: a.debit + r.debit, credit: a.credit + r.credit, net: a.net + r.net, pnet: a.pnet + r.projectedNet }), { debit: 0, credit: 0, net: 0, pnet: 0 })
            return [
              ...cr.map((r) => (
                <Tr key={c + r.family}>
                  <Td>{c}{r.sponsor ? <Badge ml={2} size='sm' colorScheme='purple'>sponsor</Badge> : null}</Td>
                  <Td>{r.unit}{r.family === 'stateful_dbu' ? ' (stateful app)' : ''}</Td>
                  <Td isNumeric>{r.plan}</Td><Td isNumeric>{f(r.debit)}</Td><Td isNumeric>{f(r.credit)}</Td><Td isNumeric>{f(r.net)}</Td><Td isNumeric>{f(r.projectedNet)}</Td>
                </Tr>
              )),
              <Tr key={c + '-total'} fontWeight={600}><Td>{c} total</Td><Td></Td><Td></Td><Td isNumeric>{f(sub.debit)}</Td><Td isNumeric>{f(sub.credit)}</Td><Td isNumeric>{f(sub.net)}</Td><Td isNumeric>{f(sub.pnet)}</Td></Tr>
            ]
          })}
          {(data.totals || []).map((t) => (
            <Tr key={'u-' + t.unit} fontWeight={600} bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'}>
              <Td>Total {t.unit}</Td><Td>{t.unit}</Td><Td></Td><Td isNumeric>{f(t.debit)}</Td><Td isNumeric>{f(t.credit)}</Td><Td isNumeric>{f(t.net)}</Td><Td isNumeric>{f(t.projectedNet)}</Td>
            </Tr>
          ))}
          {data.total && (
            <Tr fontWeight={700}><Td>Total</Td><Td>all units</Td><Td></Td><Td isNumeric>{f(data.total.debit)}</Td><Td isNumeric>{f(data.total.credit)}</Td><Td isNumeric>{f(data.total.net)}</Td><Td isNumeric>{f(data.total.projectedNet)}</Td></Tr>
          )}
        </Tbody>
      </Table>
    </VStack>
  )
}

function UserInfoPanel({ isOpen, closeModal, user, onLogout, canAddUser = false, onAddUser, onApiTokens }) {
  const { theme, toggleTheme } = useTheme()
  const badgeVariant = theme === 'dark' ? 'solid' : 'subtle'

  const grants = user?.grants || {}
  const roles = user?.roles || {}
  // Derive cluster names from both grants and roles in case a user has roles but no grants
  const clusterNames = [...new Set([...Object.keys(grants), ...Object.keys(roles)])].sort()

  // Collect all unique grant names across clusters
  const allGrants = new Set()
  clusterNames.forEach(name => {
    Object.keys(grants[name] || {}).forEach(g => allGrants.add(g))
  })
  const grantList = [...allGrants].sort()

  // Collect all unique role names across clusters
  const allRoles = new Set()
  clusterNames.forEach(name => {
    Object.keys(roles[name] || {}).forEach(r => allRoles.add(r))
  })
  const roleList = [...allRoles].sort()
  // API tokens need the token-create grant on at least one cluster (#1835).
  const canIssueTokens = clusterNames.some((name) => grants[name]?.['token-create'])

  const stickyBg = theme === 'light' ? 'white' : 'gray.800'

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader fontSize='md'>User Profile</ModalHeader>
        <ModalCloseButton />
        <ModalBody pb={4}>
          <Tabs size='sm' variant='enclosed'>
            <TabList><Tab>Profile</Tab><Tab>Consumed</Tab></TabList>
            <TabPanels>
            <TabPanel px={0}>
          <VStack align='stretch' spacing={4}>

            <Box p={3} borderRadius='md' bg={theme === 'light' ? 'gray.50' : 'rgba(255,255,255,0.05)'}>
              <HStack spacing={4}>
                <Text fontSize='sm' fontWeight={600}>User:</Text>
                <Text fontSize='sm'>{user?.DisplayName || user?.User || '-'}</Text>
                <Badge variant={badgeVariant} colorScheme={user?.AuthType === 'SSO' ? 'purple' : 'blue'} size='sm'>
                  {user?.AuthType || 'Local'}
                </Badge>
              </HStack>
              {user?.Email && (
                <HStack spacing={4}>
                  <Text fontSize='sm' fontWeight={600}>Email:</Text>
                  <Text fontSize='sm'>{user.Email}</Text>
                </HStack>
              )}
            </Box>

            <HStack spacing={3} width='100%' justify='space-between'>
              <RMButton
                variant='ghost'
                size='small'
                onClick={toggleTheme}
              >
                <HStack spacing={1}>
                  {theme === 'light' ? <HiMoon color='midnightblue' /> : <HiSun color='gold' />}
                  <Text fontSize='sm'>{theme === 'light' ? 'Dark mode' : 'Light mode'}</Text>
                </HStack>
              </RMButton>
              <HStack spacing={3}>
                {onApiTokens && canIssueTokens && (
                  <RMButton variant='outline' onClick={onApiTokens}>
                    <HStack spacing={1}>
                      <TbKey />
                      <Text fontSize='sm'>API tokens</Text>
                    </HStack>
                  </RMButton>
                )}
                {canAddUser && (
                  <RMButton onClick={onAddUser}>
                    <HStack spacing={1}>
                      <FaUserPlus />
                      <Text fontSize='sm'>Add User</Text>
                    </HStack>
                  </RMButton>
                )}
                <RMButton colorScheme='red' onClick={onLogout}>
                  Logout
                </RMButton>
              </HStack>
            </HStack>

            {clusterNames.length > 0 && roleList.length > 0 && (
              <>
                <Divider />
                <Text fontSize='sm' fontWeight={600} color={theme === 'light' ? 'gray.600' : 'gray.400'}>
                  Roles per cluster
                </Text>
                <Box overflowX='auto' fontSize='xs'>
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th position='sticky' left={0} bg={stickyBg} zIndex={1}>Role</Th>
                        {clusterNames.map(name => (
                          <Th key={name} textAlign='center'>{name}</Th>
                        ))}
                      </Tr>
                    </Thead>
                    <Tbody>
                      {roleList.map(role => (
                        <Tr key={role}>
                          <Td position='sticky' left={0} bg={stickyBg} fontSize='xs'>{role}</Td>
                          {clusterNames.map(name => (
                            <Td key={name} textAlign='center'>
                              <Badge variant={badgeVariant} colorScheme={roles[name]?.[role] ? 'blue' : 'gray'} size='sm'>
                                {roles[name]?.[role] ? CHECK_MARK : '-'}
                              </Badge>
                            </Td>
                          ))}
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </Box>
              </>
            )}

            {clusterNames.length > 0 && roleList.length === 0 && (
              <Text fontSize='sm' color={theme === 'light' ? 'gray.500' : 'gray.500'} textAlign='center' py={2}>
                No roles assigned
              </Text>
            )}

            {clusterNames.length > 0 && (
              <>
                <Divider />
                <Text fontSize='sm' fontWeight={600} color={theme === 'light' ? 'gray.600' : 'gray.400'}>
                  Grants per cluster
                </Text>
                <Box maxH='400px' overflowY='auto' overflowX='auto' fontSize='xs'>
                  <Table size='sm' variant='simple'>
                    <Thead>
                      <Tr>
                        <Th position='sticky' left={0} bg={stickyBg} zIndex={1}>Grant</Th>
                        {clusterNames.map(name => (
                          <Th key={name} textAlign='center'>{name}</Th>
                        ))}
                      </Tr>
                    </Thead>
                    <Tbody>
                      {grantList.map(grant => (
                        <Tr key={grant}>
                          <Td position='sticky' left={0} bg={stickyBg} fontSize='xs'>{grant}</Td>
                          {clusterNames.map(name => (
                            <Td key={name} textAlign='center'>
                              <Badge variant={badgeVariant} colorScheme={grants[name]?.[grant] ? 'blue' : 'gray'} size='sm'>
                                {grants[name]?.[grant] ? CHECK_MARK : '-'}
                              </Badge>
                            </Td>
                          ))}
                        </Tr>
                      ))}
                    </Tbody>
                  </Table>
                </Box>
              </>
            )}

            {clusterNames.length === 0 && (
              <Text fontSize='sm' color={theme === 'light' ? 'gray.500' : 'gray.500'} textAlign='center' py={4}>
                No cluster grants available
              </Text>
            )}

          </VStack>
            </TabPanel>
            <TabPanel px={0}>
              <ConsumedUnits theme={theme} />
            </TabPanel>
            </TabPanels>
          </Tabs>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

export default UserInfoPanel
