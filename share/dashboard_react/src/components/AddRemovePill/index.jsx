import React from 'react'
import RMButton from '../RMButton'
import styles from './styles.module.scss'
import CustomIcon from '../Icons/CustomIcon'
import { HiMinus, HiPlus } from 'react-icons/hi'
import { HStack, Text } from '@chakra-ui/react'

function AddRemovePill({ text, used = false, onAdd, onRemove, category }) {
  return (
    <RMButton
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
  )
}

export default AddRemovePill
