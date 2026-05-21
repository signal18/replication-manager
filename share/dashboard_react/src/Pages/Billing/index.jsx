import { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import {
  Badge,
  Box,
  Button,
  Divider,
  Flex,
  FormControl,
  FormLabel,
  HStack,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Select,
  SimpleGrid,
  Spinner,
  Table,
  Tbody,
  Td,
  Text,
  Th,
  Thead,
  Tr,
  VStack,
  useDisclosure
} from '@chakra-ui/react'
import {
  clearChangeSubscriptionError,
  clearUpdateBillingProfileStatus,
  fetchBillingPlansCatalog,
  fetchBillingProfile,
  fetchBillingSubscription,
  fetchBillingTransactions,
  fetchPersonalBalance,
  requestBillingSubscriptionChange,
  updateBillingProfile
} from '../../redux/billingSlice'
import { useTheme } from '../../ThemeProvider'
import parentStyles from '../../components/Modals/styles.module.scss'

const PAGE_SIZE = 20

function Billing() {
  const dispatch = useDispatch()
  const [offset, setOffset] = useState(0)
  const [selectedPlanCode, setSelectedPlanCode] = useState('')
  const [paymentUrl, setPaymentUrl] = useState(null)
  const { isOpen, onOpen, onClose } = useDisclosure()
  const { theme } = useTheme()

  const cardBorderColor = 'var(--quaternary-color)'
  const mutedTextColor = 'var(--darkgray-color)'
  const balanceCardBg = 'var(--secondary-gray-color)'
  const subscriptionCardBg = 'var(--secondary-gray-color)'
  const sectionCardBg = 'var(--secondary-gray-color)'
  const pendingBoxBg = 'var(--warning-tertiary-color)'
  const pendingBorderColor = 'var(--warning-secondary-color)'
  const tableHeadBg = 'var(--quaternary-color)'
  const modalHeaderBg = 'var(--quaternary-color)'
  const modalHeaderBorder = 'var(--primary-color)'

  const {
    balance,
    subscription,
    plansCatalog,
    transactions,
    billingProfile,
    loadingBalance,
    loadingSubscription,
    loadingPlansCatalog,
    loadingChangeSubscription,
    loadingTransactions,
    loadingBillingProfile,
    loadingUpdateBillingProfile,
    errorBalance,
    errorSubscription,
    errorPlansCatalog,
    errorChangeSubscription,
    errorTransactions,
    errorBillingProfile,
    errorUpdateBillingProfile,
    updateBillingProfileSuccess
  } = useSelector((state) => state.billing)

  const emptyProfile = { name: '', email: '', phone: '', address: '', city: '', country: '', postal_code: '', vat_number: '' }
  const [profileForm, setProfileForm] = useState(emptyProfile)

  const rows = useMemo(() => (Array.isArray(transactions) ? transactions : []), [transactions])

  useEffect(() => {
    dispatch(fetchPersonalBalance())
    dispatch(fetchBillingSubscription())
    dispatch(fetchBillingPlansCatalog())
    dispatch(fetchBillingProfile())
  }, [dispatch])

  useEffect(() => {
    if (billingProfile && typeof billingProfile === 'object') {
      const bp = billingProfile.billing_profile || billingProfile
      setProfileForm({
        name: bp.name || '',
        email: bp.email || '',
        phone: bp.phone || '',
        address: bp.address || '',
        city: bp.city || '',
        country: bp.country || '',
        postal_code: bp.postal_code || '',
        vat_number: bp.vat_number || ''
      })
    }
  }, [billingProfile])

  useEffect(() => {
    dispatch(fetchBillingTransactions({ limit: PAGE_SIZE, offset, direction: 'desc' }))
  }, [dispatch, offset])

  const balanceValue = balance?.balance ?? balance?.credits ?? balance?.amount ?? '—'
  const balanceCurrency = balance?.currency || ''
  const currentSubscription = subscription?.subscription && typeof subscription.subscription === 'object'
    ? subscription.subscription
    : null
  const currentPlan = currentSubscription?.plan && typeof currentSubscription.plan === 'object'
    ? currentSubscription.plan
    : null
  const planCode = currentPlan?.code || currentSubscription?.subscription || '—'
  const planLabel = currentPlan?.label || planCode
  const monthlyCredits = currentPlan?.monthly_credits
  const statusLabel = currentSubscription?.status || null
  const pendingChangeRequest = subscription?.pending_change_request && typeof subscription.pending_change_request === 'object'
    ? subscription.pending_change_request
    : subscription?.next_plan
      ? { requested_plan: typeof subscription.next_plan === 'object' ? (subscription.next_plan.code || subscription.next_plan.plan) : subscription.next_plan, status: 'scheduled' }
      : null
  const catalogPlans = useMemo(() => {
    const source = Array.isArray(plansCatalog) ? plansCatalog : plansCatalog?.plans
    return Array.isArray(source) ? source : []
  }, [plansCatalog])

  const handleProfileChange = (e) => {
    const { name, value } = e.target
    setProfileForm((prev) => ({ ...prev, [name]: value }))
  }

  const handleProfileSubmit = async () => {
    dispatch(clearUpdateBillingProfileStatus())
    const result = await dispatch(updateBillingProfile(profileForm))
    if (updateBillingProfile.fulfilled.match(result)) {
      dispatch(fetchBillingProfile())
    }
  }

  const crmUnavailableMessage = useMemo(() => {
    const candidates = [errorBalance, errorSubscription, errorPlansCatalog, errorTransactions, errorChangeSubscription, errorBillingProfile]
      .filter((msg) => typeof msg === 'string' && msg.trim().length > 0)

    const unavailable = candidates.find((msg) => {
      const normalized = msg.toLowerCase()
      return normalized.includes('temporarily unavailable') || normalized.includes('crm api unreachable') || normalized.includes('unreachable')
    })

    return unavailable || null
  }, [errorBalance, errorSubscription, errorPlansCatalog, errorTransactions, errorChangeSubscription, errorBillingProfile])

  const isDBaaSServiceUnavailable = useMemo(() => {
    const dbassCandidates = [errorSubscription, errorPlansCatalog, errorChangeSubscription]
      .filter((msg) => typeof msg === 'string' && msg.trim().length > 0)

    return dbassCandidates.some((msg) => {
      const normalized = msg.toLowerCase()
      return normalized.includes('temporarily unavailable') || normalized.includes('crm api unreachable') || normalized.includes('unreachable')
    })
  }, [errorSubscription, errorPlansCatalog, errorChangeSubscription])

  const showInlineServiceErrors = !crmUnavailableMessage

  const handleOpenChangePlan = () => {
    if (isDBaaSServiceUnavailable) {
      return
    }
    const selectedCurrentPlanCode = String(currentPlan?.code || currentSubscription?.subscription || '').toLowerCase()
    const fallbackPlan = catalogPlans.find((plan) => {
      const code = String(plan?.code || plan?.plan || plan?.id || '').toLowerCase()
      return code === selectedCurrentPlanCode
    })
    dispatch(clearChangeSubscriptionError())
    setSelectedPlanCode(fallbackPlan?.code || fallbackPlan?.plan || fallbackPlan?.id || '')
    onOpen()
  }

  const handleCloseChangePlan = () => {
    dispatch(clearChangeSubscriptionError())
    onClose()
  }

  const handleConfirmChangePlan = async () => {
    if (!selectedPlanCode) return
    const result = await dispatch(requestBillingSubscriptionChange({ subscription: selectedPlanCode }))
    if (requestBillingSubscriptionChange.fulfilled.match(result)) {
      handleCloseChangePlan()
      const url = result.payload?.data?.invoice?.payment_url
      if (url) {
        setPaymentUrl(url)
      }
      dispatch(fetchBillingSubscription())
      dispatch(fetchBillingPlansCatalog())
    }
  }

  return (
    <>
      <VStack align='stretch' spacing={5}>
      {crmUnavailableMessage && (
        <Box borderWidth='1px' borderColor='var(--warning-secondary-color)' borderRadius='md' p={3} bg='var(--warning-tertiary-color)'>
          <Text fontSize='sm' color='var(--warning-primary-color)' fontWeight='semibold'>Billing service notice</Text>
          <Text fontSize='sm' color='var(--text-color)'>{crmUnavailableMessage}</Text>
        </Box>
      )}
      {paymentUrl && (
        <Box borderWidth='1px' borderColor='var(--info-secondary-color, #63b3ed)' borderRadius='md' p={3} bg='var(--info-tertiary-color, #ebf8ff)'>
          <Text fontSize='sm' color='var(--info-primary-color, #2b6cb0)' fontWeight='semibold'>Payment required to activate plan change</Text>
          <Text fontSize='sm' color='var(--text-color)'>
            Complete payment to finalize your subscription upgrade:{' '}
            <Text as='a' href={paymentUrl} target='_blank' rel='noopener noreferrer' color='var(--primary-color)' textDecoration='underline'>
              Pay now
            </Text>
          </Text>
        </Box>
      )}
      <HStack align='stretch' spacing={4} flexWrap='wrap'>
        <Box
          borderWidth='1px'
          borderColor={cardBorderColor}
          borderRadius='lg'
          p={5}
          minW='300px'
          flex='1'
          boxShadow='sm'
          bg={balanceCardBg}
        >
          <Text fontSize='xs' color={mutedTextColor} textTransform='uppercase' letterSpacing='wider'>Personal Balance</Text>
          {loadingBalance ? (
            <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading balance…</Text></HStack>
          ) : errorBalance && showInlineServiceErrors ? (
            <Text fontSize='sm' color='red.500'>{errorBalance}</Text>
          ) : (
            <VStack align='start' spacing={1} pt={1}>
              <Text fontSize='2xl' fontWeight='bold' lineHeight='1.1'>{balanceValue} {balanceCurrency}</Text>
              <Text fontSize='xs' color={mutedTextColor}>Available personal credits</Text>
            </VStack>
          )}
        </Box>

        <Box
          borderWidth='1px'
          borderColor={cardBorderColor}
          borderRadius='lg'
          p={5}
          minW='300px'
          flex='1'
          boxShadow='sm'
          bg={subscriptionCardBg}
        >
          <Text fontSize='xs' color={mutedTextColor} textTransform='uppercase' letterSpacing='wider'>DBaaS Subscription</Text>
          {loadingSubscription ? (
            <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading subscription…</Text></HStack>
          ) : errorSubscription && showInlineServiceErrors ? (
            <Text fontSize='sm' color='red.500'>{errorSubscription}</Text>
          ) : (
            <VStack align='stretch' spacing={3} pt={2}>
              <HStack spacing={2} flexWrap='wrap'>
                <Text fontSize='xl' fontWeight='bold' lineHeight='1.1'>{planLabel}</Text>
                {statusLabel && (
                  <Badge colorScheme={String(statusLabel).toLowerCase() === 'active' ? 'green' : 'purple'} textTransform='capitalize' variant='solid'>
                    {String(statusLabel)}
                  </Badge>
                )}
              </HStack>
              {monthlyCredits !== undefined && monthlyCredits !== null && (
                <Text fontSize='sm' color='var(--text-color)'>Monthly credits: <Text as='span' fontWeight='semibold'>{String(monthlyCredits)}</Text></Text>
              )}
              {pendingChangeRequest && (
                <Box borderWidth='1px' borderColor={pendingBorderColor} borderRadius='md' p={2} w='full' bg={pendingBoxBg}>
                  <Text fontSize='xs' color='var(--warning-primary-color)' fontWeight='bold' textTransform='uppercase' letterSpacing='wide'>Pending Request</Text>
                  <Text fontSize='sm' color='var(--text-color)'>
                    {String(pendingChangeRequest.requested_subscription || pendingChangeRequest.requested_plan || 'change request')} ({String(pendingChangeRequest.status || 'pending')})
                  </Text>
                </Box>
              )}
              <Box>
                <Button
                  w='fit-content'
                  size='sm'
                  colorScheme='purple'
                  onClick={handleOpenChangePlan}
                  isDisabled={isDBaaSServiceUnavailable || loadingPlansCatalog || catalogPlans.length === 0}
                >
                  Change Plan
                </Button>
                {isDBaaSServiceUnavailable && (
                  <Text fontSize='xs' color='var(--warning-primary-color)' mt={2}>
                    DBaaS subscription service is unavailable.
                  </Text>
                )}
                {errorPlansCatalog && showInlineServiceErrors && (
                  <Text fontSize='xs' color='var(--warning-primary-color)' mt={2}>
                    Plan catalog unavailable at the moment.
                  </Text>
                )}
              </Box>
            </VStack>
          )}
        </Box>
      </HStack>

      <Divider />

      <Box borderWidth='1px' borderColor={cardBorderColor} borderRadius='lg' p={5} boxShadow='sm' bg={sectionCardBg}>
        <Text fontSize='md' fontWeight='semibold' mb={4}>Billing Profile</Text>
        {loadingBillingProfile ? (
          <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading profile…</Text></HStack>
        ) : errorBillingProfile && showInlineServiceErrors ? (
          <Text fontSize='sm' color='red.500' mb={3}>{errorBillingProfile}</Text>
        ) : null}
        {updateBillingProfileSuccess && (
          <Box borderWidth='1px' borderColor='var(--success-secondary-color, #68d391)' borderRadius='md' p={2} mb={3} bg='var(--success-tertiary-color, #f0fff4)'>
            <Text fontSize='sm' color='var(--success-primary-color, #276749)'>Billing profile updated successfully.</Text>
          </Box>
        )}
        {errorUpdateBillingProfile && (
          <Text fontSize='sm' color='red.500' mb={3}>{errorUpdateBillingProfile}</Text>
        )}
        <SimpleGrid columns={{ base: 1, md: 2 }} spacing={4}>
          <FormControl>
            <FormLabel fontSize='sm'>Name</FormLabel>
            <Input size='sm' name='name' value={profileForm.name} onChange={handleProfileChange} placeholder='Acme Corp' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>Billing Email</FormLabel>
            <Input size='sm' name='email' value={profileForm.email} onChange={handleProfileChange} placeholder='billing@acme.com' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>Phone</FormLabel>
            <Input size='sm' name='phone' value={profileForm.phone} onChange={handleProfileChange} placeholder='+33 1 23 45 67 89' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>Address</FormLabel>
            <Input size='sm' name='address' value={profileForm.address} onChange={handleProfileChange} placeholder='12 rue de la Paix' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>City</FormLabel>
            <Input size='sm' name='city' value={profileForm.city} onChange={handleProfileChange} placeholder='Paris' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>Country</FormLabel>
            <Input size='sm' name='country' value={profileForm.country} onChange={handleProfileChange} placeholder='FR' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>Postal Code</FormLabel>
            <Input size='sm' name='postal_code' value={profileForm.postal_code} onChange={handleProfileChange} placeholder='75001' />
          </FormControl>
          <FormControl>
            <FormLabel fontSize='sm'>VAT Number</FormLabel>
            <Input size='sm' name='vat_number' value={profileForm.vat_number} onChange={handleProfileChange} placeholder='FR12345678901' />
          </FormControl>
        </SimpleGrid>
        <Box mt={4}>
          <Button
            size='sm'
            colorScheme='blue'
            onClick={handleProfileSubmit}
            isLoading={loadingUpdateBillingProfile}
            isDisabled={loadingBillingProfile}
          >
            Save Profile
          </Button>
        </Box>
      </Box>

      <Divider />

      <Box borderWidth='1px' borderColor={cardBorderColor} borderRadius='lg' p={5} boxShadow='sm' bg={sectionCardBg}>
        <Flex justify='space-between' align='center' mb={3}>
          <Text fontSize='md' fontWeight='semibold'>Transactions</Text>
          <HStack>
            <Button size='sm' variant='outline' onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))} isDisabled={offset === 0 || loadingTransactions}>
              Previous
            </Button>
            <Button size='sm' colorScheme='blue' onClick={() => setOffset(offset + PAGE_SIZE)} isDisabled={loadingTransactions || rows.length < PAGE_SIZE}>
              Next
            </Button>
          </HStack>
        </Flex>

        {loadingTransactions ? (
          <HStack py={3}><Spinner size='sm' /><Text fontSize='sm'>Loading transactions…</Text></HStack>
        ) : errorTransactions && showInlineServiceErrors ? (
          <Text fontSize='sm' color='red.500'>{errorTransactions}</Text>
        ) : rows.length === 0 ? (
          <Text fontSize='sm' color='gray.500'>No transactions found.</Text>
        ) : (
          <Table size='sm' variant='simple'>
            <Thead bg={tableHeadBg}>
              <Tr>
                <Th>Date</Th>
                <Th>Description</Th>
                <Th isNumeric>Amount</Th>
                <Th>Status</Th>
              </Tr>
            </Thead>
            <Tbody>
              {rows.map((row, idx) => {
                const key = row?.id || row?.transaction_id || row?.tx_id || `${offset}-${idx}`
                return (
                  <Tr key={key}>
                    <Td>{String(row?.created_at || row?.date || row?.timestamp || '—')}</Td>
                    <Td>{String(row?.description || row?.label || row?.type || '—')}</Td>
                    <Td isNumeric>{String(row?.amount ?? row?.credits ?? '—')}</Td>
                    <Td>{String(row?.status || '—')}</Td>
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        )}
      </Box>
      </VStack>

      <Modal isOpen={isOpen} onClose={handleCloseChangePlan} isCentered size='lg'>
        <ModalOverlay backdropFilter='blur(2px)' />
        <ModalContent borderRadius='xl' className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
          <ModalHeader bg={modalHeaderBg} borderTopRadius='xl' borderBottomWidth='1px' borderColor={modalHeaderBorder}>Change DBaaS Plan</ModalHeader>
          <ModalCloseButton />
          <ModalBody>
            <VStack align='stretch' spacing={3}>
              <Text fontSize='sm' color={mutedTextColor}>Select a new subscription plan code.</Text>
              <Text fontSize='xs' color={mutedTextColor}>Current plan: <Text as='span' fontWeight='semibold'>{planLabel}</Text></Text>
              <Select value={selectedPlanCode} onChange={(e) => setSelectedPlanCode(e.target.value)} placeholder='Select plan' size='md'>
                {catalogPlans.map((plan, idx) => {
                  const code = plan?.code || plan?.plan || plan?.id || ''
                  if (!code) return null
                  const key = `${code}-${idx}`
                  const label = plan?.label || plan?.name || String(code)
                  return <option key={key} value={code}>{`${code} — ${label}`}</option>
                })}
              </Select>
              {errorChangeSubscription ? <Text fontSize='sm' color='red.500'>{errorChangeSubscription}</Text> : null}
            </VStack>
          </ModalBody>
          <ModalFooter>
            <HStack>
              <Button variant='ghost' onClick={handleCloseChangePlan} isDisabled={loadingChangeSubscription}>Cancel</Button>
              <Button colorScheme='blue' onClick={handleConfirmChangePlan} isLoading={loadingChangeSubscription} isDisabled={isDBaaSServiceUnavailable || !selectedPlanCode}>
                Submit Change Request
              </Button>
            </HStack>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}

export default Billing
