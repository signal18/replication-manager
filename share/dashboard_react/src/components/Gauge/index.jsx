import { Box, Flex, HStack, Text } from '@chakra-ui/react'
import React, { useEffect, useRef } from 'react'
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
  isGaugeSizeCustomized = true,
  appendTextToValue = '',
  hideMinMax = true,
  showStep = false,
  step = 0,
  handleStepChange
}) {
  const svgRef = useRef(null)
  const updateGaugePosition = () => {
    const svgElement = svgRef.current.querySelector('svg')
    if (svgElement) {
      svgElement.setAttribute('width', width)
      svgElement.setAttribute('height', height)

      const gElements = svgElement.querySelectorAll('g')
      gElements.forEach((g) => {
        const transform = g.getAttribute('transform')
        const translateMatch = /translate\(([^,]+),\s*([^)]+)\)/.exec(transform)
        if (translateMatch) {
          const [_, x, y] = translateMatch
          if (isGaugeSizeCustomized) {
            g.setAttribute('transform', `translate(${x}, 30)`)
          }
        }
      })
    }
  }

  useEffect(() => {
    if (value) {
      updateGaugePosition()
    }
  }, [value, updateGaugePosition, width, height, isGaugeSizeCustomized])

  useEffect(() => {
    setTimeout(() => {
      window.dispatchEvent(new Event('resize'))
    }, 100)
    window.addEventListener('resize', updateGaugePosition)
    return () => {
      window.removeEventListener('resize', updateGaugePosition)
    }
  }, [])

  const formatValue = (value) => {
    if (typeof value === 'number' && !Number.isInteger(value)) {
      return value.toFixed(3)
    }
    return value
  }

  return (
    <Flex direction='column' justify='center' position='relative'>
      <Box width={width} height={height} className={`${styles.container} ${className}`} ref={svgRef}>
        {value >= minValue && value <= maxValue && (
          <GaugeComponent
            minValue={minValue}
            maxValue={maxValue}
            className={styles.guage}
            arc={{
              subArcs: [
                { length: 0.33, color: '#5BE12C' },
                { length: 0.33, color: '#F5CD19' },
                { length: 0.33, color: '#EA4228' }
              ]
            }}
            style={isGaugeSizeCustomized ? {} : { width: `${width}px`, height: `${height}px` }}
            value={value}
            labels={{
              valueLabel: {
                formatTextValue: () => '',
                maxDecimalDigits: 3
              },
              tickLabels: { hideMinMax: hideMinMax, type: 'inner' }
            }}
          />
        )}
        <Box className={`${styles.textOverlay} ${textOverlayClassName}`}>
          <Text className={styles.valueText}>{`${formatValue(value)} ${appendTextToValue}`}</Text>
          <Text className={styles.labelText}>{text}</Text>
        </Box>
      </Box>
      {showStep && (
        <HStack className={styles.stepButtons} gap={2} margin='auto'>
          <RMButton
            variant='outline'
            className={styles.decreaseButton}
            onClick={() => {
              let newValue = parseInt(value) - parseInt(step)
              if (newValue < minValue) {
                newValue = minValue
              }
              handleStepChange(newValue)
            }}>{`-${step}`}</RMButton>
          <RMButton
            variant='outline'
            className={styles.increaseButton}
            onClick={() => {
              let newValue = parseInt(value) + parseInt(step)
              if (newValue > maxValue) {
                newValue = maxValue
              }
              handleStepChange(newValue)
            }}>{`+${step}`}</RMButton>
        </HStack>
      )}
    </Flex>
  )
}

export default Gauge
