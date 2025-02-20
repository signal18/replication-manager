import { Box, Flex, Image, Spacer, Text, HStack, Button, useDisclosure } from '@chakra-ui/react'
import React, { useState, useEffect} from 'react'
import { useDispatch, useSelector } from 'react-redux'
import { logout } from '../../redux/authSlice'
import ThemeIcon from '../Icons/ThemeIcon'
import RefreshCounter from '../RefreshCounter'
import { isAuthorized } from '../../utility/common'
import { Link } from 'react-router-dom'
import { clearCluster } from '../../redux/clusterSlice'
import AlertBadge from '../AlertBadge'
import AlertModal from '../Modals/AlertModal'
import { FaPowerOff, FaUserPlus } from 'react-icons/fa'
import ConfirmModal from '../Modals/ConfirmModal'
import styles from './styles.module.scss'
import RMButton from '../RMButton'
import RMIconButton from '../RMIconButton'
import { useTheme } from '../../ThemeProvider'
import AddUserModal from '../Modals/AddUserModal'
import MattermostIntegration from '../../Pages/Mattermost';
import { getMeetInfo, logoutFromMeet } from '../../redux/meetSlice';

function Navbar({ username }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [alertModalType, setAlertModalType] = useState('')
  const [isLogoutModalOpen, setIsLogoutModalOpen] = useState(false)
  const [isAddUserModalOpen, setIsAddUserModalOpen] = useState(false)
  const [unreadMessagesCount, setUnreadMessagesCount] = useState(0)
  const { meetInfo, unreadMessagesByChannel } = useSelector((state) => state?.meet)
  const [isChatOpen, setIsChatOpen] = useState(() => { return localStorage.getItem('chatOpen') === 'true'; });

  const {
    common: { isMobile, isDesktop },
    globalClusters: { monitor },
    cluster: { clusterAlerts, clusterData }
  } = useSelector((state) => state)

  useEffect(() => {
    localStorage.setItem('chatOpen', isChatOpen);
  }, [isChatOpen]);

  //to get the meet info
  useEffect(() => {
    if (isAuthorized() && isDesktop) dispatch(getMeetInfo());
  }, [dispatch]);

  useEffect(() => {
    if (meetInfo && isAuthorized()) {
      const totalUnreadMessages = Object.values(unreadMessagesByChannel).reduce((acc, count) => acc + count, 0)
      setUnreadMessagesCount(totalUnreadMessages)
    }
  }, [unreadMessagesByChannel]);

  //to toggle chat and save state
  const toggleChat = () => {
    setIsChatOpen(prevState => !prevState);
  };
  //

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
    dispatch(logoutFromMeet())
    localStorage.removeItem('chatOpen');
    localStorage.removeItem('selectedChannel');
    dispatch(logout())
    dispatch(clearCluster())
  }
  const openAddUserModal = () => {
    setIsAddUserModalOpen(true)
  }

  const closeAddUserModal = () => {
    setIsAddUserModalOpen(false)
  }

  return (
    <>
      <Flex
        as='nav'
        className={`${styles.navbarContainer} ${theme === 'light' ? styles.lightBackground : styles.darkBackground} `}
        gap='2'
        align='center'>
        <Link to='/'>
          <Image
            loading='lazy'
            height='50px'
            width={isMobile ? '180px' : 'fit-content'}
            className={`${styles.logo}`}
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
              {username && isDesktop && (
                  <>
                      <Text>{`Welcome, ${username}`}</Text>
                      <Flex className={styles.chatIcon}>
                        <AlertBadge
                          isSupport={true}
                          text='Support'
                          count={unreadMessagesCount || 0}
                          onClick={toggleChat}
                          showText={!isMobile}
                        />
                      </Flex>
                  </>
              )}
              {isMobile ? (
                <RMIconButton onClick={openLogoutModal} border='none' icon={FaPowerOff} />
              ) : (
                <RMButton onClick={openLogoutModal}>Logout</RMButton>
              )}
              {clusterData && monitor?.config?.monitoringSaveConfig && monitor?.config?.cloud18GitUser?.length > 0 && (
                <RMIconButton
                  icon={FaUserPlus}
                  tooltip={'Add User'}
                  px='2'
                  variant='outline'
                  onClick={openAddUserModal}
                />
              )}
            </>
          )}

          <ThemeIcon />

        </HStack>
      </Flex>

      <MattermostIntegration isOpen={isChatOpen} setIsChatOpen={setIsChatOpen} onClose={() => setIsChatOpen(false)}  />

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
      {isAddUserModalOpen && (
        <AddUserModal clusterName={clusterData?.name} isOpen={isAddUserModalOpen} closeModal={closeAddUserModal} />
      )}
    </>
  )
}

export default Navbar
