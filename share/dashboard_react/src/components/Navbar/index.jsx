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
import SecurityScoreModal from '../Modals/SecurityScoreModal'
import WorkloadModal from '../Modals/WorkloadModal'
import { FaPowerOff, FaUserPlus } from 'react-icons/fa'
import { MdSecurity, MdNotificationsOff } from 'react-icons/md'
import { RiSpeedFill } from 'react-icons/ri'
import ConfirmModal from '../Modals/ConfirmModal'
import InterventionPanel from '../Modals/InterventionPanel'
import { clusterService } from '../../services/clusterService'
import { getApi } from '../../services/apiHelper'
import styles from './styles.module.scss'
import RMButton from '../RMButton'
import RMIconButton from '../RMIconButton'
import { useTheme } from '../../ThemeProvider'
import AddUserModal from '../Modals/AddUserModal'
import MattermostIntegration from '../../Pages/Mattermost';
import { getMeetInfo, logoutFromMeet } from '../../redux/meetSlice';
import { selectMeetUIState } from '../../redux/memoize'
import { clearClusters } from '../../redux/globalClustersSlice'

function Navbar({ username }) {
  const dispatch = useDispatch()
  const { theme } = useTheme()
  const [alertModalType, setAlertModalType] = useState('')
  const [globalAlertModalType, setGlobalAlertModalType] = useState('')
  const [isLogoutModalOpen, setIsLogoutModalOpen] = useState(false)
  const [isSecurityModalOpen, setIsSecurityModalOpen] = useState(false)
  const [isWorkloadModalOpen, setIsWorkloadModalOpen] = useState(false)
  const [isAddUserModalOpen, setIsAddUserModalOpen] = useState(false)
  const [isInterventionPanelOpen, setIsInterventionPanelOpen] = useState(false)
  const [unreadMessagesCount, setUnreadMessagesCount] = useState(0)
  const [isChatOpen, setIsChatOpen] = useState(() => { return localStorage.getItem('chatOpen') === 'true'; });
  const [showImageLogo, setShowImageLogo] = useState(true)
  const [logoText, setLogoText] = useState('REPLICATION MANAGER')

  const { meetInfo, unreadMessagesByChannel, meetError } = useSelector(selectMeetUIState)
  const { isMobile, isDesktop } = useSelector((state) => state.common)
  const clusterAlerts = useSelector((state) => state?.cluster?.clusterAlerts)
  const clusterData = useSelector((state) => state?.cluster?.clusterData)
  const globalAlerts = useSelector((state) => state?.globalClusters?.globalAlerts)
  const isLogged = useSelector((state) => state?.auth?.isLogged)
  const baseURL = useSelector((state) => state?.auth?.baseURL)
  const monitor = useSelector((state) => state?.globalClusters?.monitor)

  useEffect(() => {
    if (isLogged){
      localStorage.setItem('chatOpen', isChatOpen);
    }
  }, [isChatOpen, isLogged]);

  //to get the meet info
  useEffect(() => {
    if (isLogged && monitor?.config?.cloud18) {
      dispatch(getMeetInfo());
    }else{
      localStorage.removeItem('chatOpen');
      localStorage.removeItem('selectedChannel');
    }
  }, [isLogged, dispatch, monitor?.config?.cloud18]);

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
    dispatch(clearClusters())
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

        {isAuthorized() && isDesktop && location.pathname !== '/slideshow' && <RefreshCounter clusterName={clusterData?.name} />}


        <Spacer />
        <HStack spacing='4'>
          


          {isAuthorized() && !clusterData && (
            <Flex className={styles.alerts}>
              <AlertBadge
                isBlocking={true}
                text='G-Blockers'
                count={globalAlerts?.errors?.length || 0}
                onClick={() => setGlobalAlertModalType('error')}
                showText={!isMobile}
              />
              <AlertBadge
                text='G-Warnings'
                count={globalAlerts?.warnings?.length || 0}
                onClick={() => setGlobalAlertModalType('warning')}
                showText={!isMobile}
              />
              <AlertBadge
                colorScheme={monitor?.activeInterventionCount > 0 ? 'red' : monitor?.isGlobalInterventionPending ? 'orange' : 'teal'}
                icon={MdNotificationsOff}
                text={monitor?.activeInterventionCount > 0 ? 'Muted' : monitor?.isGlobalInterventionPending ? 'Scheduled' : 'Mute'}
                count={monitor?.activeInterventionCount || ''}
                onClick={() => setIsInterventionPanelOpen(true)}
                showText={!isMobile}
                blink={monitor?.activeInterventionCount > 0}
              />
            </Flex>
          )}

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
              {clusterData?.securityScore?.grade && (
                <AlertBadge
                  colorScheme={(clusterData?.securityStates || []).length > 0 ? (GRADE_COLOR[clusterData.securityScore.grade] || 'orange') : 'gray'}
                  icon={MdSecurity}
                  text='Security'
                  count={(clusterData?.securityStates || []).length}
                  onClick={() => setIsSecurityModalOpen(true)}
                  showText={!isMobile}
                />
              )}
              <AlertBadge
                colorScheme={(clusterData?.workloadStates || []).length > 0 ? 'purple' : 'gray'}
                icon={RiSpeedFill}
                text='Workload'
                count={(clusterData?.workloadStates || []).length}
                bubbleStyle={{
                  background: `var(--chakra-colors-${(clusterData?.workloadStates || []).length > 0 ? 'purple' : 'gray'}-500)`,
                  color: 'white',
                }}
                onClick={() => setIsWorkloadModalOpen(true)}
                showText={!isMobile}
              />
              <AlertBadge
                colorScheme={clusterData?.isIntervention ? 'red' : 'teal'}
                icon={MdNotificationsOff}
                text={clusterData?.isIntervention ? 'Muted' : 'Mute'}
                count={clusterData?.interventionSuppressedAlerts || ''}
                onClick={() => setIsInterventionPanelOpen(true)}
                showText={!isMobile}
                blink={clusterData?.isIntervention}
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
                            isConnect={!meetError}
                            text={meetError ? 'Support (disconnected)' : 'Support'}
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

      <MattermostIntegration isOpen={isChatOpen} setIsChatOpen={setIsChatOpen} onClose={() => setIsChatOpen(false)} cloud18={monitor?.config?.cloud18} />

      {isAuthorized() && !isDesktop && location.pathname !== '/slideshow' && (
        <Box mx='auto' p='8px' marginTop='60px'>
          <RefreshCounter clusterName={clusterData?.name} />
        </Box>
      )}
      {alertModalType && (
        <AlertModal type={alertModalType} isOpen={alertModalType.length !== 0} closeModal={closeAlertModal} />
      )}
      {globalAlertModalType && (
        <AlertModal type={globalAlertModalType} isOpen={globalAlertModalType.length !== 0} closeModal={() => setGlobalAlertModalType('')} alerts={globalAlerts} />
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
      {isSecurityModalOpen && (
        <SecurityScoreModal isOpen={isSecurityModalOpen} closeModal={() => setIsSecurityModalOpen(false)} />
      )}
      {isWorkloadModalOpen && (
        <WorkloadModal isOpen={isWorkloadModalOpen} closeModal={() => setIsWorkloadModalOpen(false)} />
      )}
      {isInterventionPanelOpen && (
        <InterventionPanel
          isOpen={isInterventionPanelOpen}
          closeModal={() => setIsInterventionPanelOpen(false)}
          isGlobal={!clusterData}
          isActive={clusterData ? clusterData?.isIntervention : (monitor?.activeInterventionCount > 0 || monitor?.isGlobalInterventionPending)}
          current={clusterData ? clusterData?.interventionCurrent : monitor?.globalInterventionEntry}
          isPending={!clusterData && monitor?.isGlobalInterventionPending && !monitor?.isGlobalIntervention}
          history={clusterData ? (clusterData?.interventionHistory || []) : []}
          suppressedAlerts={clusterData ? (clusterData?.interventionSuppressedAlerts || 0) : 0}
          activeCount={!clusterData ? (monitor?.activeInterventionCount || 0) : 0}
          onStart={(reason, startAt, endAt) => {
            const api = clusterData
              ? clusterService.startIntervention(clusterData.name, reason, baseURL, startAt, endAt)
              : getApi(baseURL).post('actions/intervention-start', { reason, startAt, endAt })
            api.catch((err) => console.error('Failed to start intervention:', err))
          }}
          onEnd={() => {
            const api = clusterData
              ? clusterService.endIntervention(clusterData.name, baseURL)
              : getApi(baseURL).post('actions/intervention-end')
            api.catch((err) => console.error('Failed to end intervention:', err))
          }}
        />
      )}
    </>
  )
}

const GRADE_COLOR = { A: 'green', B: 'teal', C: 'yellow', D: 'orange', F: 'red' }

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
