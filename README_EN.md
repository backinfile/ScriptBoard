# ScriptBoard

[简体中文](./README.md) | English

> Manage, run, and schedule trusted scripts on one host from your browser.

ScriptBoard is a self-hosted script console for a single Windows or Linux host. You can work with existing scripts in place, manage host files, run scripts, follow logs, and create schedules without registering or moving every script first.

[Download the latest release](https://github.com/backinfile/ScriptBoard/releases/latest) · [Quick start](#quick-start) · [Install as a system service](#install-as-a-system-service) · [Troubleshooting](#troubleshooting)

> [!WARNING]
> ScriptBoard is not a security sandbox. Scripts run with the operating-system identity, permissions, and environment of the ScriptBoard service. Run only trusted scripts, grant access only to trusted users, and never expose the service directly to the public internet.

![ScriptBoard Quick Runs page](./integration/browser/snapshots/readme-quick-runs-en.png)

## What you can do

- Browse, search, upload, download, preview, and edit host files;
- run PowerShell, Python, Shell, Batch, and CMD scripts with live output;
- save frequently used scripts as Quick Runs with reusable parameters and variables;
- create schedules with five-field Cron expressions;
- expose bounded inbound triggers for logs, uploads, Quick Runs, and constrained variable updates;
- review remote-login activity and manage Windows Defender Firewall or Linux UFW and Fail2Ban;
- view host resources, local applications, Docker containers, websites, run history, and audit records;
- manage local or remote MySQL/MariaDB instances with checksummed logical backups and safety rollback;
- use the optional AI assistant with resources you choose to reference;
- restore files deleted through the web interface from ScriptBoard Trash;
- check, download, and install signed stable updates from the web interface.

The web interface is available in Simplified Chinese and US English and works on desktop and mobile browsers.

## Supported environments

| System | Architectures | Release package |
| --- | --- | --- |
| Windows 10/11 and Windows Server 2019+ | amd64, arm64 | ZIP with service, tray, and updater programs |
| Linux with systemd | amd64, arm64 | tar.gz with service and updater programs |

Install the interpreters required by your scripts, such as PowerShell, Python, or Bash. ScriptBoard does not provide an official Docker deployment package.

## Quick start

### 1. Download and extract

Download the complete archive for your system and architecture from [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest), then extract it into its own directory.

### 2. Start a portable instance

Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force .\state
.\scriptboard.exe serve --state-root "$PWD\state"
```

Linux:

```bash
chmod +x ./scriptboard
mkdir -p ./state
./scriptboard serve --state-root "$PWD/state"
```

### 3. Sign in

Open <http://127.0.0.1:8787> and sign in as `admin`. The initial password is stored at:

```text
state/secrets/initial-admin-password
```

Change the password under Account, then open Files to select an existing script or upload and run one.

## Install as a system service

No YAML configuration file is needed when using the built-in defaults. ScriptBoard listens only on `127.0.0.1:8787` by default.

> [!IMPORTANT]
> Run installation from a complete stable release package. If the host still has a legacy ScriptBoard service, stop and uninstall it before performing a clean installation.

### Windows

Run the following in an elevated PowerShell window:

```powershell
.\scriptboard.exe service install
.\scriptboard.exe service start
.\scriptboard.exe service status
```

The service is installed under `C:\Program Files\ScriptBoard`, and state is stored under `C:\ProgramData\ScriptBoard\state`. Installation also enables the tray app for the current Windows user.

### Linux

Run:

```bash
sudo ./scriptboard service install
sudo /opt/scriptboard/current/scriptboard service start
sudo /opt/scriptboard/current/scriptboard service status
```

The service is installed under `/opt/scriptboard`, and state is stored under `/var/lib/scriptboard/state`.

Create a YAML configuration file only when you need to change settings such as the listen address, TLS, or the state directory, then pass it during installation with `--config CONFIG_PATH`. Without that flag, ScriptBoard uses the platform's default configuration path (`C:\ProgramData\ScriptBoard\config.yaml` on Windows or `/etc/scriptboard/config.yaml` on Linux); if the file does not exist, the built-in defaults are used.

## Using ScriptBoard

### Files and scripts

On Windows, Files starts from the available volumes. On Linux, it starts from `/`. File operations affect the host filesystem directly. Files deleted or replaced through the web interface first go to ScriptBoard Trash.

Separate script arguments with spaces and quote arguments that contain spaces. The argument field does not expand pipes, redirections, wildcards, or command substitutions.

### Quick Runs and schedules

After running a script, save its path, argument template, and timeout as a Quick Run. Schedules use standard five-field Cron expressions; for example, `0 2 * * *` runs every day at 02:00. Missed triggers are not replayed after the service restarts.

### External Interfaces

Administrators and Maintainers can create time-limited keys under Configuration → External Interfaces. Each key may contain multiple named function entries for recording a log, uploading one file to a fixed directory, starting one existing Quick Run, or updating one non-password variable under Boolean, integer, enum, or short-text constraints.

Call an entry with `POST /trigger?name=ENTRY_NAME` and `Authorization: Bearer KEY`. Administrators and Maintainers can copy the complete key from the key-management area; Operators and Viewers cannot view it. Keep it in a secret store, use HTTPS outside loopback, and disable or rotate it when the calling system no longer needs access.

### Host security

Monitoring → Host Security brings together Windows login events or Linux SSH login records, remote-login configuration, and firewall status. On Windows, it can manage Windows Defender Firewall rules. On Linux, it can install Fail2Ban and UFW, inspect or remove SSH bans, and synchronize UFW rules and default policies after a change review.

Every role can inspect the detected state; only Administrators and Maintainers can change host defenses. Firewall, remote-login, and ban operations can interrupt access to the host. Confirm that the service runs with Administrator or root privileges, preserve an allow rule for the active management port, and keep an out-of-band recovery path.

### MySQL backup and restore

Administrators and Maintainers can register local or remote MySQL/MariaDB instances under Resources → Databases, inspect databases and core status, run manual or five-field Cron logical backups, and restore `.sql` or `.sql.gz` files. ScriptBoard does not bundle database clients; install `mysqldump` and `mysql` on the host PATH or configure their absolute paths in the page.

Each database is stored in a separate `.sql.gz` file with a SHA-256 digest. Replacing or deleting a database requires a successful safety backup and full-name confirmation; a failed replacement automatically attempts rollback. Artifacts default to `state_root/database-backups/mysql`, and a custom directory is also protected from host-file operations. Keep an independent off-host copy for disaster recovery.

### AI assistant

AI is disabled by default. An Administrator can install the matching Pi Runtime under System Settings → AI, then add an OpenAI, Anthropic, or OpenAI-compatible provider.

Conversation content and explicitly referenced resources are sent to the selected provider and may incur charges. Review the provider's privacy, pricing, and data-residency terms before enabling AI.

### User roles

| Role | Intended access |
| --- | --- |
| Administrator | All features, users, and system settings |
| Maintainer | Files, runs, schedules, monitoring, and system settings |
| Operator | Read regular files and start scripts |
| Viewer | Read-only monitoring and history |

Roles are fixed for the whole instance. Custom roles and per-script permissions are not currently supported.

## Network and configuration

ScriptBoard listens only on `127.0.0.1:8787` by default. For remote access, use a trusted VPN, zero-trust network, or HTTPS reverse proxy. Binding directly to a non-loopback address requires a TLS certificate and key.

Common settings:

```yaml
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
update_check: true
update_check_interval_hours: 6
```

On Linux, use `/var/lib/scriptboard/state` for `state_root`. After changing the configuration, run:

```text
scriptboard config validate --config CONFIG_PATH
scriptboard doctor --config CONFIG_PATH
```

Configuration precedence is: built-in defaults → YAML file → `SCRIPTBOARD_*` environment variables → command-line flags.

## Updates and backups

Stable releases periodically check GitHub Releases but never install updates automatically. An Administrator can download, verify, and install an update under System Settings → Updates. ScriptBoard will not switch versions while a script is running.

Back up these locations regularly:

- host files that must be retained;
- `state_root`, which contains the database, run logs, sessions, audit records, and AI data;
- the service `config.yaml` file, if you created a custom configuration.

Back up before upgrading from an older version. The current release can upgrade a schema 20 database automatically; older databases and legacy configuration files are not migrated automatically.

## Troubleshooting

Start with the read-only diagnostic:

```text
scriptboard doctor --config CONFIG_PATH
```

| Problem | Check first |
| --- | --- |
| The page does not open | Service status, listen address, port conflicts, and TLS/reverse-proxy settings |
| A script does not start | Required interpreter installation and service-account access to the script and working directory |
| A file write or run is rejected | Protected or busy paths and available disk space |
| A schedule did not catch up | Missed triggers are not replayed while the service is stopped |

Stop the service before resetting the Administrator password, then run:

```text
scriptboard admin reset --config CONFIG_PATH
```

The new one-time password is written to `state_root/secrets/initial-admin-password`.

Run `scriptboard help` for more commands. See the [project documentation](./docs/) for development, testing, and release details.
