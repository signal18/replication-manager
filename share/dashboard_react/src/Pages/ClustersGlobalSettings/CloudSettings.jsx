import {
  Box, Checkbox, Flex, FormControl, FormErrorMessage, FormLabel,
  HStack, Input, InputGroup, InputRightElement, Link, Text, VStack
} from '@chakra-ui/react'
import React, { useCallback, useEffect, useMemo, useState } from 'react'
import styles from './styles.module.scss'
import RMSwitch from '../../components/RMSwitch'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { switchGlobalSetting, setGlobalSetting, reloadClustersPlan, reloadClustersPlanInfo, registerInstance } from '../../redux/globalClustersSlice'
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
  const [showPassword, setShowPassword] = useState(false)
  const [registerForm, setRegisterForm] = useState({ email: '', password: '', domain: '', subdomain: '', zone: '' })
  const [registerErrors, setRegisterErrors] = useState({})
  const [isRegistering, setIsRegistering] = useState(false)
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
    const emailGuess = config?.cloud18GitUser || ''
    setRegisterForm({
      email: emailGuess,
      password: '',
      domain: config?.cloud18Domain || '',
      subdomain: config?.cloud18SubDomain || '',
      zone: config?.cloud18SubDomainZone || ''
    })
    setRegisterErrors({})
    setShowPassword(false)
    setIsRegisterModalOpen(true)
  }

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

  const handleRegisterSubmit = async () => {
    const errors = validateRegisterForm(registerForm)
    if (Object.keys(errors).length > 0) { setRegisterErrors(errors); return }
    setIsRegistering(true)
    const uri = `${registerForm.domain.trim()}.${registerForm.subdomain.trim()}.${registerForm.zone.trim()}`
    await dispatch(registerInstance({ email: registerForm.email.trim(), password: registerForm.password, uri }))
    setIsRegistering(false)
    setIsRegisterModalOpen(false)
  }

  const registerFormBody = (
    <VStack spacing={3} align='stretch' pb={2}>
      <Text fontSize='sm' color='gray.500'>
        Creates a new GitLab account at <Link href='https://gitlab.signal18.io' target='_blank' color='blue.400'>gitlab.signal18.io</Link> and links this instance. The email is used as the GitLab username.
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
        <RMButton isLoading={isRegistering} onClick={handleRegisterSubmit}>Register</RMButton>
      </HStack>
    </VStack>
  )

  useEffect(() => {
    // Re-render when the config prop changes
  }, [config]);

  const dataObject = [
    {
      key: 'Cloud18 Status',
      value: (
        <TagPill colorScheme={config?.cloud18 ? 'green' : 'gray'} text={config?.cloud18 ? 'ONLINE' : 'OFFLINE'} />
      )
    },
    {
      key: 'Git user',
      value: (
        <TextForm
          value={config?.cloud18GitUser}
          confirmTitle={`Confirm git username to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-gitlab-user', value }))
          }}
        />
      )
    },
    {
      key: 'Gitlab Password',
      value: (
        <TextForm
          type={"password"}
          value={config?.cloud18GitlabPassword}
          confirmTitle={`Confirm gitlab password to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-gitlab-password', value: btoa(value) }))
          }}
        />
      )
    },
    {
      key: 'Domain',
      value: (
        <TextForm
          value={config?.cloud18Domain}
          confirmTitle={`Confirm cloud18 Domain to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-domain', value }))
          }}
        />
      )
    },
    {
      key: 'Subdomain', 
      value: (
        <TextForm
          value={config?.cloud18SubDomain}
          confirmTitle={`Confirm subdomain to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-sub-domain', value }))
          }}
        />
      )
    },
    {
      key: 'Subdomain zone',
      value: (
        <TextForm
          value={config?.cloud18SubDomainZone}
          confirmTitle={`Confirm subdomain zone to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-sub-domain-zone', value }))
          }}
        />
      )
    },
    {
      key: 'Platform Description',
      value: (
        <TextForm
          value={config?.cloud18PlatformDescription}
          confirmTitle={`Confirm platform description to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-platform-description', value }))
          }}
        />
      )
    },
    {
      key: 'Gateway Domain Name',
      value: (
        <TextForm
          value={config?.cloud18GatewayDomainName}
          confirmTitle={`Confirm gateway domain name to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-gateway-domain-name', value }))
          }}
        />
      )
    },
    {
      key: 'Gateway Service',
      value: (
        <TextForm
          value={config?.cloud18GatewayService}
          confirmTitle={`Confirm gateway service to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-gateway-service', value }))
          }}
        />
      )
    },
    {
      key: 'Domain Add Script',
      value: (
        <TextForm
          value={config?.cloud18DomainAddScript}
          confirmTitle={`Confirm domain add script to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-domain-add-script', value }))
          }}
        />
      )
    },
    {
      key: 'Domain Drop Script',
      value: (
        <TextForm
          value={config?.cloud18DomainDropScript}
          confirmTitle={`Confirm domain drop script to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-domain-drop-script', value }))
          }}
        />
      )
    },
    {
      key: 'Domain User',
      value: (
        <TextForm
          value={config?.cloud18DomainUser}
          confirmTitle={`Confirm domain user to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-domain-user', value }))
          }}
        />
      )
    },
    {
      key: 'Domain Secret',
      value: (
        <TextForm
          type={"password"}
          value={config?.cloud18DomainSecret}
          confirmTitle={`Confirm domain secret to `}
          onSave={(value) => {
            dispatch(setGlobalSetting({ setting: 'cloud18-domain-secret', value: btoa(value) }))
          }}
        />
      )
    },
    {
      key: 'Cloud18 Register',
      value: (
        <HStack>
          <RMButton
            isDisabled={!!config?.cloud18}
            onClick={openRegisterModal}
            title={config?.cloud18 ? 'Already connected — disconnect first to re-register' : 'Create a new Signal18 account and link this instance'}
          >
            Register
          </RMButton>
          <RMIconButton icon={HiQuestionMarkCircle} onClick={() => { setAction({title:'Cloud18 Benefits', type: '', body: <Box className={joinClasses(modalStyles.infoTooltip, styles.infoTooltip)}><Markdown remarkPlugins={[remarkGfm]}>{benefits}</Markdown></Box>}); openCommonModal()}} />
        </HStack>
      )
    },
    {
      key: 'Cloud18 Connect',
      value: (<HStack> { config?.cloud18 ? <RMButton onClick={() => { setAction({title:'Confirm disconnect from cloud18?', type: 'cloud18-disconnect'}); openConfirmModal()}}>Disconnect</RMButton> : <RMButton isDisabled={disableConnect}  onClick={() => { setAction({title:'Confirm connect to cloud18?', type: 'cloud18-connect'}); openConfirmModal()}}>Connect</RMButton>} </HStack>)
    },
    {
      key: 'Reload All Clusters Plans',
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
      <TableType2 dataArray={dataObject} className={styles.table} />
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
          title='Register with Signal18 Cloud18'
          body={registerFormBody}
          closeModal={() => setIsRegisterModalOpen(false)}
        />
      )}
    </Flex>
  )
}

export default CloudSettings
