import { Flex, useDisclosure } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'
import AccordionComponent from '../../components/AccordionComponent'
import LogsSettings from './LogsSettings'
import ReplicationSettings from './ReplicationSettings'
import RejoinSettings from './RejoinSettings'
import BackupSettings from './BackupSettings'
import SchedulerSettings from './SchedulerSettings'
import ProxySettings from './ProxySettings'
import GraphSettings from './GraphSettings'

function Settings({ selectedCluster, user }) {
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

  const { isOpen: isGeneralOpen, onToggle: onGeneralToggle } = useDisclosure({ defaultIsOpen: true })
  const { isOpen: isMonitoringOpen, onToggle: onMonitoringToggle } = useDisclosure()
  const { isOpen: isLogsOpen, onToggle: onLogsToggle } = useDisclosure()
  const { isOpen: isReplicationOpen, onToggle: onReplicationToggle } = useDisclosure()
  const { isOpen: isRejoinOpen, onToggle: onRejoinToggle } = useDisclosure()
  const { isOpen: isBackupsOpen, onToggle: onBackupsToggle } = useDisclosure()
  const { isOpen: isSchedulersOpen, onToggle: onSchedulersToggle } = useDisclosure()
  const { isOpen: isProxiesOpen, onToggle: onProxiesToggle } = useDisclosure()
  const { isOpen: isGraphsOpen, onToggle: onGraphsToggle } = useDisclosure()
  const { isOpen: isCloud18Open, onToggle: onCloud18Toggle } = useDisclosure()
  const { isOpen: isGlobalOpen, onToggle: onGlobalToggle } = useDisclosure()

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
        heading={'Monitoring'}
        onToggle={onMonitoringToggle}
        isOpen={isMonitoringOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
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
        heading={'Replication'}
        onToggle={onReplicationToggle}
        isOpen={isReplicationOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<ReplicationSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
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
        heading={'Backups'}
        onToggle={onBackupsToggle}
        isOpen={isBackupsOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<BackupSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Schedulers'}
        onToggle={onSchedulersToggle}
        isOpen={isSchedulersOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<SchedulerSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
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
      />
      <AccordionComponent
        heading={'Global'}
        onToggle={onGlobalToggle}
        isOpen={isGlobalOpen}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
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
