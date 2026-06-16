import React, { useMemo, useState, useCallback } from 'react'
import {
  Box, Flex, Text, Slider, SliderTrack, SliderFilledTrack, SliderThumb, Tooltip
} from '@chakra-ui/react'

const COLORS = [
  '#6366f1', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444',
  '#8b5cf6', '#ec4899', '#14b8a6', '#f97316', '#64748b',
  '#a855f7', '#06b6d4'
]

const MIN_PCT_KEYS = ['aria', 'myisam']
const MIN_PCT = 10

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
  const allocated = items.reduce((s, e) => s + e.pct, 0)
  const free = 100 - allocated

  const handleChange = useCallback((idx, newVal) => {
    setDraft((prev) => {
      const base = prev || entries.map((e) => ({ ...e }))
      return base.map((e, i) => (i === idx ? { ...e, pct: newVal } : e))
    })
  }, [entries])

  const handleChangeEnd = useCallback(() => {
    if (!draft) return
    const total = draft.reduce((s, e) => s + e.pct, 0)
    const hasMinViolation = draft.some(
      (e) => MIN_PCT_KEYS.includes(e.key) && e.pct < MIN_PCT
    )
    if (total === 100 && !hasMinViolation && onSave) {
      onSave(draft.map((e) => `${e.key}:${e.pct}`).join(','))
    }
    setDraft(null)
  }, [draft, entries, onSave])

  const getMax = useCallback((idx) => {
    const others = items.reduce((s, e, i) => (i === idx ? s : s + e.pct), 0)
    return 100 - others
  }, [items])

  return (
    <Box w='100%'>
      <Flex align='center' mb={2} gap={2}>
        <Text fontSize='sm' fontWeight='bold' color='var(--text-color)'>{label}</Text>
        <Text fontSize='xs' fontWeight='bold' color={free === 0 ? 'green.500' : 'orange.500'}>
          {free > 0 ? `${free}% free` : '100% allocated'}
        </Text>
      </Flex>

      <Flex h='20px' w='100%' borderRadius='md' overflow='hidden' mb={3} bg='gray.200'>
        {items.map((e) => (
          <Tooltip key={e.key} label={`${e.key}: ${e.pct}%`} placement='top'>
            <Box
              h='100%'
              bg={e.color}
              w={`${e.pct}%`}
              minW={e.pct > 0 ? '2px' : '0'}
              transition='width 0.15s'
            />
          </Tooltip>
        ))}
      </Flex>

      <Flex direction='column' gap={1}>
        {items.map((e, i) => {
          const min = MIN_PCT_KEYS.includes(e.key) ? MIN_PCT : 0
          const max = getMax(i)
          return (
            <Flex key={e.key} align='center' gap={2}>
              <Box w='10px' h='10px' borderRadius='sm' bg={e.color} flexShrink={0} />
              <Text fontSize='xs' w='70px' flexShrink={0} color='var(--text-color)'>{e.key}</Text>
              <Slider
                min={min}
                max={max}
                value={e.pct}
                isDisabled={isDisabled}
                onChange={(val) => handleChange(i, val)}
                onChangeEnd={handleChangeEnd}
                size='sm'
                flex={1}
              >
                <SliderTrack bg='gray.200'>
                  <SliderFilledTrack bg={e.color} />
                </SliderTrack>
                <SliderThumb boxSize={3} />
              </Slider>
              <Text fontSize='xs' w='30px' textAlign='right' color='var(--text-color)'>{e.pct}%</Text>
            </Flex>
          )
        })}
      </Flex>
    </Box>
  )
}

export default MemoryPctEditor
