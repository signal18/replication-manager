import {
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
import React, { useState } from 'react'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'

// The on-premise SSH private key (onpremise-ssh-private-key), write-only: either the path of a key
// file on the replication-manager host, or the key itself, pasted. A pasted key is written by
// replication-manager to a 0600 file in the cluster's working directory, never to the config git;
// the setting then holds that path. The key is never read back.
function SSHPrivateKeyModal({ isOpen, closeModal, currentPath, onSave }) {
  const { theme } = useTheme()
  const [path, setPath] = useState('')
  const [key, setKey] = useState('')
  const [error, setError] = useState('')

  const handleSave = () => {
    setError('')
    const k = key.trim()
    const p = path.trim()
    if (k && p) {
      setError('Give either a path or a key, not both')
      return
    }
    if (k && !k.startsWith('-----BEGIN')) {
      setError('A key begins with -----BEGIN')
      return
    }
    if (p && !p.startsWith('/')) {
      setError('The path must be absolute')
      return
    }
    // empty: back to the default key of the replication-manager user (~/.ssh/id_rsa)
    onSave(k || p)
    setKey('')
    setPath('')
    closeModal()
  }

  return (
    <Modal isOpen={isOpen} onClose={closeModal} size='xl'>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>Set On-Premise SSH Private Key</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='5'>
            <FormControl>
              <FormLabel htmlFor='ssh-key-path'>Path on the replication-manager host</FormLabel>
              <Input id='ssh-key-path' type='text' placeholder={currentPath || '~/.ssh/id_rsa (default)'} value={path} onChange={(e) => setPath(e.target.value)} />
            </FormControl>
            <FormControl isInvalid={!!error}>
              <FormLabel htmlFor='ssh-key-content'>Or the private key (no passphrase)</FormLabel>
              <Textarea id='ssh-key-content' rows={8} fontFamily='mono' fontSize='xs' autoComplete='off' spellCheck={false}
                placeholder='-----BEGIN OPENSSH PRIVATE KEY-----' value={key} onChange={(e) => setKey(e.target.value)} />
              <FormHelperText>Kept in a 0600 file of the cluster's working directory, never in the config repository. Both empty: the default key.</FormHelperText>
              <FormErrorMessage>{error}</FormErrorMessage>
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

export default SSHPrivateKeyModal
