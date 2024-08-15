import React, { useState } from 'react'
import TagPill from '../../../../components/TagPill'
import { Box, Code } from '@chakra-ui/react'
import styles from './styles.module.scss'

function Logs({ logs }) {
  const [isScrollable, setIsScrollable] = useState(true)

  const handleClick = () => {
    //setIsScrollable(true)
  }

  return (
    <Box onClick={handleClick} w='100%' maxH='500px' overflow={isScrollable ? 'auto' : 'hidden'}>
      <table className={styles.table}>
        {logs
          ?.filter((log) => log.timestamp)
          .map((log, index) => {
            const levelColor =
              log.level === 'INFO' ? 'blue' : log.level === 'WARN' ? 'orange' : log.level === 'ERROR' ? 'red' : 'gray'
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
          })}
      </table>
    </Box>
  )
}

export default Logs
