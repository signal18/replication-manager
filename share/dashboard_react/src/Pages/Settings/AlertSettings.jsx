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

// the Alerts tab's sub-tabs (#2268): each a group of the alert settings rows, by their label;
// Cloud18 Support only for a Cloud18 cluster (its rows come from the former Cloud18 section)
export const ALERT_GROUPS = [
  { name: 'Mail', keys: ['Mail From', 'Mail To', 'Mail SMTP Address', 'Mail SMTP User', 'Mail SMTP Password', 'Mail SMTP TLS Skip Verify'] },
  { name: 'Slack', keys: ['Slack Channel', 'Slack URL', 'Slack User'] },
  { name: 'Teams', keys: ['Teams Proxy URL', 'Teams State', 'Teams URL'] },
  { name: 'Pushover', keys: ['Pushover App Token', 'Pushover User Token'] },
  { name: 'Script', keys: ['Extra Alert Script Path', 'Alert Script Timeout (seconds)'] },
  { name: 'Alertmanager', keys: ['Alertmanager', 'Alertmanager URL', 'Alertmanager User', 'Alertmanager Password', 'Alertmanager Bearer Token', 'Alertmanager TLS Skip Verify', 'Alertmanager Timeout (seconds)', 'Alertmanager Resend Interval (seconds)'] },
  { name: 'Telegram', keys: ['Telegram', 'Telegram Bot Token', 'Telegram Chat Id', 'Telegram Parse Mode', 'Telegram API URL', 'Telegram Timeout (seconds)'] },
  { name: 'Triggers', keys: ['Monitoring Alert Trigger', 'Scheduler Alert', 'Scheduler Alert Disable Time (seconds)', 'Alert Queue Limit', 'Alert Send Timeout (seconds)'] },
  { name: 'Cloud18 Support', keys: ['Cloud18 Alert', 'Cloud18 Alert Channel', 'Cloud18 Alert URL', 'Cloud18 Alert User'], cloud18: true }
]

function AlertSettings({ selectedCluster, user, openConfirmModal, group }) {
  const dispatch = useDispatch()
  const [action, setAction] = useState({ title: '', body: <></> })
  const [isCommonModalOpen, setIsCommonModalOpen] = useState(false)

  const openInfoModal = (title, content) => {
    setAction({ title, body: <Box className={modalStyles.infoTooltip}><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Box> })
    setIsCommonModalOpen(true)
  }

  const h = (content, title) => (
    <RMIconButton icon={HiQuestionMarkCircle} onClick={() => openInfoModal(title, content)} iconFontsize='1rem' variant='ghost' style={{ opacity: 0.5, minWidth: '1.5rem', height: '1.5rem' }} />
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

  const hAlertQueue = `**Alert Queue Limit**\n\nMaximum alerts waiting to be sent, per channel. When a channel does not keep up (down, slow), the oldest waiting alert is dropped and counted, and state WARN0360 opens. The monitor never waits on a channel.\n\nDefault: **1000**\n\nConfig: \`alert-queue-limit\``
  const hAlertSendTimeout = `**Alert Send Timeout**\n\nTimeout in seconds of one send to Teams or Pushover.\n\nDefault: **10**\n\nConfig: \`alert-send-timeout\``
  const hAlertScriptTimeout = `**Alert Script Timeout**\n\nThe alert script is killed after this many seconds.\n\nDefault: **300**\n\nConfig: \`alert-script-timeout\``
  const hAm = `**Alertmanager**\n\nSends the states of Monitoring Alert Trigger to Prometheus Alertmanager (v2 API): fired when the state opens, posted again every resend interval while it stays open, resolved when it clears.\n\nLabels: alertname and code (the state code), cluster, server, severity (critical, warning), domain.\n\nConfig: \`alert-alertmanager\``
  const hAmUrl = `**Alertmanager URL**\n\nAlertmanager base URLs, comma separated: every peer of an Alertmanager cluster gets the alerts.\nExample: \`http://am1:9093,http://am2:9093\`\n\nConfig: \`alert-alertmanager-url\``
  const hAmUser = `**Alertmanager User**\n\nBasic auth user, with the password.\n\nConfig: \`alert-alertmanager-user\``
  const hAmPass = `**Alertmanager Password**\n\nBasic auth password: encrypted in the configuration, never logged.\n\nConfig: \`alert-alertmanager-password\``
  const hAmToken = `**Alertmanager Bearer Token**\n\nSent as Authorization: Bearer, wins over basic auth: encrypted in the configuration, never logged.\n\nConfig: \`alert-alertmanager-bearer-token\``
  const hAmTls = `**Alertmanager TLS Skip Verify**\n\nDoes not verify the TLS certificate of Alertmanager.\n\nConfig: \`alert-alertmanager-tls-skip-verify\``
  const hAmTimeout = `**Alertmanager Timeout**\n\nTimeout in seconds of one post.\n\nDefault: **10**\n\nConfig: \`alert-alertmanager-timeout\``
  const hAmResend = `**Alertmanager Resend Interval**\n\nThe firing alerts are posted again at this interval, which must stay below the resolve_timeout of Alertmanager (5 minutes by default).\n\nDefault: **60**\n\nConfig: \`alert-alertmanager-resend-interval\``
  const hTg = `**Telegram**\n\nSends the alerts (the states of Monitoring Alert Trigger and the server state changes, as the mail) through a Telegram bot.\n\nConfig: \`alert-telegram\``
  const hTgToken = `**Telegram Bot Token**\n\nThe token BotFather gives: encrypted in the configuration, never logged.\n\nConfig: \`alert-telegram-bot-token\``
  const hTgChat = `**Telegram Chat Id**\n\nChat ids, comma separated: a user or group id, -100... for a channel, or @channelname. The bot must be a member of the chat.\n\nConfig: \`alert-telegram-chat-id\``
  const hTgMode = `**Telegram Parse Mode**\n\nEmpty for plain text, or \`MarkdownV2\` (the title in bold, the text escaped).\n\nConfig: \`alert-telegram-parse-mode\``
  const hTgApi = `**Telegram API URL**\n\nBot API base URL.\n\nDefault: \`https://api.telegram.org\`\n\nConfig: \`alert-telegram-api-url\``
  const hTgTimeout = `**Telegram Timeout**\n\nTimeout in seconds of one send.\n\nDefault: **10**\n\nConfig: \`alert-telegram-timeout\``

  const sw = (configKey, setting, title) => (<RMSwitch isChecked={selectedCluster?.config?.[configKey]} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={`Confirm switch settings for ${title}?`} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting }))} />)
  const num = (configKey, setting, min, max, def) => (<NumberInput min={min} max={max} defaultValue={def} value={selectedCluster?.config?.[configKey]} isDisabled={user?.grants['cluster-settings'] == false} showEditButton={true} onChange={null} onConfirm={(v) => openConfirmModal(`Confirm change '${setting}' to: ${v} `, () => () => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value: v })) })} />)

  const tf = (configKey, setting, title, encode) => <TextForm value={selectedCluster?.config?.[configKey]} confirmTitle={`Confirm ${title} to `} className={styles.textbox} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting, value: encode ? btoa(v) : v }))} />

  const hAlert = `**Cloud18 Alert**\n\nEnables Cloud18 alert notifications via Slack.\nWhen enabled, cluster events are forwarded to the configured Slack channel.\n\nConfig: \`cloud18-alert\``
  const hAlertChannel = `**Cloud18 Alert Channel**\n\nSlack channel for Cloud18 alert notifications.\nExample: \`#cloud18-alerts\`\n\nConfig: \`cloud18-alert-slack-channel\``
  const hAlertUrl = `**Cloud18 Alert URL**\n\nSlack incoming webhook URL for Cloud18 alerts.\n\nConfig: \`cloud18-alert-slack-url\``
  const hAlertUser = `**Cloud18 Alert User**\n\nSlack display name used as the sender for Cloud18 alert messages.\n\nConfig: \`cloud18-alert-slack-user\``

  const dataObject = [
    { key: 'Mail From', help: h(hMailFrom, 'Mail From'), value: tf('mailFrom', 'mail-from', 'mail-from') },
    { key: 'Mail To', help: h(hMailTo, 'Mail To'), value: tf('mailTo', 'mail-to', 'mail-to') },
    { key: 'Mail SMTP Address', help: h(hMailSmtp, 'Mail SMTP Address'), value: tf('mailSmtpAddr', 'mail-smtp-addr', 'mail-smtp-addr') },
    { key: 'Mail SMTP User', help: h(hMailUser, 'Mail SMTP User'), value: tf('mailSmtpUser', 'mail-smtp-user', 'mail-smtp-user') },
    { key: 'Mail SMTP Password', help: h(hMailPass, 'Mail SMTP Password'), value: tf('mailSmtpPassword', 'mail-smtp-password', 'mail-smtp-password') },
    { clusterSetting: 'mail-smtp-tls-skip-verify', key: 'Mail SMTP TLS Skip Verify', help: h(hMailTls, 'Mail SMTP TLS Skip Verify'), value: (<RMSwitch isChecked={selectedCluster?.config?.mailSmtpTlsSkipVerify} isDisabled={user?.grants['cluster-settings'] == false} confirmTitle={'Confirm switch settings for mail-smtp-tls-skip-verify?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'mail-smtp-tls-skip-verify' }))} />) },
    { key: 'Pushover App Token', help: h(hPushoverApp, 'Pushover App Token'), value: tf('alertPushoverAppToken', 'alert-pushover-app-token', 'pushover app token') },
    { key: 'Pushover User Token', help: h(hPushoverUser, 'Pushover User Token'), value: tf('alertPushoverUserToken', 'alert-pushover-user-token', 'pushover user token') },
    { key: 'Extra Alert Script Path', help: h(hAlertScript, 'Extra Alert Script Path'), value: tf('alertScript', 'alert-script', 'script path', true) },
    { clusterSetting: 'alert-script-timeout', key: 'Alert Script Timeout (seconds)', help: h(hAlertScriptTimeout, 'Alert Script Timeout'), value: num('alertScriptTimeout', 'alert-script-timeout', 1, 86400, 300) },
    { clusterSetting: 'alert-queue-limit', key: 'Alert Queue Limit', help: h(hAlertQueue, 'Alert Queue Limit'), value: num('alertQueueLimit', 'alert-queue-limit', 1, 1000000, 1000) },
    { clusterSetting: 'alert-send-timeout', key: 'Alert Send Timeout (seconds)', help: h(hAlertSendTimeout, 'Alert Send Timeout'), value: num('alertSendTimeout', 'alert-send-timeout', 1, 600, 10) },
    { clusterSetting: 'alert-alertmanager', key: 'Alertmanager', help: h(hAm, 'Alertmanager'), value: sw('alertAlertmanager', 'alert-alertmanager', 'alert-alertmanager') },
    { clusterSetting: 'alert-alertmanager-url', key: 'Alertmanager URL', help: h(hAmUrl, 'Alertmanager URL'), value: tf('alertAlertmanagerUrl', 'alert-alertmanager-url', 'alertmanager url', true) },
    { clusterSetting: 'alert-alertmanager-user', key: 'Alertmanager User', help: h(hAmUser, 'Alertmanager User'), value: tf('alertAlertmanagerUser', 'alert-alertmanager-user', 'alertmanager user') },
    { clusterSetting: 'alert-alertmanager-password', key: 'Alertmanager Password', help: h(hAmPass, 'Alertmanager Password'), value: tf('alertAlertmanagerPassword', 'alert-alertmanager-password', 'alertmanager password', true) },
    { clusterSetting: 'alert-alertmanager-bearer-token', key: 'Alertmanager Bearer Token', help: h(hAmToken, 'Alertmanager Bearer Token'), value: tf('alertAlertmanagerBearerToken', 'alert-alertmanager-bearer-token', 'alertmanager bearer token', true) },
    { clusterSetting: 'alert-alertmanager-tls-skip-verify', key: 'Alertmanager TLS Skip Verify', help: h(hAmTls, 'Alertmanager TLS Skip Verify'), value: sw('alertAlertmanagerTlsSkipVerify', 'alert-alertmanager-tls-skip-verify', 'alert-alertmanager-tls-skip-verify') },
    { clusterSetting: 'alert-alertmanager-timeout', key: 'Alertmanager Timeout (seconds)', help: h(hAmTimeout, 'Alertmanager Timeout'), value: num('alertAlertmanagerTimeout', 'alert-alertmanager-timeout', 1, 600, 10) },
    { clusterSetting: 'alert-alertmanager-resend-interval', key: 'Alertmanager Resend Interval (seconds)', help: h(hAmResend, 'Alertmanager Resend Interval'), value: num('alertAlertmanagerResendInterval', 'alert-alertmanager-resend-interval', 10, 3600, 60) },
    { clusterSetting: 'alert-telegram', key: 'Telegram', help: h(hTg, 'Telegram'), value: sw('alertTelegram', 'alert-telegram', 'alert-telegram') },
    { clusterSetting: 'alert-telegram-bot-token', key: 'Telegram Bot Token', help: h(hTgToken, 'Telegram Bot Token'), value: tf('alertTelegramBotToken', 'alert-telegram-bot-token', 'telegram bot token', true) },
    { clusterSetting: 'alert-telegram-chat-id', key: 'Telegram Chat Id', help: h(hTgChat, 'Telegram Chat Id'), value: tf('alertTelegramChatId', 'alert-telegram-chat-id', 'telegram chat id') },
    { clusterSetting: 'alert-telegram-parse-mode', key: 'Telegram Parse Mode', help: h(hTgMode, 'Telegram Parse Mode'), value: tf('alertTelegramParseMode', 'alert-telegram-parse-mode', 'telegram parse mode') },
    { clusterSetting: 'alert-telegram-api-url', key: 'Telegram API URL', help: h(hTgApi, 'Telegram API URL'), value: tf('alertTelegramApiUrl', 'alert-telegram-api-url', 'telegram api url', true) },
    { clusterSetting: 'alert-telegram-timeout', key: 'Telegram Timeout (seconds)', help: h(hTgTimeout, 'Telegram Timeout'), value: num('alertTelegramTimeout', 'alert-telegram-timeout', 1, 600, 10) },
    { key: 'Slack Channel', help: h(hSlackChannel, 'Slack Channel'), value: tf('alertSlackChannel', 'alert-slack-channel', 'slack channel') },
    { key: 'Slack URL', help: h(hSlackUrl, 'Slack URL'), value: tf('alertSlackUrl', 'alert-slack-url', 'slack url', true) },
    { key: 'Slack User', help: h(hSlackUser, 'Slack User'), value: tf('alertSlackUser', 'alert-slack-user', 'slack user') },
    { key: 'Teams Proxy URL', help: h(hTeamsProxy, 'Teams Proxy URL'), value: tf('alertTeamsProxyUrl', 'alert-teams-proxy-url', 'teams proxy url', true) },
    { key: 'Teams State', help: h(hTeamsState, 'Teams State'), value: tf('alertTeamsState', 'alert-teams-state', 'teams state') },
    { key: 'Teams URL', help: h(hTeamsUrl, 'Teams URL'), value: tf('alertTeamsUrl', 'alert-teams-url', 'teams url', true) },
    { key: 'Monitoring Alert Trigger', help: h(hAlertTrigger, 'Monitoring Alert Trigger'), value: tf('monitoringAlertTrigger', 'monitoring-alert-trigger', 'monitoring alert trigger') },
    { key: 'Scheduler Alert', help: h(hSchedulerAlert, 'Scheduler Alert'), value: (<Scheduler user={user} value={selectedCluster?.config?.schedulerAlertDisableCron} switchConfirmTitle={'Confirm switch settings for scheduler-alert-disable?'} isSwitchChecked={selectedCluster?.config?.schedulerAlertDisable} confirmTitle={'Confirm save scheduler alert to: '} onSwitchChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable' }))} onSave={(v) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-cron', value: v }))} />) },
    { clusterSetting: 'scheduler-alert-disable-time', key: 'Scheduler Alert Disable Time (seconds)', help: h(hSchedulerAlertTime, 'Scheduler Alert Disable Time'), value: (<NumberInput min={1} max={10000} defaultValue={3600} value={selectedCluster?.config?.schedulerAlertDisableTime} isDisabled={user?.grants['cluster-settings'] == false} showEditButton={true} onChange={null} onConfirm={(v) => openConfirmModal(`Confirm change 'scheduler-alert-disable-time' to: ${v} `, () => () => { dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'scheduler-alert-disable-time', value: v })) })} />) },
    ...(selectedCluster?.config?.cloud18 ? [
      { clusterSetting: 'cloud18-alert', key: 'Cloud18 Alert', help: h(hAlert, 'Cloud18 Alert'), value: (<RMSwitch confirmTitle={'Confirm switch settings for cloud18-alert?'} onChange={() => dispatch(switchSetting({ clusterName: selectedCluster?.name, setting: 'cloud18-alert' }))} isDisabled={user?.grants['cluster-settings'] == false} isChecked={selectedCluster?.config?.cloud18Alert} />) },
      { clusterSetting: 'cloud18-alert-slack-channel', key: 'Cloud18 Alert Channel', help: h(hAlertChannel, 'Cloud18 Alert Channel'), value: (<TextForm value={selectedCluster?.config?.cloud18AlertSlackChannel} confirmTitle={`Confirm slack channel to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'cloud18-alert-slack-channel', value }))} />) },
      { clusterSetting: 'cloud18-alert-slack-url', key: 'Cloud18 Alert URL', help: h(hAlertUrl, 'Cloud18 Alert URL'), value: (<TextForm value={selectedCluster?.config?.cloud18AlertSlackUrl} confirmTitle={`Confirm slack url to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'cloud18-alert-slack-url', value: btoa(value) }))} />) },
      { clusterSetting: 'cloud18-alert-slack-user', key: 'Cloud18 Alert User', help: h(hAlertUser, 'Cloud18 Alert User'), value: (<TextForm value={selectedCluster?.config?.cloud18AlertSlackUser} confirmTitle={`Confirm slack user to `} className={styles.textbox} onSave={(value) => dispatch(setSetting({ clusterName: selectedCluster?.name, setting: 'cloud18-alert-slack-user', value }))} />) },
    ] : []),
  ]

  return (
    <>
      <Flex justify='space-between' gap='0'>
        <TableType2
          dataArray={group ? dataObject.filter((row) => (ALERT_GROUPS.find((g) => g.name === group)?.keys || []).includes(row.key)) : dataObject}
          className={styles.tableWithHelp}
          helpColumn={true}
        />
      </Flex>
      <CommonModal isOpen={isCommonModalOpen} closeModal={() => setIsCommonModalOpen(false)} title={action.title} body={action.body} size='xl' />
    </>
  )
}

export default AlertSettings
