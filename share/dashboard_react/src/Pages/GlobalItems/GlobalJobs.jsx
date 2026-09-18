import { useMemo } from 'react'
import { useSelector } from 'react-redux'
import { Flex, Text } from '@chakra-ui/react'
import { createColumnHelper } from '@tanstack/react-table'
import { DataTable } from '../../components/DataTable'
import TagPill from '../../components/TagPill'
import AccordionComponent from '../../components/AccordionComponent'
import { formatDate } from '../../utility/common'

const jobStateColor = {
  0: 'gray',
  1: 'blue',
  2: 'orange',
  3: 'blue',
  4: 'green',
  5: 'red',
  6: 'red'
}

function JobStateTag({ state, stateLabel }) {
  return <TagPill colorScheme={jobStateColor[state] ?? 'gray'} text={stateLabel ?? 'Unknown'} />
}

const resticTaskType = (rtt) => {
  switch (rtt) {
    case 0:
      return 'init'
    case 1:
      return 'fetch'
    case 2:
      return 'backup'
    case 3:
      return 'purge'
    case 4:
      return 'unlock'
    case 5:
      return 'changepass'
    case 6:
      return 'restore'
    case 7:
      return 'check'
    case 8:
      return 'copy'
    default:
      return 'Unknown'
  }
}

function EmptyState({ text }) {
  return (
    <Text fontSize='sm' opacity={0.7} p='8px'>
      {text}
    </Text>
  )
}

function JobsTable({ jobs, emptyText }) {
  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor('clusterName', { header: 'Cluster' }),
      columnHelper.accessor('serverUrl', { header: 'Server' }),
      columnHelper.accessor('task', { header: 'Task' }),
      columnHelper.accessor('state', {
        header: 'State',
        cell: (info) => <JobStateTag state={info.getValue()} stateLabel={info.row.original.stateLabel} />
      }),
      columnHelper.accessor('result', { header: 'Desc' }),
      columnHelper.accessor((row) => (row.start ? formatDate(new Date(row.start * 1000)) : ''), {
        id: 'start',
        header: 'Start'
      }),
      columnHelper.accessor((row) => (row.end ? formatDate(new Date(row.end * 1000)) : ''), {
        id: 'end',
        header: 'End'
      })
    ],
    []
  )

  if (!jobs || jobs.length === 0) {
    return <EmptyState text={emptyText} />
  }

  return <DataTable data={jobs} columns={columns} />
}

function ResticTasksTable({ tasks }) {
  const columnHelper = createColumnHelper()
  const columns = useMemo(
    () => [
      columnHelper.accessor('clusterName', { header: 'Cluster' }),
      columnHelper.accessor((row) => resticTaskType(row.currentTask?.task_type), {
        id: 'taskType',
        header: 'Task Type'
      }),
      columnHelper.accessor((row) => row.currentTask?.status, { id: 'status', header: 'Status' }),
      columnHelper.accessor((row) => row.currentTask?.phase || '-', { id: 'phase', header: 'Phase' }),
      columnHelper.accessor(
        (row) => (typeof row.currentTask?.percent_done === 'number' ? `${(row.currentTask.percent_done * 100).toFixed(1)}%` : '-'),
        { id: 'percentDone', header: 'Progress' }
      ),
      columnHelper.accessor((row) => (row.currentTask?.started_at ? formatDate(new Date(row.currentTask.started_at)) : ''), {
        id: 'startedAt',
        header: 'Started'
      }),
      columnHelper.accessor((row) => row.currentTask?.error || '', { id: 'error', header: 'Error' })
    ],
    []
  )

  if (!tasks || tasks.length === 0) {
    return <EmptyState text='No active Restic tasks.' />
  }

  return <DataTable data={tasks} columns={columns} />
}

function GlobalJobsBody() {
  const globalJobs = useSelector((state) => state.globalClusters.globalJobs)
  const runningJobs = globalJobs?.runningJobs ?? []
  const recentCompletedJobs = globalJobs?.recentCompletedJobs ?? []
  const resticCurrentTasks = globalJobs?.resticCurrentTasks ?? []

  return (
    <Flex direction='column' gap='12px' p='12px'>
      <AccordionComponent
        heading={
          <Flex align='center' gap='8px'>
            <Text>Active Jobs</Text>
            <TagPill colorScheme='blue' text={`${runningJobs.length}`} />
          </Flex>
        }
        body={<JobsTable jobs={runningJobs} emptyText='No active jobs.' />}
      />
      <AccordionComponent
        heading={
          <Flex align='center' gap='8px'>
            <Text>Recently Done</Text>
            <TagPill colorScheme='gray' text={`${recentCompletedJobs.length}`} />
          </Flex>
        }
        body={<JobsTable jobs={recentCompletedJobs} emptyText='No recently completed jobs.' />}
      />
      <AccordionComponent
        heading={
          <Flex align='center' gap='8px'>
            <Text>Current Restic Tasks</Text>
            <TagPill colorScheme='purple' text={`${resticCurrentTasks.length}`} />
          </Flex>
        }
        body={<ResticTasksTable tasks={resticCurrentTasks} />}
      />
    </Flex>
  )
}

export default GlobalJobsBody
