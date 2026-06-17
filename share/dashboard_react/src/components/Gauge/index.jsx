import { Box, Flex, Text, Slider, SliderTrack, SliderFilledTrack, SliderThumb, Tooltip } from '@chakra-ui/react'
import { memo, useCallback, useMemo, useState } from 'react'

function Gauge({
  value,
  text,
  width = 150,
  height = 105,
  className = '',
  minValue = 0,
  maxValue = 100,
  appendTextToValue = '',
  hideMinMax = true,
  showStep = false,
  step = 1,
  handleStepChange,
  isDisabled = false,
}) {
  const safeValue = !isFinite(value) || value < minValue ? minValue : value > maxValue ? maxValue : value
  const [draft, setDraft] = useState(null)
  const [showTooltip, setShowTooltip] = useState(false)
  const current = draft !== null ? draft : safeValue

  const formatValue = useCallback((v) => {
    if (appendTextToValue === 'MB' && v >= 1024) {
      return `${(v / 1024).toFixed(v % 1024 === 0 ? 0 : 1)} GB`
    }
    if (appendTextToValue === 'GB' && v >= 1000) {
      return `${(v / 1000).toFixed(v % 1000 === 0 ? 0 : 1)} TB`
    }
    const formatted = typeof v === 'number' && !Number.isInteger(v) ? v.toFixed(1) : String(v)
    return appendTextToValue ? `${formatted} ${appendTextToValue}` : formatted
  }, [appendTextToValue])

  const sliderHeight = useMemo(() => Math.max(80, height - 25), [height])

  return (
    <Flex
      direction='column'
      align='center'
      justify='flex-start'
      className={className}
      w={`${width}px`}
      h={`${height + 40}px`}
    >
      <Text fontSize='xs' fontWeight='semibold' color='var(--text-color)' mb={1} opacity={0.75}>
        {text}
      </Text>
      <Text fontSize='md' fontWeight='bold' color='var(--text-color)' mb={2}>
        {formatValue(current)}
      </Text>
      <Slider
        orientation='vertical'
        min={minValue}
        max={maxValue}
        step={step}
        value={current}
        isDisabled={isDisabled}
        h={`${sliderHeight}px`}
        onChange={(v) => setDraft(v)}
        onChangeStart={() => setShowTooltip(true)}
        onChangeEnd={(v) => {
          setShowTooltip(false)
          setDraft(null)
          if (v !== safeValue && handleStepChange) {
            handleStepChange(v)
          }
        }}
      >
        <SliderTrack bg='gray.200' w='6px' borderRadius='full'>
          <SliderFilledTrack bg='blue.400' />
        </SliderTrack>
        <Tooltip
          label={formatValue(current)}
          placement='right'
          isOpen={showTooltip}
          hasArrow
        >
          <SliderThumb boxSize={4} bg='blue.500' />
        </Tooltip>
      </Slider>
      {!hideMinMax && (
        <Flex justify='space-between' w='100%' mt={1}>
          <Text fontSize='9px' color='gray.500'>{formatValue(minValue)}</Text>
          <Text fontSize='9px' color='gray.500'>{formatValue(maxValue)}</Text>
        </Flex>
      )}
    </Flex>
  )
}

export default memo(Gauge)
