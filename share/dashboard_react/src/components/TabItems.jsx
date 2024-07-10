import React from 'react'
import { Tabs, TabList, Tab, TabPanels, TabPanel } from '@chakra-ui/react'

function TabItems({ variant = 'enclosed', options, tabContents }) {
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
      borderTopLeftRadius: '32px',
      borderTopRightRadius: '32px'
    }
  }
  return (
    <Tabs variant={variant} size='lg'>
      <TabList sx={styles.tabList}>
        {options.map((option) => (
          <Tab sx={styles.tab}>{option}</Tab>
        ))}
      </TabList>
      <TabPanels>
        {tabContents.map((content) => (
          <TabPanel p='0'>{content}</TabPanel>
        ))}
      </TabPanels>
    </Tabs>
  )
}

export default TabItems
