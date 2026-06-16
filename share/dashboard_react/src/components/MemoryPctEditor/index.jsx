import React, { useMemo, useState } from 'react'
import {
  Box, Flex, Text, NumberInput, NumberInputField, NumberInputStepper,
  NumberIncrementStepper, NumberDecrementStepper, Tooltip
} from '@chakra-ui/react'

const COLORS = [
  '#6366f1', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444',
  '#8b5cf6', '#ec4899', '#14b8a6', '#f97316', '#64748b',
  '#a855f7', '#06b6d4'
]

function MemoryPctEditor({ label, value, isDisabled, onSave }) {
  const entries = useMemo(() => {
    if (!value) return []
    return value.split(',').map((part, i) => {
      const [key, val] = part.split(':')
      return { key, pct: parseInt(val, 10) || 0, color: COLORS[i % COLORS.length] }
    })
  }, [value])

  const [draft, setDraft] = useState(null)
  const items = draft || entries
  const total = items.reduce((s, e) => s + e.pct, 0)

  const MIN_PCT_KEYS = ['aria', 'myisam']
  const MIN_PCT = 10

  const minViolations = items.filter(
    (e) => MIN_PCT_KEYS.includes(e.key) && e.pct > 0 && e.pct < MIN_PCT
  )
  const isValid = total === 100 && minViolations.length === 0

  const handleChange = (idx, newVal) => {
    const next = (draft || entries.map((e) => ({ ...e }))).map((e, i) =>
      i === idx ? { ...e, pct: newVal } : { ...e }
    )
    setDraft(next)
  }

  const handleBlur = () => {
    if (!draft) return
    const newTotal = draft.reduce((s, e) => s + e.pct, 0)
    const hasMinViolation = draft.some(
      (e) => MIN_PCT_KEYS.includes(e.key) && e.pct > 0 && e.pct < MIN_PCT
    )
    if (newTotal === 100 && !hasMinViolation && onSave) {
      onSave(draft.map((e) => `${e.key}:${e.pct}`).join(','))
    }
    setDraft(null)
  }

  return (
    <Box w='100%'>
      <Flex align='center' mb={1} gap={2}>
        <Text fontSize='sm' fontWeight='bold'>{label}</Text>
        <Text fontSize='xs' color={isValid ? 'green.500' : 'red.500'}>
          {total}%
          {total !== 100 && ' (must be 100%)'}
          {minViolations.length > 0 && ` (${minViolations.map((e) => e.key).join(', ')} min ${MIN_PCT}%)`}
        </Text>
      </Flex>
      <Flex h='24px' w='100%' borderRadius='md' overflow='hidden' mb={2}>
        {items.map((e) => (
          <Tooltip key={e.key} label={`${e.key}: ${e.pct}%`} placement='top'>
            <Box
              h='100%'
              bg={e.color}
              w={`${e.pct}%`}
              minW={e.pct > 0 ? '2px' : '0'}
              transition='width 0.2s'
            />
          </Tooltip>
        ))}
      </Flex>
      <Flex flexWrap='wrap' gap={2}>
        {items.map((e, i) => (
          <Flex key={e.key} align='center' gap={1}>
            <Box w='10px' h='10px' borderRadius='sm' bg={e.color} flexShrink={0} />
            <Text fontSize='xs' minW='50px'>{e.key}</Text>
            <NumberInput
              size='xs'
              w='65px'
              min={0}
              max={100}
              value={e.pct}
              isDisabled={isDisabled}
              onChange={(_, val) => handleChange(i, isNaN(val) ? 0 : val)}
              onBlur={handleBlur}
            >
              <NumberInputField px={1} textAlign='right' />
              <NumberInputStepper>
                <NumberIncrementStepper />
                <NumberDecrementStepper />
              </NumberInputStepper>
            </NumberInput>
            <Text fontSize='xs'>%</Text>
          </Flex>
        ))}
      </Flex>
    </Box>
  )
}

export default MemoryPctEditor
