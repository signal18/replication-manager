import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'

import ConfirmModal from '../../components/Modals/ConfirmModal'

import AccordionComponent from '../../components/AccordionComponent'
import CloudSettings from './CloudSettings'
import { useSelector } from 'react-redux'

function ClustersGlobalSettings({}) {
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

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
        heading={'Cloud18'}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<CloudSettings monitor={monitor} openConfirmModal={openConfirmModal} />}
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

export default ClustersGlobalSettings
