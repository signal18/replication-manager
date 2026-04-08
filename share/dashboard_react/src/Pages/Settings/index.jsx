import { Flex, useDisclosure } from '@chakra-ui/react'
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

function Settings({ selectedCluster, user, onTabChange, monitor }) {
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

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
      <AccordionComponent
        heading={'Topology'}
        onToggle={onGeneralToggle}
        isOpen={isGeneralOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} onTabChange={onTabChange} monitor={monitor} />}
      />
      <AccordionComponent
        heading={'Failover'}
        onToggle={onRepFailOverToggle}
        isOpen={isRepFailOverOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RepFailOverSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Replication'}
        onToggle={onRepConfigToggle}
        isOpen={isRepConfigOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RepConfigSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Monitoring'}
        onToggle={onMonitoringToggle}
        isOpen={isMonitoringOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Alerts'}
        onToggle={onAlertsToggle}
        isOpen={isAlertsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<AlertSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Logs'}
        onToggle={onLogsToggle}
        isOpen={isLogsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<LogsSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Plugins'}
        onToggle={onPluginsToggle}
        isOpen={isPluginsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<PluginsSettings selectedCluster={selectedCluster} user={user} />}
      />
      <AccordionComponent
        heading={'Rejoin'}
        onToggle={onRejoinToggle}
        isOpen={isRejoinOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RejoinSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Proxies'}
        onToggle={onProxiesToggle}
        isOpen={isProxiesOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<ProxySettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Graphs'}
        onToggle={onGraphsToggle}
        isOpen={isGraphsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GraphSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Cloud18'}
        onToggle={onCloud18Toggle}
        isOpen={isCloud18Open}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<CloudSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Scheduler'}
        onToggle={onSchedulerToggle}
        isOpen={isSchedulerOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<SchedulerSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Backup'}
        onToggle={onBackupToggle}
        isOpen={isBackupOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<BackupSettings selectedCluster={selectedCluster} user={user} />}
      />

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
