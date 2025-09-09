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
import { updateGrants } from '../../redux/clusterSlice'
import GrantCheckList from '../GrantCheckList'

function UserGrantModal({ clusterName, selectedUser, isOpen, closeModal }) {
  const dispatch = useDispatch()
  const [user, setUser] = useState(null)
  const [isConfirm, setIsConfirm] = useState(false)

  const {
    globalClusters: { monitor },
    cluster: { clusterData }
  } = useSelector((state) => state)

  const [acls, setAcls] = useState([])
  const [grantsError, setGrantsError] = useState('')
  const [roles, setRoles] = useState([])
  const [rolesError, setRolesError] = useState('')
  const [allAcls, setAllAcls] = useState([])
  const [allRoles, setAllRoles] = useState([])
  const [firstLoad, setFirstLoad] = useState(true)
  const { theme } = useTheme()
  const { serviceAcl = [], serviceRoles = [] } = monitor

  const loggedUser = useSelector((state) => state.auth.user)

  useEffect(() => {
    const apiUser = clusterData?.apiUsers?.[loggedUser.User] ? clusterData?.apiUsers?.[loggedUser.User] : clusterData?.apiUsers?.[loggedUser.Email]

    if (apiUser) {
      setUser(apiUser)
    }
  }, [clusterData?.apiUsers, loggedUser])

  useEffect(() => {
    setFirstLoad(true)
  }, [selectedUser])

  const isAllowedGrant = (d, u, item) => {
    if (d?.roles?.['sysops']) {
      return true
    }
    return d?.grants?.[item.grant] || u?.grants?.[item.grant]
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
      const modifiedWithSelectedProp = serviceAcl.filter((item) => isAllowedGrant(user, selectedUser, item)).map((item) => Object.assign({}, item, { selected: selectedUser?.grants?.[item.grant] ?? false }))
      const modifiedRolesWithSelectedProp = serviceRoles.filter((item) => selectedUser?.roles?.[item.role] || listRoles(user).includes(item.role)).map((item) => Object.assign({}, item, { selected: selectedUser?.roles?.[item.role] ?? false }))
      setAcls(modifiedWithSelectedProp)
      setAllAcls(modifiedWithSelectedProp)
      setRoles(modifiedRolesWithSelectedProp)
      setAllRoles(modifiedRolesWithSelectedProp)
      setFirstLoad(false)
    }
  }, [serviceAcl, serviceRoles, user, selectedUser])

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
    setIsConfirm(true)
  }

  const handleCloseConfirm = () => {
    setIsConfirm(false)
  }

  const handleUserGrants = () => {
    setGrantsError('')
    setRolesError('')

    const selectedRoles = roles.filter((x) => x.selected).map((x) => x.role)
    const selectedGrants = acls

    dispatch(updateGrants({ clusterName, username: selectedUser?.user, grants: selectedGrants.join(' '), roles: selectedRoles.join(' ') }))
    closeModal()
  }

  return !isConfirm ? (
    <Modal isOpen={isOpen} onClose={closeModal}>
      <ModalOverlay />
      <ModalContent className={theme === 'light' ? parentStyles.modalLightContent : parentStyles.modalDarkContent}>
        <ModalHeader>{'Update user privileges'}</ModalHeader>
        <ModalCloseButton />
        <ModalBody>
          <Stack spacing='2'>
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
            Update Privileges
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
              <strong>Username:</strong> {selectedUser?.user || "N/A"}
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
              {acls?.length > 0 ? acls?.map((grant, index) => (
                <Text key={index} fontSize="sm" mb={1}>
                  {grant}
                </Text>
              )) : <Text key={"nogrant"} fontSize="sm" mb={1}>N/A</Text>}
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
          <RMButton onClick={handleUserGrants} size='medium'>
            Confirm
          </RMButton>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}

export default UserGrantModal
