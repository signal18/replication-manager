import {
  Box,
  Checkbox,
  Flex,
  FormControl,
  FormErrorMessage,
  FormHelperText,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack,
  Textarea
} from '@chakra-ui/react'
import { useEffect, useReducer, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { addServer, connectDockerRegistry } from '../../redux/clusterSlice'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import PasswordControl from '../PasswordControl'
import RMIconButton from '../RMIconButton'
import { HiRefresh } from 'react-icons/hi'
import { refreshAppTemplateRepo } from '../../redux/globalClustersSlice'
import PropTypes from 'prop-types'
import { getApi } from '../../services/apiHelper'

const parsePortValue = (value) => {
  if (value === undefined || value === null || value === '') return null
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 && parsed <= 65535 ? parsed : null
}

const parseFirstHostPort = (hosts) => {
  if (!hosts || typeof hosts !== 'string') return null
  const firstHost = hosts.split(',').map((item) => item.trim()).find(Boolean)
  if (!firstHost) return null

  const match = firstHost.match(/:(\d+)$/)
  if (!match) return null

  return parsePortValue(match[1])
}

const initialState = {
  formData: {
    host: '',
    port: '',
    monitorType: '',
    dockerImage: '',
    tag: '',
    dockerRegistry: {
      private: false,
      authType: 'password',
      url: '',
      username: '',
      password: '',
      template: ''
    },
  },
  serviceRepos: [],
  templateOptions: [],
  tagOptions: [],
  errors: {
    monitorType: '',
    host: '',
    port: '',
    dockerImage: '',
    registryUrl: '',
    dockerUser: '',
    dockerPassword: '',
  }
}

const formReducer = (state, action) => {
  switch (action.type) {
    case 'SET_FORM_DATA':
      return { ...state, formData: { ...state.formData, ...action.payload } }
    case 'SET_SERVICE_REPOS':
      return { ...state, serviceRepos: action.payload }
    case 'SET_TEMPLATE_OPTIONS':
      return { ...state, templateOptions: action.payload }
    case 'SET_TAG_OPTIONS':
      return { ...state, tagOptions: action.payload }
    case 'FILL_VERSION_DROPDOWN': {
      const selectedType = action.payload?.type || ''
      const defaultPort = action.payload?.defaultPort
      const repolist = selectedType === 'shardproxy' ? 'mariadb' : selectedType
      const repo = state.serviceRepos.find((r) => r.name === repolist)
      const template = selectedType === 'app' ? state.formData.dockerRegistry.template : ''

      return {
        ...state,
        formData: {
          ...state.formData,
          monitorType: selectedType,
          dockerImage: selectedType === 'app' ? state.formData.dockerImage : (repo?.image || ''),
          tag: '',
          port: defaultPort ?? state.formData.port,
          dockerRegistry: {
            ...state.formData.dockerRegistry,
            template
          }
        },
        tagOptions: repo?.options || []
      }
    }
    case 'SET_ERRORS':
      return { ...state, errors: { ...state.errors, ...action.payload } }
    case 'SET_PRIVATE_REGISTRY_USAGE':
      return {
        ...state,
        formData: {
          ...state.formData,
          dockerRegistry: {
            ...state.formData.dockerRegistry,
            private: action.payload,
            url: action.payload ? state.formData.dockerRegistry.url : '',
            username: action.payload ? state.formData.dockerRegistry.username : '',
            password: action.payload ? state.formData.dockerRegistry.password : ''
          }
        }
      }
    case 'SET_REGISTRY_CREDENTIALS':
      return {
        ...state,
        formData: {
          ...state.formData,
          dockerRegistry: {
            ...state.formData.dockerRegistry,
            ...action.payload
          }
        }
      }
    case 'SET_DOCKER_TEMPLATE':
      return {
        ...state,
        formData: {
          ...state.formData,
          dockerRegistry: {
            ...state.formData.dockerRegistry,
            template: action.payload || ''
          }
        }
      }
    case 'RESET_FORM':
      return {
        ...initialState,
        serviceRepos: state.serviceRepos,
        templateOptions: state.templateOptions,
        tagOptions: state.tagOptions,
      }
    case 'RESET':
      return initialState
    default:
      return state
  }
}

const serviceTypes = [
  { name: 'MariaDB', value: 'mariadb' },
  { name: 'MySQL', value: 'mysql' },
  { name: 'Percona', value: 'percona' },
  { name: 'ProxySQL', value: 'proxysql' },
  { name: 'HaProxy', value: 'haproxy' },
  { name: 'ShardProxy', value: 'shardproxy' },
  { name: 'MaxScale', value: 'maxscale' },
  { name: 'SphinxProxy', value: 'sphinx' },
  { name: 'VIP', value: 'extvip' },
  { name: 'Application', value: 'app' },
]

const authTypes = [
  { name: 'Password', value: 'password' },
  { name: 'Token', value: 'token' },
]

function NewServerModal({ clusterName, isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const { globalClusters: { monitor, clusters } } = useSelector((state) => state)
  const baseURL = useSelector((state) => state?.auth?.baseURL || '')
  const [formState, formDispatch] = useReducer(formReducer, initialState)
  const { formData, tagOptions, templateOptions, errors } = formState
  const { host, port, monitorType, dockerImage, tag, dockerRegistry } = formData
  const { private: isPrivateRegistry, url, username, password, authType, template } = dockerRegistry
  const isAppMonitor = monitorType === 'app'
  const isPortReadOnly = Boolean(monitorType) && !isAppMonitor
  const [hostTouched, setHostTouched] = useState(false)
  const [portTouched, setPortTouched] = useState(false)
  const [templatePreview, setTemplatePreview] = useState({
    loading: false,
    error: '',
    content: '',
    defaults: null,
    dynamicHost: false,
  })
  const selectedCluster = clusters?.find((cluster) => cluster?.name === clusterName)
  const clusterConfig = selectedCluster?.config || {}

  const detectDynamicValue = (value) => {
    const val = (value || '').trim()
    return val.includes('{{') || val.includes('}}')
  }

  const fetchTemplatePreview = async (templateName) => {
    if (!templateName) {
      setTemplatePreview({ loading: false, error: '', content: '', defaults: null, dynamicHost: false })
      return
    }

    setTemplatePreview((prev) => ({ ...prev, loading: true, error: '' }))

    try {
      const encodedTemplateName = encodeURIComponent(templateName)
      const { data, status } = await getApi(baseURL || '').get(`clusters/${clusterName}/actions/app-template/${encodedTemplateName}`)
      if (status !== 200) {
        throw new Error(typeof data === 'string' ? data : 'Failed to load template preview')
      }

      const defaults = data?.defaults || {}
      const templateHost = (defaults.host || '').trim()
      const isDynamicHost = detectDynamicValue(templateHost)

      const updates = {}
      if (!hostTouched && !host?.trim() && templateHost && !isDynamicHost) {
        updates.host = templateHost
      }

      const defaultPort = parsePortValue(defaults.port)
      if (!portTouched && (port === '' || port === null || port === undefined) && defaultPort) {
        updates.port = defaultPort
      }

      const templateDockerImage = (defaults.dockerImage || '').trim()
      if (!dockerImage?.trim() && templateDockerImage) {
        updates.dockerImage = templateDockerImage
      }

      if (Object.keys(updates).length > 0) {
        formDispatch({ type: 'SET_FORM_DATA', payload: updates })
      }

      setTemplatePreview({
        loading: false,
        error: '',
        content: data?.content || '',
        defaults,
        dynamicHost: isDynamicHost,
      })
    } catch (error) {
      setTemplatePreview({ loading: false, error: error?.message || 'Unable to load template preview', content: '', defaults: null, dynamicHost: false })
    }
  }

  const getDefaultPortForType = (type) => {
    switch (type) {
      case 'mariadb':
      case 'mysql':
      case 'percona':
        return parseFirstHostPort(clusterConfig?.dbServersHosts) ?? 3306
      case 'proxysql':
        return parsePortValue(clusterConfig?.proxysqlPort) ?? 3306
      case 'maxscale':
        return parsePortValue(clusterConfig?.maxscalePort) ?? 6603
      case 'haproxy':
        return parsePortValue(clusterConfig?.haproxyWritePort) ?? parsePortValue(clusterConfig?.haproxyReadPort) ?? 3306
      case 'shardproxy':
        return parseFirstHostPort(clusterConfig?.shardproxyServers) ?? 3306
      case 'sphinx':
        return parsePortValue(clusterConfig?.sphinxSqlPort) ?? parsePortValue(clusterConfig?.sphinxPort) ?? 9306
      case 'extvip':
        return parsePortValue(clusterConfig?.provProxyRoutePort) ?? 3306
      default:
        return null
    }
  }

  useEffect(() => {
    if (monitor?.serviceRepos?.length > 0) {
      const repos = monitor?.serviceRepos.map(entry => ({
        name: entry.name,
        image: entry.image,
        options: entry.tags?.results?.map(tagItem => ({
          name: tagItem.name,
          value: `${entry.image}:${tagItem.name}`
        }))
      }))

      formDispatch({ type: 'SET_SERVICE_REPOS', payload: repos })
    }
  }, [monitor?.serviceRepos])

  useEffect(() => {
    if (monitor?.serviceTemplates?.length > 0) {
      const templates = monitor?.serviceTemplates.map(item => ({
        name: item,
        value: item
      }))

      formDispatch({ type: 'SET_TEMPLATE_OPTIONS', payload: [{ name: 'No Template', value: '' }, ...templates] })
    }
  }, [monitor?.serviceTemplates])

  const handleCreateNewServer = () => {
    const monitorTypeError = monitorType?.length > 0 ? '' : 'Monitor type is required'
    const hostError = host?.trim().length > 0 ? '' : 'Host is required'
    const parsedPort = Number(port)
    const hasValidPort = Number.isInteger(parsedPort) && parsedPort >= 1 && parsedPort <= 65535
    const portError = hasValidPort
      ? ''
      : (isPortReadOnly
        ? 'Port could not be resolved from cluster config for this monitor type'
        : 'Port is required (1-65535)')

    const nextErrors = {
      monitorType: monitorTypeError,
      host: hostError,
      port: portError,
      dockerImage: '',
      registryUrl: '',
      dockerUser: '',
      dockerPassword: ''
    }

    if (isAppMonitor) {
      if (!template && (!dockerImage || dockerImage.trim().length === 0)) {
        nextErrors.dockerImage = 'Docker image is required'
      }

      if (isPrivateRegistry && (!url || url.trim().length === 0)) {
        nextErrors.registryUrl = 'Registry URL is required'
      }

      if (isPrivateRegistry && (!username || username.trim().length === 0)) {
        nextErrors.dockerUser = 'Username is required'
      }

      if (isPrivateRegistry && (!password || password.length === 0)) {
        nextErrors.dockerPassword = authType === 'token' ? 'Token is required' : 'Password is required'
      }
    }

    formDispatch({ type: 'SET_ERRORS', payload: nextErrors })

    if (Object.values(nextErrors).some((err) => Boolean(err))) {
      return
    }

    let finalTag = ''
    if (isAppMonitor && dockerImage && dockerImage.trim().length > 0) {
      finalTag = dockerImage.trim()
    } else if (tag && tag.length > 0) {
      finalTag = tag
    }

    dispatch(addServer({
      clusterName,
      host: host.trim(),
      port: parsedPort,
      monitorType,
      tag: finalTag,
      dockerRegistry
    }))

    closeModal()
  }

  const handleDockerAuth = () => {
    const urlError = url?.length === 0 && isPrivateRegistry ? 'Registry URL is required' : ''
    const userError = username?.length === 0 && isPrivateRegistry ? 'User is required' : ''
    const passError = password?.length === 0 && isPrivateRegistry ? (authType === 'token' ? 'Token is required' : 'Password is required') : ''

    formDispatch({ type: 'SET_ERRORS', payload: { registryUrl: urlError, dockerUser: userError, dockerPassword: passError } })
    if (urlError || userError || passError) {
      return
    }

    dispatch(connectDockerRegistry({ clusterName, dockerRegistry })).then(() => {
      // no-op
    }, (error) => {
      formDispatch({ type: 'SET_ERRORS', payload: { dockerPassword: error.message } })
    })
  }

  const handleRefreshAppTemplates = () => {
    dispatch(refreshAppTemplateRepo({ clusterName }))
  }

  useEffect(() => {
    if (!isOpen) {
      formDispatch({ type: 'RESET_FORM' })
      setHostTouched(false)
      setPortTouched(false)
      setTemplatePreview({ loading: false, error: '', content: '', defaults: null, dynamicHost: false })
    }
  }, [isOpen])

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'New server'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <FormControl isInvalid={Boolean(errors.monitorType)}>
              <FormLabel htmlFor='monitorType'>Monitor type</FormLabel>
              <Dropdown
                id='monitorType'
                isMenuPortalTarget={false}
                onChange={(option) => {
                  const selectedType = option?.value || ''
                  const defaultPort = getDefaultPortForType(selectedType)
                  formDispatch({ type: 'FILL_VERSION_DROPDOWN', payload: { type: selectedType, defaultPort } })
                  if (selectedType !== 'app') {
                    setHostTouched(false)
                    setPortTouched(false)
                    setTemplatePreview({ loading: false, error: '', content: '', defaults: null, dynamicHost: false })
                  }
                }}
                options={serviceTypes}
                selectedValue={monitorType}
              />
              <FormErrorMessage>{errors.monitorType}</FormErrorMessage>
            </FormControl>

            {isAppMonitor ? (
              <>
                <FormControl>
                  <Flex className={parentStyles.modalFieldWithAction}>
                    <FormLabel htmlFor='template' mb={0}>Template</FormLabel>
                    <RMIconButton onClick={handleRefreshAppTemplates} icon={HiRefresh} tooltip='Refresh templates from repository' />
                  </Flex>
                  <Dropdown
                    id='template'
                    isMenuPortalTarget={false}
                    onChange={(option) => {
                      const selectedTemplate = option?.value || ''
                      formDispatch({ type: 'SET_DOCKER_TEMPLATE', payload: selectedTemplate })
                      fetchTemplatePreview(selectedTemplate)
                    }}
                    options={templateOptions}
                    selectedValue={template}
                  />
                  {templatePreview.loading && <FormHelperText className={parentStyles.portHintText}>Loading template preview...</FormHelperText>}
                  {templatePreview.error && <FormHelperText className={parentStyles.templateErrorText}>{templatePreview.error}</FormHelperText>}
                  {templatePreview.dynamicHost && <FormHelperText className={parentStyles.portHintText}>Template host uses dynamic variables. Please set host manually.</FormHelperText>}
                  {templatePreview.content && (
                    <Box className={parentStyles.templatePreviewBox}>
                      <Textarea
                        value={templatePreview.content}
                        readOnly
                        size='sm'
                        className={parentStyles.templatePreviewText}
                      />
                    </Box>
                  )}
                </FormControl>

                {!template && (
                  <FormControl isInvalid={Boolean(errors.dockerImage)}>
                    <FormLabel htmlFor='dockerImage'>Docker image</FormLabel>
                    <Input
                      id='dockerImage'
                      type='text'
                      isRequired={true}
                      value={dockerImage}
                      onChange={(e) => {
                        formDispatch({ type: 'SET_FORM_DATA', payload: { dockerImage: e.target.value } })
                        if (errors.dockerImage) {
                          formDispatch({ type: 'SET_ERRORS', payload: { dockerImage: '' } })
                        }
                      }}
                    />
                    <FormErrorMessage>{errors.dockerImage}</FormErrorMessage>
                  </FormControl>
                )}
              </>
            ) : (
              <FormControl>
                <FormLabel htmlFor='tag'>Docker Version</FormLabel>
                <Dropdown
                  id='tag'
                  isMenuPortalTarget={false}
                  onChange={(option) => { formDispatch({ type: 'SET_FORM_DATA', payload: { tag: option?.value } }) }}
                  options={tagOptions}
                  selectedValue={tag}
                />
              </FormControl>
            )}

            <FormControl isInvalid={Boolean(errors.host)}>
              <FormLabel htmlFor='host'>Host</FormLabel>
              <Input
                id='host'
                type='text'
                isRequired={true}
                value={host}
                onChange={(e) => {
                  if (isAppMonitor) {
                    setHostTouched(true)
                  }
                  formDispatch({ type: 'SET_FORM_DATA', payload: { host: e.target.value } })

                  if (errors.host) {
                    formDispatch({ type: 'SET_ERRORS', payload: { host: '' } })
                  }
                }}
              />
              <FormErrorMessage>{errors.host}</FormErrorMessage>
            </FormControl>

            <FormControl isInvalid={Boolean(errors.port)}>
              <FormLabel htmlFor='port'>Port {isPortReadOnly ? '(From config)' : ''}</FormLabel>
              <Input
                id='port'
                type='number'
                min={1}
                max={65535}
                isRequired={true}
                value={port}
                isReadOnly={isPortReadOnly}
                className={isPortReadOnly ? parentStyles.readOnlyPortInput : ''}
                onChange={(e) => {
                  if (isPortReadOnly) return
                  if (isAppMonitor) {
                    setPortTouched(true)
                  }
                  formDispatch({ type: 'SET_FORM_DATA', payload: { port: e.target.value ? parseInt(e.target.value, 10) : '' } })
                  if (errors.port) {
                    formDispatch({ type: 'SET_ERRORS', payload: { port: '' } })
                  }
                }}
              />
              <FormErrorMessage>{errors.port}</FormErrorMessage>
              {isPortReadOnly && <FormHelperText className={parentStyles.portHintText}>Using cluster configured default port for this monitor type.</FormHelperText>}
            </FormControl>

            {isAppMonitor && (
              <>
                <FormControl>
                  <Checkbox
                    isChecked={isPrivateRegistry}
                    onChange={(e) =>
                      formDispatch({ type: 'SET_PRIVATE_REGISTRY_USAGE', payload: e.target.checked })
                    }
                  >
                    Use private Docker registry credentials
                  </Checkbox>
                </FormControl>

                {isPrivateRegistry && (
                  <>
                    <FormControl isInvalid={Boolean(errors.registryUrl)}>
                      <FormLabel htmlFor='dockerRegistryUrl'>Registry URL</FormLabel>
                      <Input
                        id='dockerRegistryUrl'
                        type='text'
                        value={url}
                        onChange={(e) =>
                          formDispatch({
                            type: 'SET_REGISTRY_CREDENTIALS',
                            payload: { url: e.target.value },
                          })
                        }
                      />
                      <FormErrorMessage>{errors.registryUrl}</FormErrorMessage>
                    </FormControl>

                    <FormControl isInvalid={Boolean(errors.dockerUser)}>
                      <FormLabel htmlFor='dockerRegistryUsername'>Username</FormLabel>
                      <Input
                        id='dockerRegistryUsername'
                        type='text'
                        value={username}
                        onChange={(e) =>
                          formDispatch({
                            type: 'SET_REGISTRY_CREDENTIALS',
                            payload: { username: e.target.value },
                          })
                        }
                      />
                      <FormErrorMessage>{errors.dockerUser}</FormErrorMessage>
                    </FormControl>

                    <FormControl>
                      <FormLabel htmlFor='dockerPassword'>Auth Type</FormLabel>
                      <Dropdown
                        id='type'
                        isMenuPortalTarget={false}
                        onChange={(option) => formDispatch({ type: 'SET_REGISTRY_CREDENTIALS', payload: { authType: option?.value } })}
                        options={authTypes}
                        selectedValue={authType}
                      />
                    </FormControl>

                    <FormControl isInvalid={Boolean(errors.dockerPassword)}>
                      <PasswordControl passwordError={errors.dockerPassword} inputClassName={theme === 'dark' ? parentStyles.darkLoginText : ''} labelClassName={theme === 'dark' ? parentStyles.darkLoginText : ''} className={`${parentStyles.revealButton} ${parentStyles.errorMessage} ${theme === 'dark' ? parentStyles.darkLoginText : ''}`} onChange={(e) => formDispatch({ type: 'SET_REGISTRY_CREDENTIALS', payload: { password: e.target.value } })} />
                    </FormControl>

                    <RMButton onClick={handleDockerAuth} size='medium'>
                      Connect
                    </RMButton>
                  </>
                )}
              </>
            )}
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton onClick={handleCreateNewServer} size='medium'>
            Add Monitor
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default NewServerModal

NewServerModal.propTypes = {
  clusterName: PropTypes.string.isRequired,
  isOpen: PropTypes.bool.isRequired,
  closeModal: PropTypes.func.isRequired,
}
