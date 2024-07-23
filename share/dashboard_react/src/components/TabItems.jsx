import React from 'react'
import { Tabs, TabList, Tab, TabPanels, TabPanel } from '@chakra-ui/react'

function TabItems({ variant = 'enclosed', options, tabContents, tabIndex, onChange }) {
  const styles = {
    tabList: {
      overflowX: 'auto',
      overflowY: 'hidden',
      maxWidth: '100%',
      '::-webkit-scrollbar': {
        display: 'none'
      }
    },
    tab: {
      p: '8px 24px',
      borderTopLeftRadius: '16px',
      borderTopRightRadius: '16px'
    }
  }
  return (
    <Tabs variant={variant} size='lg' index={tabIndex} onChange={onChange}>
      <TabList sx={styles.tabList}>
        {options.map((option, index) => (
          <Tab key={index} sx={styles.tab}>
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
