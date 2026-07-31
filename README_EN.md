# ScriptBoard

[简体中文](./README.md) | English

> Manage, run, and schedule trusted scripts on one host from a browser.

ScriptBoard is a self-hosted script operations console for a single Windows or Linux host. Put existing scripts in a managed directory, then use the browser to manage files, enter arguments, follow live logs, reuse common operations, create schedules, and trace every change and run.

[Download the latest release](https://github.com/backinfile/ScriptBoard/releases/latest) · [Quick start](#quick-start) · [Deploy as a system service](#deploy-as-a-system-service) · [Troubleshooting](#troubleshooting)

> [!WARNING]
> ScriptBoard is not a security sandbox. Scripts inherit the operating-system identity, permissions, and environment of the ScriptBoard service process. Run only scripts you fully trust, and grant access only to trusted users.

![ScriptBoard Quick Runs page](./integration/browser/snapshots/readme-quick-runs-en.png)

## Navigation

- [Product overview](#product-overview): capabilities, fit, and supported platforms
- [Quick start](#quick-start): complete a first run in portable mode
- [Core workflows](#core-workflows): files, runs, schedules, monitoring, and recovery
- [Deployment and operations](#deployment-and-operations): system services, updates, and backups
- [Configuration and security](#configuration-and-security): precedence, networking, and user roles
- [Troubleshooting](#troubleshooting): diagnostics, password reset, and common failures
- [Development](#development): builds, tests, releases, and project documentation

## Product overview

| Use case | What ScriptBoard provides |
| --- | --- |
| Manage scripts centrally | Browse, search, upload, download, move, rename, preview, and edit managed files |
| Run scripts manually | Run PowerShell, Python, Shell, Batch, and CMD scripts while streaming stdout and stderr |
| Reuse common operations | Save scripts, argument templates, and timeouts as Quick Runs; group, sort, copy, and soft-lock them |
| Run on a schedule | Create five-field Cron schedules, preview trigger times, and configure timeouts and overlap policy |
| Observe and troubleshoot | Inspect run history, audit records, host resources, local applications, Docker containers, and website endpoints |
| Recover from mistakes | Restore deleted files from the application Trash; optionally enable local Git version protection |
| Update safely | Check official stable releases, then let an administrator approve download, verification, restart, and rollback |

The web interface supports Simplified Chinese and US English. It chooses a language from the browser by default and can be switched at any time. Both desktop and mobile browsers are supported.

### Where it fits

ScriptBoard is designed for personal servers, home labs, small workstations, and script hosts maintained by a few trusted users. It fits especially well when:

- scripts live on one fixed host and should not need individual registration;
- a browser should replace remote desktop sessions or repetitive command entry;
- live logs, run history, schedules, and basic file recovery are required;
- the fixed Administrator, Maintainer, Operator, and Viewer roles are enough to express the access boundary.

ScriptBoard is not currently a fit when you need custom roles, per-script authorization, untrusted-code isolation, multi-host orchestration, a task queue, a public API, external notifications, an interactive terminal, or a highly available deployment. The project also does not provide an official Docker deployment.

### Supported platforms

| Operating system | Architectures | Release archive |
| --- | --- | --- |
| Windows 10/11, Windows Server 2019+ | amd64, arm64 | ZIP with service, tray controller, tray launcher, and updater |
| Linux with systemd | amd64, arm64 | tar.gz with service and updater |

The matching interpreter must also be installed on the host before a script can run, such as PowerShell, Python, or Bash. ScriptBoard selects the first available program from the platform's default interpreter candidates.

## Quick start

This walkthrough uses `managed` and `state` directories inside the extracted release directory without installing a system service:

- `managed`: files the browser can manage and execute;
- `state`: the database, logs, sessions, and sign-in credentials.

### 1. Download the complete release

Download and fully extract the archive for your platform from [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest). Do not copy only one executable out of the archive. The release page provides `SHA256SUMS` for manual verification.

### 2. Start a portable instance

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force .\managed, .\state
.\scriptboard.exe serve --managed-root "$PWD\managed" --state-root "$PWD\state"
```

Open another PowerShell window in the same directory and read the initial password:

```powershell
Get-Content .\state\secrets\initial-admin-password
```

Linux:

```bash
chmod +x ./scriptboard
mkdir -p ./managed ./state
./scriptboard serve \
  --managed-root "$PWD/managed" \
  --state-root "$PWD/state"
```

Open another terminal in the same directory and read the initial password:

```bash
cat ./state/secrets/initial-admin-password
```

### 3. Run the first script

1. Open <http://127.0.0.1:8787>.
2. Sign in as `admin` with the initial password you just read.
3. Open Account and replace the initial password.
4. Upload a script from Files, or copy an existing script into `managed`.
5. Open the script, choose Run, enter arguments and a timeout, then start it.
6. Follow live output, stop the run, or save its configuration as a Quick Run from the run detail page.

The argument field uses simplified shellwords syntax: whitespace separates arguments, and single or double quotes preserve spaces. ScriptBoard does not expand pipelines, redirections, wildcards, or command substitutions.

## Core workflows

### Manage and run files

- Upload one or more files, create directories, search, and sort;
- preview text, Markdown, code, and common raster images;
- edit UTF-8 text files up to 1 MiB in the browser;
- execute a script in its own directory and see external host changes reflected in the web interface;
- refuse to follow symbolic links, Windows junctions, or cross-volume mount boundaries.

### Reuse arguments and operations

A Quick Run saves a script path, argument template, and timeout. Quick Runs can be grouped, sorted, copied, and soft-locked, and can be created from either a managed script or run history.

Variables can be reused in argument templates, but their values are stored as plaintext in SQLite. The password type only hides a value by default in the interface; it is not encrypted storage and is not a replacement for a secrets vault.

### Create schedules

Schedules use standard five-field Cron:

```text
minute hour day month weekday
```

For example, `0 2 * * *` runs every day at 02:00. The interface shows a rule summary and the next five trigger times. Each schedule can set its own timeout and decide whether to skip a trigger while the same script is already running.

Triggers missed while the service is stopped are not caught up. ScriptBoard uses its own scheduler and never reads or modifies the system crontab.

### Monitor and trace

- Host Status presents CPU, memory, storage, disk I/O, networking, and the ScriptBoard service;
- Applications provides read-only resource facts for local processes and Docker containers, with pins for important applications;
- Website Monitor checks HTTP, HTTPS, WebSocket, and WSS endpoints from the current host;
- Run History and Audit retain execution outcomes and trace evidence for high-impact operations;
- Docker containers and managed text files provide on-demand live logs.

Website Monitor includes short-term availability, TLS certificate facts, and a preview of Nginx candidates. Nginx configuration is read only after an administrator explicitly asks for it. ScriptBoard never modifies or reloads Nginx, and it does not send email, SMS, or webhook notifications.

### Recover deleted or changed files

Files deleted or replaced through the web interface first move to the application Trash. For a more complete edit history, enable local Git version protection under Settings:

- create automatic checkpoints for managed files;
- inspect the history of one file;
- restore an earlier version through a new local commit;
- never run `push`, `pull`, `fetch`, or another remote operation.

Version protection helps recover accidental edits. It is not an off-host backup.

## Deployment and operations

### Deploy as a system service

Run installation commands from a fully extracted release archive. ScriptBoard copies itself into a versioned installation root:

- Windows: `C:\Program Files\ScriptBoard`
- Linux: `/opt/scriptboard`

The current baseline is incompatible with the old service layout that directly targeted a single executable. If an old-style `ScriptBoard` service already exists, stop and uninstall it before performing a fresh installation. The installer does not guess, migrate, or delete old directories.

#### Windows

Save this configuration as `C:\ProgramData\ScriptBoard\config.yaml`:

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

Run in an elevated PowerShell:

```powershell
.\scriptboard.exe config validate --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service install --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service start
.\scriptboard.exe service status
```

The Windows service runs as `LocalSystem` by default. Installation also configures tray autostart for the current Windows user; exiting the tray does not stop the service.

#### Linux

Save this configuration as `/etc/scriptboard/config.yaml`:

```yaml
managed_root: /var/lib/scriptboard/managed
state_root: /var/lib/scriptboard/state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

Install and start the systemd service:

```bash
sudo ./scriptboard config validate --config /etc/scriptboard/config.yaml
sudo ./scriptboard service install --config /etc/scriptboard/config.yaml
sudo /opt/scriptboard/current/scriptboard service start
sudo /opt/scriptboard/current/scriptboard service status
```

Installation creates both the main service and a separate updater helper unit. The systemd service runs as `root` by default. Uninstalling the service does not delete configuration, managed files, private state, local Git history, or installed version directories.

### Application updates

Official releases check for a stable update every six hours by default, but never install one in the background. An administrator must first choose Download and verify, then Install and restart. The update verifies the signed release manifest, archive SHA-256, target platform, and archive safety. It will not switch versions while a Run is active.

If an update fails, the system restores the previous version and pre-update database. Portable instances can only check for and download a new release; source `development` builds do not check the network.

<details>
<summary>Recover an incomplete update operation</summary>

If the update page reports `needs_recovery`, do not delete `state_root/updates` or manually overwrite a version directory. Stop the ScriptBoard service, save a forensic copy of the Install Root and State Root, then run this with the Operation ID shown by the page:

```text
scriptboard update recover --operation <ID> --confirm-operation <ID>
```

If the page cannot open, read the same ID from `state_root/updates/active.json`. This command only recovers an update transaction that has not committed. It is not a general downgrade or backup recovery command.

</details>

To disable periodic network checks:

```yaml
update_check: false
```

Automatic checks contact only GitHub Releases for `backinfile/ScriptBoard`; they do not upload scripts, configuration, or host information.

### Data and backups

Include all of these locations in a long-term, off-host backup:

| Data | Location |
| --- | --- |
| Managed files | `managed_root` |
| SQLite, run logs, sessions, audit, and internal state | `state_root` |
| Service configuration | Windows: `C:\ProgramData\ScriptBoard\config.yaml`<br>Linux: `/etc/scriptboard/config.yaml` |

The database snapshot and rollback used by application updates are not a backup. ScriptBoard runs forward-only SQLite migrations at startup; do not open an upgraded `state_root` with an older release.

## Configuration and security

### Configuration precedence

```text
built-in defaults → YAML configuration → SCRIPTBOARD_* environment → command-line flags
```

Common settings:

| Setting | Default | Purpose |
| --- | --- | --- |
| `managed_root` | `managed` under the platform data directory | The only directory the browser can manage |
| `state_root` | `state` under the platform data directory | Database, logs, sessions, and private internal state |
| `listen` | `127.0.0.1:8787` | HTTP or HTTPS listen address |
| `tls_cert`, `tls_key` | empty | TLS certificate and key; required for non-loopback listening |
| `trusted_proxies` | empty | Trusted proxy IP addresses or CIDRs allowed to provide forwarding headers |
| `git_executable` | auto-detected | Absolute path to the system Git CLI |
| `run_timeout_grace_seconds` | `30` | Grace period before force-killing a process tree after automatic timeout |
| `update_check` | `true` | Periodically check the official stable release; never installs automatically |
| `update_check_interval_hours` | `6` | Automatic check interval, from 1 to 168 hours |
| `admin_username` | empty | Override the system administrator username at startup |
| `admin_password_file` | empty | Read the system administrator startup password from a permission-restricted file |
| `executor_chains` | platform defaults | Override interpreter chains by script extension |

YAML fields are validated strictly; an unknown setting causes validation or startup to fail. After editing configuration, run:

```text
scriptboard config validate --config CONFIG_PATH
scriptboard doctor --config CONFIG_PATH
```

<details>
<summary>Environment variables and default interpreters</summary>

Supported environment variables:

```text
SCRIPTBOARD_MANAGED_ROOT
SCRIPTBOARD_STATE_ROOT
SCRIPTBOARD_LISTEN
SCRIPTBOARD_GIT_EXECUTABLE
SCRIPTBOARD_TLS_CERT
SCRIPTBOARD_TLS_KEY
SCRIPTBOARD_TRUSTED_PROXIES
SCRIPTBOARD_RUN_TIMEOUT_GRACE_SECONDS
SCRIPTBOARD_UPDATE_CHECK
SCRIPTBOARD_UPDATE_CHECK_INTERVAL_HOURS
SCRIPTBOARD_ADMIN_USERNAME
SCRIPTBOARD_ADMIN_PASSWORD
SCRIPTBOARD_ADMIN_PASSWORD_FILE
```

Default interpreters:

| Platform | Extension | Candidate interpreters |
| --- | --- | --- |
| Windows | `.ps1` | `pwsh.exe` → `powershell.exe` |
| Windows | `.py` | `py.exe -3` → `python.exe` |
| Windows | `.bat`, `.cmd` | `cmd.exe` |
| Windows | `.sh` | `bash.exe` |
| Linux | `.sh` | `bash` → `sh` |
| Linux | `.py` | `python3` → `python` |
| Linux | `.ps1` | `pwsh` |

Fallback happens only before a script starts. Once an interpreter successfully starts a script, ScriptBoard does not retry that script with another interpreter if execution fails.

</details>

### Network boundary

- The default listener is `127.0.0.1:8787` only;
- plaintext HTTP may bind only to a loopback address;
- a non-loopback listener requires both `tls_cert` and `tls_key`;
- a same-host HTTPS reverse proxy should be declared explicitly through `trusted_proxies`;
- every script inherits the service identity; the application does not switch identity or provide container isolation;
- only one ScriptBoard instance may use a given `state_root` at a time.

Do not expose ScriptBoard directly to the public internet. Remote access should use a trusted VPN, zero-trust network, or correctly configured HTTPS reverse proxy with restricted sources.

### User roles

The system Administrator is the single always-enabled account. It can create users with the other fixed roles:

| Role | Permission scope |
| --- | --- |
| Administrator | All capabilities, including user management and system settings |
| Maintainer | Operations, files, execution, audit, and system settings, but not user management |
| Operator | View pages and files and start runs; may stop only Runs they started |
| Viewer | Read-only access to monitoring, configuration summaries, and history |

Roles are fixed instance-wide permissions. Custom roles and per-script authorization are not supported. Passwords are stored with Argon2id hashes; parameter variables remain plaintext data.

## Troubleshooting

Start with the read-only diagnostic:

```text
scriptboard doctor --config CONFIG_PATH
```

| Symptom | Check first |
| --- | --- |
| A script does not start | Matching interpreter installation, service-account access to the script and work directory, and absolute paths in `executor_chains` |
| The page does not open | Service status, the `listen` address, port conflicts, and TLS or reverse-proxy setup for remote access |
| A file write or Run is rejected | Whether free disk space is below 100 MiB or an active Run lease protects the target |
| A schedule was not caught up | Triggers missed while the service is stopped are deliberately not replayed |
| A variable appears encrypted | The password type hides the UI value only; variables remain plaintext |

### Reset the system administrator password

Stop the service, then use the original configuration:

```powershell
.\scriptboard.exe admin reset --config C:\ProgramData\ScriptBoard\config.yaml
```

Linux:

```bash
sudo scriptboard admin reset --config /etc/scriptboard/config.yaml
```

The new one-time password is written to `state_root/secrets/initial-admin-password`.

### Command reference

```text
scriptboard serve
scriptboard service install|uninstall|start|stop|restart|status
scriptboard update status|check
scriptboard update recover --operation ID --confirm-operation ID
scriptboard admin reset
scriptboard config validate
scriptboard doctor
scriptboard version [--json]
```

Run `scriptboard help` for all flags.

## Development

Building from source requires Go 1.26:

```powershell
go test ./... -count=1
go build ./cmd/scriptboard
go build ./cmd/scriptboard-tray
```

The browser regression gate uses test-only Node.js dependencies and does not add a production runtime dependency:

```powershell
cd integration/browser
pnpm install
pnpm exec playwright install chromium
pnpm test
```

Build portable archives for Windows/Linux and amd64/arm64:

```powershell
./scripts/build-release.ps1 -Version development
```

An official tag build also requires the release signing key. See the [release guide](./docs/RELEASING.md).

### Project documentation

| Document | Purpose |
| --- | --- |
| [Product requirements](./docs/PRD.md) | Product scope and requirements |
| [Acceptance criteria](./docs/ACCEPTANCE.md) | Verifiable completion conditions |
| [Data model and state machines](./docs/DATA-MODEL.md) | Persistence and state transitions |
| [Domain language](./CONTEXT.md) | Canonical project vocabulary |
| [Product and interface principles](./PRODUCT.md) | Product positioning and experience constraints |
| [Design system](./DESIGN.md) | Visual and interaction rules |
| [Architecture decisions](./docs/adr/README.md) | ADR conventions, topic index, and supersession relationships |
| [Chromium browser gate](./integration/browser/README.md) | End-to-end regression test instructions |
