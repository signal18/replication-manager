import React from 'react'
import { Accordion, AccordionButton, AccordionIcon, AccordionItem, AccordionPanel, Box } from '@chakra-ui/react'

function AccordionComponent({ heading, body, panelSX, headerSX }) {
  const styles = {
    panel: {
      padding: 0
    }
  }
  return (
    <Accordion allowToggle={true} defaultIndex={0}>
      <AccordionItem>
        <h2>
          <AccordionButton sx={headerSX}>
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
