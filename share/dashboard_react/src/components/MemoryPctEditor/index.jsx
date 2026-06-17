import React, { useMemo, useState, useRef, useCallback } from 'react'
import { Box, Flex, Text, Tooltip } from '@chakra-ui/react'

const COLORS = [
  '#6366f1', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444',
  '#8b5cf6', '#ec4899', '#14b8a6', '#f97316', '#64748b',
  '#a855f7', '#06b6d4'
]

const MIN_PCT_KEYS = ['aria', 'myisam']
const MIN_PCT = 10
const ALLOW_ZERO_KEYS = ['querycache', 'tokudb']
const MIN_STEP_MB = 128

function MemoryPctEditor({ label, value, totalMemoryMB, isDisabled, onSave }) {
  const entries = useMemo(() => {
    if (!value) return []
    return value.split(',').map((part, i) => {
      const [key, val] = part.split(':')
      return { key, pct: parseInt(val, 10) || 0, color: COLORS[i % COLORS.length] }
    })
  }, [value])

  const memMB = useMemo(() => parseFloat(totalMemoryMB) || 0, [totalMemoryMB])

  const step = useMemo(() => {
    if (memMB <= 0) return 1
    return Math.max(1, Math.round((MIN_STEP_MB / memMB) * 100))
  }, [memMB])

  const [draft, setDraft] = useState(null)
  const items = draft || entries
  const allocated = items.reduce((s, e) => s + e.pct, 0)
  const free = 100 - allocated

  const barRef = useRef(null)
  const dragRef = useRef(null)

  const mbLabel = (pct) => {
    if (memMB <= 0) return ''
    const mb = Math.round(memMB * pct / 100)
    return mb >= 1024 ? `${(mb / 1024).toFixed(1)}GB` : `${mb}MB`
  }

  const snap = useCallback((val, key) => {
    const s = Math.round(val / step) * step
    if (ALLOW_ZERO_KEYS.includes(key) && s < step) return 0
    const min = MIN_PCT_KEYS.includes(key) ? MIN_PCT : 0
    return Math.max(min, s)
  }, [step])

  const handleDragStart = useCallback((idx, e) => {
    if (isDisabled) return
    e.preventDefault()
    const bar = barRef.current
    if (!bar) return
    const barRect = bar.getBoundingClientRect()
    const startItems = (draft || entries).map((en) => ({ ...en }))

    dragRef.current = { idx, barRect, startItems }

    const onMove = (ev) => {
      const clientX = ev.touches ? ev.touches[0].clientX : ev.clientX
      const { idx: di, barRect: br, startItems: si } = dragRef.current

      const pxFromLeft = clientX - br.left
      const totalPct = Math.max(0, Math.min(100, (pxFromLeft / br.width) * 100))

      const leftOfSegment = si.slice(0, di).reduce((s, en) => s + en.pct, 0)
      const rawPct = totalPct - leftOfSegment
      const others = si.reduce((s, en, i) => (i === di ? s : s + en.pct), 0)
      const maxPct = 100 - others
      const newPct = snap(Math.max(0, Math.min(maxPct, rawPct)), si[di].key)

      setDraft(si.map((en, i) => (i === di ? { ...en, pct: newPct } : en)))
    }

    const onEnd = () => {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onEnd)
      document.removeEventListener('touchmove', onMove)
      document.removeEventListener('touchend', onEnd)
      const origItems = dragRef.current?.startItems
      dragRef.current = null

      setDraft((prev) => {
        if (!prev || !origItems) return null
        const total = prev.reduce((s, en) => s + en.pct, 0)
        const hasMinViolation = prev.some(
          (en) => MIN_PCT_KEYS.includes(en.key) && en.pct < MIN_PCT
        )
        if (total <= 100 && !hasMinViolation && onSave) {
          const changed = prev.filter((en, j) => en.pct !== origItems[j].pct).map((en) => en.key)
          onSave(prev.map((en) => `${en.key}:${en.pct}`).join(','), changed.join(', '))
        }
        return null
      })
    }

    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onEnd)
    document.addEventListener('touchmove', onMove, { passive: false })
    document.addEventListener('touchend', onEnd)
  }, [isDisabled, draft, entries, snap, onSave])

  return (
    <Box w='100%'>
      <Flex align='center' mb={1} gap={2}>
        <Text fontSize='sm' fontWeight='bold' color='var(--text-color)'>{label}</Text>
        <Text fontSize='xs' fontWeight='bold' color={free < 0 ? 'red.500' : free === 0 ? 'green.500' : 'gray.500'}>
          {free < 0 ? `${-free}% over!` : free > 0 ? `${free}% free (${mbLabel(free)})` : '100% allocated'}
        </Text>
      </Flex>

      <Box
        ref={barRef}
        position='relative'
        h='40px'
        w='100%'
        borderRadius='md'
        overflow='visible'
        bg='gray.200'
        cursor={isDisabled ? 'default' : 'col-resize'}
        userSelect='none'
      >
        <Flex h='100%' w='100%' borderRadius='md' overflow='hidden'>
          {items.map((e) => (
            <Tooltip key={e.key} label={`${e.key}: ${e.pct}% (${mbLabel(e.pct)})`} placement='top'>
              <Box
                h='100%'
                bg={e.color}
                w={`${e.pct}%`}
                minW={e.pct > 0 ? '1px' : '0'}
                transition={dragRef.current ? 'none' : 'width 0.15s'}
                display='flex'
                alignItems='center'
                justifyContent='center'
                overflow='hidden'
              >
                {e.pct >= 8 && (
                  <Text fontSize='10px' color='white' fontWeight='bold' whiteSpace='nowrap'>
                    {e.key} {e.pct}%
                  </Text>
                )}
              </Box>
            </Tooltip>
          ))}
          {free > 0 && (
            <Box
              h='100%'
              bg='gray.500'
              w={`${free}%`}
              display='flex'
              alignItems='center'
              justifyContent='center'
              overflow='hidden'
              transition={dragRef.current ? 'none' : 'width 0.15s'}
            >
              {free >= 5 && (
                <Text fontSize='10px' color='white' fontWeight='bold' whiteSpace='nowrap'>
                  free {free}% ({mbLabel(free)})
                </Text>
              )}
            </Box>
          )}
        </Flex>

        {items.map((e, i) => {
          const leftPct = items.slice(0, i + 1).reduce((s, en) => s + en.pct, 0)
          if (e.pct === 0 && ALLOW_ZERO_KEYS.includes(e.key)) return null
          return (
            <Tooltip key={`handle-${e.key}`} label={`Resize ${e.key}`} placement='top' openDelay={300}>
              <Box
                position='absolute'
                left={`${leftPct}%`}
                top='0'
                h='100%'
                w='8px'
                ml='-4px'
                cursor={isDisabled ? 'default' : 'col-resize'}
                zIndex={2}
                onMouseDown={(ev) => handleDragStart(i, ev)}
                onTouchStart={(ev) => handleDragStart(i, ev)}
                _hover={{ '& > div': { opacity: 1 } }}
              >
                <Box
                  h='100%'
                  w='4px'
                  mx='auto'
                  bg='white'
                  opacity={0.5}
                  borderRadius='sm'
                  transition='opacity 0.15s'
                  boxShadow='0 0 2px rgba(0,0,0,0.3)'
                />
              </Box>
            </Tooltip>
          )
        })}
      </Box>

      <Flex flexWrap='wrap' gap={2} mt={2}>
        {items.map((e) => (
          <Flex key={e.key} align='center' gap={1}>
            <Box w='8px' h='8px' borderRadius='sm' bg={e.color} flexShrink={0} />
            <Text fontSize='xs' color='var(--text-color)'>
              {e.key}: {e.pct}% {mbLabel(e.pct)}
            </Text>
          </Flex>
        ))}
        {free > 0 && (
          <Flex align='center' gap={1}>
            <Box w='8px' h='8px' borderRadius='sm' bg='gray.500' flexShrink={0} />
            <Text fontSize='xs' color='white'>
              free: {free}% {mbLabel(free)}
            </Text>
          </Flex>
        )}
      </Flex>
    </Box>
  )
}

export default MemoryPctEditor
