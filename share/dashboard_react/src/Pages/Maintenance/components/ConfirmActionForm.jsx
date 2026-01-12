import React, { useState } from 'react'
import { Box, HStack, Select, VStack } from '@chakra-ui/react'

const QueueMoveForm = React.memo(({ list = [], currentId, onChange = (dir, afterId) => { } }) => {
  const [direction, setDirection] = useState('first')

  const handleDirectionChange = (e) => {
    setDirection(e.target.value)
    if (e.target.value !== 'after') {
      onChange(e.target.value, null)
    }
  }

  const handleAfterChange = (e) => {
    onChange('after', e.target.value)
  }

  return (
    <VStack>
      <HStack>
        <Box>Move {direction}:</Box>
        <Select onChange={handleDirectionChange} value={direction}>
          <option value="first">First</option>
          <option value="before">After</option>
          <option value="last">Last</option>
        </Select>
      </HStack>
      {direction === 'after' && (
        <HStack>
          <Box>Move After:</Box>
          <Select onChange={handleAfterChange}>
            {list
              .filter((item) => item.task_id !== currentId)
              .map((item) => (
                <option key={item.task_id} value={item.task_id}>
                  Task ID #{item.task_id}
                </option>
              ))}
          </Select>
        </HStack>
      )}
    </VStack>
  )
})

const formConfig = {
  queueMove: {
    component: QueueMoveForm,
    getProps: ({ queueData, payload, onMove }) => ({
      list: queueData,
      currentId: payload?.data?.taskId,
      onChange: onMove
    })
  }
}

function ConfirmActionForm({ payload, queueData, onMove }) {
  if (!payload) return null

  const entry = formConfig[payload.action]
  if (!entry) return null

  const Component = entry.component
  const props = entry.getProps({ payload, queueData, onMove })

  return <Component {...props} />
}

export default ConfirmActionForm
