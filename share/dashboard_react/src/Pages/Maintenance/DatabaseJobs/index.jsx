import { HStack, VStack, Text, Tag } from '@chakra-ui/react'
import React, { useEffect, useMemo, useState } from 'react'
import { useDispatch, useSelector } from 'react-redux'
import AccordionComponent from '../../../components/AccordionComponent'
import { DataTable } from '../../../components/DataTable'
import styles from './styles.module.scss'
import { createColumnHelper } from '@tanstack/react-table'
import TagPill from '../../../components/TagPill'
import { canCancelJob, formatDate } from '../../../utility/common'
import ServerStatus from '../../../components/ServerStatus'
import RMIconButton from '../../../components/RMIconButton'
import { FaTrash } from 'react-icons/fa'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import { cancelServerJob } from '../../../redux/clusterSlice'

function DatabaseJobs({ clusterName }) {
  const {
    cluster: { jobs, clusterServers }
  } = useSelector((state) => state)
  const dispatch = useDispatch()
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [taskToCancel, setTaskToCancel] = useState(null)

  const columnHelper = createColumnHelper()

  const columns = useMemo(
    () => [
      columnHelper.accessor((row) => row.task, {
        header: 'Task'
      }),
      columnHelper.accessor((row) => row.state, {
        header: 'State',
        cell: (info) => getJobState(info.getValue())
      }),
      columnHelper.accessor((row) => row.result, {
        header: 'Desc'
      }),
      columnHelper.accessor((row) => (row.start ? formatDate(new Date(row.start * 1000)) : ''), {
        header: 'Start'
      }),
      columnHelper.accessor((row) => (row.end ? formatDate(new Date(row.end * 1000)) : ''), {
        header: 'End'
      }),
      columnHelper.accessor((row) => row, {
        header: 'Cancel Task',
        cell: (info) =>
          canCancelJob(info.getValue()) ? (
            <RMIconButton
              className={styles.btnCancelTask}
              tooltip={'Cancel task'}
              icon={FaTrash}
              iconFontsize='1rem'
              onClick={() => {
                openConfirmModal(info.getValue())
              }}
            />
          ) : null
      })
    ],
    []
  )

  const openConfirmModal = (taskData) => {
    setIsConfirmModalOpen(true)
    setTaskToCancel(taskData)
  }

  const closeConfirmModal = () => {
    setIsConfirmModalOpen(false)
    setTaskToCancel(null)
  }

  const handleCancelTask = (serverId, taskId) => {
    dispatch(cancelServerJob({ clusterName, serverId, taskId }))
  }

  const getJobState = (state) => {
    switch (state.toString()) {
      case '0':
        return <TagPill text={'Init'} />
      case '1':
        return <TagPill colorScheme='blue' text={'Running'} />
      case '2':
        return <TagPill colorScheme='orange' text={'Halted'} />
      case '3':
        return <TagPill colorScheme='blue' text={'Done'} />
      case '4':
        return <TagPill colorScheme='green' text={'Success'} />
      case '5':
        return <TagPill colorScheme='red' text={'Error'} />
      case '6':
        return <TagPill colorScheme='red' text={'PTError'} />
    }
  }

  return (
    <VStack className={styles.jobsContainer}>
      {jobs?.servers &&
        Object.entries(jobs.servers).map((entry) => {
          const [dbId, jobServer] = entry
          const dbServer = clusterServers.find((server) => server.url === jobServer.serverUrl)
          const serverStatus = dbServer?.state || ''
          const updatedTasks = [...jobServer.tasks].map((task) => {
            const updatedTask = { ...task }
            updatedTask.dbId = dbId
            updatedTask.dbhost = dbServer?.host
            updatedTask.dbport = dbServer?.port
            return updatedTask
          })

          return (
            <AccordionComponent
              className={styles.accordion}
              headerClassName={styles.accordionHeader}
              heading={
                <HStack>
                  <Text> {jobServer.serverUrl}</Text>
                  <ServerStatus state={serverStatus} isVirtualMaster={dbServer?.isVirtualMaster} isBlinking={true} />
                </HStack>
              }
              body={<DataTable data={updatedTasks} columns={columns} className={styles.table} />}
            />
          )
        })}
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={closeConfirmModal}
          title={`Warning! \n\n This action will forcefully cancel the job. Ensure the job is not currently running.\n\n Confirm to proceed with the cancellation of task id ${taskToCancel.id} on server ${taskToCancel.dbhost}:${taskToCancel.dbport} (${taskToCancel.dbId})`}
          onConfirmClick={() => {
            handleCancelTask(taskToCancel.dbId, taskToCancel.id)
            closeConfirmModal()
          }}
        />
      )}
    </VStack>
  )
}

export default DatabaseJobs
