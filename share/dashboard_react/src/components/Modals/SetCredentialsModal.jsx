import {
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
import React, { useState } from 'react'
import { useDispatch } from 'react-redux'
import { setCredentials } from '../../redux/clusterSlice'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'

function SetCredentialsModal({ clusterName, isOpen, closeModal, type }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [userName, setUserName] = useState('')
  const [password, setPassword] = useState('')

  const [userNameError, setUserNameError] = useState('')
  const [passwordError, setPasswordError] = useState('')

  const handleSave = () => {
    setUserNameError('')
    setPasswordError('')

    if (!userName) {
      setUserNameError('User is required')
      return
    }

    if (!password) {
      setPasswordError('Password is required')
      return
    }

    if (userName.includes(':')) {
      setUserNameError('User cannot contain colon')
      return
    }

    const typeLower = type.toLowerCase()

    switch (typeLower) {
      case 'db-servers-credential':
        dispatch(
          setCredentials({ 
            clusterName, 
            credentialType: 'db-servers-credential', 
            credential: `${userName}:${password}` 
          })
        )
        break
      case 'replication-credential':
        dispatch(
          setCredentials({
            clusterName,
            credentialType: 'replication-credential',
            credential: `${userName}:${password}`
          })
        )
        break
      case 'cloud18-dba-user-credentials':
        dispatch(
          setCredentials({
            clusterName,
            credentialType: 'cloud18-dba-user-credentials',
            credential: btoa(`${userName}:${password}`)
          })
        )
        break
      case 'cloud18-sponsor-user-credentials':
        dispatch(
          setCredentials({
            clusterName,
            credentialType: 'cloud18-sponsor-user-credentials',
            credential: btoa(`${userName}:${password}`)
          })
        )
        break
      case 'proxysql-servers-credential':
        dispatch(
          setCredentials({
            clusterName,
            credentialType: 'proxysql-servers-credential',
            credential: `${userName}:${password}`
          })
        )
        break
      case 'maxscale-servers-credential':
        dispatch(
          setCredentials({
            clusterName,
            credentialType: 'maxscale-servers-credential',
            credential: `${userName}:${password}`
          })
        )
        break
      case 'shardproxy-servers-credential':
        dispatch(
          setCredentials({
            clusterName,
            credentialType: 'shardproxy-servers-credential',
            credential: `${userName}:${password}`
          })
        )
        break
      default:
        setUserNameError('Invalid credential type')
        return
    }

    closeModal()
  }

  const getTitle = (type) => {
    switch (type) {
      case 'db-servers-credential':
        return 'Set Database Credentials';
      case 'replication-credential':
        return 'Set Replication Credentials';
      case 'cloud18-dba-user-credentials':
        return 'Set DBA Credentials'
      case 'cloud18-sponsor-user-credentials':
        return 'Set Sponsor DB Credentials'
      case 'proxysql-servers-credential':
        return 'Set ProxySQL Credentials'
      case 'maxscale-servers-credential':
        return 'Set Maxscale Credentials'
      case 'shardproxy-servers-credential':
        return 'Set Sharding Proxy Credentials'
      default:
        return ''
    }
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{getTitle(type)}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <FormControl isInvalid={userNameError}>
              <FormLabel htmlFor='username'>User</FormLabel>
              <Input
                id='username'
                type='text'
                isRequired={true}
                value={userName}
                onChange={(e) => setUserName(e.target.value)}
              />
              <FormErrorMessage>{userNameError}</FormErrorMessage>
            </FormControl>
            <FormControl isInvalid={passwordError}>
              <FormLabel htmlFor='password'>Password</FormLabel>
              <Input
                id='password'
                type='password'
                isRequired={true}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <FormErrorMessage>{passwordError}</FormErrorMessage>
            </FormControl>
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton onClick={handleSave} size='medium'>
            Save
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default SetCredentialsModal
