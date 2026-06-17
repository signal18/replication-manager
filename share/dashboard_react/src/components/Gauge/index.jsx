import { Flex, Text, Slider, SliderTrack, SliderFilledTrack, SliderThumb, Tooltip } from '@chakra-ui/react'
import { memo, useCallback, useMemo, useState } from 'react'
import GaugeComponent from 'react-gauge-component'
import styles from './styles.module.scss'

function Gauge({
  value,
  text,
  width = 210,
  height = 90,
  className = '',
  textOverlayClassName,
  minValue = 0,
  maxValue = 100,
  appendTextToValue = '',
  hideMinMax = true,
  showStep = false,
  step = 0,
  handleStepChange,
  isDisabled = false,
}) {
  const safeValue = !isFinite(value) || value < minValue ? minValue : value > maxValue ? maxValue : value

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

  const arcConfig = useMemo(() => ({
    width: 0.18,
    cornerRadius: 3,
    padding: 0.02,
    subArcs: [
      { length: 0.33, color: '#5BE12C' },
      { length: 0.33, color: '#F5CD19' },
      { length: 0.34, color: '#EA4228' },
    ],
  }), [])

  const pointerConfig = useMemo(() => ({
    animate: true,
    animationDuration: 500,
    elastic: false,
    length: 0.65,
    width: 12,
    color: '#9ca3af',
  }), [])

  const labelsConfig = useMemo(() => ({
    valueLabel: {
      formatTextValue: formatValue,
      style: {
        fontSize: '28px',
        fontWeight: 'bold',
        textShadow: 'none',
        fill: '#9ca3af',
      },
    },
    tickLabels: {
      hideMinMax: hideMinMax,
      type: 'inner',
      defaultTickValueConfig: {
        style: { fontSize: '8px', fill: '#9ca3af' },
      },
      defaultTickLineConfig: {
        color: '#9ca3af',
        length: 5,
      },
    },
  }), [hideMinMax, formatValue])

  const gaugeStyle = useMemo(() => ({
    width: `${width}px`,
    height: `${height}px`,
  }), [width, height])

  const editable = showStep && handleStepChange

  return (
    <Flex direction={editable ? 'row' : 'column'} align='center' justify='center' gap={editable ? 2 : 0} className={className}>
      <Flex direction='column' align='center' justify='center'>
        <GaugeComponent
          minValue={minValue}
          maxValue={maxValue}
          value={safeValue}
          style={gaugeStyle}
          arc={arcConfig}
          pointer={pointerConfig}
          labels={labelsConfig}
        />
        <Text
          fontSize='xs'
          fontWeight='semibold'
          textAlign='center'
          className={`${styles.labelText} ${textOverlayClassName ?? ''}`}
          mt='-6px'
          opacity={0.75}
        >
          {text}
        </Text>
      </Flex>
      {editable && (
        <VerticalSlider
          value={safeValue}
          minValue={minValue}
          maxValue={maxValue}
          step={step}
          formatValue={formatValue}
          handleStepChange={handleStepChange}
          isDisabled={isDisabled}
          height={height}
        />
      )}
    </Flex>
  )
}

function VerticalSlider({
  value,
  minValue,
  maxValue,
  step,
  formatValue,
  handleStepChange,
  isDisabled,
  height,
}) {
  const [draft, setDraft] = useState(null)
  const [showTooltip, setShowTooltip] = useState(false)
  const current = draft !== null ? draft : value
  const sliderHeight = Math.max(60, height - 10)

  return (
    <Flex direction='column' align='center' justify='center' h={`${sliderHeight + 30}px`}>
      <Text fontSize='9px' color='gray.500' mb={1}>{formatValue(maxValue)}</Text>
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
          if (v !== value && handleStepChange) {
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
      <Text fontSize='9px' color='gray.500' mt={1}>{formatValue(minValue)}</Text>
    </Flex>
  )
}

export default memo(Gauge)
