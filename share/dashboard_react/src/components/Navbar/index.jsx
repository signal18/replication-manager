import { Box, Flex, Image, Spacer, Text, HStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { logout } from '../../redux/authSlice'
import ThemeIcon from '../Icons/ThemeIcon'
import RefreshCounter from '../RefreshCounter'
import { isAuthorized } from '../../utility/common'
import { Link } from 'react-router-dom'
import { clearCluster } from '../../redux/clusterSlice'
import AlertBadge from '../AlertBadge'
import AlertModal from '../Modals/AlertModal'
import { FaPowerOff } from 'react-icons/fa'
import ConfirmModal from '../Modals/ConfirmModal'
import styles from './styles.module.scss'
import Button from '../Button'
import IconButton from '../IconButton'

function Navbar({ username }) {
  const dispatch = useDispatch()
  const [alertModalType, setAlertModalType] = useState('')
  const [isLogoutModalOpen, setIsLogoutModalOpen] = useState(false)
  const {
    common: { isMobile, isDesktop },
    cluster: { clusterAlerts, clusterData }
  } = useSelector((state) => state)

  const openAlertModal = (type) => {
    setAlertModalType(type)
  }
  const closeAlertModal = (type) => {
    setAlertModalType('')
  }

  const openLogoutModal = () => {
    setIsLogoutModalOpen(true)
  }
  const closeLogoutModal = () => {
    setIsLogoutModalOpen(false)
  }

  const handleLogout = () => {
    dispatch(logout())
    dispatch(clearCluster())
  }

  return (
    <>
      <Flex as='nav' className={styles.navbarContainer} gap='2' align='center'>
        <Link to='/'>
          <Image
            loading='lazy'
            height='50px'
            width={isMobile ? '180px' : 'fit-content'}
            className={styles.logo}
            objectFit='contain'
            src='/images/logo.png'
            alt='Replication
           Manager'
          />
        </Link>
        <Spacer />

        {isAuthorized() && isDesktop && <RefreshCounter clusterName={clusterData?.name} />}

        <Spacer />
        <HStack spacing='4'>
          {isAuthorized() && clusterData && (
            <Flex className={styles.alerts}>
              <AlertBadge
                isBlocking={true}
                text='Blockers'
                count={clusterAlerts?.errors?.length || 0}
                onClick={() => openAlertModal('error')}
                showText={!isMobile}
              />
              <AlertBadge
                text='Warnings'
                count={clusterAlerts?.warnings?.length || 0}
                onClick={() => openAlertModal('warning')}
                showText={!isMobile}
              />
            </Flex>
          )}

          {isAuthorized() && (
            <>
              {username && isDesktop && <Text>{`Welcome, ${username}`}</Text>}
              {isMobile ? (
                <IconButton onClick={openLogoutModal} border='none' icon={FaPowerOff} />
              ) : (
                <Button onClick={openLogoutModal}>Logout</Button>
              )}
            </>
          )}

          <ThemeIcon />
        </HStack>
      </Flex>
      {isAuthorized() && !isDesktop && (
        <Box mx='auto' p='8px' marginTop='60px'>
          <RefreshCounter clusterName={clusterData?.name} />
        </Box>
      )}
      {alertModalType && (
        <AlertModal type={alertModalType} isOpen={alertModalType.length !== 0} closeModal={closeAlertModal} />
      )}
      {isLogoutModalOpen && (
        <ConfirmModal
          onConfirmClick={handleLogout}
          closeModal={closeLogoutModal}
          isOpen={isLogoutModalOpen}
          title={'Are you sure you want to log out?'}
        />
      )}
    </>
  )
}

export default Navbar
