import { Box, Flex } from '@chakra-ui/react'
import React, { useState } from 'react'
import { useDispatch } from 'react-redux'
import TableType2 from '../../components/TableType2'
import { setSetting, switchSetting } from '../../redux/settingsSlice'
import TextForm from '../../components/TextForm'
import styles from './styles.module.scss'
import Scheduler from './Scheduler'
import NumberInput from '../../components/NumberInput'
import RMSwitch from '../../components/RMSwitch'
import CommonModal from '../../components/Modals/CommonModal'
import modalStyles from '../../components/Modals/styles.module.scss'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { HiQuestionMarkCircle } from 'react-icons/hi'
import RMIconButton from '../../components/RMIconButton'

function AlertSettings({ selectedCluster, user, openConfirmModal }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} />
  )

  const hMailFrom = `**Mail From**\n\nSender address used in alert emails.\nExample: \`replication-manager@company.com\`\n\nConfig: \`mail-from\``
  const hMailTo = `**Mail To**\n\nComma-separated list of recipient addresses for alert emails.\n\nConfig: \`mail-to\``
  const hMailSmtp = `**Mail SMTP Address**\n\nSMTP server in \`host:port\` format.\nExample: \`smtp.company.com:587\`\n\nConfig: \`mail-smtp-addr\``
  const hMailUser = `**Mail SMTP User**\n\nSMTP authentication username.\n\nConfig: \`mail-smtp-user\``
  const hMailPass = `**Mail SMTP Password**\n\nSMTP authentication password.\n\nConfig: \`mail-smtp-password\``
  const hMailTls = `**Mail SMTP TLS Skip Verify**\n\nSkips TLS certificate verification for the SMTP connection.\nOnly enable in controlled environments.\n\nConfig: \`mail-smtp-tls-skip-verify\``
  const hPushoverApp = `**Pushover App Token**\n\nApplication API token from your Pushover dashboard.\nRequired to send push notifications via Pushover.\n\nConfig: \`alert-pushover-app-token\``
  const hPushoverUser = `**Pushover User Token**\n\nUser or group key from your Pushover account.\n\nConfig: \`alert-pushover-user-token\``
  const hAlertScript = `**Extra Alert Script Path**\n\nPath to a custom script executed on every alert.\nThe script receives the alert message as its first argument.\n\nConfig: \`alert-script\``
  const hSlackChannel = `**Slack Channel**\n\nSlack channel name to post alerts to.\nExample: \`#db-alerts\`\n\nConfig: \`alert-slack-channel\``
  const hSlackUrl = `**Slack URL**\n\nIncoming webhook URL for your Slack workspace.\n\nConfig: \`alert-slack-url\``
  const hSlackUser = `**Slack User**\n\nDisplay name used as the sender in Slack alert messages.\n\nConfig: \`alert-slack-user\``
  const hTeamsProxy = `**Teams Proxy URL**\n\nHTTP proxy URL for Microsoft Teams alerts. Leave empty if no proxy is needed.\n\nConfig: \`alert-teams-proxy-url\``
  const hTeamsState = `**Teams State**\n\nOptional state label attached to Teams alert messages.\n\nConfig: \`alert-teams-state\``
  const hTeamsUrl = `**Teams URL**\n\nIncoming webhook URL for your Microsoft Teams channel.\n\nConfig: \`alert-teams-url\``
  const hAlertTrigger = `**Monitoring Alert Trigger**\n\nComma-separated list of error codes that trigger alert notifications.\nLeave empty to alert on all state changes.\n\nExample: \`ERR00001,WARN0042\`\n\nConfig: \`monitoring-alert-trigger\``
  const hSchedulerAlert = `**Scheduler Alert**\n\nCron schedule that controls when alerting is active.\nWhen the cron window is not active, alerts are suppressed.\n\nConfig: \`scheduler-alert-disable / scheduler-alert-disable-cron\``
  const hSchedulerAlertTime = `**Scheduler Alert Disable Time (seconds)**\n\nDuration in seconds the alert scheduler remains disabled once triggered.\nDefault: **3600** (1 hour).\n\nConfig: \`scheduler-alert-disable-time\``

  const tf = (configKey, setting, title, encode) => <TextForm value={selectedCluster?.config?.[configKey]} confirmTitle={`Confirm ${title} to `} className={styles.textbox} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value: encode ? btoa(v) : v }))} />

  const dataObject = [
    { key: 'Mail From', help: h(hMailFrom, 'Mail From'), value: tf('mailFrom', 'mail-from', 'mail-from') },
    { key: 'Mail To', help: h(hMailTo, 'Mail To'), value: tf('mailTo', 'mail-to', 'mail-to') },
    { key: 'Mail SMTP Address', help: h(hMailSmtp, 'Mail SMTP Address'), value: tf('mailSmtpAddr', 'mail-smtp-addr', 'mail-smtp-addr') },
    { key: 'Mail SMTP User', help: h(hMailUser, 'Mail SMTP User'), value: tf('mailSmtpUser', 'mail-smtp-user', 'mail-smtp-user') },
    { key: 'Mail SMTP Password', help: h(hMailPass, 'Mail SMTP Password'), value: tf('mailSmtpPassword', 'mail-smtp-password', 'mail-smtp-password') },
    { key: 'Mail SMTP TLS Skip Verify', help: h(hMailTls, 'Mail SMTP TLS Skip Verify'), value: (<RMSwitch isChecked={selectedCluster?.config?.mailSmtpTlsSkipVerify} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for mail-smtp-tls-skip-verify?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-tls-skip-verify' }))} />) },
    { key: 'Pushover App Token', help: h(hPushoverApp, 'Pushover App Token'), value: tf('alertPushoverAppToken', 'alert-pushover-app-token', 'pushover app token') },
    { key: 'Pushover User Token', help: h(hPushoverUser, 'Pushover User Token'), value: tf('alertPushoverUserToken', 'alert-pushover-user-token', 'pushover user token') },
    { key: 'Extra Alert Script Path', help: h(hAlertScript, 'Extra Alert Script Path'), value: tf('alertScript', 'alert-script', 'script path', true) },
    { key: 'Slack Channel', help: h(hSlackChannel, 'Slack Channel'), value: tf('alertSlackChannel', 'alert-slack-channel', 'slack channel') },
    { key: 'Slack URL', help: h(hSlackUrl, 'Slack URL'), value: tf('alertSlackUrl', 'alert-slack-url', 'slack url', true) },
    { key: 'Slack User', help: h(hSlackUser, 'Slack User'), value: tf('alertSlackUser', 'alert-slack-user', 'slack user') },
    { key: 'Teams Proxy URL', help: h(hTeamsProxy, 'Teams Proxy URL'), value: tf('alertTeamsProxyUrl', 'alert-teams-proxy-url', 'teams proxy url', true) },
    { key: 'Teams State', help: h(hTeamsState, 'Teams State'), value: tf('alertTeamsState', 'alert-teams-state', 'teams state') },
    { key: 'Teams URL', help: h(hTeamsUrl, 'Teams URL'), value: tf('alertTeamsUrl', 'alert-teams-url', 'teams url', true) },
    { key: 'Monitoring Alert Trigger', help: h(hAlertTrigger, 'Monitoring Alert Trigger'), value: tf('monitoringAlertTrigger', 'monitoring-alert-trigger', 'monitoring alert trigger') },
    { key: 'Scheduler Alert', help: h(hSchedulerAlert, 'Scheduler Alert'), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerAlertDisableCron} switchConfirmTitle={'Confirm switch settings for scheduler-alert-disable?'} isSwitchChecked={selectedCluster?.config?.schedulerAlertDisable} confirmTitle={'Confirm save scheduler alert to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable' }))} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-cron', value: v }))} />) },
    { key: 'Scheduler Alert Disable Time (seconds)', help: h(hSchedulerAlertTime, 'Scheduler Alert Disable Time'), value: (<NumberInput min={1} max={10000} defaultValue={3600} value={selectedCluster.config.schedulerAlertDisableTime} isDisabled={user?.grants['cluster-settings'] == false} showEditButton={true} onChange={null} onConfirm={(v) => openConfirmModal(`Confirm change 'scheduler-alert-disable-time' to: ${v} `, () => () => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-time', value: v })) })} />) },
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} helpColumn={true} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default AlertSettings
