import React from 'react'
import styles from './styles.module.scss'
import { Accordion, AccordionButton, AccordionIcon, AccordionItem, AccordionPanel, Box } from '@chakra-ui/react'

function AccordionComponent({
  heading,
  body,
  sx,
  className,
  headerClassName,
  panelClassName,
  isOpen = null,
  onToggle
}) {
  return (
    <Accordion
      className={className}
      allowToggle={true}
      sx={sx}
      defaultIndex={0}
      {...(isOpen !== null ? { index: isOpen ? [0] : [] } : {})}>
      <AccordionItem className={styles.accordionItem}>
        <h2>
          <AccordionButton
            className={`${styles.button} ${styles.baseColor} ${headerClassName}`}
            {...(onToggle ? { onClick: onToggle } : {})}>
            <Box as='h4' flex='1' textAlign='left'>
              {heading}
            </Box>
            <AccordionIcon className={styles.icon} />
          </AccordionButton>
        </h2>
        <AccordionPanel className={`${styles.panel} ${panelClassName}`}>{body}</AccordionPanel>
      </AccordionItem>
    </Accordion>
  )
}

export default AccordionComponent
