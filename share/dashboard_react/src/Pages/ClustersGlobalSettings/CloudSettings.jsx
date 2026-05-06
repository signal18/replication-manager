import {
  Box, Flex, FormControl, FormErrorMessage, FormLabel,
  HStack, Input, InputGroup, InputRightElement, Link, Radio, RadioGroup,
  Spinner, Text, VStack
} from '@chakra-ui/react'
import React, { useEffect, useMemo, useRef, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setGlobalSetting, switchGlobalSetting, getMonitoredData, registerInstance, confirmRegisterInstance, pollRegisterStatus, fetchSubscriptionPlans, fetchSubscription, updateSubscription, unregisterInstance } from '../../redux/globalClustersSlice'
import TextForm from '../../components/TextForm'
import RMSwitch from '../../components/RMSwitch'
import Dropdown from '../../components/Dropdown'
import RMIconButton from '../../components/RMIconButton'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import { HiEye, HiEyeOff } from 'react-icons/hi'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import TagPill from '../../components/TagPill'
import RMButton from '../../components/RMButton'
import Markdown from 'react-markdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import remarkGfm from 'remark-gfm'
import SignupForm from '../../components/Auth/SignupForm'
import { authService } from '../../services/authService'

function CloudSettings({ config }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
  const [action, setAction] = useState({ title: '', body: <></> })
  const { title } = action
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [isRegisterModalOpen, setIsRegisterModalOpen] = useState(false)
  const [registerStep, setRegisterStep] = useState(1)
  const [showPassword, setShowPassword] = useState(false)
  const [registerForm, setRegisterForm] = useState({ email: '', password: '', domain: '', subdomain: '', zone: '' })
  const [registerErrors, setRegisterErrors] = useState({})
  const [signupSeed, setSignupSeed] = useState(null) // { email } from successful signup
  const [isSignupModalOpen, setIsSignupModalOpen] = useState(false)
  const signupCloseTimerRef = useRef(null)
  const [isSendingCode, setIsSendingCode] = useState(false)
  const [isConfirming, setIsConfirming] = useState(false)
  const [regStatus, setRegStatus] = useState(null)   // {state, message} from server
  const [regElapsed, setRegElapsed] = useState(0)    // seconds since step 2 started
  const pollIntervalRef = useRef(null)
  const tickIntervalRef = useRef(null)
  const REG_TIMEOUT_SEC = 5 * 60
  const [isUnregisterModalOpen, setIsUnregisterModalOpen] = useState(false)
  const [isUnregistering, setIsUnregistering] = useState(false)
  const [isConnecting, setIsConnecting] = useState(false)
  const [isDisconnecting, setIsDisconnecting] = useState(false)
  // Set to true only after a successful Unregister (CRM call that drops GitLab projects
  // and the subscription DB record). That is the only valid gate for URI editing —
  // disconnect alone is not enough because the CRM still holds the old URI record.
  // Reset automatically when a connection is re-established.
  const [uriUnlocked, setUriUnlocked] = useState(false)

  const handleUnregister = async () => {
    setIsUnregistering(true)
    const result = await dispatch(unregisterInstance())
    setIsUnregistering(false)
    setIsUnregisterModalOpen(false)
    if (result?.payload?.status === 200) {
      setUriUnlocked(true)
      await dispatch(getMonitoredData({}))
    }
  }

  const handleConnect = async () => {
    setIsConnecting(true)
    await dispatch(setGlobalSetting({ setting: 'cloud18', value: 'true', errMsgFunc: errInvalidGrant }))
    // Force an immediate monitor refresh so the UI transitions without waiting
    // for the next polling interval. The connect may have already set cloud18=true
    // on the server; we need to pull that state now.
    await dispatch(getMonitoredData({}))
    setIsConnecting(false)
  }

  const handleDisconnect = async () => {
    setIsDisconnecting(true)
    await dispatch(setGlobalSetting({ setting: 'cloud18', value: 'false', errMsgFunc: errInvalidGrant }))
    await dispatch(getMonitoredData({}))
    setIsDisconnecting(false)
  }

  // Subscription modal state
  const [isSubModalOpen, setIsSubModalOpen] = useState(false)
  const [subLoading, setSubLoading] = useState(false)
  const [subChanging, setSubChanging] = useState(false)
  const [subInfo, setSubInfo] = useState(null)   // {email, plan} from CRM
  const [subError, setSubError] = useState(null)
  const [selectedPlan, setSelectedPlan] = useState('')
  const errInvalidGrant = (err) => { if (err?.message?.includes("invalid_grant")) err.message = <>{err.message}. <Link href="https://gitlab.signal18.io/users/sign_up" target='_blank'><u>Click here to Sign Up</u></Link></>; return err }

  const benefits = `Registered Replication Manager to Cloud18 benefit many advantages  
* Get access to our community via https://meet.signal18.io  
* Backup encrypted configs in our cloud repository for possible recover on start  
* Get access to RDBA OPS and SYS OPS support plans via https://meet.signal18.io  
* Expose your API on the net and give local clusters access to other Cloud18 users via ACL  
* Get extra alerting on MariaDB blocker issues that may affect your version  
* Sale or Subscribe to database clusters on the Cloud18 market-place  

Start create an account in https://gitlab.signal18.io
  `

  const openCommonModal = () => {
    setIsCommonModalOpen(true)
  }

  const closeCommonModal = () => {
    setIsCommonModalOpen(false)
  }

  const disableConnect = useMemo(() => (config?.cloud18GitUser === "" || config?.cloud18Domain === "" || config?.cloud18SubDomain === "" || config?.cloud18SubDomainZone === ""),[config?.cloud18GitUser, config?.cloud18Domain, config?.cloud18SubDomain, config?.cloud18SubDomainZone])

  const openRegisterModal = () => {
    setRegisterStep(1)
    setRegisterForm({
      email: signupSeed?.email || config?.cloud18GitUser || '',
      password: '',
      domain: config?.cloud18Domain || '',
      subdomain: config?.cloud18SubDomain || '',
      zone: config?.cloud18SubDomainZone || ''
    })
    setRegisterErrors({})
    setShowPassword(false)
    setIsRegisterModalOpen(true)
  }

  // Stop all polling intervals
  const stopPolling = () => {
    if (pollIntervalRef.current) { clearInterval(pollIntervalRef.current); pollIntervalRef.current = null }
    if (tickIntervalRef.current) { clearInterval(tickIntervalRef.current); tickIntervalRef.current = null }
  }

  const clearSignupSeed = () => {
    setSignupSeed(null)
    setRegisterForm(f => ({ ...f, email: '', password: '' }))
    setRegisterErrors(e => ({ ...e, email: undefined, password: undefined }))
  }

  const handleSignupSuccess = (_, payload) => {
    const seeded = {
      email: payload?.email || ''
    }
    setSignupSeed(seeded)
    setRegisterForm(f => ({ ...f, email: seeded.email }))
    clearTimeout(signupCloseTimerRef.current)
    signupCloseTimerRef.current = setTimeout(() => setIsSignupModalOpen(false), 1500)
  }

  // Start polling when step 2 becomes active
  useEffect(() => {
    if (registerStep !== 2 || !isRegisterModalOpen) { stopPolling(); return }

    setRegStatus(null)
    setRegElapsed(0)

    // Elapsed-time ticker (every second for the countdown display)
    tickIntervalRef.current = setInterval(() => {
      setRegElapsed(s => s + 1)
    }, 1000)

    // Status poller (every 10 s)
    pollIntervalRef.current = setInterval(async () => {
      const result = await dispatch(pollRegisterStatus())
      const payload = result?.payload?.data
      if (!payload) return
      const { state, message } = payload
      setRegStatus({ state, message })

      if (state === 'complete') {
        stopPolling()
        setIsRegisterModalOpen(false)
      } else if (state === 'timeout' || state === 'error') {
        stopPolling()
      }
    }, 10000)

    return stopPolling
  }, [registerStep, isRegisterModalOpen])

  const validateRegisterForm = (form) => {
    const errors = {}
    const email = (signupSeed?.email || form.email || '').trim()
    const password = form.password || ''

    if (!email) errors.email = 'Email is required'
    else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) errors.email = 'Enter a valid email address'
    if (!password) errors.password = 'Password is required'
    else if (password.length < 8) errors.password = 'Password must be at least 8 characters'
    if (!form.domain.trim()) errors.domain = 'Domain is required'
    else if (!/^[a-z0-9-]+$/.test(form.domain.trim())) errors.domain = 'Only lowercase letters, digits and hyphens'
    if (!form.subdomain.trim()) errors.subdomain = 'Subdomain is required'
    else if (!/^[a-z0-9-]+$/.test(form.subdomain.trim())) errors.subdomain = 'Only lowercase letters, digits and hyphens'
    if (!form.zone.trim()) errors.zone = 'Zone is required'
    else if (!/^[a-z0-9-]+$/.test(form.zone.trim())) errors.zone = 'Only lowercase letters, digits and hyphens'
    return errors
  }

  // Step 1: create GitLab account — GitLab sends confirmation email
  const handleSendConfirmation = async () => {
    const errors = validateRegisterForm(registerForm)
    if (Object.keys(errors).length > 0) { setRegisterErrors(errors); return }
    setIsSendingCode(true)
    const uri = `${registerForm.domain.trim()}.${registerForm.subdomain.trim()}.${registerForm.zone.trim()}`
    const email = (signupSeed?.email || registerForm.email || '').trim()
    const password = registerForm.password || ''
    const result = await dispatch(registerInstance({ email, password, uri }))
    setIsSendingCode(false)
    if (result?.payload?.status === 202) {
      setRegisterForm(f => ({ ...f, email, password }))
      setRegisterStep(2)
    }
  }

  // Step 2: user has confirmed email via GitLab link — complete registration
  const handleConfirmRegistration = async () => {
    setIsConfirming(true)
    const uri = `${registerForm.domain.trim()}.${registerForm.subdomain.trim()}.${registerForm.zone.trim()}`
    const email = (signupSeed?.email || registerForm.email || '').trim()
    const password = registerForm.password || ''
    const result = await dispatch(confirmRegisterInstance({ email, password, uri }))
    setIsConfirming(false)
    if (result?.payload?.status === 201) {
      setIsRegisterModalOpen(false)
    }
  }

  const registerFormBody = registerStep === 1 ? (
    <VStack spacing={3} align='stretch' pb={2}>
      <Text fontSize='sm' color='gray.500'>
        Creates a GitLab account at <Link href='https://gitlab.signal18.io' target='_blank' color='blue.400'>gitlab.signal18.io</Link>. GitLab will send a confirmation email — you will need to click it before completing registration.
      </Text>
      <HStack justify='space-between' align='center' bg='blue.50' borderRadius='md' p={3}>
        <Text fontSize='xs' color='blue.700'>No GitLab SSO account yet?</Text>
        <RMButton size='xs' variant='outline' onClick={() => setIsSignupModalOpen(true)}>Sign up now</RMButton>
      </HStack>
      <FormControl isInvalid={!!registerErrors.email} isRequired>
        <FormLabel fontSize='sm'>Email</FormLabel>
        {signupSeed ? (
          <Input size='sm' type='email' value={signupSeed.email} isReadOnly />
        ) : (
          <Input size='sm' type='email' placeholder='admin@mycompany.com'
            value={registerForm.email}
            onChange={(e) => setRegisterForm(f => ({ ...f, email: e.target.value }))}
          />
        )}
        <FormErrorMessage>{registerErrors.email}</FormErrorMessage>
      </FormControl>
      <FormControl isInvalid={!!registerErrors.password} isRequired>
        <FormLabel fontSize='sm'>GitLab Password</FormLabel>
        <InputGroup size='sm'>
          <Input type={showPassword ? 'text' : 'password'} placeholder='min 8 characters'
            value={registerForm.password}
            onChange={(e) => setRegisterForm(f => ({ ...f, password: e.target.value }))}
          />
          <InputRightElement>
            <Box as={showPassword ? HiEyeOff : HiEye} cursor='pointer' onClick={() => setShowPassword(v => !v)} />
          </InputRightElement>
        </InputGroup>
        <FormErrorMessage>{registerErrors.password}</FormErrorMessage>
      </FormControl>
      {signupSeed && (
        <HStack justify='space-between' align='center' bg='green.50' borderRadius='md' p={3}>
          <Text fontSize='xs' color='green.700'>Using newly created GitLab SSO account.</Text>
          <RMButton size='xs' variant='ghost' onClick={clearSignupSeed}>Use different account</RMButton>
        </HStack>
      )}
      <FormControl isInvalid={!!registerErrors.domain} isRequired>
        <FormLabel fontSize='sm'>Domain <Text as='span' fontWeight='normal' color='gray.400'>(company namespace)</Text></FormLabel>
        <Input size='sm' placeholder='mycompany'
          value={registerForm.domain}
          onChange={(e) => setRegisterForm(f => ({ ...f, domain: e.target.value.toLowerCase() }))}
        />
        <FormErrorMessage>{registerErrors.domain}</FormErrorMessage>
      </FormControl>
      <FormControl isInvalid={!!registerErrors.subdomain} isRequired>
        <FormLabel fontSize='sm'>Subdomain <Text as='span' fontWeight='normal' color='gray.400'>(datacenter / environment)</Text></FormLabel>
        <Input size='sm' placeholder='ovh'
          value={registerForm.subdomain}
          onChange={(e) => setRegisterForm(f => ({ ...f, subdomain: e.target.value.toLowerCase() }))}
        />
        <FormErrorMessage>{registerErrors.subdomain}</FormErrorMessage>
      </FormControl>
      <FormControl isInvalid={!!registerErrors.zone} isRequired>
        <FormLabel fontSize='sm'>Zone</FormLabel>
        <Input size='sm' placeholder='fr-1'
          value={registerForm.zone}
          onChange={(e) => setRegisterForm(f => ({ ...f, zone: e.target.value.toLowerCase() }))}
        />
        <FormErrorMessage>{registerErrors.zone}</FormErrorMessage>
      </FormControl>
      <Text fontSize='xs' color='gray.400'>URI: {registerForm.domain || 'domain'}.{registerForm.subdomain || 'subdomain'}.{registerForm.zone || 'zone'}</Text>
      <HStack justify='flex-end' pt={1}>
        <RMButton variant='ghost' onClick={() => setIsRegisterModalOpen(false)}>Cancel</RMButton>
        <RMButton isLoading={isSendingCode} onClick={handleSendConfirmation}>Send Confirmation Email</RMButton>
      </HStack>
    </VStack>
  ) : (() => {
    const remaining = Math.max(0, REG_TIMEOUT_SEC - regElapsed)
    const mins = Math.floor(remaining / 60)
    const secs = remaining % 60
    const timeStr = `${mins}:${secs.toString().padStart(2, '0')}`
    const isTerminal = regStatus?.state === 'timeout' || regStatus?.state === 'error'
    const isComplete = regStatus?.state === 'complete'
    return (
      <VStack spacing={4} align='stretch' pb={2}>
        <Text fontSize='sm'>
          A confirmation email has been sent to <strong>{registerForm.email}</strong> by GitLab.
          Click the link in the email to confirm your account.
        </Text>

        {/* Polling progress area */}
        {!isTerminal && !isComplete && (
          <HStack spacing={3} bg='blue.50' borderRadius='md' p={3}>
            <Spinner size='sm' color='blue.500' flexShrink={0} />
            <VStack align='start' spacing={0}>
              <Text fontSize='sm' fontWeight='medium'>Waiting for email confirmation…</Text>
              <Text fontSize='xs' color='gray.500'>
                {regStatus?.message || 'Checking every 10 seconds'} &nbsp;·&nbsp; {timeStr} remaining
              </Text>
            </VStack>
          </HStack>
        )}

        {isTerminal && (
          <Box bg='red.50' borderRadius='md' p={3}>
            <Text fontSize='sm' fontWeight='medium' color='red.700'>
              {regStatus.state === 'timeout' ? 'Timed out' : 'Error'}
            </Text>
            <Text fontSize='xs' color='red.600'>{regStatus.message}</Text>
            <Text fontSize='xs' color='gray.500' mt={1}>
              You can retry manually using the button below.
            </Text>
          </Box>
        )}

        <HStack justify='space-between' pt={1}>
          <RMButton variant='ghost' onClick={() => { stopPolling(); setRegisterStep(1) }}>Back</RMButton>
          <HStack>
            <RMButton variant='ghost' onClick={() => { stopPolling(); setIsRegisterModalOpen(false) }}>Cancel</RMButton>
            <RMButton isLoading={isConfirming} onClick={handleConfirmRegistration} title='Manually trigger confirmation check'>
              {isTerminal ? 'Retry Confirm' : 'Complete Registration'}
            </RMButton>
          </HStack>
        </HStack>
      </VStack>
    )
  })()

  const signupSeedBadgeColor = registerStep === 1 ? 'blue' : 'orange'

  const registerModalTitle = (
    <HStack spacing={2} align='center'>
      <Text>
        {registerStep === 1 ? 'Register with Signal18 Cloud18 (1/2)' : 'Waiting for email confirmation (2/2)'}
      </Text>
      {signupSeed && <TagPill colorScheme={signupSeedBadgeColor} text='Using newly created account' />}
    </HStack>
  )

  // Plans fetched from CRM; fallback list shown only if CRM is unreachable
  const PLANS_FALLBACK = [
    { value: 'free',             label: 'Free',                                    desc: 'Community access, config backup to GitLab, basic alerting' },
    { value: 'support',          label: 'Contributing under Support',              desc: 'Support ticket access, SLA, and community priority queue' },
    { value: 'support-services', label: 'Contributing under Support and Services', desc: 'Full support + managed DBA/SysOps services access' },
    { value: 'partner',          label: 'Market Place Partner',                    desc: 'Marketplace listing, revenue sharing, and partner API access' },
  ]
  const [plans, setPlans] = useState(PLANS_FALLBACK)

  const openSubModal = async () => {
    setIsSubModalOpen(true)
    setSubInfo(null)
    setSubError(null)
    setSubLoading(true)
    // Fetch plan catalog and current subscription in parallel
    const [plansResult, subResult] = await Promise.all([
      dispatch(fetchSubscriptionPlans()),
      dispatch(fetchSubscription()),
    ])
    setSubLoading(false)
    const plansData = plansResult?.payload?.data
    if (Array.isArray(plansData) && plansData.length > 0) setPlans(plansData)
    const subData = subResult?.payload?.data
    if (subData?.email) {
      setSubInfo(subData)
      setSelectedPlan(subData.plan || 'free')
    } else {
      setSubError(subData?.error || 'Could not fetch subscription from CRM')
    }
  }

  const handleChangePlan = async () => {
    setSubChanging(true)
    const result = await dispatch(updateSubscription({ plan: selectedPlan }))
    setSubChanging(false)
    const status = result?.payload?.status
    if (status === 200 || status === 201) {
      setIsSubModalOpen(false)
    }
  }

  // Same helper pattern as cluster settings — opens an info modal with markdown
  const h = (content, title) => (
    <RMIconButton
      icon={HiQuestionMarkCircle}
      onClick={() => { setAction({ title,  body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> }); openCommonModal() }}
      iconFontsize='1rem'
      variant='ghost'
      style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
    />
  )

  const hStatus = `**Cloud18 Status**\n\nShows whether this instance is actively connected to Signal18 Cloud18.\nWhen ONLINE, configuration is automatically backed up to GitLab on every change, and marketplace features are available.\n\nConfig: \`cloud18\``
  const hGitUser = `**Git User**\n\nGitLab email or username at \`gitlab.signal18.io\`.\nCreated during registration — used for authentication, encrypted configuration backup, and marketplace identity.\n\n**Locked while connected.** To change the email, unregister first (which drops the GitLab projects for this URI), then register again with the new email.\n\nConfig: \`cloud18-gitlab-user\``
  const hGitPass = `**GitLab Password**\n\nPassword for the GitLab account at \`gitlab.signal18.io\`.\nStored encrypted in the replication-manager configuration.\n\nConfig: \`cloud18-gitlab-password\``
  const hDomain = `**Domain**\n\nCompany namespace on Cloud18 (e.g. \`mycompany\`).\nA top-level GitLab group with this name is created at \`gitlab.signal18.io\` to host your cluster configurations.\n\nConfig: \`cloud18-domain\``
  const hSubdomain = `**Subdomain**\n\nDatacenter or environment identifier (e.g. \`ovh\`, \`aws\`).\nCreates a subgroup under your domain group, allowing multiple independent deployment environments.\n\nConfig: \`cloud18-sub-domain\``
  const hZone = `**Subdomain Zone**\n\nGeographic zone or region (e.g. \`fr-1\`, \`us-east\`).\nCompletes the three-part URI: \`domain.subdomain.zone\`. This URI uniquely identifies your instance on Cloud18.\n\nConfig: \`cloud18-sub-domain-zone\``
  const hConnect = `**Connect / Disconnect / Register**\n\nWhen credentials are stored: **Connect** activates Cloud18 using the stored GitLab credentials, or **Disconnect** deactivates it (credentials are preserved). When connected, **Unregister** drops the GitLab projects for this URI and unlocks fields so you can change the URI.\n\nWhen no credentials are stored: **Register** creates a GitLab account and links this instance.\n\nConfig: \`cloud18\``
  const hSubscription = `**Subscription Plan**\n\nYour Signal18 Cloud18 subscription level. Clicking **Change** verifies your registration with the CRM using your GitLab token and lets you switch plan.\n\n| Plan | Description |\n|------|-------------|\n| Free | Community access, config backup, basic alerting |\n| Contributing under Support | Support tickets, SLA |\n| Contributing under Support and Services | Full support + managed services |\n| Market Place Partner | Marketplace listing, revenue sharing, partner API |\n\nConfig: \`cloud18-subscription-plan\``

  const isConnected = !!config?.cloud18

  // Reset URI unlock when connection is re-established (new registration completed)
  useEffect(() => {
    if (isConnected) {
      setUriUnlocked(false)
      setSignupSeed(null)
    }
  }, [isConnected])

  useEffect(() => () => clearTimeout(signupCloseTimerRef.current), [])

  // True when a full set of credentials + URI is stored, meaning a direct Connect is
  // possible. After Unregister (uriUnlocked=true) we force this false so the UI shows
  // Register instead of Connect — the CRM subscription row was dropped and must be recreated.
  const hasCredentials = !uriUnlocked && !!(config?.cloud18GitUser && config?.cloud18Domain && config?.cloud18SubDomain && config?.cloud18SubDomainZone)

  // URI fields are locked unless the user has explicitly unregistered (CRM call that drops
  // both the GitLab projects and the subscription DB record for this URI).
  const uriLocked = isConnected || (!uriUnlocked && !!(config?.cloud18Domain && config?.cloud18SubDomain && config?.cloud18SubDomainZone))

  const registrationData = [
    { key: 'Status',         help: h(hStatus,    'Cloud18 Status'),    value: <TagPill colorScheme={isConnected ? 'green' : 'gray'} text={isConnected ? 'ONLINE' : 'OFFLINE'} /> },
    { key: 'Git User',       help: h(hGitUser,   'Git User'),          value: isConnected
        ? <Text fontSize='sm'>{config?.cloud18GitUser}</Text>
        : <TextForm value={config?.cloud18GitUser}        confirmTitle='Confirm git username to '   onSave={(v) => dispatch(setGlobalSetting({ setting: 'cloud18-gitlab-user', value: v }))} /> },
    { key: 'GitLab Password',help: h(hGitPass,   'GitLab Password'),   value: isConnected
        ? <Text fontSize='sm'>••••••••</Text>
        : <TextForm value={config?.cloud18GitlabPassword} type='password' confirmTitle='Confirm GitLab password to ' onSave={(v) => dispatch(setGlobalSetting({ setting: 'cloud18-gitlab-password', value: btoa(v) }))} /> },
    { key: 'Domain',         help: h(hDomain,    'Domain'),            value: uriLocked
        ? <Text fontSize='sm'>{config?.cloud18Domain}</Text>
        : <TextForm value={config?.cloud18Domain}         confirmTitle='Confirm domain to '         onSave={(v) => dispatch(setGlobalSetting({ setting: 'cloud18-domain', value: v }))} /> },
    { key: 'Subdomain',      help: h(hSubdomain, 'Subdomain'),         value: uriLocked
        ? <Text fontSize='sm'>{config?.cloud18SubDomain}</Text>
        : <TextForm value={config?.cloud18SubDomain}      confirmTitle='Confirm subdomain to '      onSave={(v) => dispatch(setGlobalSetting({ setting: 'cloud18-sub-domain', value: v }))} /> },
    { key: 'Subdomain Zone', help: h(hZone,      'Subdomain Zone'),    value: uriLocked
        ? <Text fontSize='sm'>{config?.cloud18SubDomainZone}</Text>
        : <TextForm value={config?.cloud18SubDomainZone}  confirmTitle='Confirm subdomain zone to ' onSave={(v) => dispatch(setGlobalSetting({ setting: 'cloud18-sub-domain-zone', value: v }))} /> },
    {
      key: 'Connect',
      help: h(hConnect, 'Connect / Disconnect'),
      value: (
        <HStack>
          {isConnected ? (
            <>
              <RMButton
                isLoading={isDisconnecting}
                onClick={handleDisconnect}
                title='Deactivate Cloud18 connection (credentials are preserved)'
              >
                Disconnect
              </RMButton>
              <RMButton
                colorScheme='red'
                variant='outline'
                isDisabled={isDisconnecting}
                onClick={() => setIsUnregisterModalOpen(true)}
                title='Drop GitLab projects for this URI and unlock fields to change URI'
              >
                Unregister
              </RMButton>
            </>
          ) : hasCredentials ? (
            <RMButton
              isLoading={isConnecting}
              isDisabled={disableConnect || isConnecting}
              onClick={handleConnect}
              title='Connect using stored credentials'
            >
              Connect
            </RMButton>
          ) : (
            <RMButton
              onClick={openRegisterModal}
              title='Create a new Signal18 account and link this instance'
            >
              Register
            </RMButton>
          )}
          <RMIconButton
            icon={HiQuestionMarkCircle}
            onClick={() => { setAction({ title: 'Cloud18 Benefits',  body: <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}><Markdown remarkPlugins={[remarkGfm]}>{benefits}</Markdown></Box> }); openCommonModal() }}
            iconFontsize='1rem'
            variant='ghost'
            style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }}
          />
        </HStack>
      )
    },
    ...(config?.cloud18 ? [{
      key: 'Subscription',
      help: h(hSubscription, 'Subscription Plan'),
      value: (
        <HStack>
          <Text fontSize='sm'>
            {plans.find(p => p.value === (config?.cloud18SubscriptionPlan || 'free'))?.label || 'Free'}
          </Text>
          <RMButton size='xs' variant='outline' onClick={openSubModal}>Change</RMButton>
        </HStack>
      )
    }] : []),
    ...(isConnected ? [{
      key: 'Peer Health Mode',
      help: h(`**Peer Health Mode**\n\nHow peer cluster health status is determined:\n\n- **peering**: Direct HTTP polling of all peer instances — gives real-time reachability but scales O(N²)\n- **smart** (default): Always polls your own fleet (same registered GitLab user). Shared/marketplace peers are only polled when a user viewing them is connected\n- **pulling**: Health comes from peer.json via the back office — lowest overhead, scales O(N), but with a few minutes delay\n\nConfig: \`cloud18-peer-health-mode\``, 'Peer Health Mode'),
      value: (
        <Dropdown
          options={[
            { value: 'peering', label: 'Peering (poll all)' },
            { value: 'smart', label: 'Smart (own fleet + active users)' },
            { value: 'pulling', label: 'Pulling (via back office)' }
          ]}
          selectedValue={config?.cloud18PeerHealthMode || 'smart'}
          confirmTitle="Confirm peer health mode: "
          onChange={(opt) => dispatch(setGlobalSetting({ setting: 'cloud18-peer-health-mode', value: opt.value }))}
        />
      )
    },
    {
      key: 'Disable Peers',
      help: h(`**Disable Peers**\n\nWhen enabled, peer clusters are hidden from the dashboard and all peer health polling is skipped.\n\nConfig: \`cloud18-disable-peers\``, 'Disable Peers'),
      value: (
        <RMSwitch
          confirmTitle='Confirm switch settings for cloud18-disable-peers?'
          onChange={() => dispatch(switchGlobalSetting({ setting: 'cloud18-disable-peers' }))}
          isChecked={config?.cloud18DisablePeers}
        />
      )
    },
    ...((config?.cloud18SubscriptionPlan && config?.cloud18SubscriptionPlan !== 'free') ? [{
      key: 'Disable Marketplace',
      help: h(`**Disable Marketplace**\n\nWhen enabled, clusters for sale are hidden from the marketplace view.\n\nThis option is only available on paid plans. Free plan users always see the marketplace.\n\nConfig: \`cloud18-disable-for-sale\``, 'Disable Marketplace'),
      value: (
        <RMSwitch
          confirmTitle='Confirm switch settings for cloud18-disable-for-sale?'
          onChange={() => dispatch(switchGlobalSetting({ setting: 'cloud18-disable-for-sale' }))}
          isChecked={config?.cloud18DisableForSale}
        />
      )
    }] : [])] : []),
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={registrationData} className={styles.tableWithHelp} helpColumn />
      </Flex>
      <CommonModal
        isOpen={isCommonModalOpen}
        size='xl'
        title={title}
        body={action.body}
        closeModal={closeCommonModal}
      />
      {isUnregisterModalOpen && (
        <ConfirmModal
          isOpen={isUnregisterModalOpen}
          closeModal={() => setIsUnregisterModalOpen(false)}
          title='Unregister from Cloud18?'
          confirmButtonText='Unregister'
          confirmButtonProps={{ colorScheme: 'red', isLoading: isUnregistering }}
          body={
            <VStack align='start' spacing={2}>
              <Text fontSize='sm'>This will <strong>permanently delete</strong> the GitLab projects for:</Text>
              <Text fontSize='sm' fontFamily='mono' bg='gray.100' px={2} py={1} borderRadius='md'>
                {config?.cloud18Domain}.{config?.cloud18SubDomain}.{config?.cloud18SubDomainZone}
              </Text>
              <Text fontSize='sm' color='gray.500'>Once deleted, URI fields will be unlocked so you can register a new URI.</Text>
            </VStack>
          }
          onConfirmClick={handleUnregister}
        />
      )}
      {isRegisterModalOpen && (
        <CommonModal
          isOpen={isRegisterModalOpen}
          size='md'
          title={registerModalTitle}
          body={registerFormBody}
          closeModal={() => { stopPolling(); setIsRegisterModalOpen(false) }}
        />
      )}
      {isSignupModalOpen && (
        <CommonModal
          isOpen={isSignupModalOpen}
          size='md'
          title='Create GitLab SSO account'
          closeModal={() => {
            clearTimeout(signupCloseTimerRef.current)
            setIsSignupModalOpen(false)
          }}
          body={
            <SignupForm
              onSubmit={authService.signup}
              onSuccess={handleSignupSuccess}
              onCancel={() => {
                clearTimeout(signupCloseTimerRef.current)
                setIsSignupModalOpen(false)
              }}
              submitLabel='Create GitLab SSO account'
              loadingText='Creating account'
              successMessage='Account created. You can continue registration now.'
            />
          }
        />
      )}
      {isSubModalOpen && (
        <CommonModal
          isOpen={isSubModalOpen}
          size='md'
          title='Change Subscription Plan'
          closeModal={() => setIsSubModalOpen(false)}
          body={
            <VStack spacing={4} align='stretch' pb={2}>
              {subLoading && (
                <HStack justify='center' py={4}>
                  <Spinner size='sm' />
                  <Text fontSize='sm' color='gray.500'>Verifying registration with CRM…</Text>
                </HStack>
              )}
              {subError && (
                <Box bg='red.50' borderRadius='md' p={3}>
                  <Text fontSize='sm' color='red.700'>{subError}</Text>
                </Box>
              )}
              {!subLoading && subInfo && (
                <>
                  <Box bg='blue.50' borderRadius='md' p={3}>
                    <Text fontSize='xs' color='gray.500'>Registered account</Text>
                    <Text fontSize='sm' fontWeight='medium'>{subInfo.email}</Text>
                  </Box>
                  <Text fontSize='sm' color='gray.600'>Select your new subscription plan:</Text>
                  <RadioGroup value={selectedPlan} onChange={setSelectedPlan}>
                    <VStack align='stretch' spacing={3}>
                      {plans.map(plan => (
                        <Box key={plan.value} borderWidth={1} borderRadius='md' p={3}
                          borderColor={selectedPlan === plan.value ? 'blue.400' : 'gray.200'}
                          bg={selectedPlan === plan.value ? 'blue.50' : 'transparent'}
                          cursor='pointer' onClick={() => setSelectedPlan(plan.value)}
                        >
                          <Radio value={plan.value}>
                            <Text fontWeight='medium' fontSize='sm'>{plan.label}</Text>
                          </Radio>
                          <Text fontSize='xs' color='gray.500' pl={6} mt={1}>{plan.desc}</Text>
                        </Box>
                      ))}
                    </VStack>
                  </RadioGroup>
                  <HStack justify='flex-end' pt={2}>
                    <RMButton variant='ghost' onClick={() => setIsSubModalOpen(false)}>Cancel</RMButton>
                    <RMButton
                      isLoading={subChanging}
                      isDisabled={selectedPlan === subInfo.plan}
                      onClick={handleChangePlan}
                    >
                      Confirm Change
                    </RMButton>
                  </HStack>
                </>
              )}
            </VStack>
          }
        />
      )}
    </>
  )
}

export default CloudSettings
