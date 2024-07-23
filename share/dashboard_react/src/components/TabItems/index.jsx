import React from 'react'
import { Tabs, TabList, Tab, TabPanels, TabPanel } from '@chakra-ui/react'
import styles from './styles.module.scss'

function TabItems({ variant = 'enclosed', options, tabContents, tabIndex, onChange }) {
  return (
    <Tabs variant={variant} size='lg' index={tabIndex} onChange={onChange}>
      <TabList className={styles.tabList}>
        {options.map((option, index) => (
          <Tab key={index} className={styles.tab}>
            {option}
          </Tab>
        ))}
      </TabList>
      <TabPanels>
        {tabContents.map((content, index) => (
          <TabPanel key={index} px='0' py='2'>
            {content}
          </TabPanel>
        ))}
      </TabPanels>
    </Tabs>
  )
}

export default TabItems