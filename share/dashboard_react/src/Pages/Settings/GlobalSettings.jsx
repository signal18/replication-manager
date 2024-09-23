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

  const dataObject = [
    {
      key: 'API Token Timeout in Hours',
      value: (
        <NumberInput
          min={1}
          value={selectedCluster.config.apiTokenTimeout}
          isDisabled={user?.grants['cluster-settings'] == false}
          showEditButton={true}
          onConfirm={(value) =>
            openConfirmModal(`Confirm change 'api-token-timeout' to: ${value} `, () => () => {
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'api-token-timeout',
                  value: value
                })
              )
            })
          }
        />
      )
    }
  ]
  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default GlobalSettings
