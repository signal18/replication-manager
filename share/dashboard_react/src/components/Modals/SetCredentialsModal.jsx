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
      setUserName('User is required')
      return
    }

    if (!password) {
      setPassword('Password is required')
      return
    }
    const typeLower = type.toLowerCase()

    if (typeLower.includes('database')) {
      dispatch(
        setCredentials({ clusterName, credentialType: 'db-servers-credential', credential: `${userName}:${password}` })
      )
    } else if (typeLower.includes('replication')) {
      dispatch(
        setCredentials({
          clusterName,
          credentialType: 'replication-credential',
          credential: `${userName}:${password}`
        })
      )
    } else if (typeLower.includes('proxysql')) {
      dispatch(
        setCredentials({
          clusterName,
          credentialType: 'proxysql-servers-credential',
          credential: `${userName}:${password}`
        })
      )
    } else if (typeLower.includes('maxscale')) {
      dispatch(
        setCredentials({
          clusterName,
          credentialType: 'maxscale-servers-credential',
          credential: `${userName}:${password}`
        })
      )
    } else if (typeLower.includes('sharding')) {
      dispatch(
        setCredentials({
          clusterName,
          credentialType: 'shardproxy-servers-credential',
          credential: `${userName}:${password}`
        })
      )
    }
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{type}</ModalHeader>
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
