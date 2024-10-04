import React, { useState, useEffect } from 'react'
import TagPill from '../../../../components/TagPill'
import { Box, Code } from '@chakra-ui/react'
import styles from './styles.module.scss'
import NotFound from '../../../../components/NotFound'

function Logs({ logs, className }) {
  const [isScrollable, setIsScrollable] = useState(true)
  const [logsData, setLogsData] = useState([])

  useEffect(() => {
    if (logs?.length > 0) {
      const nonEmptyLogs = logs.filter((log) => log.timestamp)
      setLogsData(nonEmptyLogs)
    }
  }, [logs])

  const handleClick = () => {
    //setIsScrollable(true)
  }

  return (
    <Box
      className={`${styles.logContainer} ${className}`}
      onClick={handleClick}
      overflow={isScrollable ? 'auto' : 'hidden'}>
      <table className={styles.table}>
        {logsData?.length > 0 ? (
          logsData.map((log, index) => {
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
          <NotFound text={'No logs found'} className={styles.notfound} />
        )}
      </table>
    </Box>
  )
}

export default Logs
