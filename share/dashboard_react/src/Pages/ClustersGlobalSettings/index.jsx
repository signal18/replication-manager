import { Flex } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import { adminMode } from '../../services/adminMode'

import ConfirmModal from '../../components/Modals/ConfirmModal'

import AccordionComponent from '../../components/AccordionComponent'
import CloudSettings from './CloudSettings'
import MarketplaceSettings from './MarketplaceSettings'
import { useDispatch, useSelector } from 'react-redux'
import GlobalSettings from './GlobalSettings'
import ArbitrationSettings from './ArbitrationSettings'
import { setGlobalSetting } from '../../redux/globalClustersSlice'
import AppTemplateRepoSection from '../Settings/components/AppTemplateRepoSection'
import StandbyBanner from '../../components/StandbyBanner'

function ClustersGlobalSettings({ user }) {
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')
  const dispatch = useDispatch()

  const {
    globalClusters: { monitor }
  } = useSelector((state) => state)

  // Admin mode (#2262) unlocks the admin zone: a Cloud18 session that has entered admin mode (the
  // local admin login held in memory) may edit global settings even without its own cluster-settings
  // grant — the edit then carries the admin token (apiHelper retries on X-Repman-Admin-Required).
  const [adminActive, setAdminActive] = useState(adminMode.isActive())
  useEffect(() => {
    const refresh = () => setAdminActive(adminMode.isActive())
    window.addEventListener(adminMode.CHANGED, refresh)
    return () => window.removeEventListener(adminMode.CHANGED, refresh)
  }, [])

  const canEditGlobalSettings = adminActive || (user?.grants?.['cluster-settings'] !== false && (user?.User === 'admin' || user?.grants?.['cluster-settings'] !== undefined))

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
      <StandbyBanner />
      <AccordionComponent
        heading={'Registration'}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<CloudSettings config={monitor?.config} openConfirmModal={openConfirmModal} />}
      />
      {/* GlobalSettings renders its own Global Settings / Alerts / Logs cards */}
      <GlobalSettings config={monitor?.config} openConfirmModal={openConfirmModal} />
      <AccordionComponent
        heading={'Arbitration'}
        headerClassName={styles.accordionHeader}
        panelClassName={styles.accordionPanel}
        body={<ArbitrationSettings config={monitor?.config} />}
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
