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
  Text,
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
import GrantCheckList from '../GrantCheckList'

function AddUserModal({ clusterName, isOpen, closeModal, apiUsers }) {
  const dispatch = useDispatch()
  const [user, setUser] = useState(null) 
  const [isConfirm, setIsConfirm] = useState(false)

  const {
    globalClusters: { monitor },
    cluster: { clusterData }
  } = useSelector((state) => state)

  const [userName, setUserName] = useState('')
  const [userNameError, setUserNameError] = useState('')
  // const [password, setPassword] = useState('')
  // const [passwordError, setPasswordError] = useState('')
  const [acls, setAcls] = useState([])
  const [grantsError, setGrantsError] = useState('')
  const [roles, setRoles] = useState([])
  const [rolesError, setRolesError] = useState('')
  const [allAcls, setAllAcls] = useState([])
  const [allRoles, setAllRoles] = useState([])
  const [firstLoad, setFirstLoad] = useState(true)
  const [selectedGroups, setSelectedGroups] = useState([]);
  const { theme } = useTheme()
  const emailRegex = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
  const { serviceAcl = [], serviceRoles = [] } = monitor
  
  useEffect(() => {
    if (monitor === null) {
      dispatch(getMonitoredData({}))
    }
  }, [monitor])

  useEffect(() => {
    // Prefer apiUsers passed by the caller (cluster list rows already carry
    // them) so opening the modal from the list does not require loading the
    // cluster into the global store — which flipped the navbar into cluster
    // context while still on the list.
    const users = apiUsers ?? clusterData?.apiUsers
    if (users) {
      const loggedUser = localStorage.getItem('username')
      if (loggedUser && users[loggedUser]) {
        setUser(users[loggedUser])
      }
    }
  }, [apiUsers, clusterData])

  const isAllowedGrant = (user, item) => {
    if (user.roles['sysops']) {
      return true
    } 
    return user.grants[item.grant]
  }

  const listRoles = (user) => {
    if (user.roles['sysops']) {
      return ['dbops', 'extdbops', 'extsysops']
    } else if (user.roles['dbops']) {
      return ['extdbops']
    } else if (user.roles['sponsor']) {
      return ['extdbops', 'extsysops']
    }
    return []
  }

  useEffect(() => {
    if (serviceAcl?.length > 0 && user != null && firstLoad) {
      const modifiedWithSelectedProp = serviceAcl.filter((item) => isAllowedGrant(user, item)).map((item) => Object.assign({}, item, { selected: false }))
      const modifiedRolesWithSelectedProp = serviceRoles.filter((item) => listRoles(user).includes(item.role)).map((item) => Object.assign({}, item, { selected: false }))
      setAcls(modifiedWithSelectedProp)
      setAllAcls(modifiedWithSelectedProp)
      setRoles(modifiedRolesWithSelectedProp)
      setAllRoles(modifiedRolesWithSelectedProp)
      setFirstLoad(false)
    }
  }, [serviceAcl, serviceRoles, user])

  const handleCheckRoles = (e, role) => {
    const isChecked = e.target.checked;
    const updatedList = structuredClone(allRoles).map((x) => {
      if (x.role === role.role) {
        x.selected = isChecked;
      }
      return x;
    });
    setRoles(updatedList);
    setAllRoles(updatedList);
  };

  const handleSearchRoles = (e) => {
    const search = e.target.value
    if (search) {
      const searchRoleValue = search.toLowerCase()
      const searchedRoles = allRoles.filter((x) => {
        if (x.role.toLowerCase().includes(searchRoleValue)) {
          return x
        }
      })
      setRoles(searchedRoles)
    } else {
      setRoles(allRoles)
    }
  }

  const handleSubmit = (e) => {
    e.preventDefault()
    setUserNameError('')
    setGrantsError('')
    setRolesError('')
    if (!userName) {
      setUserNameError('User is required')
      return
    }

    if (!emailRegex.test(userName)) {
      setUserNameError('User must be email address')
      return
    }
    setIsConfirm(true)
  }

  const handleCloseConfirm = () => {
    setIsConfirm(false)
  }


  const handleAddUser = () => {
    const selectedRoles = roles.filter((x) => x.selected).map((x) => x.role)
    const selectedGrants = acls

    dispatch(addUser({ clusterName, username: userName, grants: selectedGrants.join(' '), roles: selectedRoles.join(' ') }))
    closeModal()
  }

  return !isConfirm ? (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'Add a new user'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='2'>
            <FormControl isInvalid={userNameError}>
              <FormLabel htmlFor='username'>User Email</FormLabel>
              <Input
                id='username'
                type='text'
                isRequired={true}
                value={userName}
                onChange={(e) => setUserName(e.target.value)}
              />
              <FormErrorMessage>{userNameError}</FormErrorMessage>
            </FormControl>
            <Message message={rolesError} />
            <VStack className={parentStyles.roleContainer}>
              <Input id='searchRole' type='search' onChange={handleSearchRoles} placeholder='Search ROLE' />
              <List className={parentStyles.roleList}>
                {roles.length > 0 &&
                  roles.map((role) => (
                    <ListItem className={parentStyles.roleListItem}>
                      <Checkbox
                        isChecked={!!roles.find((x) => x.role === role.role && x.selected)}
                        onChange={(e) => handleCheckRoles(e, role)}>
                        {role.role}
                      </Checkbox>
                    </ListItem>
                  ))}
              </List>
            </VStack>
            <Message message={grantsError} />
            <GrantCheckList grantOptions={allAcls} onChange={setAcls} parentStyles={parentStyles} user={user} />
          </Stack>
        </ModalBody>

        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={closeModal}>
            Cancel
          </RMButton>
          <RMButton onClick={handleSubmit} size='medium'>
            Add User
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  ) : (
    <Modal isOpen={isOpen} onClose={handleCloseConfirm}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'Update user privileges'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='2'>
          <Text>
              Are you sure you want to submit the following details?
            </Text>
            <Text mt={4}>
              <strong>Cluster Name:</strong> {clusterName}
            </Text>
            <Text>
              <strong>Username:</strong> {userName || "N/A"}
            </Text>
            <Text>
              <strong>Grants:</strong>
            </Text>
            <div
              style={{
                maxHeight: "150px", // Set maximum height for the scrollable area
                overflowY: "auto", // Add vertical scroll
                border: "1px solid #E2E8F0", // Optional: Add a border to distinguish the section
                padding: "8px", // Optional: Add padding for better readability
                borderRadius: "8px",
              }}
            >
              { acls.length > 0 ? acls.map((grant, index) => (
                <Text key={index} fontSize="sm" mb={1}>
                  {grant}
                </Text>
              )) : <Text key={"nogrant"} fontSize="sm" mb={1}>N/A</Text> }
            </div>
            <Text>
              <strong>Roles:</strong> {roles.filter((x) => x.selected).map((x) => x.role).join(" ") || "N/A"}
            </Text>
          </Stack>
        </ModalBody>
        <ModalFooter gap={3} margin='auto'>
          <RMButton colorScheme='blue' size='medium' variant='outline' onClick={handleCloseConfirm}>
            Cancel
          </RMButton>
          <RMButton onClick={handleAddUser} size='medium'>
            Confirm
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default AddUserModal
