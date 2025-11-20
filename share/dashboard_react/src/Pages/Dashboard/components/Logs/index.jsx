import React, { useState, useEffect, useRef } from 'react'
import TagPill from '../../../../components/TagPill'
import { Box, Code, Flex, HStack, Input, VStack } from '@chakra-ui/react'
import styles from './styles.module.scss'
import NotFound from '../../../../components/NotFound'
import { useSelector } from 'react-redux'
import { isEqual } from 'lodash'

function Logs({ logs, className, searchable = false, isScrollable = true }) {
  const [logsData, setLogsData] = useState([])
  const [data, setData] = useState([])
  const [search, setSearch] = useState('')
  const prevLogs = useRef(logs)

  const searchData = (serverData) => {
    const searchedData = serverData.filter((x) => {
      const searchValue = search.toLowerCase()
      if (x.text.toLowerCase().includes(searchValue)) {
        return x
      }
    })
    return searchedData
  }

  useEffect(() => {
    if (logs?.length > 0 && !isEqual(logs, prevLogs.current)) {
      prevLogs.current = logs
      const nonEmptyLogs = logs.filter((log) => log.timestamp)
      setLogsData(nonEmptyLogs)
      setData(searchData(nonEmptyLogs))
    }
  }, [logs])

  const handleClick = () => {
    //setIsScrollable(true)
  }

  const handleSearch = (e) => {
    setSearch(e.target.value)
  }

  useEffect(() => {
    setData(searchData(logsData))
  }, [search])

  return (
    <Box
      className={`${styles.logContainer} ${className}`}
      onClick={handleClick}
      overflow={isScrollable ? 'auto' : 'hidden'}>
      <VStack spacing={4} >
        {searchable && (
          <Flex direction={'row'} w={'100%'} p={4} >
            <HStack gap='4'>
              <HStack className={styles.search}>
                <label htmlFor='search'>Search</label>
                <Input id='search' type='search' onChange={handleSearch} />
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
                      <Code bg='transparent'>{log.text}</Code>
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
  return <Logs logs={logs?.buffer} className={className} />
}

export const TaskLogs = ({ className }) => {
  const taskLogs = useSelector((state) => state.cluster.clusterLogs.task)
  return <Logs logs={taskLogs?.buffer} className={className} />
}

export default Logs
