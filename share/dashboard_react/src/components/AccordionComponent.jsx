import React from 'react'
import { Accordion, AccordionButton, AccordionIcon, AccordionItem, AccordionPanel, Box } from '@chakra-ui/react'

function AccordionComponent({ heading, body, panelSX }) {
  return (
    <Accordion allowToggle={true} defaultIndex={0}>
      <AccordionItem>
        <h2>
          <AccordionButton>
            <Box as='span' flex='1' textAlign='left'>
              {heading}
            </Box>
            <AccordionIcon />
          </AccordionButton>
        </h2>
        <AccordionPanel sx={panelSX} pb={4}>
          {body}
        </AccordionPanel>
      </AccordionItem>
    </Accordion>
  )
}

export default AccordionComponent
