import { Flex, HStack, VStack } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'
import Dropdown from '../Dropdown'
import styles from './styles.module.scss'
import TimePicker from 'react-time-picker'

function Scheduler({ value }) {
  const [months, setMonths] = useState([
    { name: 'All', value: '*' },
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
    { name: 'All', value: '*' },
    { name: 'Sun', value: 0 },
    { name: 'Mon', value: 1 },
    { name: 'Tue', value: 2 },
    { name: 'Wed', value: 3 },
    { name: 'Thu', value: 4 },
    { name: 'Fri', value: 5 },
    { name: 'Sat', value: 6 }
  ])
  const [monthDays, setMonthDays] = useState([
    { name: 'All', value: '*' },
    ...Array.from({ length: 31 }, (_, i) => {
      return { name: `${i + 1}`, value: i + 1 }
    })
  ])

  const [sections, setSections] = useState(['From', 'To', 'Interval'])

  const [selectedFromMonth, setSelectedFromMonth] = useState({ name: 'All', value: '*' })
  const [selectedToMonth, setSelectedToMonth] = useState({ name: 'All', value: '*' })

  const [selectedFromWeekday, setSelectedFromWeekday] = useState({ name: 'All', value: '*' })
  const [selectedToWeekday, setSelectedToWeekday] = useState({ name: 'All', value: '*' })

  const [selectedFromDay, setSelectedFromDay] = useState({ name: 'All', value: '*' })
  const [selectedToDay, setSelectedToDay] = useState({ name: 'All', value: '*' })

  const [selectedFromTime, setSelectedFromTime] = useState('0:0:0')
  const [selectedToTime, setSelectedToTime] = useState('0:0:0')
  const [selectedInterval, setSelectedInterval] = useState('0:0:0')

  useEffect(() => {
    if (value) {
      console.log('value::', value)
      //(value)

      ///from
      const fromHour = value.split(' ')[2].split('-')[0].split('/')[0] ?? 0
      const fromMin = value.split(' ')[1].split('-')[0].split('/')[0] ?? 0
      const fromSecond = value.split(' ')[0].split('-')[0].split('/')[0] ?? 0
      setSelectedFromTime(`${fromHour}:${fromMin}:${fromSecond}`)

      const fromDay = value.split(' ')[3].split('-')[0].split('/')[0]
      setSelectedFromDay(monthDays.find((x) => x.value == fromDay))

      const fromMonth = value.split(' ')[4].split('-')[0].split('/')[0]
      setSelectedFromMonth(months.find((x) => x.value == fromMonth))

      const fromWeek = value.split(' ')[5].split('-')[0].split('/')[0]
      setSelectedFromWeekday(weekDays.find((x) => x.value == fromWeek))

      ///to
      const toHour = value.split(' ')[2].split('-')[1] ?? 0
      const toMin = value.split(' ')[1].split('-')[1] ?? 0
      const toSecond = value.split(' ')[0].split('-')[1] ?? 0
      console.log('toHour::', toHour, toMin, toSecond)

      setSelectedToTime(`${toHour}:${toMin}:${toSecond}`)

      const toDay = value.split(' ')[3].split('-')[1]
      setSelectedToDay(monthDays.find((x) => x.value == toDay))

      const toMonth = value.split(' ')[4].split('-')[1]
      setSelectedToMonth(months.find((x) => x.value == toMonth))

      const toWeek = value.split(' ')[5].split('-')[1]
      setSelectedToWeekday(weekDays.find((x) => x.value == toWeek))

      ///interval
      const intervalHour = value.split(' ')[2].split('/')[1] ?? 0
      const intervalMin = value.split(' ')[1].split('/')[1] ?? 0
      const intevalSecond = value.split(' ')[0].split('/')[1] ?? 0
      setSelectedInterval(`${intervalHour}:${intervalMin}:${intevalSecond}`)
    }
  }, [value])

  const handleTimeChange = (time, section) => {
    if (section === 'From') {
      setSelectedFromTime(time)
    } else if (section === 'To') {
      setSelectedToTime(time)
    } else {
      setSelectedInterval(time)
    }
  }
  const handleChangeDay = (day, section) => {
    if (section === 'From') {
      setSelectedFromDay(day)
    } else {
      setSelectedToDay(day)
    }
  }

  const handleChangeMonth = (month, section) => {
    if (section === 'From') {
      setSelectedFromMonth(month)
    } else {
      setSelectedToMonth(month)
    }
  }

  const handleChangeWeek = (week, section) => {
    if (section === 'From') {
      setSelectedFromWeekday(week)
    } else {
      setSelectedToWeekday(week)
    }
  }
  console.log('selectedFromTime::', selectedFromTime)

  return (
    <VStack className={styles.scheduler}>
      {sections.map((section) => (
        <Flex className={styles.schedulerItem}>
          <div className={styles.label}>{section}</div>
          <TimePicker
            className={styles.timepicker}
            onChange={(val) => {
              handleTimeChange(val, section)
            }}
            value={section === 'From' ? selectedFromTime : section === 'To' ? selectedToTime : selectedInterval}
            format='h:m:s'
            hourPlaceholder='hh'
            minutePlaceholder='mm'
            secondPlaceholder='ss'
            disableClock={true}
            maxDetail='second'
          />
          {section !== 'Interval' && (
            <HStack className={styles.dayMonthWeek}>
              <Dropdown
                options={months}
                selectedValue={section === 'From' ? selectedFromMonth?.value : selectedToMonth?.value}
                buttonClassName={styles.btnDrodown}
                menuListClassName={styles.menuList}
                inlineLabel='Month '
                onChange={(month) => handleChangeMonth(month, section)}
              />
              <Dropdown
                options={weekDays}
                selectedValue={section === 'From' ? selectedFromWeekday?.value : selectedToWeekday?.value}
                buttonClassName={styles.btnDrodown}
                menuListClassName={styles.menuList}
                inlineLabel='Week '
                onChange={(weekday) => handleChangeWeek(weekday, section)}
              />
              <Dropdown
                options={monthDays}
                selectedValue={section === 'From' ? selectedFromDay?.value : selectedToDay?.value}
                buttonClassName={styles.btnDrodown}
                menuListClassName={styles.menuList}
                inlineLabel='Day '
                onChange={(day) => handleChangeDay(day, section)}
              />
            </HStack>
          )}
        </Flex>
      ))}
    </VStack>
  )
}

export default Scheduler
