import { Flex, HStack } from '@chakra-ui/react'
import React, { useEffect, useState } from 'react'
import styles from './styles.module.scss'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import NumberInput from '../../components/NumberInput'
import { HiCheck, HiPencilAlt, HiX } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'
import { setSetting } from '../../redux/settingsSlice'

function GlobalSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [isReadOnly, setIsReadOnly] = useState(true)
  const [currentValue, setCurrentValue] = useState(0)
  const [previousValue, setPreviousValue] = useState(0)

  useEffect(() => {
    if (selectedCluster?.config?.apiTokenTimeout) {
      setCurrentValue(selectedCluster.config.apiTokenTimeout)
      setPreviousValue(selectedCluster.config.apiTokenTimeout)
    }
  }, [selectedCluster?.config?.apiTokenTimeout])

  const handleChange = (valueAsString, valueAsNumber) => {
    if (valueAsString) {
      setCurrentValue(valueAsNumber)
    } else {
      setCurrentValue(0)
    }
  }
  const dataObject = [
    {
      key: 'API Token Timeout in Hours',
      value: (
        <HStack>
          <NumberInput
            value={currentValue}
            readonly={
              isReadOnly ||
              user?.grants['cluster-settings'] == false ||
              parseInt(localStorage.getItem('refresh_interval')) <= 1
            }
            onChange={handleChange}
          />
          {isReadOnly ? (
            <RMIconButton
              icon={HiPencilAlt}
              tooltip='Edit'
              onClick={() => {
                setIsReadOnly(!isReadOnly)
              }}
            />
          ) : (
            <>
              <RMIconButton
                icon={HiX}
                tooltip='Cancel'
                colorScheme='red'
                onClick={() => {
                  setIsReadOnly(true)
                  setCurrentValue(previousValue)
                }}
              />
              <RMIconButton
                icon={HiCheck}
                colorScheme='green'
                tooltip='Save'
                onClick={() => {
                  setIsReadOnly(true)
                  openConfirmModal(`Confirm change 'api-token-timeout' to: ${currentValue} `, () => () => {
                    dispatch(
                      setSetting({
                        clusterName: selectedCluster?.name,
                        setting: 'api-token-timeout',
                        value: currentValue
                      })
                    )
                  })
                }}
              />
            </>
          )}
        </HStack>
      )
    }
  ]
  return (
    <Flex justify='space-between' gap='0'>
      <TableType2
        dataArray={dataObject}
        className={styles.table}
        labelClassName={styles.label}
        valueClassName={styles.value}
        rowDivider={true}
        rowClassName={styles.row}
      />
    </Flex>
  )
}

export default GlobalSettings
