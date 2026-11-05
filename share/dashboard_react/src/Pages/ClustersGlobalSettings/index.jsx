import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'

import ConfirmModal from '../../components/Modals/ConfirmModal'

import TabItems from '../../components/TabItems'
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
  // the global settings' tabs (#2268): Cloud18 first, with Registration / Support / Marketplace
  const [globalTab, setGlobalTab] = useState(() => parseInt(localStorage.getItem('globalSettingsTab'), 10) || 0)
  const changeGlobalTab = (index) => {
    setGlobalTab(index)
    localStorage.setItem('globalSettingsTab', String(index))
  }
  const [cloud18Tab, setCloud18Tab] = useState(0)
  return (
    <Flex className={styles.settingsContainer}>
      <StandbyBanner />
      <TabItems
        className={styles.fullWidthTabs}
        options={['Cloud18', 'General', 'Arbitration', 'App Templates Repo']}
        tabIndex={globalTab}
        onChange={changeGlobalTab}
        tabContents={[
          <TabItems
            key='cloud18'
            className={styles.fullWidthTabs}
            options={['Registration', 'Support', 'Marketplace']}
            tabIndex={cloud18Tab}
            onChange={setCloud18Tab}
            tabContents={[
              <CloudSettings key='registration' config={monitor?.config} openConfirmModal={openConfirmModal} part='registration' />,
              <CloudSettings key='support' config={monitor?.config} openConfirmModal={openConfirmModal} part='support' />,
              <Flex key='marketplace' direction='column' gap='8px' width='100%'>
                <CloudSettings config={monitor?.config} openConfirmModal={openConfirmModal} part='marketplace' />
                {monitor?.config?.cloud18 && <MarketplaceSettings config={monitor?.config} />}
              </Flex>
            ]}
          />,
          // GlobalSettings renders its own Global Settings / Alerts / Logs cards
          <GlobalSettings key='general' config={monitor?.config} openConfirmModal={openConfirmModal} />,
          <ArbitrationSettings key='arbitration' config={monitor?.config} />,
      <AppTemplateRepoSection key='templates'
        scope='global'
        config={monitor?.config}
        canEdit={canEditGlobalSettings}
        onSet={(setting, value) => dispatch(setGlobalSetting({ setting, value }))}
      />
        ]}
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
