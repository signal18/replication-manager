import { Flex, List, ListItem, useDisclosure } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Button from '../../components/Button'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'
import AccordionComponent from '../../components/AccordionComponent'

function Settings({ selectedCluster, user }) {
  // const [selected, setSelected] = useState('General')
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

  const [items, _] = useState([
    'General',
    'Monitoring',
    'Logs',
    'Replication',
    'Rejoin',
    'Backups',
    'Schedulers',
    'Proxies',
    'Graphs',
    'Cloud18',
    'Global'
  ])

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

  useEffect(() => {
    console.log('useeffect called')
  }, [])

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
      {/* {items.map((item) => {
        return (
          <AccordionComponent
            heading={item}
            onToggle={onGeneralToggle}
            isOpen={isGeneralOpen}
            headerClassName={styles.accordionHeader}
            body={
              item === 'General' ? (
                <GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />
              ) : (
                item === 'Monitoring'?
              )
            }
          />
        )
      })} */}
      <AccordionComponent
        heading={'General'}
        onToggle={onGeneralToggle}
        isOpen={isGeneralOpen}
        headerClassName={styles.accordionHeader}
        body={<GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Monitoring'}
        onToggle={onMonitoringToggle}
        isOpen={isMonitoringOpen}
        headerClassName={styles.accordionHeader}
        body={<MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Logs'}
        onToggle={onLogsToggle}
        isOpen={isLogsOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Replication'}
        onToggle={onReplicationToggle}
        isOpen={isReplicationOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Rejoin'}
        onToggle={onRejoinToggle}
        isOpen={isRejoinOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Backups'}
        onToggle={onBackupsToggle}
        isOpen={isBackupsOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Schedulers'}
        onToggle={onSchedulersToggle}
        isOpen={isSchedulersOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Proxies'}
        onToggle={onProxiesToggle}
        isOpen={isProxiesOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Graphs'}
        onToggle={onGraphsToggle}
        isOpen={isGraphsOpen}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Cloud18'}
        onToggle={onCloud18Toggle}
        isOpen={isCloud18Open}
        headerClassName={styles.accordionHeader}
      />
      <AccordionComponent
        heading={'Global'}
        onToggle={onGlobalToggle}
        isOpen={isGlobalOpen}
        headerClassName={styles.accordionHeader}
      />

      {/* <List className={styles.listContainer} spacing={2}>
        {items.map((item) => (
          <ListItem className={`${styles.listItem} ${selected === item ? styles.selecetdListItem : ''}`}>
            <Button onClick={() => setSelected(item)}>{item}</Button>
          </ListItem>
        ))}
      </List>

      <Flex className={styles.settingsContent}>
        <Flex className={styles.content}>
          {selected === 'General' ? (
            <GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />
          ) : selected === 'Monitoring' ? (
            <MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} s />
          ) : null}
        </Flex>
      </Flex>
      <Flex />*/}
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
