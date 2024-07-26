import React from 'react'
import styles from './styles.module.scss'
import { Accordion, AccordionButton, AccordionIcon, AccordionItem, AccordionPanel, Box } from '@chakra-ui/react'

function AccordionComponent({ heading, body, sx, panelSX, headerSX, isOpen = null, onToggle }) {
  return (
    <Accordion allowToggle={true} sx={sx} defaultIndex={0} {...(isOpen !== null ? { index: isOpen ? [0] : [] } : {})}>
      <AccordionItem className={styles.accordionItem}>
        <h2>
          <AccordionButton
            className={`${styles.button} ${headerSX ? '' : styles.baseColor}`}
            sx={headerSX}
            {...(onToggle ? { onClick: onToggle } : {})}>
            <Box as='span' flex='1' textAlign='left'>
              {heading}
            </Box>
            <AccordionIcon className={styles.icon} />
          </AccordionButton>
        </h2>
        <AccordionPanel className={styles.panel} sx={panelSX}>
          {body}
        </AccordionPanel>
      </AccordionItem>
    </Accordion>
  )
}

export default AccordionComponent
