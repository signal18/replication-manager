import React from 'react'
import TagPill from '../../../components/TagPill'

function Logs({ logs }) {
  const styles = {
    table: {
      width: '100%',
      borderCollapse: 'collapse',
      maxHeight: '300px',
      overFlowY: 'auto'
    },
    td: {
      border: '1px solid black',
      padding: '8px',
      textAlign: 'left'
    },
    levels: {
      info: { color: 'blue' },
      state: { color: 'orange' },
      warn: { color: 'red' },
      start: { color: 'green' }
    }
  }

  return (
    <table style={styles.table}>
      {logs?.map((log) => {
        if (!log.timestamp) {
          return null
        }
        const levelColor =
          log.level === 'INFO' ? 'blue' : log.level === 'WARN' ? 'orange' : log.level === 'ERROR' ? 'red' : 'gray'
        return (
          <tr>
            <td style={styles.timestamp}>{log.timestamp}</td>
            <TagPill text={log.level} colorScheme={levelColor} />
            <td style={styles.text}>{log.text}</td>
          </tr>
        )
      })}
    </table>
  )
}

export default Logs
