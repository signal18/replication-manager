import { Box, Flex, Image, Spacer, Text, HStack, VStack, Button, useDisclosure } from '@chakra-ui/react'
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
  const [showImageLogo, setShowImageLogo] = useState(true)
  const [logoText, setLogoText] = useState('REPLICATION MANAGER')

  const {
    common: { isMobile, isDesktop },
    globalClusters: { monitor },
    cluster: { clusterAlerts, clusterData },
    auth: { isLogged }
  } = useSelector((state) => state)

  useEffect(() => {
    localStorage.setItem('chatOpen', isChatOpen);
  }, [isChatOpen]);

  useEffect(() => {
    if (!username){
      localStorage.removeItem('chatOpen');
      localStorage.removeItem('selectedChannel');
      localStorage.removeItem('userID')
    }
    
  }, [isChatOpen, username]);

  //to get the meet info
  useEffect(() => {
    if (isLogged) {
      dispatch(getMeetInfo());
    }
  }, [isLogged, dispatch]);

  useEffect(() => {
    if (meetInfo) {
      const totalUnreadMessages = Object.values(unreadMessagesByChannel).reduce((acc, count) => acc + count, 0)
      setUnreadMessagesCount(totalUnreadMessages)
    }
  }, [unreadMessagesByChannel, meetInfo]);

  //to toggle chat and save state
  const toggleChat = () => {
    setIsChatOpen(prevState => !prevState);
  };
  //
  useEffect(() => {
    let text = logoText;
    if (clusterData?.partner?.Name) {
      text = clusterData?.partner?.Name?.toUpperCase()
    } else if (monitor?.partner?.Name) {
      text = monitor?.partner?.Name?.toUpperCase()
    }

    text = text === "SIGNAL18" ? "REPLICATION MANAGER" : text;

    if (text !== logoText) {
      setLogoText(text)
    }
  }, [clusterData?.partner?.Name, monitor?.partner?.Name])

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
    localStorage.removeItem('userID')
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
          <HStack>
            {showImageLogo && <Image loading='lazy' height='50px' width={'fit-content'} className={`${styles.logo}`} objectFit='contain' src={`${theme === 'light' ? '/images/logo-no-text.png' : '/images/logo-no-text-dark.png'}`} onError={() => { setShowImageLogo(false) }} />}
            <TextLogo className={`${styles.logo} ${theme === 'light' ? styles.lightTextLogo : styles.darkTextLogo}`} text={logoText} />
          </HStack>
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
                      {monitor?.config?.cloud18 && (
                        <Flex className={styles.chatIcon}>
                          <AlertBadge
                            isSupport={true}
                            text='Support'
                            count={unreadMessagesCount || 0}
                            onClick={toggleChat}
                            showText={!isMobile}
                          />
                        </Flex>
                      )}
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

export function TextLogo({ className, text = "REPLICATION MANAGER", height = "50px" }) {
  // Split the text by space to create an array of words
  const words = text?.split(' ');

  // If only one word is present, apply styling for that case
  if (words.length === 1) {
    return (
      <VStack height={height} className={className} gap={0} alignItems={'flex-start'}>
        <Text className={`${styles.singleWord}`} fontSize="3xl" height={height} fontWeight="bold">
          {text}
        </Text>
      </VStack>
    );
  }

  // If only one word is present, apply styling for that case
  if (words.length === 2) {
    return (
      <VStack height={height} className={className} gap={0} alignItems={'flex-start'}>
        <Text className={`${className} ${styles.line1}`} fontSize="2xl" fontWeight="bold">
          {words[0]}
        </Text>
        <Text className={`${className} ${styles.line2}`} fontSize="2xl" fontWeight="bold">
          {words[1]}
        </Text>
      </VStack>
    );
  }


  // Count and split into two lines if there are more than two words
  if (words.length > 2) {
    words[1] = words.slice(1).join(' '); // Join the words from index 1 to the end
    words.splice(2); // Remove the words from index 2 to the end
  }

  return (
    <VStack height={height} className={className} gap={0} alignItems={'flex-start'}>
      <Text className={`${className} ${styles.line1}`} fontSize="2xl" fontWeight="bold">
        {words[0]}
      </Text>
      <Text className={`${className} ${styles.line2}`} fontSize="xl" fontWeight="bold">
        {words[1]}
      </Text>
    </VStack>
  );
}
