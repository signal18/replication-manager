import { Flex, HStack, Radio, RadioGroup, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../../../components/Dropdown'
import styles from './styles.module.scss'
import TimePicker from 'react-time-picker'
import { getDaysInMonth, padWithZero } from '../../../utility/common'
import RMButton from '../../../components/RMButton'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import RMSwitch from '../../../components/RMSwitch'
import RMIconButton from '../../../components/RMIconButton'
import { GrPowerReset } from 'react-icons/gr'
import Message from '../../../components/Message'

function Scheduler({
  value,
  user,
  isSwitchChecked,
  onSave,
  hasSwitch = true,
  onSwitchChange,
  confirmTitle,
  switchConfirmTitle
}) {
  const [currentValue, setCurrentValue] = useState(value)
  const [previousValue, setPreviousValue] = useState(value)
  const [isConfirmModalOpen, setIsConfirmModalOpen] = useState(false)
  const [months, setMonths] = useState([
    { name: 'Jan', value: 1 },
    { name: 'Feb', value: 2 },
    { name: 'Mar', value: 3 },
    { name: 'Apr', value: 4 },
    { name: 'May', value: 5 },
    { name: 'Jun', value: 6 },
    { name: 'Jul', value: 7 },
    { name: 'Aug', value: 8 },
    { name: 'Sep', value: 9 },
    { name: 'Oct', value: 10 },
    { name: 'Nov', value: 11 },
    { name: 'Dec', value: 12 }
  ])
  const [weekDays, setWeekdays] = useState([
    { name: 'Sun', value: 0 },
    { name: 'Mon', value: 1 },
    { name: 'Tue', value: 2 },
    { name: 'Wed', value: 3 },
    { name: 'Thu', value: 4 },
    { name: 'Fri', value: 5 },
    { name: 'Sat', value: 6 }
  ])

  const [fromDays, setFromDays] = useState([])
  const [toDays, setToDays] = useState([])

  const [recurrentType, setRecurrentType] = useState('daily')

  const [sections, setSections] = useState(['From', 'To', 'Interval'])

  const [selectedFromMonth, setSelectedFromMonth] = useState()
  const [selectedToMonth, setSelectedToMonth] = useState()

  const [selectedFromWeekday, setSelectedFromWeekday] = useState()
  const [selectedToWeekday, setSelectedToWeekday] = useState()

  const [selectedFromDay, setSelectedFromDay] = useState()
  const [selectedToDay, setSelectedToDay] = useState()

  const [selectedFromTime, setSelectedFromTime] = useState('00:00:00')
  const [selectedToTime, setSelectedToTime] = useState('00:00:00')
  const [selectedInterval, setSelectedInterval] = useState('00:00:00')

  const [valuesChanged, setValuesChanged] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')

  useEffect(() => {
    setCurrentValue(value)
    setPreviousValue(value)
  }, [value])

  useEffect(() => {
    if (currentValue && !valuesChanged) {
      ///from
      const fromHour = currentValue.split(' ')[2].split('-')[0].split('/')[0] ?? 0
      const fromMin = currentValue.split(' ')[1].split('-')[0].split('/')[0] ?? 0
      const fromSecond = currentValue.split(' ')[0].split('-')[0].split('/')[0] ?? 0
      if (fromHour === '*' || fromMin === '*' || fromSecond === '*') {
        setSelectedFromTime('')
      } else {
        setSelectedFromTime(`${padWithZero(fromHour)}:${padWithZero(fromMin)}:${padWithZero(fromSecond)}`)
      }

      const fromMonth = currentValue.split(' ')[4].split('-')[0].split('/')[0]
      setSelectedFromMonth(months.find((x) => x.value == fromMonth))

      const fromDay = currentValue.split(' ')[3].split('-')[0].split('/')[0]

      if (fromMonth && fromDay) {
        setFromDays(getDaysInMonth(fromMonth))
        setTimeout(() => setSelectedFromDay(fromDays.find((x) => x.value == fromDay)), 10)
      }

      const fromWeek = currentValue.split(' ')[5].split('-')[0].split('/')[0]
      setSelectedFromWeekday(weekDays.find((x) => x.value == fromWeek))

      ///to
      const toHour = parseInt(currentValue.split(' ')[2].split('-')[1]) || 0
      const toMin = parseInt(currentValue.split(' ')[1].split('-')[1]) || 0
      const toSecond = parseInt(currentValue.split(' ')[0].split('-')[1]) || 0
      if (toHour === '*' || toMin === '*' || toSecond === '*') {
        setSelectedToTime('')
      } else {
        setSelectedToTime(`${padWithZero(toHour)}:${padWithZero(toMin)}:${padWithZero(toSecond)}`)
      }

      const toMonth = currentValue.split(' ')[4].split('-')[1]
      setSelectedToMonth(months.find((x) => x.value == toMonth))

      const toDay = currentValue.split(' ')[3].split('-')[1]
      if (fromMonth && fromDay) {
        setToDays(getDaysInMonth(toMonth))
        setTimeout(() => setSelectedToDay(toDays.find((x) => x.value == toDay)), 10)
      }

      const toWeek = currentValue.split(' ')[5].split('-')[1]
      setSelectedToWeekday(weekDays.find((x) => x.value == toWeek))

      ///interval
      const intervalHour = currentValue.split(' ')[2].split('-')[2] ?? 0
      const intervalMin = currentValue.split(' ')[1].split('/')[1] ?? 0
      const intevalSecond = currentValue.split(' ')[0].split('/')[1] ?? 0

      setSelectedInterval(`${padWithZero(intervalHour)}:${padWithZero(intervalMin)}:${padWithZero(intevalSecond)}`)

      if (fromDay != '*' || (toDay !== undefined && toDay != '*')) {
        setRecurrentType('monthly')
      } else if (fromWeek != '*' || (toWeek !== undefined && toWeek != '*')) {
        setRecurrentType('weekly')
      } else {
        setRecurrentType('daily')
      }
    }
  }, [currentValue, valuesChanged])

  useEffect(() => {
    if (valuesChanged) {
      let finalMonth = '*'
      let finalDay = '*'
      let finalWeek = '*'

      if (recurrentType === 'weekly') {
        finalMonth = selectedToMonth?.value
          ? `${selectedFromMonth?.value || '*'}-${selectedToMonth.value}`
          : selectedFromMonth?.value

        finalWeek =
          selectedToWeekday?.value >= 0
            ? `${selectedFromWeekday?.value >= 0 ? selectedFromWeekday?.value : '*'}-${selectedToWeekday.value}`
            : selectedFromWeekday?.value
      } else if (recurrentType === 'monthly') {
        finalMonth = selectedToMonth?.value
          ? `${selectedFromMonth?.value || '*'}-${selectedToMonth.value}`
          : selectedFromMonth?.value

        finalDay = selectedToDay?.value
          ? `${selectedFromDay?.value || '*'}-${selectedToDay.value}`
          : selectedFromDay?.value
      }
      ///make expression for time
      const arrFromTime = selectedFromTime?.split(':')
      const arrToTime = selectedToTime?.split(':')
      const arrIntervalTime = selectedInterval?.split(':')

      if (!arrFromTime) {
        setErrorMessage('From time is required')
      } else {
        let finalHour = `${parseInt(arrFromTime[0])}`
        if (arrToTime && parseInt(arrFromTime[0]) >= 0) {
          finalHour += `-${parseInt(arrToTime[0])}`
        } else {
          finalHour += `-undefined`
        }
        if (arrIntervalTime && parseInt(arrIntervalTime[0]) > 0) {
          finalHour += '-' + parseInt(arrIntervalTime[0])
        }
        let finalMin = `${parseInt(arrFromTime[1])}`
        if (arrToTime && parseInt(arrFromTime[1]) >= 0) {
          finalMin += `-${parseInt(arrToTime[1])}`
        }
        if (arrIntervalTime && parseInt(arrIntervalTime[1]) > 0) {
          finalMin += '/' + parseInt(arrIntervalTime[1])
        }
        let finalSec = `${parseInt(arrFromTime[2])}`
        if (arrToTime && parseInt(arrFromTime[2]) >= 0) {
          finalMin += `-${parseInt(arrToTime[2])}`
        }
        if (arrIntervalTime && parseInt(arrIntervalTime[2]) > 0) {
          finalSec += '/' + parseInt(arrIntervalTime[2])
        }

        //assemble final string to pass in api
        const finalValue = `${finalSec} ${finalMin} ${finalHour} ${finalDay} ${finalMonth} ${finalWeek}`
        setCurrentValue(finalValue)
      }
    }
  }, [
    recurrentType,
    valuesChanged,
    selectedFromMonth,
    selectedToMonth,
    selectedFromDay,
    selectedToDay,
    selectedFromWeekday,
    selectedToWeekday,
    selectedFromTime,
    selectedToTime,
    selectedInterval
  ])

  const handleTimeChange = (time, section) => {
    setValuesChanged(true)
    if (section === 'From') {
      setSelectedFromTime(time)
    } else if (section === 'To') {
      setSelectedToTime(time)
    } else {
      setSelectedInterval(time)
    }
  }
  const handleChangeDay = (day, section) => {
    setValuesChanged(true)
    if (section === 'From') {
      setSelectedFromDay(day)
    } else {
      setSelectedToDay(day)
    }
  }

  const handleChangeMonth = (month, section) => {
    setValuesChanged(true)
    if (section === 'From') {
      setSelectedFromMonth(month)
    } else {
      setSelectedToMonth(month)
    }
    if (section === 'From') {
      setFromDays(getDaysInMonth(month))
    } else if (section === 'To') {
      setToDays(getDaysInMonth(month))
    }
  }

  const handleChangeWeek = (week, section) => {
    setValuesChanged(true)
    if (section === 'From') {
      setSelectedFromWeekday(week)
    } else {
      setSelectedToWeekday(week)
    }
  }

  const handleRecurrentChange = (recurrentVal) => {
    setValuesChanged(true)
    setRecurrentType(recurrentVal)
  }

  const handleSaveScheduler = () => {
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
    <VStack className={styles.scheduler} align='flex-start'>
      {hasSwitch && (
        <RMSwitch
          confirmTitle={switchConfirmTitle}
          onChange={onSwitchChange}
          isDisabled={user?.grants['cluster-settings'] == false}
          isChecked={isSwitchChecked}
        />
      )}

      {(!hasSwitch || isSwitchChecked) && (
        <>
          <RadioGroup value={recurrentType} onChange={handleRecurrentChange}>
            <HStack spacing={6}>
              <Radio value='daily' size='lg'>
                Daily
              </Radio>
              <Radio value='weekly' size='lg'>
                Weekly
              </Radio>
              <Radio value='monthly' size='lg'>
                Monthly
              </Radio>
            </HStack>
          </RadioGroup>
          {sections.map((section) => (
            <Flex className={styles.schedulerItem}>
              <div className={styles.label}>{section}</div>
              <TimePicker
                format='HH:mm:ss'
                disableClock={true}
                className={styles.timepicker}
                hourPlaceholder='HH'
                minutePlaceholder='mm'
                secondPlaceholder='ss'
                maxDetail='second'
                value={section === 'From' ? selectedFromTime : section === 'To' ? selectedToTime : selectedInterval}
                onChange={(val) => {
                  handleTimeChange(val, section)
                }}
              />

              {section !== 'Interval' && (
                <HStack className={styles.dayMonthWeek}>
                  {(recurrentType === 'monthly' || recurrentType === 'weekly') && (
                    <Dropdown
                      placeholder='Select month'
                      options={months}
                      selectedValue={section === 'From' ? selectedFromMonth?.value : selectedToMonth?.value}
                      buttonClassName={styles.btnDrodown}
                      menuListClassName={styles.menuList}
                      inlineLabel='Month '
                      onChange={(month) => handleChangeMonth(month, section)}
                    />
                  )}

                  {recurrentType === 'monthly' && (
                    <Dropdown
                      placeholder='Select day'
                      options={section === 'From' ? fromDays : toDays}
                      selectedValue={section === 'From' ? selectedFromDay?.value : selectedToDay?.value}
                      buttonClassName={styles.btnDrodown}
                      menuListClassName={styles.menuList}
                      inlineLabel='Day '
                      onChange={(day) => handleChangeDay(day, section)}
                    />
                  )}
                  {recurrentType === 'weekly' && (
                    <Dropdown
                      placeholder='Select week'
                      options={weekDays}
                      selectedValue={section === 'From' ? selectedFromWeekday?.value : selectedToWeekday?.value}
                      buttonClassName={styles.btnDrodown}
                      menuListClassName={styles.menuList}
                      inlineLabel='Week '
                      onChange={(weekday) => handleChangeWeek(weekday, section)}
                    />
                  )}
                </HStack>
              )}
            </Flex>
          ))}
          {errorMessage && <Message message={errorMessage} />}
          {valuesChanged && (
            <HStack>
              <RMButton isDisabled={errorMessage?.length > 0} onClick={handleSaveScheduler}>
                Save
              </RMButton>
              <RMIconButton
                icon={GrPowerReset}
                tooltip={'Reset scheduler'}
                onClick={() => {
                  setValuesChanged(false)
                  setCurrentValue(previousValue)
                  //  setTimeout(() => setCurrentValue(previousValue), 100)
                  setErrorMessage('')
                }}
              />
            </HStack>
          )}
        </>
      )}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal('cancel')
          }}
          title={`${confirmTitle} ${currentValue}`}
          onConfirmClick={() => {
            onSave(currentValue)
            closeConfirmModal('')
          }}
        />
      )}
    </VStack>
  )
}

export default Scheduler
