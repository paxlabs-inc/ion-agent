# Ion Operator Manual

This file is generated from the actions and activity types Ion actually
supports. Do not edit it by hand.

## First production start

```sh
export ION_DATA=/var/lib/ion-agent
sudo install -d -m 700 -o "$USER" -g "$USER" "$ION_DATA"
ion init --data-dir "$ION_DATA"
ion dashboard --data-dir "$ION_DATA"
```

On Linux, Ion protects its encryption key with the server automatically.
Production startup does not require a desktop session, keyring daemon,
`secret-tool`, environment secret, or interactive password. Keep the entire
server or VM backup protected: encrypted Ion data remains tied to the
server security identity that created it.

For unattended scheduling, install the supplied systemd unit after installing
the binary and initializing the data directory as its dedicated service user:

```sh
sudo install -m 0755 bin/ion /usr/local/bin/ion
sudo useradd --system --home-dir /var/lib/ion-agent --shell /usr/sbin/nologin ion-agent
sudo install -d -m 0700 -o ion-agent -g ion-agent /var/lib/ion-agent
sudo -u ion-agent /usr/local/bin/ion init --data-dir /var/lib/ion-agent
sudo install -m 0644 packaging/systemd/ion.service /etc/systemd/system/ion.service
sudo systemctl daemon-reload
sudo systemctl enable --now ion.service
sudo systemctl is-enabled ion.service
sudo systemctl is-active ion.service
```

Put protected provider or channel settings in
`/etc/ion-agent/environment`, owned by root with mode `0600`,
when the service needs them. `Restart=always` recovers process exits and
`WantedBy=multi-user.target` starts Ion at boot. The scheduler
then immediately catches up every overdue alarm from encrypted durable state.

## Start the clients

```text
ion dashboard [--data-dir PATH] [--dev-file-kek] [--listen ADDRESS]
ion tui [--data-dir PATH] [--dev-file-kek] [--attach] [--check]
```

The dashboard binds plain HTTP only to loopback. Use a TLS reverse proxy and an
exact `--origin` for remote browser access. `ion tui` starts a
supervised local daemon by default; `--attach` requests a fresh short-lived
capability from a running dashboard. `--check` verifies that the terminal
client and its secure local connection are ready, then exits.

Direct remote TUI connections are intentionally disabled in this release. Use
SSH and run or attach the TUI on the Ion host. This keeps terminal
credentials off the network. Durable provider and channel credentials are
write-only and never appear in URLs, browser storage, snapshots, logs, or
diagnostics.

## Dashboard authentication

Loopback-only development retains the automatic local operator session. Before
exposing the dashboard through a TLS proxy or deploying it on Railway, set:

```sh
ION_WEB_ORIGIN=https://ion.example.com
ION_AUTH_USERNAME=operator
ION_AUTH_PASSWORD=replace-with-a-generated-password
```

Set exactly one of `ION_AUTH_PASSWORD` or an Argon2id v=19 PHC
`ION_AUTH_PASSWORD_HASH`. Ion rejects partial or ambiguous credentials,
remote HTTP origins, unauthenticated remote origins, and Railway startup without
credentials. Store password values in a protected or sealed deployment variable,
not in source control.

The browser receives no actor identity or operator session before successful
login. Password verification uses Argon2id and generic, rate-limited failures.
Successful login creates a 12-hour signed Secure, HttpOnly, SameSite=Strict
session with a separate CSRF proof. Sign out revokes the current session and
removes both cookies; restarting Ion invalidates every prior browser session.

## Living context and identity continuity

Every primary browser or TUI provider step receives one bounded immutable
snapshot containing the current approved SOUL identity, actor/domain-scoped
relationship guidance, absolute session/task/deadline signals, durable
emotional decision guidance, authorized memory activation, self-model,
premises, and task state. Context preparation has an independent deadline and
cannot change tool classification, approval, authorization, evidence, or
verification.

The production identity file is `DATA_DIRECTORY/presence/SOUL.md`. It is a
private regular file and direct edits fail the next identity verification.
Use `soul.propose` to create a candidate and inspect its diff, then send a
separate explicit RED-confirmed approval or rollback through the same operation.
`soul.get` returns the current hash/version, encrypted durable history, and
actor-scoped pending proposals. Browser and TUI consume these same control-plane
projections.

## Telegram: first-class chat

Telegram uses the same production turn coordinator as dashboard chat. It does
not have a smaller prompt, separate memory, reduced tool set, or alternate
safety policy. Each Telegram user/chat/topic receives an isolated encrypted
session, while SOUL, relationship, temporal, emotional, memory, provider, tool,
approval, recovery, and audit behavior remain the same.

Setup is two protected environment values:

```sh
# Create the bot with Telegram @BotFather, then add:
TELEGRAM_BOT_TOKEN=123456:replace-with-the-botfather-token
TELEGRAM_ALLOWED_USERS=123456789
```

`TELEGRAM_ALLOWED_USERS` is a comma-separated list of numeric Telegram
user IDs. Ion fails closed for every other sender. Restart the dashboard
after changing either value, then send the bot a normal message. `/new`
starts a fresh encrypted conversation; `/help` shows the small command
set. On first enable, Ion establishes a new update cursor instead of
replaying old queued messages. Tokens and user IDs never appear in channel
health, logs, or operator projections.

While a Telegram request is running, channel health reports `working` and an
`active_updates` entry instead of falsely reporting an idle connection. A
complete Telegram agent turn has a ten-minute deadline; expiration cancels the
durable turn and quarantines the update as `channel_turn_timeout`. The
`channel.retry` command queues recovery immediately and resumes through
`turn.retry`, so an operator disconnect cannot duplicate the original user
message. After a daemon restart, interrupted processing is quarantined as
`processing_interrupted`, while an uncertain external send remains
`delivery_outcome_unknown` and is never sent twice automatically.

## Image and Video Studio

The dashboard includes a Media Studio for Novita-backed text-to-image,
image-to-image, text-to-video, image-to-video, inpainting, cleanup, background,
text-removal, face-merge, and upscaling workflows. Add the API key only to the
server's permission-restricted environment:

```sh
NOVITA_API_KEY=replace-with-the-novita-key
# Optional; defaults to https://api.novita.ai
NOVITA_BASE_URL=https://api.novita.ai
```

Restart Ion after changing these values. The key stays server-side and is not
returned to the browser, model, logs, or job history. Prompts and uploaded
source images are encrypted at rest. Accepted provider jobs continue while the
page is closed, resume polling after daemon restart, and copy completed outputs
into Ion-owned storage before presenting them as durable library items.

## Native browser workflows and agent email

Ion can operate ordinary websites through a locally installed Chromium
browser. This path is native to the production tool manager and does not
require the website to expose an API or an MCP server. Browser state is isolated
per actor and conversation. Local/private network destinations are blocked,
page observations are bounded, and cookies, browser profiles, passwords, and
hidden values are not returned to the model.

Set `ION_BROWSER_EXECUTABLE` when Chromium is not on the normal
executable path. Harmless observation is autonomous; network navigation and
reversible interaction are monitored; form submission, account creation,
verification consumption, payment, publishing, deletion, and consent require
an exact RED approval. CAPTCHA, passkey, legal identity, terms, payment, and
ambiguous anti-bot checks pause for human handoff.

The preferred account identity is the running machine-mail service. A
permission-restricted `.env` in the launch directory may provide:

```sh
MACHINE_MAIL_ADDRESS=agent@machinemail.org
MACHINE_MAIL_API_KEY=replace-with-the-mailbox-key
# Optional; defaults to https://api.machinemail.org
MACHINE_MAIL_URL=https://api.machinemail.org
```

Ion calls machine-mail directly over HTTPS; no machine-mail MCP server
is required. For an operator-supplied mailbox, IMAP/TLS remains available as a
fallback:

```sh
ION_AGENT_EMAIL=agent@example.com
ION_AGENT_IMAP_ADDRESS=imap.example.com:993
ION_AGENT_IMAP_USERNAME=agent@example.com
ION_AGENT_IMAP_PASSWORD=replace-with-an-app-password
```

The `.env` loader reads only the documented provider, Novita media,
Telegram, and `MACHINE_MAIL_*` connection names, never executes the file as shell code,
refuses group/world-readable files, and does not override process environment.
Mailbox state is encrypted by the Ion
Vault. The model sees only
redacted sender/domain/subject metadata. After approval, a confirmation link or
code moves directly from encrypted server state into the origin-matched browser
target; it is not placed in model context, tool output, browser storage, URLs,
logs, or operator events. Restart the dashboard after changing these protected
settings. The Connections page reports browser and mailbox readiness without
showing their secrets.

Skill refinements are staged rather than deployed immediately. Each proposal
must cite bounded outcome evidence. Promotion is RED-gated and requires at
least three held-out validation runs, a safety pass, and a material score
improvement; rejected and stale candidates remain auditable.

## Continuous presence

The production supervisor runs a non-blocking 60-second heartbeat over
schedules, Automatrix, subagent completion checks, emotional state, and
Dreamweaver when idle. Encrypted restart-safe schedules run strategic
forgetting, liveness cognition and user-approval-only goal proposals, and the
weekly integrity digest. Missed due runs are recovered after restart.
`schedule.list` reports exact last attempts, results, errors, and next due
times; `schedule.update` with `{"name":"...","action":"run_now"}`
runs a supported job immediately. Morning brief delivery stays visibly
setup-required until typed project and calendar sources are configured.

Ion also owns an encrypted scheduler for agent-created work. Direct
tools `schedule_create`, `schedule_list`, `schedule_get`, and
`schedule_cancel` support one-time delays, RFC3339 instants, recurring cron
expressions, descriptors, intervals, and IANA time zones. Every alarm is bound
to its authenticated actor and owning conversation. Due alarms are claimed
before delivery and enter the normal production turn path with an
occurrence-stable idempotency key. Delivery retries are bounded; exhausted
one-time alarms fail visibly, while recurring alarms retain the failure and
advance rather than wedging. Wake messages are scanned both when saved and
again when fired. Scheduled turns cannot use RED or externally communicating
tools, and all ordinary policy and approval boundaries remain active.

The scheduler catches missed alarms when the Ion daemon restarts. It
The supplied systemd service supervises it and starts it at host boot. A
powered-off host cannot execute work at the requested instant; on its next
boot, Ion safely catches up overdue alarms using their stable occurrence
identities.

## Keyboard model

- Browser: `Ctrl/Command+K` opens the command palette.
- TUI: `Tab` cycles views, `1` through `6` select a view,
  `Esc` switches navigation/composer focus, `Ctrl+E` opens
  `$EDITOR`, and `q` exits from navigation focus.
- Pending TUI approvals use `a` to approve and `d` to deny.

## TUI slash commands

Type `/help` to show every operation currently advertised by the
authenticated server. Exact operation names are executable as slash commands:
`/config.get` reads settings and
`/config.patch {"provider":"..."}` demonstrates the generic
`/operation {json}` form. Query and command behavior comes from the
live `commands.catalog` response, so newly available server
operations appear without a separately maintained TUI command list.

`/settings` is a readable shortcut for `/config.get` and
`/new` starts a new encrypted conversation. Type the beginning of an
operation after `/` to see matching commands and press
`Tab` to complete the first match. Operations that require a session
use the active conversation automatically; required turn, task, or approval
identifiers belong in the JSON payload.

## Technical action catalog

| Operation | Kind |
| --- | --- |
| `approval.respond` | command |
| `artifact.list` | query |
| `artifact.record` | command |
| `artifact.verify` | command |
| `automatrix.approve` | command |
| `automatrix.list` | query |
| `automatrix.reject` | command |
| `autonomy.get` | query |
| `autonomy.update` | command |
| `browser.credential.list` | query |
| `browser.workflow.cancel` | command |
| `browser.workflow.handoff` | command |
| `browser.workflow.list` | query |
| `browser.workflow.pause` | command |
| `browser.workflow.resume` | command |
| `channel.health` | query |
| `channel.list` | query |
| `channel.retry` | command |
| `channel.skip` | command |
| `citation.verify` | query |
| `commands.catalog` | query |
| `computer.browser.interact` | command |
| `computer.browser.navigate` | command |
| `computer.browser.observe` | query |
| `computer.browser.submit` | command |
| `computer.control.acquire` | command |
| `computer.control.get` | query |
| `computer.control.release` | command |
| `computer.control.renew` | command |
| `config.get` | query |
| `config.patch` | command |
| `continuity.brief` | query |
| `curiosity.targets` | query |
| `dreamweaver.beliefs` | query |
| `events.acknowledge` | command |
| `events.replay` | query |
| `integrity.latest` | query |
| `integrity.run` | command |
| `integrity.verify` | query |
| `liveness.get` | query |
| `logs.query` | query |
| `mcp.reload` | command |
| `mcp.servers` | query |
| `mcp.tools` | query |
| `memory.activation` | query |
| `memory.get` | query |
| `memory.graph` | query |
| `memory.pin` | command |
| `memory.recover` | command |
| `memory.search` | query |
| `plugin.lifecycle` | command |
| `plugin.list` | query |
| `policy.events` | query |
| `policy.explain` | query |
| `prediction.list` | query |
| `premise.list` | query |
| `project.attach` | command |
| `project.ci.patch.plan` | query |
| `project.citation.verify` | query |
| `project.clone` | command |
| `project.create` | command |
| `project.delivery.get` | query |
| `project.dependencies.install` | command |
| `project.dependencies.plan` | query |
| `project.deployment.apply` | command |
| `project.deployment.plan` | command |
| `project.deployment.reconcile` | command |
| `project.deployment.rollback` | command |
| `project.environment.put` | command |
| `project.get` | query |
| `project.git.blame` | query |
| `project.git.branch.create` | command |
| `project.git.checkpoint` | command |
| `project.git.commit` | command |
| `project.git.diff` | query |
| `project.git.force-with-lease` | command |
| `project.git.get` | query |
| `project.git.merge` | command |
| `project.git.preview.close` | command |
| `project.git.preview.start` | command |
| `project.git.provider.changes` | query |
| `project.git.provider.checks` | query |
| `project.git.provider.draft` | command |
| `project.git.provider.grant` | command |
| `project.git.provider.issues` | query |
| `project.git.provider.mergeability` | query |
| `project.git.provider.repositories` | query |
| `project.git.provider.review` | query |
| `project.git.pull` | command |
| `project.git.push` | command |
| `project.git.restore.plan` | query |
| `project.git.review.comment` | command |
| `project.git.review.comments` | query |
| `project.git.review.get` | query |
| `project.git.review.resolve` | command |
| `project.git.stage` | command |
| `project.git.stage.hunks` | command |
| `project.git.sync` | command |
| `project.git.tag.create` | command |
| `project.import` | command |
| `project.index.get` | query |
| `project.index.refresh` | command |
| `project.list` | query |
| `project.migration.apply` | command |
| `project.migration.plan` | command |
| `project.migration.rollback` | command |
| `project.patch.apply` | command |
| `project.patch.history` | query |
| `project.patch.rollback` | command |
| `project.portable.export` | command |
| `project.process.start` | command |
| `project.release.prepare` | command |
| `project.resource.apply` | command |
| `project.resource.plan` | command |
| `project.runtime.annotate` | command |
| `project.runtime.get` | query |
| `project.runtime.inspect` | query |
| `project.runtime.list` | query |
| `project.runtime.phase` | command |
| `project.runtime.plan` | query |
| `project.runtime.problems` | query |
| `project.runtime.reload` | command |
| `project.runtime.report` | command |
| `project.runtime.restart` | command |
| `project.runtime.start` | command |
| `project.runtime.stop` | command |
| `project.runtime.style.propose` | command |
| `project.search` | query |
| `project.terminal.cancel` | command |
| `project.terminal.input` | command |
| `project.terminal.replay` | query |
| `project.terminal.resize` | command |
| `project.terminal.signal` | command |
| `project.toolchain.get` | query |
| `project.verification.manifest.derive` | command |
| `project.verification.manifest.get` | query |
| `project.verification.run` | command |
| `project.verification.runs` | query |
| `project.verification.waiver.create` | command |
| `project.verification.waivers` | query |
| `provider.list` | query |
| `provider.usage` | query |
| `receipt.list` | query |
| `receipt.verify` | query |
| `relationship.update` | command |
| `review.plan` | query |
| `schedule.list` | query |
| `schedule.update` | command |
| `session.archive` | command |
| `session.branch` | command |
| `session.close` | command |
| `session.create` | command |
| `session.delete` | command |
| `session.export` | query |
| `session.list` | query |
| `session.rename` | command |
| `session.resume` | command |
| `skill.get` | query |
| `skill.lifecycle` | query |
| `skill.list` | query |
| `skill.refine` | command |
| `skill.rollback` | command |
| `skill.save` | command |
| `soul.get` | query |
| `soul.propose` | command |
| `studio.completion.check` | query |
| `studio.context.plan` | query |
| `studio.correlation.record` | command |
| `studio.drift.get` | query |
| `studio.intent.compile` | command |
| `studio.intent.get` | query |
| `studio.intent.list` | query |
| `studio.proposal.apply` | command |
| `studio.proposal.decide` | command |
| `studio.scope.propose` | command |
| `supervisor.cancel` | command |
| `supervisor.get` | query |
| `supervisor.list` | query |
| `supervisor.start` | command |
| `supervisor.steer` | command |
| `swarm.abort` | command |
| `swarm.list` | query |
| `system.health` | query |
| `system.metrics` | query |
| `system.snapshot` | query |
| `taskgraph.get` | query |
| `taskgraph.todo` | query |
| `tool.invoke` | command |
| `tool.readiness` | query |
| `tool.surface` | query |
| `turn.cancel` | command |
| `turn.retry` | command |
| `turn.steer` | command |
| `turn.submit` | command |
| `work.brief` | query |
| `work.contract.complete` | command |
| `work.contract.put` | command |
| `workflow.advance` | command |
| `workflow.list` | query |
| `workflow.start` | command |
| `workspace.capabilities` | query |
| `workspace.lifecycle` | command |

## Technical activity catalog

- `connection.ready`
- `connection.degraded`
- `connection.recovered`
- `turn.started`
- `turn.delta`
- `turn.completed`
- `turn.incomplete`
- `turn.recovery`
- `turn.failed`
- `reasoning.summary`
- `tool.requested`
- `tool.awaiting_approval`
- `tool.started`
- `tool.delta`
- `tool.completed`
- `tool.failed`
- `tool.denied`
- `tool.interrupted`
- `tool.outcome_unknown`
- `computer.control.acquired`
- `computer.control.renewed`
- `computer.control.released`
- `approval.requested`
- `approval.resolved`
- `approval.expired`
- `premise.created`
- `premise.cited`
- `premise.refuted`
- `prediction.created`
- `prediction.matched`
- `prediction.mismatched`
- `task.changed`
- `convergence.warning`
- `agent.spawned`
- `agent.progress`
- `agent.completed`
- `agent.aborted`
- `memory.committed`
- `memory.tombstoned`
- `memory.recovered`
- `emotional.changed`
- `relationship.changed`
- `temporal.changed`
- `liveness.decision`
- `repair.learned`
- `aesthetic.changed`
- `soul.changed`
- `circuit.opened`
- `circuit.closed`
- `heartbeat.pulse`
- `automatrix.queued`
- `automatrix.completed`
- `curiosity.targeted`
- `dreamweaver.derived`
- `policy.decision`
- `security.alert`
- `integrity.generated`
- `workspace.operation.queued`
- `workspace.operation.started`
- `workspace.operation.progress`
- `workspace.operation.completed`
- `workspace.operation.failed`
- `workspace.operation.cancelled`
- `journal.gap`

## Security and recovery

All mutations carry an idempotency key and optional expected revision. The
server authenticates actor and scope, applies policy, redacts responses, commits
revision state, and appends durable audit evidence. Browser mutations require
CSRF and exact Origin proof. WebSocket tickets and local capabilities are
short-lived and single-use. Reconnect replays exact retained events; retention
loss produces an explicit gap marker plus a redacted reconstructible snapshot.
