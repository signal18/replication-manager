import React from 'react'
import { Tabs, TabList, Tab, TabPanels, TabPanel } from '@chakra-ui/react'
import styles from './styles.module.scss'

function TabItems({ variant = 'enclosed', options, tabContents, tabIndex, onChange, className, tabPrefix = [], tabSuffix = [], blinkLabels = [] }) {
  return (
    <Tabs variant={variant} className={className} size='lg' index={tabIndex} onChange={onChange}>
      <TabList className={styles.tabList}>
        {...tabPrefix}
        {options.map((option, index) => {
          // A string option whose label is in blinkLabels gets the red blink (e.g. the
          // Settings tab on a Standby node, to flag that changes there won't persist).
          const shouldBlink = typeof option === 'string' && blinkLabels.includes(option)
          return (
            <Tab key={index} className={`${styles.tab} ${shouldBlink ? styles.blink : ''}`}>
              {option}
            </Tab>
          )
        })}
        {...tabSuffix}
      </TabList>
      <TabPanels>
        {tabContents.map((content, index) => (
          <TabPanel key={index} px='0' py='2' className={styles.tabPanel}>
            {index === tabIndex && tabContents[index]}
          </TabPanel>
        ))}
      </TabPanels>
    </Tabs>
  )
}

export default TabItems
