import { Box, Flex, Text } from '@chakra-ui/react'
import { useTheme } from '@emotion/react'
import React from 'react'
import GaugeComponent from 'react-gauge-component'

function Gauge({ value, text, width, containerSx }) {
  const styles = {
    container: {
      width: `${width}px`
    },
    textValue: {
      textShadow: 'none'
    }
  }

  return (
    <Flex direction='column' justify='center'>
      <GaugeComponent
        style={styles.container}
        value={value}
        labels={{
          valueLabel: { formatTextValue: (value) => value, style: styles.textValue, maxDecimalDigits: 3 },
          tickLabels: { hideMinMax: true }
        }}
      />
      <Text textAlign='center' fontWeight='500'>
        {text}
      </Text>
    </Flex>
  )
}

export default Gauge
