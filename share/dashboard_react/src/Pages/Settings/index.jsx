import { Flex, List, ListItem } from '@chakra-ui/react'
import React, { useState } from 'react'
import Button from '../../components/Button'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'

function Settings({ selectedCluster, user }) {
  const [selected, setSelected] = useState('General')
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
      <List className={styles.listContainer} spacing={2}>
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
      <Flex />
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
