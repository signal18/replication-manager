import { Flex, HStack, Text } from '@chakra-ui/react'
import { memo, useCallback, useMemo } from 'react'
import GaugeComponent from 'react-gauge-component'
import styles from './styles.module.scss'
import RMButton from '../RMButton'

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

  const handleMinValue = (value, minValue) => {
    let newValue = parseInt(value) - parseInt(step)
    if (newValue < minValue) newValue = minValue
    handleStepChange(newValue)
  }

  const handleMaxValue = (value, maxValue) => {
    let newValue = parseInt(value) + parseInt(step)
    if (newValue > maxValue) newValue = maxValue
    handleStepChange(newValue)
  }

  return (
    <Flex direction='column' align='center' justify='center' className={className}>
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
      {showStep && (
        <HStack className={styles.stepButtons} gap={2} margin='auto' mt='4px'>
          <RMButton
            isDisabled={isDisabled}
            variant='outline'
            className={styles.decreaseButton}
            onClick={() => handleMinValue(value, minValue)}>
            {`-${step}`}
          </RMButton>
          <RMButton
            isDisabled={isDisabled}
            variant='outline'
            className={styles.increaseButton}
            onClick={() => handleMaxValue(value, maxValue)}>
            {`+${step}`}
          </RMButton>
        </HStack>
      )}
    </Flex>
  )
}

export default memo(Gauge)
