import { Button, Flex } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'
import TabItems from '../../components/TabItems'
import SectionFrame from '../../components/SectionFrame'
import PluginsSettings from './PluginsSettings'
import AlertSettings, { ALERT_GROUPS } from './AlertSettings'
import StandbyBanner from '../../components/StandbyBanner'
import LogsSettings from './LogsSettings'
import RejoinSettings from './RejoinSettings'
import ProxySettings from './ProxySettings'
import GraphSettings from './GraphSettings'
import CloudSettings from './CloudSettings'
import RepFailOverSettings from './RepFailOverSettings'
import RepConfigSettings from './RepConfigSettings'
import CredentialsSettings from './CredentialsSettings'
import AppTemplateRepoSection from './components/AppTemplateRepoSection'
import { setSetting } from '../../redux/settingsSlice'
import { useDispatch } from 'react-redux'

// the Setup tab's sub-tabs (#2268): each a group of the cluster's settings sections, in order. The
// Scheduler, Backup, Archive and S3 Providers settings live in the Maintenance tab, Dynamic Config in
// Configs > Databases > Scaling. Alerts has
// its own tabs inside (by channel, Cloud18 Support for a Cloud18 cluster).
const SETUP_GROUPS = [
  { name: 'Connect', sections: ['isCredentialsOpen'] },
  { name: 'High Availability', sections: ['isGeneralOpen', 'isRepFailOverOpen', 'isRejoinOpen', 'isRepConfigOpen', 'isProxiesOpen'] },
  { name: 'Collecting', sections: ['isMonitoringOpen', 'isLogsOpen', 'isGraphsOpen'] },
  { name: 'Alerts', sections: ['alerts'] },
  { name: 'Marketplace', sections: ['isCloud18Open', 'isAppTemplateRepoOpen'] },
  { name: 'Plugins', sections: ['isPluginsOpen'] }
]
const SECTION_ORDER = SETUP_GROUPS.flatMap((g) => g.sections)

function Settings({ selectedCluster, user, onTabChange, monitor }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmHandler, setConfirmHandler] = useState(null)
  const [confirmTitle, setConfirmTitle] = useState('')

  // When navigated from Maintenance gear icon, only show filtered sections
  const [sectionFilter, setSectionFilter] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem('settingsFilter')) || null
    } catch { return null }
  })
  const isVisible = (key) => !sectionFilter || sectionFilter.includes(key)
  const [setupTab, setSetupTab] = useState(() => parseInt(localStorage.getItem('setupTab'), 10) || 0)
  const [alertTab, setAlertTab] = useState(0)
  // the level-3 tab of each Setup sub-tab
  const [sectionTab, setSectionTab] = useState({})
  const alertGroups = ALERT_GROUPS.filter((g) => !g.cloud18 || selectedCluster?.config?.cloud18)
  const changeSetupTab = (index) => {
    setSetupTab(index)
    localStorage.setItem('setupTab', String(index))
  }
  const clearFilter = () => {
    localStorage.removeItem('settingsFilter')
    setSectionFilter(null)
  }







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
  // the sections, by their key (#2268): shown as the Setup sub-tabs, or filtered when a gear in
  // another tab opened some of them
  const sectionBlocks = {
    isGeneralOpen: { title: 'Topology', body: <GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} onTabChange={onTabChange} monitor={monitor} /> },
    isCredentialsOpen: { title: 'Credentials', body: <CredentialsSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isRepFailOverOpen: { title: 'Failover', body: <RepFailOverSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isRepConfigOpen: { title: 'Replication', body: <RepConfigSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isMonitoringOpen: { title: 'Monitoring', body: <MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isLogsOpen: { title: 'Logs', body: <LogsSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isRejoinOpen: { title: 'Rejoin', body: <RejoinSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isProxiesOpen: { title: 'Proxies', body: <ProxySettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isGraphsOpen: { title: 'Graphs', body: <GraphSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    // Marketplace (the Cloud18 settings): a sub-tab of its own
    isCloud18Open: { title: 'Cloud18', body: <CloudSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} /> },
    isAppTemplateRepoOpen: { title: 'App Templates Repo', body: <AppTemplateRepoSection
                scope='cluster'
                config={selectedCluster?.config}
                canEdit={
                  user?.grants?.['cluster-settings'] !== false &&
                  user?.grants?.['cluster-settings'] !== undefined &&
                  selectedCluster?.config?.provAppTemplateRepoAllowOverride !== false
                }
                onSet={(setting, value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value }))}
              /> },
    // Alerts: its channels as tabs (level 3)
    alerts: { title: 'Alerts', body: (
      <TabItems
        className={styles.fullWidthTabs}
        options={alertGroups.map((g) => g.name)}
        tabIndex={Math.min(alertTab, alertGroups.length - 1)}
        onChange={setAlertTab}
        tabContents={alertGroups.map((g) => (
          <AlertSettings key={g.name} selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} group={g.name} />
        ))}
      />
    ) },
    // Plugins: a sub-tab of its own
    isPluginsOpen: { title: 'Plugins', body: <PluginsSettings selectedCluster={selectedCluster} user={user} /> }
  }
  // a section in a frame (#2268), always with its heading
  const frame = (k) => (
    <SectionFrame key={k} title={sectionBlocks[k].title}>
      {sectionBlocks[k].body}
    </SectionFrame>
  )
  // a filtered view (a gear in another tab): the requested sections, framed, one under the other
  const sectionsOf = (keys) => keys.filter(isVisible).map((k) => frame(k))
  // a Setup sub-tab: its one section framed, or its sections as level-3 tabs
  const groupOf = (g) => {
    const keys = g.sections.filter((k) => sectionBlocks[k])
    if (keys.length === 1) return frame(keys[0])
    const index = Math.min(sectionTab[g.name] || 0, keys.length - 1)
    return (
      <TabItems
        className={styles.fullWidthTabs}
        options={keys.map((k) => sectionBlocks[k].title)}
        tabIndex={index}
        onChange={(i) => setSectionTab((t) => ({ ...t, [g.name]: i }))}
        tabContents={keys.map((k) => frame(k))}
      />
    )
  }

  return (
    <Flex className={styles.settingsContainer}>
      <StandbyBanner />
      {sectionFilter && (
        <Button size='sm' variant='outline' mb={2} onClick={clearFilter}>
          Show all settings
        </Button>
      )}
      {sectionFilter ? (
        sectionsOf(SECTION_ORDER)
      ) : (
        <TabItems
          className={styles.fullWidthTabs}
          options={SETUP_GROUPS.map((g) => g.name)}
          tabIndex={setupTab}
          onChange={changeSetupTab}
          tabContents={SETUP_GROUPS.map((g) => (
            <Flex key={g.name} direction='column' gap='8px' width='100%'>
              {groupOf(g)}
            </Flex>
          ))}
        />
      )}

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
