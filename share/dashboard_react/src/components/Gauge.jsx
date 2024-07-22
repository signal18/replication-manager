import { Box, Flex, Text } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React, { useEffect, useRef } from 'react'
import GaugeComponent from 'react-gauge-component'

function Gauge({ value, text, width, height, containerSx }) {
  const svgRef = useRef(null)

  const updateGaugePosition = () => {
    const svgElement = svgRef.current.querySelector('svg')
    if (svgElement) {
      const gElements = svgElement.querySelectorAll('g')
      gElements.forEach((g) => {
        const transform = g.getAttribute('transform')
        const translateMatch = /translate\(([^,]+),\s*([^)]+)\)/.exec(transform)
        if (translateMatch) {
          const [_, x, y] = translateMatch
          g.setAttribute('transform', `translate(${x}, 30)`)
        }
      })
    }
  }

  useEffect(() => {
    updateGaugePosition()
  }, [value, updateGaugePosition])

  useEffect(() => {
    window.addEventListener('resize', updateGaugePosition)
    return () => {
      window.removeEventListener('resize', updateGaugePosition)
    }
  }, [])

  const styles = {
    container: {
      width: `${width}px`,
      height: `${height}px`,
      position: 'relative',
      display: 'inline-block'
    },
    gauge: {
      height: '100%'
    },
    textOverlay: {
      position: 'absolute',
      bottom: '-20px',
      left: '50%',
      transform: 'translate(-50%, -50%)',
      textAlign: 'center'
    },
    valueText: {
      display: 'block',
      fontSize: '15px'
    },
    labelText: {
      display: 'block',
      fontSize: '12px'
    }
  }

  const formatValue = (value) => {
    if (typeof value === 'number' && !Number.isInteger(value)) {
      return value.toFixed(3)
    }
    return value
  }

  return (
    <Flex direction='column' justify='center'>
      <Box sx={styles.container} ref={svgRef}>
        <GaugeComponent
          style={styles.guage}
          value={value}
          labels={{
            valueLabel: {
              formatTextValue: () => '',
              style: styles.textValue,
              maxDecimalDigits: 3
            },
            tickLabels: { hideMinMax: true }
          }}
        />
        <Box sx={styles.textOverlay}>
          <Text sx={styles.valueText}>{formatValue(value)}</Text>
          <Text sx={styles.labelText}>{text}</Text>
        </Box>
      </Box>
    </Flex>
  )
}

export default Gauge
