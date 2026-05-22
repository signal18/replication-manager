import { Button, Flex, useDisclosure } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'
import AccordionComponent from '../../components/AccordionComponent'
import LogsSettings from './LogsSettings'
import PluginsSettings from './PluginsSettings'
import RejoinSettings from './RejoinSettings'
import ProxySettings from './ProxySettings'
import GraphSettings from './GraphSettings'
import CloudSettings from './CloudSettings'
import RepFailOverSettings from './RepFailOverSettings'
import RepConfigSettings from './RepConfigSettings'
import AlertSettings from './AlertSettings'
import BackupSettings from './BackupSettings'
import SchedulerSettings from './SchedulerSettings'
import S3ProvidersSettings from './S3ProvidersSettings'
import AppTemplateRepoSection from './components/AppTemplateRepoSection'
import { setSetting } from '../../redux/settingsSlice'
import { useDispatch } from 'react-redux'

function Settings({ selectedCluster, user, onTabChange, monitor }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

  // When navigated from Maintenance gear icon, only show filtered sections
  const [sectionFilter, setSectionFilter] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem('settingsFilter')) || null
    } catch { return null }
  })
  const isVisible = (key) => !sectionFilter || sectionFilter.includes(key)
  const clearFilter = () => {
    localStorage.removeItem('settingsFilter')
    setSectionFilter(null)
  }

  const { isOpen: isBackupOpen, onToggle: onBackupToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isBackupOpen')) || false
  })
  const { isOpen: isSchedulerOpen, onToggle: onSchedulerToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isSchedulerOpen')) || false
  })
  const { isOpen: isGeneralOpen, onToggle: onGeneralToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isGeneralOpen')) === false ? false : true
  })
  const { isOpen: isMonitoringOpen, onToggle: onMonitoringToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isMonitoringOpen')) || false
  })
  const { isOpen: isLogsOpen, onToggle: onLogsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isLogsOpen')) || false
  })
  const { isOpen: isRepFailOverOpen, onToggle: onRepFailOverToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isRepFailOverOpen')) || false
  })
  const { isOpen: isRepConfigOpen, onToggle: onRepConfigToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isRepConfigOpen')) || false
  })
  const { isOpen: isRejoinOpen, onToggle: onRejoinToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isRejoinOpen')) || false
  })
  const { isOpen: isAlertsOpen, onToggle: onAlertsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isAlertsOpen')) || false
  })
  const { isOpen: isProxiesOpen, onToggle: onProxiesToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isProxiesOpen')) || false
  })
  const { isOpen: isGraphsOpen, onToggle: onGraphsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isGraphsOpen')) || false
  })
  const { isOpen: isCloud18Open, onToggle: onCloud18Toggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isCloud18Open')) || false
  })
  const { isOpen: isPluginsOpen, onToggle: onPluginsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isPluginsOpen')) || false
  })
  const { isOpen: isS3ProvidersOpen, onToggle: onS3ProvidersToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isS3ProvidersOpen')) || false
  })
  const { isOpen: isAppTemplateRepoOpen, onToggle: onAppTemplateRepoToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isAppTemplateRepoOpen')) || false
  })

  useEffect(() => {
    localStorage.setItem('isBackupOpen', JSON.stringify(isBackupOpen))
  }, [isBackupOpen])
  useEffect(() => {
    localStorage.setItem('isSchedulerOpen', JSON.stringify(isSchedulerOpen))
  }, [isSchedulerOpen])
  useEffect(() => {
    localStorage.setItem('isGeneralOpen', JSON.stringify(isGeneralOpen))
  }, [isGeneralOpen])
  useEffect(() => {
    localStorage.setItem('isMonitoringOpen', JSON.stringify(isMonitoringOpen))
  }, [isMonitoringOpen])
  useEffect(() => {
    localStorage.setItem('isLogsOpen', JSON.stringify(isLogsOpen))
  }, [isLogsOpen])
  useEffect(() => {
    localStorage.setItem('isRepFailOverOpen', JSON.stringify(isRepFailOverOpen))
  }, [isRepFailOverOpen])
  useEffect(() => {
    localStorage.setItem('isRepConfigOpen', JSON.stringify(isRepConfigOpen))
  }, [isRepConfigOpen])

  useEffect(() => {
    localStorage.setItem('isRejoinOpen', JSON.stringify(isRejoinOpen))
  }, [isRejoinOpen])

  useEffect(() => {
    localStorage.setItem('isAlertsOpen', JSON.stringify(isAlertsOpen))
  }, [isAlertsOpen])
  useEffect(() => {
    localStorage.setItem('isProxiesOpen', JSON.stringify(isProxiesOpen))
  }, [isProxiesOpen])
  useEffect(() => {
    localStorage.setItem('isGraphsOpen', JSON.stringify(isGraphsOpen))
  }, [isGraphsOpen])

  useEffect(() => {
    localStorage.setItem('isCloud18Open', JSON.stringify(isCloud18Open))
  }, [isCloud18Open])
  useEffect(() => {
    localStorage.setItem('isPluginsOpen', JSON.stringify(isPluginsOpen))
  }, [isPluginsOpen])
  useEffect(() => {
    localStorage.setItem('isS3ProvidersOpen', JSON.stringify(isS3ProvidersOpen))
  }, [isS3ProvidersOpen])
  useEffect(() => {
    localStorage.setItem('isAppTemplateRepoOpen', JSON.stringify(isAppTemplateRepoOpen))
  }, [isAppTemplateRepoOpen])

  const openConfirmModal = (title, handler) => {
    setIsConfirmModalOpen(true)
    setConfirmHandler(handler)
    setConfirmTitle(title)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setConfirmHandler(null)
    setConfirmTitle('')
  }
  return (
    <Flex className={styles.settingsContainer}>
      {sectionFilter && (
        <Button size='sm' variant='outline' mb={2} onClick={clearFilter}>
          Show all settings
        </Button>
      )}
      {isVisible('isGeneralOpen') && <AccordionComponent
        heading={'Topology'}
        onToggle={onGeneralToggle}
        isOpen={isGeneralOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} onTabChange={onTabChange} monitor={monitor} />}
      />}
      {isVisible('isRepFailOverOpen') && <AccordionComponent
        heading={'Failover'}
        onToggle={onRepFailOverToggle}
        isOpen={isRepFailOverOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RepFailOverSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isRepConfigOpen') && <AccordionComponent
        heading={'Replication'}
        onToggle={onRepConfigToggle}
        isOpen={isRepConfigOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RepConfigSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isMonitoringOpen') && <AccordionComponent
        heading={'Monitoring'}
        onToggle={onMonitoringToggle}
        isOpen={isMonitoringOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isAlertsOpen') && <AccordionComponent
        heading={'Alerts'}
        onToggle={onAlertsToggle}
        isOpen={isAlertsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<AlertSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isLogsOpen') && <AccordionComponent
        heading={'Logs'}
        onToggle={onLogsToggle}
        isOpen={isLogsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<LogsSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isPluginsOpen') && <AccordionComponent
        heading={'Plugins'}
        onToggle={onPluginsToggle}
        isOpen={isPluginsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<PluginsSettings selectedCluster={selectedCluster} user={user} />}
      />}
      {isVisible('isRejoinOpen') && <AccordionComponent
        heading={'Rejoin'}
        onToggle={onRejoinToggle}
        isOpen={isRejoinOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RejoinSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isProxiesOpen') && <AccordionComponent
        heading={'Proxies'}
        onToggle={onProxiesToggle}
        isOpen={isProxiesOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<ProxySettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isGraphsOpen') && <AccordionComponent
        heading={'Graphs'}
        onToggle={onGraphsToggle}
        isOpen={isGraphsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GraphSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isCloud18Open') && <AccordionComponent
        heading={'Cloud18'}
        onToggle={onCloud18Toggle}
        isOpen={isCloud18Open}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<CloudSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isSchedulerOpen') && <AccordionComponent
        heading={'Scheduler'}
        onToggle={onSchedulerToggle}
        isOpen={isSchedulerOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<SchedulerSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />}
      {isVisible('isBackupOpen') && <AccordionComponent
        heading={'Backup'}
        onToggle={onBackupToggle}
        isOpen={isBackupOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<BackupSettings selectedCluster={selectedCluster} user={user} />}
      />}
      {isVisible('isS3ProvidersOpen') && <AccordionComponent
        heading={'S3 Providers'}
        onToggle={onS3ProvidersToggle}
        isOpen={isS3ProvidersOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<S3ProvidersSettings selectedCluster={selectedCluster} user={user} />}
      />}
      {isVisible('isAppTemplateRepoOpen') && <AccordionComponent
        heading={'App Templates Repo'}
        onToggle={onAppTemplateRepoToggle}
        isOpen={isAppTemplateRepoOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={
          <AppTemplateRepoSection
            scope='cluster'
            config={selectedCluster?.config}
            canEdit={
              user?.grants?.['cluster-settings'] !== false &&
              user?.grants?.['cluster-settings'] !== undefined &&
              selectedCluster?.config?.provAppTemplateRepoAllowOverride !== false
            }
            onSet={(setting, value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value }))}
          />
        }
      />}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={confirmTitle}
          onConfirmClick={() => {
            confirmHandler()
            closeConfirmModal()
          }}
        />
      )}
    </Flex>
  )
}

export default Settings
