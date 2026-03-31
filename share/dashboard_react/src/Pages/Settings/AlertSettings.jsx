import { Box, Flex, HStack, Text } from '@chakra-ui/react'
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

  const helpKey = (label, content) => (
    <HStack spacing={1} align="center" width="fit-content">
      <Text>{label}</Text>
      <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(label, content)} />
    </HStack>
  )

  const helpMailFrom = `**Mail From**\n\nSender address used in alert emails.\nExample: \`replication-manager@company.com\``
  const helpMailTo = `**Mail To**\n\nComma-separated list of recipient addresses for alert emails.\nExample: \`dba@company.com,oncall@company.com\``
  const helpMailSmtp = `**Mail SMTP Address**\n\nSMTP server address in \`host:port\` format.\nExample: \`smtp.company.com:587\``
  const helpMailUser = `**Mail SMTP User**\n\nSMTP authentication username.`
  const helpMailPass = `**Mail SMTP Password**\n\nSMTP authentication password. Stored encrypted.`
  const helpMailTls = `**Mail SMTP TLS Skip Verify**\n\nWhen enabled, TLS certificate verification is skipped for the SMTP connection.\nOnly enable in controlled environments where the certificate cannot be trusted by the system store.`
  const helpPushoverApp = `**Pushover App Token**\n\nApplication API token from your Pushover dashboard.\nRequired to send push notifications via Pushover.`
  const helpPushoverUser = `**Pushover User Token**\n\nUser or group key from your Pushover account.\nIdentifies the recipient of push notifications.`
  const helpAlertScript = `**Extra Alert Script Path**\n\nPath to a custom script executed on every alert.\nThe script receives the alert message as its first argument.\nUseful for integrating with ticketing systems or custom notification pipelines.`
  const helpSlackChannel = `**Slack Channel**\n\nSlack channel name to post alerts to.\nExample: \`#db-alerts\``
  const helpSlackUrl = `**Slack URL**\n\nIncoming webhook URL for your Slack workspace.\nCreate one at \`https://api.slack.com/messaging/webhooks\`.`
  const helpSlackUser = `**Slack User**\n\nDisplay name used as the sender in Slack alert messages.`
  const helpTeamsProxy = `**Teams Proxy URL**\n\nHTTP proxy URL to use when sending alerts to Microsoft Teams.\nLeave empty if no proxy is required.`
  const helpTeamsState = `**Teams State**\n\nOptional state label attached to Teams alert messages.\nUseful for tagging alerts with environment or cluster context.`
  const helpTeamsUrl = `**Teams URL**\n\nIncoming webhook URL for your Microsoft Teams channel.\nCreate one via the Teams channel Connectors settings.`
  const helpAlertTrigger = `**Monitoring Alert Trigger**\n\nComma-separated list of error or warning codes that will trigger an alert notification.\nLeave empty to alert on all state changes.\n\nExample: \`ERR00001,WARN0042\``
  const helpSchedulerAlert = `**Scheduler Alert**\n\nCron schedule that controls when alerting is **active**.\nThe switch enables or disables the schedule entirely.\nWhen the cron window is not active, alerts are suppressed.`
  const helpSchedulerAlertTime = `**Scheduler Alert Disable Time (seconds)**\n\nDuration in seconds that the alert scheduler remains in the disabled state once triggered.\nDefault: **3600** (1 hour).\nUseful to suppress repeated alerts during a known maintenance window.`

  const dataObject = [
    { key: helpKey('Mail From', helpMailFrom), value: (<TextForm value={selectedCluster?.config?.mailFrom} confirmTitle={`Confirm mail-from to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'mail-from', value }))} />) },
    { key: helpKey('Mail To', helpMailTo), value: (<TextForm value={selectedCluster?.config?.mailTo} confirmTitle={`Confirm mail-to to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'mail-to', value }))} />) },
    { key: helpKey('Mail SMTP Address', helpMailSmtp), value: (<TextForm value={selectedCluster?.config?.mailSmtpAddr} confirmTitle={`Confirm mail-smtp-addr to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-addr', value }))} />) },
    { key: helpKey('Mail SMTP User', helpMailUser), value: (<TextForm value={selectedCluster?.config?.mailSmtpUser} confirmTitle={`Confirm mail-smtp-user to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-user', value }))} />) },
    { key: helpKey('Mail SMTP Password', helpMailPass), value: (<TextForm value={selectedCluster?.config?.mailSmtpPassword} confirmTitle={`Confirm mail-smtp-password to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-password', value }))} />) },
    { key: helpKey('Mail SMTP TLS Skip Verify', helpMailTls), value: (<RMSwitch isChecked={selectedCluster?.config?.mailSmtpTlsSkipVerify} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for mail-smtp-tls-skip-verify?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-tls-skip-verify' }))} />) },
    { key: helpKey('Pushover App Token', helpPushoverApp), value: (<TextForm value={selectedCluster?.config?.alertPushoverAppToken} confirmTitle={`Confirm pushover app token to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-pushover-app-token', value }))} />) },
    { key: helpKey('Pushover User Token', helpPushoverUser), value: (<TextForm value={selectedCluster?.config?.alertPushoverUserToken} confirmTitle={`Confirm pushover user token to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-pushover-user-token', value }))} />) },
    { key: helpKey('Extra Alert Script Path', helpAlertScript), value: (<TextForm value={selectedCluster?.config?.alertScript} confirmTitle={`Confirm script path to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-script', value: btoa(value) }))} />) },
    { key: helpKey('Slack Channel', helpSlackChannel), value: (<TextForm value={selectedCluster?.config?.alertSlackChannel} confirmTitle={`Confirm slack channel to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-slack-channel', value }))} />) },
    { key: helpKey('Slack URL', helpSlackUrl), value: (<TextForm value={selectedCluster?.config?.alertSlackUrl} confirmTitle={`Confirm slack url to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-slack-url', value: btoa(value) }))} />) },
    { key: helpKey('Slack User', helpSlackUser), value: (<TextForm value={selectedCluster?.config?.alertSlackUser} confirmTitle={`Confirm slack user to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-slack-user', value }))} />) },
    { key: helpKey('Teams Proxy URL', helpTeamsProxy), value: (<TextForm value={selectedCluster?.config?.alertTeamsProxyUrl} confirmTitle={`Confirm teams proxy url to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-teams-proxy-url', value: btoa(value) }))} />) },
    { key: helpKey('Teams State', helpTeamsState), value: (<TextForm value={selectedCluster?.config?.alertTeamsState} confirmTitle={`Confirm teams state to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-teams-state', value }))} />) },
    { key: helpKey('Teams URL', helpTeamsUrl), value: (<TextForm value={selectedCluster?.config?.alertTeamsUrl} confirmTitle={`Confirm teams url to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'alert-teams-url', value: btoa(value) }))} />) },
    { key: helpKey('Monitoring Alert Trigger', helpAlertTrigger), value: (<TextForm value={selectedCluster?.config?.monitoringAlertTrigger} confirmTitle={`Confirm monitoring alert trigger to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'monitoring-alert-trigger', value }))} />) },
    {
      key: helpKey('Scheduler Alert', helpSchedulerAlert),
      value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerAlertDisableCron} switchConfirmTitle={'Confirm switch settings for scheduler-alert-disable?'} isSwitchChecked={selectedCluster?.config?.schedulerAlertDisable} confirmTitle={'Confirm save scheduler alert to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable' }))} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-cron', value }))} />)
    },
    {
      key: helpKey('Scheduler Alert Disable Time (seconds)', helpSchedulerAlertTime),
      value: (<NumberInput min={1} max={10000} defaultValue={3600} value={selectedCluster.config.schedulerAlertDisableTime} isDisabled={user?.grants['cluster-settings'] == false} showEditButton={true} onChange={null} onConfirm={(value) => openConfirmModal(`Confirm change 'scheduler-alert-disable-time' to: ${value} `, () => () => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-time', value })) })} />)
    }
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2 dataArray={dataObject} className={styles.table} />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default AlertSettings
