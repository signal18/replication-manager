import { useState, useMemo } from 'react'
import TagPill from '../../../../components/TagPill'
import { Box, Code, Flex, HStack, Input, VStack } from '@chakra-ui/react'
import styles from './styles.module.scss'
import NotFound from '../../../../components/NotFound'
import { useSelector } from 'react-redux'

function Logs({ logs, className, searchable = false, isScrollable = true }) {
  const [search, setSearch] = useState('')

  const logsData = useMemo(
    () => logs?.length > 0 ? logs.filter((log) => log.timestamp) : [],
    [logs]
  )

  const data = useMemo(
    () => logsData.filter((x) => x.text.toLowerCase().includes(search.toLowerCase())),
    [logsData, search]
  )

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  return (
    <Box
      className={`${styles.logContainer} ${className}`}
      overflow={isScrollable ? 'auto' : 'hidden'}>
      <VStack spacing={4} >
        {searchable && (
          <Flex direction={'row'} w={'100%'} p={4} >
            <HStack gap='4'>
              <HStack className={styles.search}>
                <label htmlFor='logSearch'>Search</label>
                <Input id='logSearch' type='search' onChange={handleSearch} />
              </HStack>
            </HStack>
          </Flex>
        )}
        <table className={styles.table}>
          <tbody>
            {data?.length > 0 ? (
              data.map((log, index) => {
                const levelColor =
                  log.level === 'INFO' || log.level.toLowerCase() === 'note'
                    ? 'blue'
                    : log.level.toLowerCase().startsWith('warn')
                      ? 'orange'
                      : log.level === 'ERROR'
                        ? 'red'
                        : 'gray'
                return (
                  <tr key={index} className={styles.tr}>
                    <td className={`${styles.td} ${styles.timestamp}`}>
                      <Code bg='transparent'>{log.timestamp}</Code>{' '}
                    </td>
                    <td className={styles.td}>
                      <TagPill text={log.level} colorScheme={levelColor} />
                    </td>
                    <td className={`${styles.td} ${styles.text}`}>
                      <Code bg='transparent'>{log.text.replace(/,(?!\s)/g, ', ')}</Code>
                    </td>
                  </tr>
                )
              })
            ) : (
              <tr>
                <td>
                  <NotFound text={'No logs found'} className={styles.notfound} />
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </VStack>
    </Box>
  )
}

export const GeneralLogs = ({ className }) => {
  const logs = useSelector((state) => state.cluster.clusterLogs.general)
  return <Logs key={"general"} logs={logs?.buffer} className={className} />
}

export const TaskLogs = ({ className }) => {
  const taskLogs = useSelector((state) => state.cluster.clusterLogs.task)
  return <Logs key={"task"} logs={taskLogs?.buffer} className={className} />
}

export const SecurityLogs = ({ className }) => {
  const securityLogs = useSelector((state) => state.cluster.clusterLogs.security)
  return <Logs key={"security"} logs={securityLogs?.buffer} className={className} />
}

export const WorkloadLogs = ({ className }) => {
  const workloadLogs = useSelector((state) => state.cluster.clusterLogs.workload)
  return <Logs key={"workload"} logs={workloadLogs?.buffer} className={className} />
}

export const DDLLogs = ({ className }) => {
  const ddlLogs = useSelector((state) => state.cluster.clusterLogs.ddl)
  return <Logs key={"ddl"} logs={ddlLogs?.buffer} className={className} searchable={true} />
}

export const VariableChangeLogs = ({ className }) => {
  const varChangeLogs = useSelector((state) => state.cluster.clusterLogs['variable-change'])
  return <Logs key={"variable-change"} logs={varChangeLogs?.buffer} className={className} searchable={true} />
}

export default Logs
