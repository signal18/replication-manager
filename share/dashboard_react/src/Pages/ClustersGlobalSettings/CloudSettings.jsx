import {
  Box, Checkbox, Flex, FormControl, FormErrorMessage, FormLabel,
  HStack, Input, InputGroup, InputRightElement, Link, Spinner, Text, VStack
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchGlobalSetting, setGlobalSetting, reloadClustersPlan, reloadClustersPlanInfo, registerInstance, confirmRegisterInstance, pollRegisterStatus } from '../../redux/globalClustersSlice'
import TextForm from '../../components/TextForm'
import RMIconButton from '../../components/RMIconButton'
import { HiOutlineInformationCircle, HiQuestionMarkCircle, HiRefresh } from 'react-icons/hi'
import { HiEye, HiEyeOff } from 'react-icons/hi'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import TagPill from '../../components/TagPill'
import RMButton from '../../components/RMButton'
import Markdown from 'react-markdown'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import remarkGfm from 'remark-gfm'

function CloudSettings({ config }) {
  const dispatch = useDispatch()
  const joinClasses = (...classes) => classes.filter(Boolean).join(' ')
  const [action, setAction] = useState({
    title: '',
    type: '',
    body: <></>
  })
  const {title,type} = action
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [shouldRedownloadPlans, setShouldRedownloadPlans] = useState(true)
  const [isRegisterModalOpen, setIsRegisterModalOpen] = useState(false)
  const [registerStep, setRegisterStep] = useState(1)
  const [showPassword, setShowPassword] = useState(false)
  const [registerForm, setRegisterForm] = useState({ email: '', password: '', domain: '', subdomain: '', zone: '' })
  const [registerErrors, setRegisterErrors] = useState({})
  const [isSendingCode, setIsSendingCode] = useState(false)
  const [isConfirming, setIsConfirming] = useState(false)
  const [regStatus, setRegStatus] = useState(null)   // {state, message} from server
  const [regElapsed, setRegElapsed] = useState(0)    // seconds since step 2 started
  const pollIntervalRef = useRef(null)
  const tickIntervalRef = useRef(null)
  const REG_TIMEOUT_SEC = 5 * 60
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

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }

  const actionHandler = useCallback(() => {
    if (type === 'cloud18-connect') {
      dispatch(setGlobalSetting({ setting: 'cloud18', value: "true", errMsgFunc: errInvalidGrant }))
    } else if (type === 'cloud18-disconnect') {
      dispatch(setGlobalSetting({ setting: 'cloud18', value: "false", errMsgFunc: errInvalidGrant }))
    } else if (type === 'reload-clusters-plan') {
      dispatch(reloadClustersPlan({ download: shouldRedownloadPlans }))
    } else if (type === 'reload-clusters-plan-info') {
      dispatch(reloadClustersPlanInfo({ download: shouldRedownloadPlans }))
    }
  }, [type, shouldRedownloadPlans])

  const disableConnect = useMemo(() => (config?.cloud18GitUser === "" || config?.cloud18Domain === "" || config?.cloud18SubDomain === "" || config?.cloud18SubDomainZone === ""),[config?.cloud18GitUser, config?.cloud18Domain, config?.cloud18SubDomain, config?.cloud18SubDomainZone])

  const openRegisterModal = () => {
    setRegisterStep(1)
    setRegisterForm({
      email: config?.cloud18GitUser || '',
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
    if (!form.email.trim()) errors.email = 'Email is required'
    else if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.email.trim())) errors.email = 'Enter a valid email address'
    if (!form.password) errors.password = 'Password is required'
    else if (form.password.length < 8) errors.password = 'Password must be at least 8 characters'
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
    const result = await dispatch(registerInstance({ email: registerForm.email.trim(), password: registerForm.password, uri }))
    setIsSendingCode(false)
    if (result?.payload?.status === 202) {
      setRegisterStep(2)
    }
  }

  // Step 2: user has confirmed email via GitLab link — complete registration
  const handleConfirmRegistration = async () => {
    setIsConfirming(true)
    const uri = `${registerForm.domain.trim()}.${registerForm.subdomain.trim()}.${registerForm.zone.trim()}`
    const result = await dispatch(confirmRegisterInstance({ email: registerForm.email.trim(), password: registerForm.password, uri }))
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
      <FormControl isInvalid={!!registerErrors.email} isRequired>
        <FormLabel fontSize='sm'>Email</FormLabel>
        <Input size='sm' type='email' placeholder='admin@mycompany.com'
          value={registerForm.email}
          onChange={(e) => setRegisterForm(f => ({ ...f, email: e.target.value }))}
        />
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

  // Helper: question-mark icon with tooltip
  const qmark = (tip) => (
    <RMIconButton icon={HiQuestionMarkCircle} tooltip={tip} variant='ghost' size='xs' />
  )

  const registrationData = [
    {
      key: 'Status',
      help: qmark('Shows whether this instance is actively connected to Signal18 Cloud18. When ONLINE, configuration is backed up to GitLab and marketplace features are available.'),
      value: (
        <TagPill colorScheme={config?.cloud18 ? 'green' : 'gray'} text={config?.cloud18 ? 'ONLINE' : 'OFFLINE'} />
      )
    },
    {
      key: 'Git User',
      help: qmark('GitLab email/username at gitlab.signal18.io. Created during registration — used for authentication, encrypted configuration backup, and marketplace identity.'),
      value: (
        <TextForm
          value={config?.cloud18GitUser}
          confirmTitle='Confirm git username to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gitlab-user', value }))}
        />
      )
    },
    {
      key: 'GitLab Password',
      help: qmark('Password for the GitLab account at gitlab.signal18.io. Stored encrypted in the replication-manager configuration.'),
      value: (
        <TextForm
          type='password'
          value={config?.cloud18GitlabPassword}
          confirmTitle='Confirm GitLab password to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gitlab-password', value: btoa(value) }))}
        />
      )
    },
    {
      key: 'Domain',
      help: qmark('Company namespace on Cloud18 (e.g. mycompany). A top-level GitLab group with this name is created at gitlab.signal18.io to host your cluster configurations.'),
      value: (
        <TextForm
          value={config?.cloud18Domain}
          confirmTitle='Confirm Cloud18 domain to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain', value }))}
        />
      )
    },
    {
      key: 'Subdomain',
      help: qmark('Datacenter or environment identifier (e.g. ovh, aws). Creates a subgroup under your domain group, allowing multiple independent deployment environments.'),
      value: (
        <TextForm
          value={config?.cloud18SubDomain}
          confirmTitle='Confirm subdomain to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-sub-domain', value }))}
        />
      )
    },
    {
      key: 'Subdomain Zone',
      help: qmark('Geographic zone or region (e.g. fr-1, us-east). Completes the three-part URI: domain.subdomain.zone. This URI uniquely identifies your instance on Cloud18.'),
      value: (
        <TextForm
          value={config?.cloud18SubDomainZone}
          confirmTitle='Confirm subdomain zone to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-sub-domain-zone', value }))}
        />
      )
    },
    {
      key: 'Register',
      help: qmark('Create a Signal18 Cloud18 account and link this instance. Triggers GitLab account creation, sends a confirmation email, and sets up the configuration backup repository.'),
      value: (
        <HStack>
          <RMButton
            isDisabled={!!config?.cloud18}
            onClick={openRegisterModal}
            title={config?.cloud18 ? 'Already connected — disconnect first to re-register' : 'Create a new Signal18 account and link this instance'}
          >
            Register
          </RMButton>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({ title: 'Cloud18 Benefits', type: '', body: <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}><Markdown remarkPlugins={[remarkGfm]}>{benefits}</Markdown></Box> }); openCommonModal() }} />
        </HStack>
      )
    },
    {
      key: 'Connect',
      help: qmark('Activate or deactivate the Cloud18 connection using the stored credentials. When connected, configuration changes are automatically pushed to the GitLab repository.'),
      value: (
        <HStack>
          {config?.cloud18
            ? <RMButton onClick={() => { setAction({ title: 'Confirm disconnect from Cloud18?', type: 'cloud18-disconnect' }); openConfirmModal() }}>Disconnect</RMButton>
            : <RMButton isDisabled={disableConnect} onClick={() => { setAction({ title: 'Confirm connect to Cloud18?', type: 'cloud18-connect' }); openConfirmModal() }}>Connect</RMButton>
          }
        </HStack>
      )
    },
  ]

  const marketplaceData = [
    {
      key: 'Platform Description',
      help: qmark('Human-readable description of this replication-manager platform shown to other Cloud18 users in the marketplace. Helps buyers and subscribers identify your offering.'),
      value: (
        <TextForm
          value={config?.cloud18PlatformDescription}
          confirmTitle='Confirm platform description to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-platform-description', value }))}
        />
      )
    },
    {
      key: 'Gateway Domain Name',
      help: qmark('Public FQDN for the Cloud18 API gateway that exposes this instance on the internet (e.g. repman.mycompany.cloud18.io). Required for clusters accessible from the marketplace.'),
      value: (
        <TextForm
          value={config?.cloud18GatewayDomainName}
          confirmTitle='Confirm gateway domain name to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gateway-domain-name', value }))}
        />
      )
    },
    {
      key: 'Gateway Service',
      help: qmark('OpenSVC service name of the Cloud18 gateway proxy. The gateway routes inbound marketplace traffic to this replication-manager instance via the OpenSVC orchestrator.'),
      value: (
        <TextForm
          value={config?.cloud18GatewayService}
          confirmTitle='Confirm gateway service to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-gateway-service', value }))}
        />
      )
    },
    {
      key: 'Domain Add Script',
      help: qmark('Shell script executed when a new marketplace subscription is activated. Typically creates DNS records and routing rules for the new tenant\'s domain.'),
      value: (
        <TextForm
          value={config?.cloud18DomainAddScript}
          confirmTitle='Confirm domain add script to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-add-script', value }))}
        />
      )
    },
    {
      key: 'Domain Drop Script',
      help: qmark('Shell script executed when a marketplace subscription is cancelled. Should remove the DNS entries and routing rules created by the Domain Add Script.'),
      value: (
        <TextForm
          value={config?.cloud18DomainDropScript}
          confirmTitle='Confirm domain drop script to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-drop-script', value }))}
        />
      )
    },
    {
      key: 'Domain User',
      help: qmark('Username for the domain management API (DNS provider, load balancer, etc.) called by the add/drop scripts to automate tenant routing.'),
      value: (
        <TextForm
          value={config?.cloud18DomainUser}
          confirmTitle='Confirm domain user to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-user', value }))}
        />
      )
    },
    {
      key: 'Domain Secret',
      help: qmark('API key or password for domain management authentication. Stored encrypted in the replication-manager configuration.'),
      value: (
        <TextForm
          type='password'
          value={config?.cloud18DomainSecret}
          confirmTitle='Confirm domain secret to '
          onSave={(value) => dispatch(setGlobalSetting({ setting: 'cloud18-domain-secret', value: btoa(value) }))}
        />
      )
    },
    {
      key: 'Reload Plans',
      help: qmark('Download and reapply marketplace service plans from the Cloud18 GitLab repository. Plans define available database topologies, resource profiles, and OpenSVC provisioning templates.'),
      value: (
        <HStack>
          <RMIconButton
            icon={HiRefresh}
            tooltip='Reload plans (reapply)'
            aria-label='Reload all clusters plans'
            onClick={() => {
              setShouldRedownloadPlans(true)
              setAction({ title: 'Confirm reload all clusters plans?', type: 'reload-clusters-plan' })
              openConfirmModal()
            }}
          />
          <RMIconButton
            icon={HiOutlineInformationCircle}
            tooltip='Reload plan info only'
            aria-label='Reload all clusters plan info'
            onClick={() => {
              setShouldRedownloadPlans(true)
              setAction({ title: 'Confirm reload all clusters plan info?', type: 'reload-clusters-plan-info' })
              openConfirmModal()
            }}
          />
        </HStack>
      )
    },
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <VStack align='stretch' spacing={0} flex={1}>
        <Text fontSize='xs' fontWeight='700' textTransform='uppercase' letterSpacing='wider'
          color='gray.500' px={2} py={1} bg='gray.100' borderBottom='1px solid' borderColor='gray.200'>
          Cloud18 Registration
        </Text>
        <TableType2 dataArray={registrationData} className={styles.table} helpColumn />
        {config?.cloud18 && (
          <>
            <Text fontSize='xs' fontWeight='700' textTransform='uppercase' letterSpacing='wider'
              color='gray.500' px={2} py={1} mt={4} bg='gray.100' borderBottom='1px solid' borderColor='gray.200'>
              Market Place
            </Text>
            <TableType2 dataArray={marketplaceData} className={styles.table} helpColumn />
          </>
        )}
      </VStack>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal()
          }}
          title={title}
          body={
            (type === 'reload-clusters-plan' || type === 'reload-clusters-plan-info') && (
              <Checkbox
                isChecked={shouldRedownloadPlans}
                onChange={(event) => setShouldRedownloadPlans(event.target.checked)}
              >
                Redownload plan repository before reload
              </Checkbox>
            )
          }
          onConfirmClick={() => {
            actionHandler()
            closeConfirmModal()
          }}
        />
      )}
      {isCommonModalOpen && (
        <CommonModal
          isOpen={isCommonModalOpen}
          size='lg'
          title={title}
          body={action.body}
          contentClassName={joinClasses(modalStyles.infoModalContent, styles.infoModalContent)}
          headerClassName={joinClasses(modalStyles.infoModalHeader, styles.infoModalHeader)}
          bodyClassName={joinClasses(modalStyles.infoModalBody, styles.infoModalBody)}
          closeModal={() => {
            closeCommonModal()
          }}
        />
      )}
      {isRegisterModalOpen && (
        <CommonModal
          isOpen={isRegisterModalOpen}
          size='md'
          title={registerStep === 1 ? 'Register with Signal18 Cloud18 (1/2)' : 'Waiting for email confirmation (2/2)'}
          body={registerFormBody}
          closeModal={() => { stopPolling(); setIsRegisterModalOpen(false) }}
        />
      )}
    </Flex>
  )
}

export default CloudSettings
