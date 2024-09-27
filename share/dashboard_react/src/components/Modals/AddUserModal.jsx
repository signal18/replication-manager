import {
  Box,
  Checkbox,
  FormControl,
  FormErrorMessage,
  FormLabel,
  HStack,
  Input,
  List,
  ListItem,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Stack,
  VStack
} from '@chakra-ui/react'
import React, { useState, useEffect, act } from 'react'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getMonitoredData } from '../../redux/globalClustersSlice'

function AddUserModal({ clusterName, isOpen, closeModal }) {
  const dispatch = useDispatch()
  const [userName, setUserName] = useState('')
  const [userNameError, setUserNameError] = useState('')
  const [selectedAcls, setSelectedAcls] = useState([])
  const [acls, setAcls] = useState([])
  const { theme } = useTheme()
  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  useEffect(() => {
    if (monitor === null) {
      dispatch(getMonitoredData({}))
    }
  }, [monitor])

  useEffect(() => {
    if (monitor?.serviceAcl?.length > 0) {
      setAcls(monitor.serviceAcl)
    }
  }, [monitor?.serviceAcl])

  const handleCheck = (e, acl) => {
    const isChecked = e.target.checked

    if (isChecked) {
      setSelectedAcls((prev) => [...prev, acl])
    } else {
      setSelectedAcls((prev) => prev.filter((i) => i.grant !== acl.grant))
    }
  }

  const handleSearch = (e) => {
    const search = e.target.value
    if (search) {
      const searchValue = search.toLowerCase()
      const searchedAcls = monitor?.serviceAcl?.filter((x) => {
        if (x.grant.toLowerCase().includes(searchValue)) {
          return x
        }
      })
      setAcls(searchedAcls)
    } else {
      setAcls(monitor?.serviceAcl)
    }
  }

  const handleAddUser = () => {}
  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'Add a new user'}</ModalHeader>
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
            <VStack className={parentStyles.aclContainer}>
              <Input id='search' type='search' onChange={handleSearch} placeholder='Search ACL' />
              <List className={parentStyles.aclList}>
                {acls?.length > 0 &&
                  acls.map((acl) => (
                    <ListItem className={parentStyles.aclListItem}>
                      <Checkbox size='lg' isChecked={selectedAcls.includes(acl)} onChange={(e) => handleCheck(e, acl)}>
                        {acl.grant}
                      </Checkbox>
                    </ListItem>
                  ))}
              </List>
            </VStack>
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton onClick={handleAddUser} size='medium'>
            Add User
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default AddUserModal
