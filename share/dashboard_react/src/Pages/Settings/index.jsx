import { Flex, useDisclosure } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'
import AccordionComponent from '../../components/AccordionComponent'
import LogsSettings from './LogsSettings'
import RejoinSettings from './RejoinSettings'
import ProxySettings from './ProxySettings'
import GraphSettings from './GraphSettings'
import CloudSettings from './CloudSettings'
import GlobalSettings from './GlobalSettings'
import RepFailOverSettings from './RepFailOverSettings'
import RepConfigSettings from './RepConfigSettings'
import AlertSettings from './AlertSettings'

function Settings({ selectedCluster, user }) {
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

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
  const { isOpen: isGlobalOpen, onToggle: onGlobalToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isGlobalOpen')) || false
  })

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
    localStorage.setItem('isGlobalOpen', JSON.stringify(isGlobalOpen))
  }, [isGlobalOpen])

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
        heading={'General'}
        onToggle={onGeneralToggle}
        isOpen={isGeneralOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Replication Failover Constraints'}
        onToggle={onRepFailOverToggle}
        isOpen={isRepFailOverOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<RepFailOverSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Replication Configuration'}
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
        heading={'Global'}
        onToggle={onGlobalToggle}
        isOpen={isGlobalOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GlobalSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
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
