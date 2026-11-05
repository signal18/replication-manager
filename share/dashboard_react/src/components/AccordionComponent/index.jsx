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
  allowToggle = true,
  onToggle,
  headerActions,
  headerControls
}) {
  return (
    <Accordion
      className={className}
      allowToggle={allowToggle}
      sx={sx}
      defaultIndex={0}
      {...(isOpen !== null ? { index: isOpen ? [0] : [] } : {})}>
      <AccordionItem className={styles.accordionItem}>
        <h2 style={headerControls ? { position: 'relative' } : undefined}>
          <AccordionButton
            className={`${styles.button} ${styles.baseColor} ${headerClassName}`}
            {...(onToggle ? { onClick: onToggle } : {})}>
            <Box as='h4' flex='1' textAlign='left'>
              {heading}
            </Box>
            {headerActions && (
              <Box mr={2} onClick={(e) => e.stopPropagation()}>
                {headerActions}
              </Box>
            )}
            {headerControls && <Box className={styles.headerControlsSpace} />}
            {allowToggle && <AccordionIcon className={styles.icon} />}
          </AccordionButton>
          {/* form controls (a select, an input) cannot live inside the header's button: they sit
              over its right side, outside it, so they work and the rest of the header toggles */}
          {headerControls && <Box className={styles.headerControls}>{headerControls}</Box>}
        </h2>
        <AccordionPanel className={`${styles.panel} ${panelClassName}`}>{body}</AccordionPanel>
      </AccordionItem>
    </Accordion>
  )
}

export default AccordionComponent
