import {
  Checkbox,
  FormControl,
  FormErrorMessage,
  FormLabel,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack
} from '@chakra-ui/react'
import React, { useEffect, useReducer, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { addServer, connectDockerRegistry } from '../../redux/clusterSlice'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import PasswordControl from '../PasswordControl'
import { showSuccessToast } from '../../redux/toastSlice'
import RMIconButton from '../RMIconButton'
import { HiRefresh } from 'react-icons/hi'
import { refreshAppTemplateRepo } from '../../redux/globalClustersSlice'

const initialState = {
  formData: {
    host: '',
    port: 0,
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
    host: '',
    port: '',
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
    case 'FILL_VERSION_DROPDOWN':
      const repolist = action.payload === 'shardproxy' ? 'mariadb' : action.payload
      const repo = state.serviceRepos.find((r) => r.name === repolist)
      const tmpValue = action.payload === 'app' ? state.formData.template : ''
      return {
        ...state,
        formData: {
          ...state.formData,
          monitorType: action.payload,
          dockerImage: repo?.image || '',
          tag: '',
          dockerRegistry: {
            ...state.formData.dockerRegistry,
            template: tmpValue
          }
        },
        tagOptions: repo?.options || []
      }
    case 'SET_ERRORS':
      return { ...state, errors: { ...state.errors, ...action.payload } }
    // inside your formReducer add cases to handle new fields:
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
      return { ...initialState, serviceRepos: state.serviceRepos, tagOptions: state.tagOptions }
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
  const { globalClusters: { monitor } } = useSelector((state) => state)
  const [formState, formDispatch] = useReducer(formReducer, initialState)
  const { formData, tagOptions, templateOptions, errors } = formState
  const { host, port, monitorType, dockerImage, tag, dockerRegistry } = formData
  const { private: isPrivateRegistry, url, username, password, authType, template } = dockerRegistry

  useEffect(() => {
    if (monitor?.serviceRepos?.length > 0) {
      const repos = monitor?.serviceRepos.map(entry => ({
        name: entry.name,
        image: entry.image,
        options: entry.tags?.results?.map(tag => ({
          name: tag.name,
          value: `${entry.image}:${tag.name}`
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

  // useEffect(() => {
  //   console.log("::formState::", formState)
  // },[formState])

  const handleCreateNewServer = () => {
    const hostError = host ? '' : 'Host is required'
    const portError = port >= 0 && port <= 65535 ? '' : 'Port is required'

    formDispatch({ type: 'SET_ERRORS', payload: { host: hostError, port: portError } })
    if (hostError || portError) {
      return
    }

    if (monitorType === 'app') {
      if (!template || template.length === 0) {
        if (monitorType === 'app' && (!dockerImage || dockerImage.length === 0)) {
          formDispatch({ type: 'SET_ERRORS', payload: { dockerImage: 'Docker image is required' } })
          return
        }
      }

      if (isPrivateRegistry && (!url || url.length === 0)) {
        formDispatch({ type: 'SET_ERRORS', payload: { dockerPassword: 'Registry URL is required' } })
        return
      }

      if (isPrivateRegistry && (!username || username.length === 0)) {
        formDispatch({ type: 'SET_ERRORS', payload: { dockerUser: 'Username is required' } })
        return
      }

      if (isPrivateRegistry && (!password || password.length === 0)) {
        formDispatch({ type: 'SET_ERRORS', payload: { dockerPassword: 'Password is required' } })
        return
      }
    } else {

    }

    let finalTag = ""
    if (monitorType === 'app' && dockerImage && dockerImage.length > 0) {
      finalTag = dockerImage
    } else if (tag && tag.length > 0) {
      finalTag = tag
    }

    dispatch(addServer({ clusterName, host, port, monitorType, tag: finalTag, dockerRegistry }))
    closeModal()
  }

  const handleDockerAuth = () => {
    const userError = username?.length == 0 && isPrivateRegistry ? 'User is required' : ''
    const passError = password?.length == 0 && isPrivateRegistry ? 'Password is required' : ''

    formDispatch({ type: 'SET_ERRORS', payload: { dockerUser: userError, dockerPassword: passError } })
    if (userError || passError) {
      return
    }

    dispatch(connectDockerRegistry({ clusterName, dockerRegistry })).then((response) => {
      // console.log("::response::", response)
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
            <FormControl>
              <FormLabel htmlFor='monitorType'>Monitor type</FormLabel>
              <Dropdown
                id='monitorType'
                isMenuPortalTarget={false}
                onChange={(option) => formDispatch({ type: 'FILL_VERSION_DROPDOWN', payload: option.value })}
                options={serviceTypes}
                selectedValue={monitorType}
              />
            </FormControl>

            {monitorType === 'app' ? (
              <>
                <FormControl>
                  <FormLabel htmlFor='template'>Template</FormLabel>
                  <Dropdown
                    id='template'
                    isMenuPortalTarget={false}
                    onChange={(option) => { formDispatch({ type: 'SET_DOCKER_TEMPLATE', payload: option.value }) }}
                    options={templateOptions}
                    selectedValue={template}
                  />
                  <RMIconButton onClick={handleRefreshAppTemplates} icon={HiRefresh} tooltip='Refresh templates from repository' />
                </FormControl>

                {!template && (
                  <FormControl>
                    <FormLabel htmlFor='dockerImage'>Docker image</FormLabel>
                    <Input id='dockerImage' type='text' isRequired={true} value={dockerImage} onChange={(e) => formDispatch({ type: 'SET_FORM_DATA', payload: { dockerImage: e.target.value } })} />
                  </FormControl>
                )}
              </>
            ) : (
              <FormControl >
                <FormLabel htmlFor='tag'>Docker Version</FormLabel>
                <Dropdown
                  id='tag'
                  isMenuPortalTarget={false}
                  onChange={(option) => { formDispatch({ type: 'SET_FORM_DATA', payload: { tag: option.value } }) }}
                  options={tagOptions}
                  selectedValue={tag}
                />
              </FormControl>
            )}
            <FormControl isInvalid={errors.host}>
              <FormLabel htmlFor='host'>Host</FormLabel>
              <Input id='host' type='text' isRequired={true} value={host} onChange={(e) => formDispatch({ type: 'SET_FORM_DATA', payload: { host: e.target.value } })} />
              <FormErrorMessage>{errors.host}</FormErrorMessage>
            </FormControl>
            <FormControl isInvalid={errors.port}>
              <FormLabel htmlFor='port'>Port {template ? "(Optional)" : ""}</FormLabel>
              <Input id='port' type='number' max={65535} value={port} onChange={(e) => formDispatch({ type: 'SET_FORM_DATA', payload: { port: e.target.value ? parseInt(e.target.value, 10) : 0 } })} />
              <FormErrorMessage>{errors.port}</FormErrorMessage>
            </FormControl>

            {monitorType === 'app' && (
              <>
                {/* Private Docker Registry Checkbox */}
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

                {/* Conditionally render Docker Registry inputs */}
                {isPrivateRegistry && (
                  <>
                    <FormControl>
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
                    </FormControl>
                    <FormControl>
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
                        onChange={(option) => formDispatch({ type: 'SET_REGISTRY_CREDENTIALS', payload: { authType: option.value } })}
                        options={authTypes}
                        selectedValue={authType}
                      />
                    </FormControl>
                    <FormControl>
                      <PasswordControl passwordError={errors.dockerPassword} inputClassName={theme === 'dark' ? parentStyles.darkLoginText : ""} labelClassName={theme === 'dark' ? parentStyles.darkLoginText : ""} className={`${parentStyles.revealButton} ${parentStyles.errorMessage} ${theme === 'dark' ? parentStyles.darkLoginText : ""}`} onChange={(e) => formDispatch({ type: 'SET_REGISTRY_CREDENTIALS', payload: { password: e.target.value } })} />
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
