import React from 'react'
import RMButton from '../RMButton'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'
import { HiMinus, HiPlus } from 'react-icons/hi'
import { FiEye } from 'react-icons/fi'
import { HStack, Text, IconButton, Tooltip } from '@chakra-ui/react'

function AddRemovePill({ text, used = false, onAdd, onRemove, onViewContent, category, isDisabled = false }) {
  return (
    <HStack spacing='1'>
      <RMButton
        isDisabled={isDisabled}
        className={`${styles.addRemovePill} ${used ? styles.used : styles.unused}`}
        onClick={used ? () => onRemove(`Confirm drop tag ${text}?`) : () => onAdd(`Confirm add tag ${text}?`)}>
        {category && <Text className={styles.category}>{category}</Text>}

        <HStack className={styles.tagData}>
          <span className={`${used ? styles.usedConfigText : styles.unusedConfigText}`}>{text}</span>

          {used ? (
            <CustomIcon className={styles.usedIcon} icon={HiMinus} fontSize='1rem' fill={'red'} />
          ) : (
            <CustomIcon className={styles.unusedIcon} icon={HiPlus} fontSize='1rem' fill={'green'} />
          )}
        </HStack>
      </RMButton>
      {onViewContent && (
        <Tooltip label='View tag content' placement='top' hasArrow>
          <IconButton
            aria-label='View tag content'
            icon={<FiEye />}
            size='xs'
            variant='ghost'
            onClick={(e) => { e.stopPropagation(); onViewContent(text) }}
          />
        </Tooltip>
      )}
    </HStack>
  )
}

export default AddRemovePill
