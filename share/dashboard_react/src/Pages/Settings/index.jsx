import { Button, Flex, Text, useDisclosure } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import styles from './styles.module.scss'
import GeneralSettings from './GeneralSettings'
import ConfirmModal from '../../components/Modals/ConfirmModal'
import MonitoringSettings from './MonitoringSettings'
import AccordionComponent from '../../components/AccordionComponent'
import TabItems from '../../components/TabItems'
import PluginsSettings from './PluginsSettings'
import StandbyBanner from '../../components/StandbyBanner'
import LogsSettings from './LogsSettings'
import RejoinSettings from './RejoinSettings'
import ProxySettings from './ProxySettings'
import GraphSettings from './GraphSettings'
import CloudSettings from './CloudSettings'
import RepFailOverSettings from './RepFailOverSettings'
import RepConfigSettings from './RepConfigSettings'
import AlertSettings from './AlertSettings'
import DynamicConfigSettings from './DynamicConfigSettings'
import CredentialsSettings from './CredentialsSettings'
import AppTemplateRepoSection from './components/AppTemplateRepoSection'
import { setSetting } from '../../redux/settingsSlice'
import { useDispatch } from 'react-redux'

// the Setup tab's sub-tabs (#2268): each a group of the cluster's settings sections, in order. The
// Scheduler, Backup, Archive and S3 Providers settings live in the Maintenance tab.
const SETUP_GROUPS = [
  { name: 'General', sections: ['isGeneralOpen', 'isCredentialsOpen', 'isAlertsOpen', 'isDynamicConfigOpen', 'isCloud18Open', 'isAppTemplateRepoOpen'] },
  { name: 'High Availability', sections: ['isRepFailOverOpen', 'isRejoinOpen', 'isRepConfigOpen', 'isProxiesOpen'] },
  { name: 'Collecting', sections: ['isMonitoringOpen', 'isLogsOpen', 'isGraphsOpen'] },
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
  const changeSetupTab = (index) => {
    setSetupTab(index)
    localStorage.setItem('setupTab', String(index))
  }
  const clearFilter = () => {
    localStorage.removeItem('settingsFilter')
    setSectionFilter(null)
  }

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
  const { isOpen: isDynamicConfigOpen, onToggle: onDynamicConfigToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isDynamicConfigOpen')) || false
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
  const { isOpen: isCredentialsOpen, onToggle: onCredentialsToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isCredentialsOpen')) || false
  })
  const { isOpen: isAppTemplateRepoOpen, onToggle: onAppTemplateRepoToggle } = useDisclosure({
    defaultIsOpen: JSON.parse(localStorage.getItem('isAppTemplateRepoOpen')) || false
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
    localStorage.setItem('isDynamicConfigOpen', JSON.stringify(isDynamicConfigOpen))
  }, [isDynamicConfigOpen])

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
    localStorage.setItem('isAppTemplateRepoOpen', JSON.stringify(isAppTemplateRepoOpen))
  }, [isAppTemplateRepoOpen])
  useEffect(() => {
    localStorage.setItem('isCredentialsOpen', JSON.stringify(isCredentialsOpen))
  }, [isCredentialsOpen])

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
    isGeneralOpen: (
      <AccordionComponent
            heading={'Topology'}
            onToggle={onGeneralToggle}
            isOpen={isGeneralOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<GeneralSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} onTabChange={onTabChange} monitor={monitor} />}
          />
    ),
    isCredentialsOpen: (
      <AccordionComponent
            heading={'Credentials'}
            onToggle={onCredentialsToggle}
            isOpen={isCredentialsOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<CredentialsSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isRepFailOverOpen: (
      <AccordionComponent
            heading={'Failover'}
            onToggle={onRepFailOverToggle}
            isOpen={isRepFailOverOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<RepFailOverSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isRepConfigOpen: (
      <AccordionComponent
            heading={'Replication'}
            onToggle={onRepConfigToggle}
            isOpen={isRepConfigOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<RepConfigSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isMonitoringOpen: (
      <AccordionComponent
            heading={'Monitoring'}
            onToggle={onMonitoringToggle}
            isOpen={isMonitoringOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<MonitoringSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isAlertsOpen: (
      <AccordionComponent
            heading={'Alerts'}
            onToggle={onAlertsToggle}
            isOpen={isAlertsOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<AlertSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isLogsOpen: (
      <AccordionComponent
            heading={'Logs'}
            onToggle={onLogsToggle}
            isOpen={isLogsOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<LogsSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isRejoinOpen: (
      <AccordionComponent
            heading={'Rejoin'}
            onToggle={onRejoinToggle}
            isOpen={isRejoinOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<RejoinSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isDynamicConfigOpen: (
      <AccordionComponent
            heading={'Dynamic Config'}
            onToggle={onDynamicConfigToggle}
            isOpen={isDynamicConfigOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<DynamicConfigSettings selectedCluster={selectedCluster} user={user} />}
          />
    ),
    isProxiesOpen: (
      <AccordionComponent
            heading={'Proxies'}
            onToggle={onProxiesToggle}
            isOpen={isProxiesOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<ProxySettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isGraphsOpen: (
      <AccordionComponent
            heading={'Graphs'}
            onToggle={onGraphsToggle}
            isOpen={isGraphsOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<GraphSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isCloud18Open: (
      <AccordionComponent
            heading={'Cloud18'}
            onToggle={onCloud18Toggle}
            isOpen={isCloud18Open}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={<CloudSettings selectedCluster={selectedCluster} user={user} openConfirmModal={openConfirmModal} />}
          />
    ),
    isAppTemplateRepoOpen: (
      <AccordionComponent
            heading={'App Templates Repo'}
            onToggle={onAppTemplateRepoToggle}
            isOpen={isAppTemplateRepoOpen}
            headerClassName={styles.accordionHeader}
            panelClassName={styles.accordionPanel}
            body={
              <AppTemplateRepoSection
                scope='cluster'
                config={selectedCluster?.config}
                canEdit={
                  user?.grants?.['cluster-settings'] !== false &&
                  user?.grants?.['cluster-settings'] !== undefined &&
                  selectedCluster?.config?.provAppTemplateRepoAllowOverride !== false
                }
                onSet={(setting, value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value }))}
              />
            }
          />
    ),
    // Plugins: a sub-tab of its own
    isPluginsOpen: <PluginsSettings selectedCluster={selectedCluster} user={user} />
  }
  const sectionsOf = (keys) => keys.filter(isVisible).map((k) => <React.Fragment key={k}>{sectionBlocks[k]}</React.Fragment>)

  return (
    <Flex className={styles.settingsContainer}>
      <StandbyBanner />
      {(() => {
        const ts = selectedCluster?.lastConfigSaveToDisk
        const saved = ts && !ts.startsWith('0001-01-01')
        return (
          <Text fontSize='xs' color='gray.500' mb={1}>
            Config last saved to disk: {saved ? new Date(ts).toLocaleString() : 'not yet saved this session'}
          </Text>
        )
      })()}
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
              {sectionsOf(g.sections)}
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
