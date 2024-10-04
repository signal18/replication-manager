import {
  Checkbox,
  FormControl,
  FormErrorMessage,
  FormLabel,
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
import React, { useState, useEffect } from 'react'
import RMButton from '../RMButton'
import { useTheme } from '../../ThemeProvider'
import parentStyles from './styles.module.scss'
import { useDispatch, useSelector } from 'react-redux'
import { getMonitoredData } from '../../redux/globalClustersSlice'
import Message from '../Message'
import { addUser } from '../../redux/clusterSlice'

function AddUserModal({ clusterName, isOpen, closeModal }) {
  const dispatch = useDispatch()

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const [userName, setUserName] = useState('')
  const [userNameError, setUserNameError] = useState('')
  const [password, setPassword] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [grantsError, setGrantsError] = useState('')
  const [acls, setAcls] = useState([])
  const [allAcls, setAllAcls] = useState([])
  const [firstLoad, setFirstLoad] = useState(true)
  const { theme } = useTheme()

  useEffect(() => {
    if (monitor === null) {
      dispatch(getMonitoredData({}))
    }
  }, [monitor])

  useEffect(() => {
    if (monitor?.serviceAcl?.length > 0 && firstLoad) {
      const modifiedWithSelectedProp = monitor.serviceAcl.map((item) => Object.assign({}, item, { selected: false }))
      setAcls(modifiedWithSelectedProp)
      setAllAcls(modifiedWithSelectedProp)
      setFirstLoad(false)
    }
  }, [monitor?.serviceAcl])

  const handleCheck = (e, acl) => {
    const isChecked = e.target.checked
    const updatedList = allAcls.map((x) => {
      if (x.grant === acl.grant) {
        x.selected = isChecked
      }
      return x
    })
    setAcls(updatedList)
    setAllAcls(updatedList)
  }

  const handleSearch = (e) => {
    const search = e.target.value
    if (search) {
      const searchValue = search.toLowerCase()
      const searchedAcls = allAcls.filter((x) => {
        if (x.grant.toLowerCase().includes(searchValue)) {
          return x
        }
      })
      setAcls(searchedAcls)
    } else {
      setAcls(allAcls)
    }
  }

  const handleAddUser = () => {
    setUserNameError('')
    setPasswordError('')
    setGrantsError('')
    if (!userName) {
      setUserNameError('User is required')
      return
    }

    const selectedGrants = acls.filter((x) => x.selected).map((x) => x.grant)
    if (selectedGrants.length === 0) {
      setGrantsError('Please select atleast one grant')
      return
    }
    dispatch(addUser({ clusterName, username: userName, password, grants: selectedGrants.join(' ') }))
    closeModal()
  }
  return (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'Add a new user'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='2'>
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
              <Input id='password' type='password' value={password} onChange={(e) => setPassword(e.target.value)} />
              <Message type='error' message={passwordError} />
            </FormControl>
            <Message message={grantsError} />
            <VStack className={parentStyles.aclContainer}>
              <Input id='search' type='search' onChange={handleSearch} placeholder='Search ACL' />
              <List className={parentStyles.aclList}>
                {acls.length > 0 &&
                  acls.map((acl) => (
                    <ListItem className={parentStyles.aclListItem}>
                      <Checkbox
                        size='lg'
                        isChecked={!!acls.find((x) => x.grant === acl.grant && x.selected)}
                        onChange={(e) => handleCheck(e, acl)}>
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
