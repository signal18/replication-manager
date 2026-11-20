import { useState } from 'react'
import PropTypes from 'prop-types'
import { HStack, Tooltip, Box } from '@chakra-ui/react'
import { HiCog } from 'react-icons/hi'
import AccordionComponent from '../AccordionComponent/index.jsx'
import TabItems from '../TabItems'
import { CountPill } from '../AlertBadge'
import RMIconButton from '../RMIconButton'
import styles from './styles.module.scss'

// LogsTabs shows several logs as the tabs of one section: the cluster's logs
// (Pages/Dashboard/components/ClusterLogsTabs) and the instance's (Pages/GlobalItems).
// See doc/implementation/ui-components/CLUSTER_LOGS_TABS.md.
//   tabs:        [{ key, title, description, Component, pill(pillContext), settings?, settingsTooltip? }]
//                key: the log's key in `logs`; Component: the existing view of that log;
//                pill: the { colorScheme, shade, blink } of its count badge (utility/domainPills);
//                settings: the openSettings entry its gear opens
//   logs:        { [key]: { buffer } }, the logs as the API returns them
//   storageKey:  the localStorage key that remembers the selected tab
// TabItems mounts only the open tab.

// the entries a log buffer really holds: a server ring is pre-filled with empty slots, so
// count the entries with a timestamp, the rule the Logs component uses to skip them
function countEntries(log) {
  return (log?.buffer || []).filter((entry) => entry?.timestamp).length
}

function readStoredTab(tabs, storageKey) {
  try {
    const index = tabs.findIndex((tab) => tab.key === localStorage.getItem(storageKey))
    return index >= 0 ? index : 0
  } catch {
    return 0
  }
}

// a tab label: the log's name, the count badge when it holds entries, and a "?" tooltip
// explaining its content. Divs, not spans: the global stylesheet forces every span to
// --text-color, which would not follow the selected tab's colour.
function LogTabLabel({ title, description, count, pill }) {
  return (
    <HStack spacing={2}>
      <Box>{title}</Box>
      {count > 0 && (
        <Tooltip label='Entries in the live buffer (not counting older history or filters)' placement='top' hasArrow>
          <Box>
            <CountPill count={count} colorScheme={pill.colorScheme} shade={pill.shade} blink={pill.blink} />
          </Box>
        </Tooltip>
      )}
      <Tooltip label={description} placement='top' hasArrow>
        <Box className={styles.help}>?</Box>
      </Tooltip>
    </HStack>
  )
}

function LogsTabs({ tabs, logs, pillContext = {}, storageKey, heading = 'Logs', openSettings = {}, className }) {
  const [selectedTab, setSelectedTabState] = useState(() => readStoredTab(tabs, storageKey))
  const setSelectedTab = (index) => {
    setSelectedTabState(index)
    try {
      localStorage.setItem(storageKey, tabs[index].key)
    } catch {
      // the tab is only not remembered
    }
  }

  const current = tabs[selectedTab] || tabs[0]
  const onOpenSettings = current.settings ? openSettings[current.settings] : undefined

  return (
    <AccordionComponent
      className={className}
      heading={heading}
      headerActions={
        onOpenSettings ? (
          <RMIconButton icon={HiCog} tooltip={current.settingsTooltip} onClick={onOpenSettings} size='xs' variant='ghost' />
        ) : null
      }
      body={
        <TabItems
          className={styles.fullWidthTabs}
          options={tabs.map((tab) => (
            <LogTabLabel
              key={tab.key}
              title={tab.title}
              description={tab.description}
              count={countEntries(logs?.[tab.key])}
              pill={tab.pill(pillContext)}
            />
          ))}
          tabIndex={selectedTab}
          onChange={setSelectedTab}
          tabContents={tabs.map(({ key, Component }) => (
            <Component key={key} />
          ))}
        />
      }
    />
  )
}

LogTabLabel.propTypes = {
  title: PropTypes.string.isRequired,
  description: PropTypes.string,
  count: PropTypes.number,
  pill: PropTypes.shape({ colorScheme: PropTypes.string, shade: PropTypes.number, blink: PropTypes.bool })
}

LogsTabs.propTypes = {
  tabs: PropTypes.arrayOf(
    PropTypes.shape({
      key: PropTypes.string.isRequired,
      title: PropTypes.string.isRequired,
      description: PropTypes.string,
      Component: PropTypes.elementType.isRequired,
      pill: PropTypes.func.isRequired,
      settings: PropTypes.string,
      settingsTooltip: PropTypes.string
    })
  ).isRequired,
  logs: PropTypes.object,
  pillContext: PropTypes.object,
  storageKey: PropTypes.string.isRequired,
  heading: PropTypes.string,
  openSettings: PropTypes.object,
  className: PropTypes.string
}

export default LogsTabs
