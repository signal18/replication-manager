import { Flex, HStack } from '@chakra-ui/react'
import React, { useState } from 'react'
import styles from './styles.module.scss'
import RMIconButton from '../../../../components/RMIconButton'
import { HiPlay, HiStop, HiTable } from 'react-icons/hi'
import { FaFile } from 'react-icons/fa'
import RMButton from '../../../../components/RMButton'
import { isEqualLongQueryTime } from '../../../../utility/common'
import { useDispatch } from 'react-redux'
import { toggleDatabaseActions, updateLongQueryTime } from '../../../../redux/clusterSlice'
import ConfirmModal from '../../../../components/Modals/ConfirmModal'

function Toolbar({ tab, dbId, selectedDBServer, clusterName }) {
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [value, setValue] = useState('')
  const [actionType, setActionType] = useState('')
  const handleAction = () => {
    if (actionType === 'longquery') {
      dispatch(updateLongQueryTime({ clusterName, dbId, time: value }))
    } else if (actionType === 'toggleCapture') {
      dispatch(toggleDatabaseActions({ clusterName, dbId, serviceName: 'toogle-slow-query' }))
    } else if (actionType === 'toggleMode') {
      dispatch(toggleDatabaseActions({ clusterName, dbId, serviceName: 'toogle-slow-query-table' }))
    } else if (actionType === 'togglePFSCapture') {
      dispatch(toggleDatabaseActions({ clusterName, dbId, serviceName: 'toogle-pfs-slow-query' }))
    } else if (actionType === 'toggleMetadataLock') {
      dispatch(toggleDatabaseActions({ clusterName, dbId, serviceName: 'toogle-meta-data-locks' }))
    } else if (actionType === 'toggleRespTime') {
      dispatch(toggleDatabaseActions({ clusterName, dbId, serviceName: 'toogle-query-response-time' }))
    }
  }

  const openConfirmModal = (type, time) => {
    const serverName = `${selectedDBServer.host}:${selectedDBServer.port} (${dbId})`
    if (type === 'longquery') {
      setConfirmTitle(`Confirm change Long Query Time on server: ${serverName} to `)
      setValue(time)
    } else if (type === 'toggleCapture') {
      setConfirmTitle(`Confirm toggle slow query log capture on server: ${serverName}`)
    } else if (type === 'toggleMode') {
      setConfirmTitle(`Confirm toggle slow query mode between TABLE and FILE on server: ${serverName}`)
    } else if (type === 'togglePFSCapture') {
      setConfirmTitle(`Confirm toggle slow query log PFS capture on server: ${serverName}`)
    } else if (type === 'toggleMetadataLock') {
      setConfirmTitle(`Confirm toggle metadata lock plugin on server: ${serverName}`)
    } else if (type === 'toggleRespTime') {
      setConfirmTitle(`Confirm toggle query response time plugin on server: ${serverName}`)
    }
    setActionType(type)
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
  }
  return (
    <Flex className={styles.toolbarContainer}>
      <HStack className={styles.actions}>
        {tab === 'slowQueries' && (
          <RMIconButton
            onClick={() => openConfirmModal('toggleCapture')}
            tooltip='Toggle slow query capture'
            icon={selectedDBServer?.slowQueryLog === 'ON' ? HiStop : HiPlay}
          />
        )}
        {tab === 'digestQueries' && (
          <RMIconButton
            onClick={() => openConfirmModal('togglePFSCapture')}
            tooltip='Toggle slow query PFS capture'
            icon={selectedDBServer?.havePFSSlowQueryLog ? HiStop : HiPlay}
          />
        )}
        {tab === 'metadataLocks' && (
          <RMIconButton
            onClick={() => openConfirmModal('toggleMetadataLock')}
            icon={selectedDBServer?.haveMetaDataLocksLog ? HiStop : HiPlay}
          />
        )}
        {tab === 'responseTime' && (
          <RMIconButton
            onClick={() => openConfirmModal('toggleRespTime')}
            icon={selectedDBServer?.haveQueryResponseTimeLog ? HiStop : HiPlay}
          />
        )}
        {tab === 'slowQueries' && (
          <RMIconButton
            onClick={() => openConfirmModal('toggleMode')}
            tooltip='Toggle slow query mode between TABLE and FILE'
            icon={selectedDBServer?.logOutput === 'TABLE' ? HiTable : FaFile}
          />
        )}
      </HStack>

      <HStack className={styles.querytimes}>
        {(tab === 'slowQueries' || tab === 'digestQueries') && (
          <>
            {/* {isEqualLongQueryTime(selectedDBServer?.longQueryTime, '10')} */}
            <RMButton
              onClick={() => openConfirmModal('longquery', 10)}
              {...(isEqualLongQueryTime(selectedDBServer?.longQueryTime, '10')
                ? { className: styles.selectedtime }
                : {})}>
              10<sup>1</sup>
            </RMButton>
            <RMButton
              onClick={() => openConfirmModal('longquery', 1)}
              {...(isEqualLongQueryTime(selectedDBServer?.longQueryTime, '1')
                ? { className: styles.selectedtime }
                : {})}>
              10<sup>-1</sup>
            </RMButton>
            <RMButton
              onClick={() => openConfirmModal('longquery', 0.1)}
              {...(isEqualLongQueryTime(selectedDBServer?.longQueryTime, '0.1')
                ? { className: styles.selectedtime }
                : {})}>
              10<sup>-2</sup>
            </RMButton>
            <RMButton
              onClick={() => openConfirmModal('longquery', 0.01)}
              {...(isEqualLongQueryTime(selectedDBServer?.longQueryTime, '0.01')
                ? { className: styles.selectedtime }
                : {})}>
              10<sup>-3</sup>
            </RMButton>
            <RMButton
              onClick={() => openConfirmModal('longquery', 0.001)}
              {...(isEqualLongQueryTime(selectedDBServer?.longQueryTime, '0.001')
                ? { className: styles.selectedtime }
                : {})}>
              10<sup>-4</sup>
            </RMButton>
            <RMButton
              onClick={() => openConfirmModal('longquery', 0.00001)}
              {...(isEqualLongQueryTime(selectedDBServer?.longQueryTime, '0.00001')
                ? { className: styles.selectedtime }
                : {})}>
              10<sup>-5</sup>
            </RMButton>
          </>
        )}
      </HStack>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={`${confirmTitle} ${value}`}
          onConfirmClick={() => {
            handleAction()
            closeConfirmModal()
          }}
        />
      )}
    </Flex>
  )
}

export default Toolbar
