# Alert channels: one interface, a registry, a queue per channel

Issue #2458. Laws: T7 (one interface, several backends), F2 and T18 (a dead channel never
stalls the monitor), T5 (alerts from state transitions), T14 (an off-switch per channel), F9/F10
(secrets encrypted, never logged), F6 (the alert script stays the client's override).

## Before

Each channel was called from where its alert was born, with its own condition:

| Channel | Called from | Trigger |
|---|---|---|
| mail | `Cluster.SendAlert` (goroutine per alert) | states of `monitoring-alert-trigger` (`CheckAlert`), server state changes (`ServerMonitor.SendAlert`) |
| alert script | `Cluster.SendAlert` -> `BashScriptAlert`, **synchronous in the monitor tick, no timeout** | same as mail |
| Slack (`alert-slack-url` hook and the Cloud18 Mattermost-compatible hook, both in `cluster.LogSlack`) | `LogModuleWithFieldsPrintf`, `logPrintStateTo`, the send-alert API, the local-login notice | ERROR/WARN lines (not maintenance modules), ALERT/ALERTOK (Cloud18 alert, paid plan), START, state lines of `monitoring-alert-trigger` |
| Pushover (`cluster.LogPushover`) | `LogModuleWithFieldsPrintf`, synchronous logrus hook on `http.DefaultClient` (no deadline) | ALERT, ALERTOK, START |
| Teams | `LogModuleWithFieldsPrintf`, `logPrintStateTo`, goroutine per message | ERROR/WARN (not maintenance), ALERT, ALERTOK, START, state lines of `alert-teams-state` |

`alert.PostMeetMessage` (a Mattermost post through the Meet chat client) had no caller: it is
removed. The Cloud18 Mattermost alerts go through the `cloud18` Slack-compatible hook, which
the Slack channel carries.

## Now

- `cluster/alert_channel.go`: `AlertChannel` (`Name`, `Enabled`, `Accepts`, `Send`, `Timeout`)
  and `AlertEvent` (kind, time, the legacy `alert.Alert` payload, the state and its domain, the
  log level, the rendered texts and the logrus fields the hooks read).
- `cluster/alert_channel_registry.go`: the registry (`newAlertChannelList`, built at first use,
  like `newOrchestratorList`), `dispatchAlert`, one bounded queue and one worker per channel,
  `CheckAlertChannels` (each monitor tick), `stopAlertChannels` (last phase of `Cluster.Stop`,
  waits 5 s at most).
- One file per family: `alert_channel_mail.go` (mail, script), `alert_channel_chat.go`
  (Slack, Pushover, Teams), `alert_channel_alertmanager.go`, `alert_channel_telegram.go`;
  the HTTP clients of the new backends in `utils/alert/alertmanager` and `utils/alert/telegram`
  (net/http and JSON, no new dependency).
- `alert_channel_dispatch_test.go` (`TestNoAlertSendOutsideRegistry`): a hook fired on
  `LogSlack`/`LogPushover`, a call to `EmailMessage`, `sendMsTeams`, `BashScriptAlert`,
  `PostMeetMessage`, an `exec` of `AlertScript`, or `alertmanager.New`/`telegram.New` outside
  `cluster/alert_channel_*.go` fails the test. Configuration calls on the hooks (`Activate`,
  `SetURL`, `IsHookActive`) are not sends.

### No behaviour change for the existing channels

The call sites keep their gating (active monitor, `IsAlertDisable`, intervention and the
suppressed-alert counter) and build an event; each channel's `Accepts` carries the condition
its call site had (table above), its `Send` produces the same message (the same logrus entry
with the same fields for the hooks, the same card for Teams, the same mail, the same script
arguments). Keys and settings are unchanged. What changes is where the send runs:

- every send runs on the channel's worker, never on the monitor tick or the caller of a log
  line; the alert script no longer blocks the tick;
- each send runs in a goroutine of its own under the channel's `Timeout()` (`registry.send`):
  a panic is a failed send and the worker goes on; a send that ignores its context (the mail,
  a hook with its own client) is counted failed at its timeout, and while it still runs the
  channel's next events fail at once (`inFlight`): one stuck goroutine per channel at most.
  A send counted failed at its timeout may still be delivered later: alerting is at least once;
- the alert script is killed after `alert-script-timeout` (default 300 s; it had no limit),
  in a process group of its own so its children die with it, with `WaitDelay` so a child
  holding the output does not hold the worker; its output is a bounded tail (`capBuffer`); a
  script that fails or is killed is a failed send (WARN0363 after 3);
- Teams honours `alert-send-timeout` (default 10 s) through `SendWithContext`;
- Pushover, whose client has no deadline, runs one send at a time: a send past the timeout is
  reported failed and the next waits for it to end, so a hung API never piles up goroutines.
- a mail failure keeps `failSendCount` (now atomic: the worker writes it, the tick reads it) and WARN0138 as before; the failed send is also the channel's failure (WARN0363).

### Queue, drops, failures (T18)

Each channel's queue is capped by `alert-queue-limit` (default 1000, `<= 0` falls back to it,
read at each push, so it applies live). Over the cap the oldest event is dropped, the slice is
copied into a fresh array so the dropped events are freed, and the drop is counted.
`CheckAlertChannels` opens:

- **WARN0362** while a channel dropped alerts in the last 5 minutes (held, so it does not flap),
  naming the channels and their drop counts;
- **WARN0363** while a channel's last 3 sends or more failed, with its last error (the channels
  redact their secrets from errors).

The first 3 consecutive failures of a channel are logged; after that the state carries it.

### Prometheus Alertmanager

`alert-alertmanager` (off by default) + `alert-alertmanager-url` (comma list: every peer of an
Alertmanager cluster gets every post; a post fails only when no peer accepted it).
Auth: `alert-alertmanager-bearer-token` wins over `alert-alertmanager-user` /
`alert-alertmanager-password`. `alert-alertmanager-tls-skip-verify`, `alert-alertmanager-timeout`.

Events: the states of `monitoring-alert-trigger` opening and clearing in the HA state machine
(`CheckAlert`, transitions that are already flap-free). Labels: `alertname` and `code` (the
state code), `cluster`, `server` (when the state is a server's), `severity` (`critical` for
ERROR, `warning` for WARN and WARNING, `info` otherwise), `domain` (`ha`), `source=replication-manager`.
Annotations: `summary` (first line), `description`, `monitor`. `generatorURL` is
`api-public-url` when it is an http(s) URL. The active and the standby monitor send the same
labels, so Alertmanager deduplicates.

Alertmanager resolves an alert it has not heard of within `resolve_timeout` (5 min by default)
and expects an explicit resolve, so the channel keeps the firing set:

- open: posted at once, added to the set;
- clear: posted at once with `endsAt` (and the `startsAt` it fired with), removed;
- every `alert-alertmanager-resend-interval` (default 60 s, on the active monitor only, queued by
  `CheckAlertChannels`) the whole set is posted again in one request, after a reconciliation
  with the state machine (F5): a trigger state open in the state machine and absent from the set
  is added (a restart, where the first tick opens no edge; an alert suppressed by an
  intervention that ended), and an entry the state machine no longer holds on two rounds in a
  row is posted resolved and removed (a resolve missed while suppressed). New entries are not
  added during an intervention or the alert-disable window.

The rate is bounded: one post per transition plus one per interval. The set is bounded by
`alert-queue-limit` (oldest dropped).

Not yet: the workload, security, schema and config machines do not feed
`monitoring-alert-trigger` (only the HA machine calls `CheckAlert`); the `domain` label is in
the event, ready when they do.

### Telegram

`alert-telegram` (off by default), `alert-telegram-bot-token` (secret), `alert-telegram-chat-id`
(comma list), `alert-telegram-parse-mode` (empty for plain text, or `MarkdownV2`: title in bold,
everything escaped: `_*[]()~\`>#+-=|{}.!` and the backslash), `alert-telegram-api-url` (default
`https://api.telegram.org`), `alert-telegram-timeout`. Events: the same as the mail (states of
the trigger, server state changes), the same text. The token is in the request URL
(`/bot<token>/sendMessage`) and net/http quotes the URL in its errors: `telegram.Client` replaces
the token in every error it returns, so it never reaches a log or WARN0363.

### Settings, API, GUI

Every key is a TOML key and a flag (`server/server.go`), set through the existing cluster
settings endpoints (`settings/actions/set/{key}/{value}`, `settings/actions/switch/{key}`), so the
existing ACL rule (`GrantClusterSettings`) and Swagger entries cover them: no new endpoint. URLs
and secrets are base64 in the API path, like the other alert URLs. The GUI rows are in
`AlertSettings.jsx` (sub-tabs Alertmanager and Telegram; queue and timeouts under Triggers and
Script). The three secrets follow `doc/implementation/config/SECRET_KEYS_FLAT.md`.

## Tests

- `utils/alert/alertmanager`: post to every URL with the bearer token; one peer down is not a
  failure, every peer down is, and no credential reaches the error.
- `utils/alert/telegram`: sendMessage to every chat; the token redacted from an API refusal,
  a transport error and a timeout; MarkdownV2 escaping.
- `cluster/alert_channel_test.go`: the queue bound and drop-oldest with WARN0362 while a channel
  blocks, `dispatchAlert` never waits; WARN0363 from a refusing Telegram without the token;
  Alertmanager fire and resolve end to end from `CheckAlert` against a fake server (labels,
  `startsAt`/`endsAt`, bearer); the resend reconciliation; the off-switches; the routing table of
  the chat channels; intervention suppression.
- `cluster/alert_channel_dispatch_test.go`: the lock.
