import { Flex, HStack, Spinner } from '@chakra-ui/react'
import React, { useState, useEffect } from 'react'

import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import styles from './styles.module.scss'
import Scheduler from './Scheduler'
import NumberInput from '../../components/NumberInput'
import RMSwitch from '../../components/RMSwitch'

function AlertSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()

  const dataObject = [
    {
      key: 'Mail From',
      value: (
        <TextForm
          value={selectedCluster?.config?.mailFrom}
          confirmTitle={`Confirm mail-from to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'mail-from',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Mail To',
      value: (
        <TextForm
          value={selectedCluster?.config?.mailTo}
          confirmTitle={`Confirm mail-to to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'mail-to',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Mail SMTP Address',
      value: (
        <TextForm
          value={selectedCluster?.config?.mailSmtpAddr}
          confirmTitle={`Confirm mail-smtp-addr to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'mail-smtp-addr',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Mail SMTP User',
      value: (
        <TextForm
          value={selectedCluster?.config?.mailSmtpUser}
          confirmTitle={`Confirm mail-smtp-user to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'mail-smtp-user',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Mail SMTP Password',
      value: (
        <TextForm
          value={selectedCluster?.config?.mailSmtpPassword}
          confirmTitle={`Confirm mail-smtp-password to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'mail-smtp-password',
                value: value
              })
            )
          }
        />
      )
    },

    {
      key: 'Mail SMTP TLS Skip Verify',
      value: (
        <RMSwitch
          isChecked={selectedCluster?.config?.mailSmtpTlsSkipVerify}
          isDisabled={user?.grants['cluster-settings'] == false}
          confirmTitle={'Confirm switch settings for mail-smtp-tls-skip-verify?'}
          onChange={() =>
            dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-tls-skip-verify' }))
          }
        />
      )
    },
    {
      key: 'Pushover app token',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertPushoverAppToken}
          confirmTitle={`Confirm pushover app token to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-pushover-app-token',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Pushover user token',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertPushoverUserToken}
          confirmTitle={`Confirm pushover user token to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-pushover-user-token',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Extra alert script path ',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertScript}
          confirmTitle={`Confirm script path to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-script',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Slack Channel',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertSlackChannel}
          confirmTitle={`Confirm slack channel to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-slack-channel',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Slack Url',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertSlackUrl}
          confirmTitle={`Confirm slack url to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-slack-url',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Slack User',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertSlackUser}
          confirmTitle={`Confirm slack user to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-slack-user',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Teams Proxy Url',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertTeamsProxyUrl}
          confirmTitle={`Confirm teams proxy url to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-teams-proxy-url',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Teams State',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertTeamsState}
          confirmTitle={`Confirm teams state to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-teams-state',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Teams Url',
      value: (
        <TextForm
          value={selectedCluster?.config?.alertTeamsUrl}
          confirmTitle={`Confirm teams url to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'alert-teams-url',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Monitoring Alert Trigger',
      value: (
        <TextForm
          value={selectedCluster?.config?.monitoringAlertTrigger}
          confirmTitle={`Confirm monitoring alert trigger to `}
          className={styles.textbox}
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'monitoring-alert-trigger',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Scheduler Alert',
      value: (
        <Scheduler
          user={user}
          value={selectedCluster?.config?.schedulerAlertDisableCron}
          switchConfirmTitle={'Confirm switch settings for scheduler-alert-disable?'}
          isSwitchChecked={selectedCluster?.config?.schedulerAlertDisable}
          confirmTitle={'Confirm save scheduler alert to: '}
          onSwitchChange={() =>
            dispatch(
              switchSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-alert-disable'
              })
            )
          }
          onSave={(value) =>
            dispatch(
              setSetting({
                clusterName: selectedCluster?.name,
                setting: 'scheduler-alert-disable-cron',
                value: value
              })
            )
          }
        />
      )
    },
    {
      key: 'Scheduler Alert Disable Time (seconds)',
      value: (
        <NumberInput
          min={1}
          max={10000}
          defaultValue={3600}
          value={selectedCluster.config.schedulerAlertDisableTime}
          isDisabled={user?.grants['cluster-settings'] == false}
          showEditButton={true}
          onChange={null}
          onConfirm={(value) =>
            openConfirmModal(`Confirm change 'scheduler-alert-disable-time' to: ${value} `, () => () => {
              dispatch(
                setSetting({
                  clusterName: selectedCluster?.name,
                  setting: 'scheduler-alert-disable-time',
                  value: value
                })
              )
            })
          }
        />
      )
    }
  ]

  return (
    <Flex justify='space-between' gap='0'>
      <TableType2 dataArray={dataObject} className={styles.table} />
    </Flex>
  )
}

export default AlertSettings
