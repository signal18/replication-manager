import React, { useState } from 'react'
import TagPill from '../../../components/TagPill'
import { Box, Code, useColorMode } from '@chakra-ui/react'

function Logs({ logs }) {
  const { colorMode } = useColorMode()
  const [isScrollable, setIsScrollable] = useState(true)
  const styles = {
    table: {
      width: '100%',
      borderCollapse: 'collapse'
    },
    tr: {
      borderBottom: '1px solid',
      borderColor: colorMode === 'light' ? '#E9E9E9' : ''
    },
    td: {
      paddingTop: '3px',
      paddingBottom: '3px'
    },
    timestamp: {
      width: '200px'
    }
  }

  const handleClick = () => {
    //setIsScrollable(true)
  }

  return (
    <Box onClick={handleClick} w='100%' maxH='500px' overflow={isScrollable ? 'auto' : 'hidden'}>
      <table style={styles.table}>
        {logs
          ?.filter((log) => log.timestamp)
          .map((log, index) => {
            const levelColor =
              log.level === 'INFO' ? 'blue' : log.level === 'WARN' ? 'orange' : log.level === 'ERROR' ? 'red' : 'gray'
            return (
              <tr key={index} style={styles.tr}>
                <td style={{ ...styles.td, ...styles.timestamp }}>
                  <Code bg='transparent'>{log.timestamp}</Code>{' '}
                </td>
                <td style={styles.td}>
                  <TagPill text={log.level} colorScheme={levelColor} />
                </td>
                <td style={{ ...styles.td, ...styles.text }}>
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
