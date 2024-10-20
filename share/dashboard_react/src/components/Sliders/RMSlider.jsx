import { Slider, SliderFilledTrack, SliderMark, SliderThumb, SliderTrack, Spinner, VStack } from '@chakra-ui/react'
import React, { useState, useEffect, act } from 'react'
import styles from './styles.module.scss'
import ConfirmModal from '../Modals/ConfirmModal'

function RMSlider({
  min = 0,
  max = 10,
  step = 1,
  showMark = true,
  showMarkAtInterval = 2,
  selectedMarkLabelCSS,
  value,
  loading,
  confirmTitle,
  onChange
}) {
  const [currentValue, setCurrentValue] = useState(value)
  const [previousValue, setPreviousValue] = useState(value)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)

  useEffect(() => {
    setCurrentValue(value)
    setPreviousValue(value)
  }, [value])
  const renderMarks = () => {
    const marks = []
    if (showMark) {
      for (let i = min; i <= max; ) {
        marks.push(
          <SliderMark key={i} value={i} className={styles.markLabel}>
            {i}
          </SliderMark>
        )
        i = i + showMarkAtInterval
        if (i > max && i % showMarkAtInterval !== 0) {
          marks.push(
            <SliderMark key={i} value={max} className={styles.markLabel}>
              {max}
            </SliderMark>
          )
        }
      }
    }
    marks.push(
      <SliderMark
        value={currentValue}
        textAlign='center'
        className={`${styles.selectedMarkLabel} ${selectedMarkLabelCSS}`}>
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
      <Slider
        min={min}
        max={max}
        step={step}
        value={currentValue}
        onChangeEnd={handleChangeEnd}
        onChange={handleChange}
        className={styles.slider}>
        {renderMarks()}
        {loading && <Spinner />}
        <SliderTrack>
          <SliderFilledTrack />
        </SliderTrack>
        <SliderThumb />
      </Slider>
      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={`${confirmTitle} ${currentValue}`}
          onConfirmClick={() => {
            onChange(currentValue)
            closeConfirmModal('')
          }}
        />
      )}
    </VStack>
  )
}

export default RMSlider
