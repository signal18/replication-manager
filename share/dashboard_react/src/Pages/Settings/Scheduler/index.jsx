import { Flex, HStack, Input, Radio, RadioGroup, Text, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../../../components/Dropdown'
import styles from './styles.module.scss'
import TimePicker from 'react-time-picker'
import { compareTimes, getDaysInMonth, getOrdinalSuffix, padWithZero } from '../../../utility/common'
import RMButton from '../../../components/RMButton'
import ConfirmModal from '../../../components/Modals/ConfirmModal'
import RMSwitch from '../../../components/RMSwitch'
import RMIconButton from '../../../components/RMIconButton'
import { GrPowerReset } from 'react-icons/gr'
import Message from '../../../components/Message'
import TabItems from '../../../components/TabItems'
import { HiPencilAlt } from 'react-icons/hi'

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
  const [recurrentOptions, setRecurrentOptions] = useState([
    // { key: 'once', value: 'Once' },
    { key: 'everyMinute', value: 'Every Minute' },
    { key: 'hourly', value: 'Hourly' },
    { key: 'daily', value: 'Daily' },
    { key: 'weekly', value: 'Weekly' },
    { key: 'monthly', value: 'Monthly' }
  ])
  const [months, setMonths] = useState([
    { name: 'January', value: 1 },
    { name: 'February', value: 2 },
    { name: 'March', value: 3 },
    { name: 'April', value: 4 },
    { name: 'May', value: 5 },
    { name: 'June', value: 6 },
    { name: 'July', value: 7 },
    { name: 'August', value: 8 },
    { name: 'September', value: 9 },
    { name: 'October', value: 10 },
    { name: 'November', value: 11 },
    { name: 'December', value: 12 }
  ])
  const [weekDays, setWeekdays] = useState([
    { name: 'Sun', value: 0, selected: false },
    { name: 'Mon', value: 1, selected: false },
    { name: 'Tue', value: 2, selected: false },
    { name: 'Wed', value: 3, selected: false },
    { name: 'Thu', value: 4, selected: false },
    { name: 'Fri', value: 5, selected: false },
    { name: 'Sat', value: 6, selected: false }
  ])

  const [editMode, setEditMode] = useState(false)
  const [description, setDescription] = useState('')

  const [fromDays, setFromDays] = useState([])
  const [toDays, setToDays] = useState([])

  const [recurrentType, setRecurrentType] = useState('daily')

  const [selectedFromMonth, setSelectedFromMonth] = useState()
  const [selectedToMonth, setSelectedToMonth] = useState()

  const [selectedFromDay, setSelectedFromDay] = useState()
  const [selectedToDay, setSelectedToDay] = useState()

  const [selectedFromHour, setSelectedFromHour] = useState(0)
  const [selectedFromMinute, setSelectedFromMinute] = useState(0)
  const [selectedToHour, setSelectedToHour] = useState(0)
  const [selectedToMinute, setSelectedToMinute] = useState(0)

  const [everyHour, setEveryHour] = useState(2)
  const [everyMinute, setEveryMinute] = useState(30)

  const [valuesChanged, setValuesChanged] = useState(false)
  const [errorMessage, setErrorMessage] = useState('')

  useEffect(() => {
    setCurrentValue(value)
    setPreviousValue(value)
  }, [value])

  useEffect(() => {
    if (currentValue && !valuesChanged) {
      let desc = 'Runs '
      //evaluate month part
      const monthPart = currentValue.split(' ')[4]
      const fromMonth = monthPart.split('-')[0]
      const toMonth = monthPart.split('-')[1]
      setSelectedFromMonth(fromMonth)
      setSelectedToMonth(toMonth)

      //evaluate day part
      setFromDays(getDaysInMonth(fromMonth))
      setToDays(getDaysInMonth(toMonth))
      const dayPart = currentValue.split(' ')[3]
      const fromDay = dayPart.split('-')[0]
      const toDay = dayPart.split('-')[1]
      setSelectedFromDay(fromDay)
      setSelectedToDay(toDay)

      //evaluate weekday part
      const weekdayPart = currentValue.split(' ')[5]
      if (weekdayPart !== '*') {
        setRecurrentType('weekly')
        const arrSelectedWkdays = weekdayPart.split(',')
        let weekdayNames = ''
        const updatedWeekdays = weekDays.map((wd) => {
          if (arrSelectedWkdays.includes(wd.value.toString())) {
            wd.selected = true
            weekdayNames += `${wd.name}, `
          }
          return wd
        })
        setWeekdays(updatedWeekdays)
        desc += `<strong>weekly</strong> on <strong>${weekdayNames.replace(/, $/, '')}</strong> starting from <strong>${getOrdinalSuffix(fromDay)} ${getMonthName(fromMonth)}</strong> till <strong>${getOrdinalSuffix(toDay)} ${getMonthName(toMonth)}</strong> <br/>`
      } else if (selectedToMonth > 0 && !selectedToDay) {
        setRecurrentType('monthly')
        desc += `<strong>monthly</strong> on the date <strong>${fromDay}</strong> starting from the month <strong>${getMonthName(fromMonth)}</strong> till <strong>${getMonthName(toMonth)}</strong> <br/>`
      } else if (selectedToMonth > 0 && selectedToDay > 0) {
        setRecurrentType('daily')
        desc += `<strong>daily</strong> starting from <strong>${getOrdinalSuffix(fromDay)} ${getMonthName(fromMonth)}</strong> till <strong>${getOrdinalSuffix(toDay)} ${getMonthName(toMonth)}</strong> <br/>`
      }

      //evaluate hour part
      const hourPart = currentValue.split(' ')[2]
      const fromHour = hourPart.split('/')[0].split('-')[0]
      const toHour = hourPart.split('/')[0].split('-')[1] || 0
      let hourInterval = hourPart.split('/')[1]
      setSelectedFromHour(fromHour)
      setSelectedToHour(toHour)
      if (hourInterval) {
        setEveryHour(hourInterval)
      }

      //evaluate minute part
      const minutePart = currentValue.split(' ')[1]
      const fromMinute = minutePart.split('-')[0].split('/')[0]
      const toMinute = minutePart.split('-')[1] || 0
      setSelectedFromMinute(fromMinute)
      setSelectedToMinute(toMinute)

      let minuteInterval = 0
      if (minutePart.includes('/')) {
        minuteInterval = minutePart.split('/')[1]
        setEveryMinute(minuteInterval)
      }

      if (minuteInterval > 0) {
        setRecurrentType('everyMinute')
        desc += `every <strong>${minuteInterval} ${minuteInterval == 1 ? 'minute' : 'minutes'}</strong><br/>`
      } else if (hourInterval > 0) {
        setRecurrentType('hourly')
        desc += `every <strong>${hourInterval} ${hourInterval == 1 ? 'hour' : 'hours'}</strong><br/>`
      }

      desc += `At <strong>${padWithZero(fromHour)}:${padWithZero(fromMinute)}</strong> till <strong>${padWithZero(toHour)}:${padWithZero(toMinute)}</strong>`
      setDescription(desc)
    }
  }, [currentValue, valuesChanged])

  useEffect(() => {
    if (valuesChanged) {
      const toHour = selectedToHour && selectedFromHour !== selectedToHour ? `-${selectedToHour}` : ''
      const hr = `${selectedFromHour}${toHour}`
      const toMin = selectedToMinute && selectedFromMinute !== selectedToMinute ? `-${selectedToMinute}` : ''
      const min = `${selectedFromMinute}${toMin}`
      let finalStr = ''
      if (recurrentType === 'everyMinute') {
        const everyMin = everyMinute > 0 ? `/${everyMinute}` : ''
        finalStr = `0 ${`${min}${everyMin}`} ${hr} * * *`
      } else if (recurrentType === 'hourly') {
        const everyHr = everyHour > 0 ? `/${everyHour}` : ''
        finalStr = `0 ${min} ${`${hr}${everyHr}`} * * *`
      } else if (recurrentType === 'daily') {
        const day =
          selectedToDay && selectedFromDay !== selectedToDay ? `${selectedFromDay}-${selectedToDay}` : selectedFromDay
        const month =
          selectedToMonth && selectedFromMonth !== selectedToMonth
            ? `${selectedFromMonth}-${selectedToMonth}`
            : selectedToDay
        finalStr = `0 ${min} ${hr} ${day} ${month} *`
      } else if (recurrentType === 'weekly') {
        const day =
          selectedToDay && selectedFromDay !== selectedToDay ? `${selectedFromDay}-${selectedToDay}` : selectedFromDay
        const month =
          selectedToMonth && selectedFromMonth !== selectedToMonth
            ? `${selectedFromMonth}-${selectedToMonth}`
            : selectedFromMonth
        const wkdays = weekDays
          .filter((d) => d.selected)
          .map((d) => d.value)
          .join(',')
        finalStr = `0 ${min} ${hr} ${day} ${month} ${wkdays}`
      } else if (recurrentType === 'monthly') {
        const day = selectedFromDay
        const month =
          selectedToMonth && selectedFromMonth !== selectedToMonth
            ? `${selectedFromMonth}-${selectedToMonth}`
            : selectedFromMonth
        finalStr = `0 ${min} ${hr} ${day} ${month} *`
      }
      setCurrentValue(finalStr)
    }
  }, [
    recurrentType,
    valuesChanged,
    everyHour,
    selectedFromMonth,
    selectedToMonth,
    selectedFromDay,
    selectedToDay,
    weekDays,
    selectedFromHour,
    selectedToHour,
    selectedFromMinute,
    selectedToMinute
  ])

  const handleTimeChange = (time, section) => {
    setValuesChanged(true)
    setErrorMessage('')
    if (section === 'From') {
      if (!time) {
        setErrorMessage('Start time is required')
      } else {
        const hour = parseInt(time.split(':')[0])
        const min = parseInt(time.split(':')[1])
        setSelectedFromHour(hour)
        setSelectedFromMinute(min)
      }
    } else if (section === 'To') {
      if (!compareTimes(`${selectedFromHour}:${selectedFromMinute}`, time)) {
        setErrorMessage('End time should be later than start time.')
      } else if (time) {
        const hour = parseInt(time.split(':')[0])
        const min = parseInt(time.split(':')[1])
        setSelectedToHour(hour)
        setSelectedToMinute(min)
      }
    } else {
      setSelectedInterval(time)
    }
  }

  const getMonthName = (monthNumber) => {
    const month = months.find((m) => m.value == monthNumber)
    return month?.name
  }

  const isValid = () => {
    if (recurrentType === 'weekly') {
      const atleastOneWeekdaySelected = weekDays.some((x) => x.selected)
      if (!atleastOneWeekdaySelected) {
        setErrorMessage('Please select atleast one weekday')
        return false
      }
    }
    return true
  }
  const handleChangeDay = (day, section) => {
    setValuesChanged(true)
    if (section === 'From') {
      setSelectedFromDay(day.value)
    } else {
      setSelectedToDay(day.value)
    }
  }

  const handleChangeMonth = (month, section) => {
    setValuesChanged(true)
    if (section === 'From') {
      setSelectedFromMonth(month.value)
    } else {
      setSelectedToMonth(month.value)
    }
    if (section === 'From') {
      setFromDays(getDaysInMonth(month.value))
    } else if (section === 'To') {
      setToDays(getDaysInMonth(month.value))
    }
  }

  const handleRecurrentChange = (recurrentVal) => {
    setValuesChanged(true)
    setRecurrentType(recurrentVal)
  }

  const handleHourChange = (e) => {
    setErrorMessage('')
    const hour = e.target.value
    if (!hour) {
      setErrorMessage('Every hour input is required')
    } else {
      setEveryHour(hour)
    }
  }

  const handleMinuteChange = () => {
    setErrorMessage('')
    const minute = e.target.value
    if (!minute) {
      setErrorMessage('Every minute input is required')
    } else {
      setEveryMinute(minute)
    }
  }

  const handleWeekdayChange = (weekday) => {
    setErrorMessage('')
    const updatedWeekdays = weekDays.map((day) => {
      if (day.value === weekday) {
        day.selected = !day.selected
      }
      return day
    })
    setWeekdays(updatedWeekdays)
  }

  const handleSaveScheduler = () => {
    if (isValid()) {
      openConfirmModal()
    }
  }

  const openConfirmModal = () => {
    setIsConfirmModalOpen(true)
  }
  const closeConfirmModal = () => {
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

      {!hasSwitch || isSwitchChecked ? (
        editMode ? (
          <>
            <RMButton className={styles.btnCancelEdit} onClick={() => setEditMode(false)}>
              Cancel edit
            </RMButton>
            <RadioGroup value={recurrentType} onChange={handleRecurrentChange}>
              <HStack spacing={6}>
                {recurrentOptions.map((recur) => (
                  <Radio key={recur.key} value={recur.key} size='lg'>
                    {recur.value}
                  </Radio>
                ))}
              </HStack>
            </RadioGroup>
            <Flex className={styles.schedulerItem}>
              <HStack>
                <div className={styles.label}>Start Time</div>
                <TimePicker
                  format='HH:mm'
                  disableClock={true}
                  className={styles.timepicker}
                  hourPlaceholder='HH'
                  minutePlaceholder='mm'
                  // secondPlaceholder='ss'
                  maxDetail='minute'
                  value={`${selectedFromHour}:${selectedFromMinute}`}
                  onChange={(val) => {
                    handleTimeChange(val, 'From')
                  }}
                />
                {(recurrentType === 'daily' || recurrentType === 'weekly' || recurrentType === 'monthly') && (
                  <HStack>
                    <Dropdown
                      placeholder='Select month'
                      options={months}
                      selectedValue={selectedFromMonth}
                      buttonClassName={styles.btnDrodown}
                      menuListClassName={styles.menuList}
                      inlineLabel='Month '
                      onChange={(month) => handleChangeMonth(month, 'From')}
                    />
                    <Dropdown
                      placeholder='Select day'
                      options={fromDays}
                      selectedValue={selectedFromDay}
                      buttonClassName={styles.btnDrodown}
                      menuListClassName={styles.menuList}
                      inlineLabel='Day '
                      onChange={(day) => handleChangeDay(day, 'From')}
                    />
                  </HStack>
                )}
              </HStack>
              <HStack>
                <div className={styles.label}>End Time</div>
                <TimePicker
                  format='HH:mm'
                  disableClock={true}
                  className={styles.timepicker}
                  hourPlaceholder='HH'
                  minutePlaceholder='mm'
                  // secondPlaceholder='ss'
                  maxDetail='minute'
                  value={`${selectedToHour}:${selectedToMinute}`}
                  onChange={(val) => {
                    handleTimeChange(val, 'To')
                  }}
                />
                {(recurrentType === 'daily' || recurrentType === 'weekly' || recurrentType === 'monthly') && (
                  <HStack>
                    <Dropdown
                      placeholder='Select month'
                      options={months}
                      selectedValue={selectedToMonth}
                      buttonClassName={styles.btnDrodown}
                      menuListClassName={styles.menuList}
                      inlineLabel='Month '
                      onChange={(month) => handleChangeMonth(month, 'To')}
                    />
                    {recurrentType !== 'monthly' && (
                      <Dropdown
                        placeholder='Select day'
                        options={toDays}
                        selectedValue={selectedToDay}
                        buttonClassName={styles.btnDrodown}
                        menuListClassName={styles.menuList}
                        inlineLabel='Day '
                        onChange={(day) => handleChangeDay(day, 'To')}
                      />
                    )}
                  </HStack>
                )}
              </HStack>
              {recurrentType === 'hourly' && (
                <HStack>
                  <div className={styles.label}>Every </div>
                  <Input className={styles.numberInput} value={everyHour} type='number' onChange={handleHourChange} />
                  <div className={styles.label}>hours</div>
                </HStack>
              )}
              {recurrentType === 'everyMinute' && (
                <HStack>
                  <div className={styles.label}>Every </div>
                  <Input
                    className={styles.numberInput}
                    value={everyMinute}
                    type='number'
                    onChange={handleMinuteChange}
                  />
                  <div className={styles.label}>Minutes</div>
                </HStack>
              )}
              {recurrentType === 'weekly' && (
                <HStack>
                  <div className={styles.label}>Select weekdays</div>
                  {weekDays.map((weekday) => {
                    return (
                      <RMButton
                        {...(!weekday.selected ? { variant: 'outline' } : {})}
                        //  variant={weekday.selected ? 'solid' : 'outline'}
                        onClick={() => handleWeekdayChange(weekday.value)}>
                        {weekday.name}
                      </RMButton>
                    )
                  })}
                </HStack>
              )}
            </Flex>

            {errorMessage && <Message message={errorMessage} />}

            <HStack>
              {valuesChanged && errorMessage.length === 0 && (
                <>
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
                </>
              )}
            </HStack>
          </>
        ) : (
          <VStack>
            <Text dangerouslySetInnerHTML={{ __html: description }} />
            <RMIconButton
              className={styles.btnEdit}
              icon={HiPencilAlt}
              tooltip='Edit scheduler'
              onClick={() => {
                setEditMode(true)
              }}
            />
          </VStack>
        )
      ) : null}

      {isConfirmModalOpen && (
        <ConfirmModal
          isOpen={isConfirmModalOpen}
          closeModal={() => {
            closeConfirmModal()
          }}
          title={`${confirmTitle} ${currentValue}`}
          onConfirmClick={() => {
            onSave(currentValue)
            closeConfirmModal()
          }}
        />
      )}
    </VStack>
  )
}

export default Scheduler
