import { Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'

import ConfirmModal from '../../components/Modals/ConfirmModal'

import TabItems from '../../components/TabItems'
import SectionFrame from '../../components/SectionFrame'
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
  // the global settings' tabs (#2268): Cloud18 (Registration / Support / Marketplace), Monitoring
  // Units, HTTP & API, Logs, Arbitration, AI Assistant (the MCP server)
  const [globalTab, setGlobalTab] = useState(() => parseInt(localStorage.getItem('globalSettingsTab'), 10) || 0)
  const changeGlobalTab = (index) => {
    setGlobalTab(index)
    localStorage.setItem('globalSettingsTab', String(index))
  }
  const [cloud18Tab, setCloud18Tab] = useState(0)
  // Cloud18 > Marketplace: Pricing and Gateway & Domain for a Cloud18 instance only
  const [marketplaceTab, setMarketplaceTab] = useState(0)
  const marketplaceTabs = ['Peering', ...(monitor?.config?.cloud18 ? ['Pricing', 'Gateway & Domain'] : []), 'App Templates Repo']
  return (
    <Flex className={styles.settingsContainer}>
      <StandbyBanner />
      <TabItems
        className={styles.fullWidthTabs}
        options={['Cloud18', 'Monitoring Units', 'HTTP & API', 'Logs', 'Arbitration', 'AI Assistant']}
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
              <Flex key='support' direction='column' gap='8px' width='100%'>
                <CloudSettings config={monitor?.config} openConfirmModal={openConfirmModal} part='support' />
                <GlobalSettings config={monitor?.config} openConfirmModal={openConfirmModal} cards={['Alerts']} />
              </Flex>,
              <TabItems
                key='marketplace'
                className={styles.fullWidthTabs}
                options={marketplaceTabs}
                tabIndex={Math.min(marketplaceTab, marketplaceTabs.length - 1)}
                onChange={setMarketplaceTab}
                tabContents={[
                  <CloudSettings key='peering' config={monitor?.config} openConfirmModal={openConfirmModal} part='marketplace' />,
                  ...(monitor?.config?.cloud18
                    ? [
                        <SectionFrame key='pricing' title='Pricing'>
                          <MarketplaceSettings config={monitor?.config} part='pricing' />
                        </SectionFrame>,
                        <SectionFrame key='gateway' title='Gateway and Domain'>
                          <MarketplaceSettings config={monitor?.config} part='gateway' />
                        </SectionFrame>
                      ]
                    : []),
                  <SectionFrame key='templates' title='App Templates Repo'>
                    <AppTemplateRepoSection
                scope='global'
                config={monitor?.config}
                canEdit={canEditGlobalSettings}
                onSet={(setting, value) => dispatch(setGlobalSetting({ setting, value }))}
              />
                  </SectionFrame>
                ]}
              />
            ]}
          />,
          <GlobalSettings key='units' config={monitor?.config} openConfirmModal={openConfirmModal} cards={['Monitoring Units']} />,
          <GlobalSettings key='http' config={monitor?.config} openConfirmModal={openConfirmModal} cards={['HTTP & API']} />,
          <GlobalSettings key='logs' config={monitor?.config} openConfirmModal={openConfirmModal} cards={['Logs']} />,
          <SectionFrame key='arbitration' title='Arbitration'>
            <ArbitrationSettings config={monitor?.config} />
          </SectionFrame>,
          <GlobalSettings key='assistant' config={monitor?.config} openConfirmModal={openConfirmModal} cards={['AI Assistant (MCP)']} />
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
