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
import { addServer } from '../../redux/clusterSlice'
import Dropdown from '../Dropdown'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'

function NewServerModal({ clusterName, isOpen, closeModal }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [host, setHost] = useState('')
  const [port, setPort] = useState(0)
  const [dbType, setDbType] = useState('')
  const [hostError, setHostError] = useState('')
  const [portError, setPortError] = useState('')
  const [dbTypeError, setDbTypeError] = useState('')

  const handleCreateNewServer = () => {
    setHostError('')
    setPortError('')

    if (!host) {
      setHostError('Host is required')
      return
    }

    if (!port || port === 0) {
      setPortError('Port is required')
      return
    }

    if (!dbType) {
      setDbTypeError('Database type is required')
      return
    }

    dispatch(addServer({ clusterName, host, port, dbType }))
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'New server'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <FormControl isInvalid={hostError}>
              <FormLabel htmlFor='host'>Host</FormLabel>
              <Input id='host' type='text' isRequired={true} value={host} onChange={(e) => setHost(e.target.value)} />
              <FormErrorMessage>{hostError}</FormErrorMessage>
            </FormControl>
            <FormControl isInvalid={portError}>
              <FormLabel htmlFor='port'>Port</FormLabel>
              <Input id='port' type='number' isRequired={true} value={port} onChange={(e) => setPort(e.target.value)} />
              <FormErrorMessage>{portError}</FormErrorMessage>
            </FormControl>
            <FormControl isInvalid={dbTypeError}>
              <FormLabel htmlFor='dbType'>Database type</FormLabel>
              <Dropdown
                id='dbType'
                isMenuPortalTarget={false}
                onChange={(option) => {
                  setDbType(option.value)
                }}
                options={[
                  { name: 'MariaDB', value: 'mariadb' },
                  { name: 'MySQL', value: 'mysql' },
                  { name: 'Percona', value: 'percona' },
                  { name: 'ProxySQL', value: 'proxysql' },
                  { name: 'HaProxy', value: 'haproxy' },
                  { name: 'ShardProxy', value: 'shardproxy' },
                  { name: 'MaxScale', value: 'maxscale' },
                  { name: 'SphinxProxy', value: 'sphinx' },
                  { name: 'VIP', value: 'extvip' }
                ]}
              />
              <FormErrorMessage>{dbTypeError}</FormErrorMessage>
            </FormControl>
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            No
          </RMButton>
          <RMButton onClick={handleCreateNewServer} size='medium'>
            Yes
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default NewServerModal
