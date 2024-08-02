import {
  HStack,
  Slider,
  SliderFilledTrack,
  SliderMark,
  SliderThumb,
  SliderTrack,
  Spinner,
  VStack
} from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import TagPill from '../TagPill'
import styles from './styles.module.scss'
import ConfirmModal from '../Modals/ConfirmModal'

function LogSlider({ min = 0, max = 4, step = 1, showMark = true, value, onChange, confirmTitle, loading }) {
  const [currentValue, setCurrentValue] = useState(value)
  const [previousValue, setPreviousValue] = useState(value)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    setCurrentValue(value)
    setPreviousValue(value)
  }, [value])
  const renderMarks = () => {
    const marks = []
    for (let i = min; i <= max; i += step) {
      marks.push(
        <SliderMark key={i} value={i} className={styles.markLabel}>
          {i}
        </SliderMark>
      )
    }
    marks.push(
      <SliderMark
        value={currentValue}
        textAlign='center'
        className={`${styles.selectedMarkLabel} ${styles[`markColor${currentValue}`]}`}>
        {currentValue}
      </SliderMark>
    )
    return marks
  }

  const handleChange = (val) => {
    setCurrentValue(val)
  }

  const handleChangeEnd = (val) => {
    setCurrentValue(val)
    openConfirmModal()
  }

  const getTextValues = () => {
    let textValue = ''
    switch (currentValue) {
      case 0:
        textValue = 'No Logging'
        break
      case 1:
        textValue = 'Error'
        break
      case 2:
        textValue = 'Error & Warning'
        break
      case 3:
        textValue = 'Error, Warning & Info'
        break
      case 4:
        textValue = 'Error, Warning, Info & Debug'
        break
    }
    return textValue
  }

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = (action) => {
    if (action === 'cancel') {
      setCurrentValue(previousValue)
    }
    setIsConfirmModalOpen(false)
  }

  return (
    <VStack className={styles.sliderContainer}>
      <HStack className={styles.tags}>
        {currentValue === 0 && <TagPill text={'No Logging'} />}
        {currentValue >= 1 && <TagPill colorScheme='red' text={'Error'} />}
        {currentValue >= 2 && <TagPill colorScheme='orange' text={'Warning'} />}
        {currentValue >= 3 && <TagPill customColorScheme='#3e9de9' text={'Info'} />}
        {currentValue >= 4 && <TagPill customColorScheme='#0066b2' text={'Debug'} />}
        {loading && <Spinner />}
      </HStack>
      <Slider
        min={min}
        max={max}
        step={step}
        value={currentValue}
        onChange={handleChange}
        onChangeEnd={handleChangeEnd}
        className={styles.slider}>
        {showMark && renderMarks()}
        <SliderTrack>
          <SliderFilledTrack className={styles[`filledTrack${currentValue}`]} />
        </SliderTrack>
        <SliderThumb />
      </Slider>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={`${confirmTitle} ${getTextValues()}`}
          onConfirmClick={() => {
            onChange(currentValue)
            closeConfirmModal('')
          }}
        />
      )}
    </VStack>
  )
}

export default LogSlider
