import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'

import ConfirmModal from '../../components/Modals/ConfirmModal'

import AccordionComponent from '../../components/AccordionComponent'
import CloudSettings from './CloudSettings'
import MarketplaceSettings from './MarketplaceSettings'
import { useDispatch, useSelector } from 'react-redux'
import GlobalSettings from './GlobalSettings'
import { setGlobalSetting } from '../../redux/globalClustersSlice'
import AppTemplateRepoSection from '../Settings/components/AppTemplateRepoSection'

function ClustersGlobalSettings({ user }) {
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')
  const dispatch = useDispatch()

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  const canEditGlobalSettings = user?.grants?.['cluster-settings'] !== false && (user?.User === 'admin' || user?.grants?.['cluster-settings'] !== undefined)

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
        heading={'Registration'}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<CloudSettings config={monitor?.config} openConfirmModal={openConfirmModal} />}
      />
      <AccordionComponent
        heading={'Global Settings'}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<GlobalSettings config={monitor?.config} openConfirmModal={openConfirmModal} />}
      />
      {monitor?.config?.cloud18 && (
        <AccordionComponent
          heading={'Market Place'}
          headerClassName={styles.accordionHeader}
          panelClassName={styles.accordionPanel}
          body={<MarketplaceSettings config={monitor?.config} />}
        />
      )}
      <AccordionComponent
        heading={'App Templates Repo'}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={
          <AppTemplateRepoSection
            scope='global'
            config={monitor?.config}
            canEdit={canEditGlobalSettings}
            onSet={(setting, value) => dispatch(setGlobalSetting({ setting, value }))}
          />
        }
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
