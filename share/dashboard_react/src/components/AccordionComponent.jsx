import React from 'react'
import { Accordion, AccordionButton, AccordionIcon, AccordionItem, AccordionPanel, Box } from '@chakra-ui/react'

function AccordionComponent({ heading, body, sx, panelSX, headerSX, isOpen = null, onToggle }) {
  const styles = {
    header: {
      paddingTop: '6px',
      paddingBottom: '6px'
    },
    panel: {
      padding: 0
    }
  }
  return (
    <Accordion allowToggle={true} sx={sx} defaultIndex={0} {...(isOpen !== null ? { index: isOpen ? [0] : [] } : {})}>
      <AccordionItem>
        <h2>
          <AccordionButton sx={{ ...styles.header, ...headerSX }} {...(onToggle ? { onClick: onToggle } : {})}>
            <Box as='span' flex='1' textAlign='left'>
              {heading}
            </Box>
            <AccordionIcon />
          </AccordionButton>
        </h2>
        <AccordionPanel sx={{ ...styles.panel, ...panelSX }}>{body}</AccordionPanel>
      </AccordionItem>
    </Accordion>
  )
}

export default AccordionComponent
