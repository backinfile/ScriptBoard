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
- save frequently used scripts as Quick Runs with reusable parameters and variables, then review recent results at a glance;
- create schedules with five-field Cron expressions;
- expose bounded inbound triggers for logs, uploads, Quick Runs, and constrained variable updates;
- review remote-login activity and manage Windows Defender Firewall or Linux UFW and Fail2Ban;
- view host resources, local applications, Docker containers, one Kubernetes cluster, websites, run history, and audit records, and compose importable custom monitoring dashboards, including multi-image Registry version cards;
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

After installing ScriptBoard as a system service, Administrators and Maintainers can restart it from System Settings → Updates. A restart briefly disconnects the page and stops every active Run; the page reconnects when the service is ready. This control is not available to portable instances.

## Using ScriptBoard

### Files and scripts

On Windows, Files starts from the available volumes. On Linux, it starts from `/`. File operations affect the host filesystem directly. Files deleted or replaced through the web interface first go to ScriptBoard Trash. Files with unknown extensions also receive a read-only preview when bounded content detection recognizes safe UTF-8 text; files that do not pass detection remain download-only.

After a multi-file upload, a dialog reports every successful, skipped, or failed item. Closing it refreshes the current directory without leaving Files.

Separate script arguments with spaces and quote arguments that contain spaces. The argument field does not expand pipes, redirections, wildcards, or command substitutions.

Run details keep output navigation, TXT download, and live pause controls in the log toolbar, so you can move directly to the top or bottom without changing the whole-page scroll position.

### Quick Runs and schedules

After running a script, save its path, argument template, and timeout as a Quick Run. The Quick Runs list shows the five most recent results and latest duration, with direct links to each Run and the script directory. Schedules use standard five-field Cron expressions; for example, `0 2 * * *` runs every day at 02:00. Missed triggers are not replayed after the service restarts.

### Kubernetes monitoring

Administrators and maintainers can configure one cluster under **Monitor → Kubernetes**. Enter an absolute kubeconfig path readable on the host running the ScriptBoard service and optionally select a context; the kubeconfig `current-context` is used by default. Connections start in observe-only mode. Limited operations can be explicitly enabled for rolling redeploys, single-step replica changes, and running a CronJob now.

The kubeconfig `server` may use `http://` or `https://`. HTTPS connections validate the kubeconfig CA and client certificate or system trust. HTTP connections may still use static tokens or basic authentication, but credentials and cluster data travel in plaintext. ScriptBoard does not store kubeconfig tokens, certificates, or private keys, and rejects `exec`/`auth-provider` login plugins and `insecure-skip-tls-verify`. When ScriptBoard runs under systemd or as a Windows service, ensure that service identity can read the kubeconfig and any referenced CA, token, or client-certificate files.

### Custom dashboards and website monitoring

Administrators and Maintainers can combine external JSON data with existing Website Monitoring results under Configuration → Custom Dashboards, create number, percentage, quota, key-value, website, and Registry cards, and import or export dashboard configurations. JSON sources and registries support HTTP and HTTPS, and a Registry Bearer token service may independently use either mode. HTTP sends request headers, credentials, and responses in plaintext. Unsaved card settings can be tested and mapped from the returned JSON structure. Failed refreshes retain the last successful value and expose redacted request diagnostics only to authorized operators; test responses are not written to the database or audit records. Imports and exports preserve URL schemes, while Registry passwords are excluded from exports. Public dashboards expose only generic result states without revealing sources, request headers, formulas, diagnostics, or management controls.

Website checks support HTTP, HTTPS, WS, and WSS. They can send custom HTTP or handshake headers and resolve `{{VARIABLE_NAME}}` references when a check runs; imports and exports preserve the scheme and TLS-verification setting. For secrets, use password variables instead of writing credentials directly into an exportable monitor configuration. To consolidate multiple ScriptBoard hosts, use the bounded external interface below over HTTP or HTTPS; HTTP sends the Key and monitoring response in plaintext.

### External Interfaces

Administrators and Maintainers can create time-limited keys under Configuration → External Interfaces. Each key may contain multiple named function entries for recording a log, uploading one file to a fixed directory, starting one existing Quick Run, updating one non-password variable under Boolean, integer, enum, or short-text constraints, or exposing a read-only Website Monitoring snapshot.

Call an entry with `POST /trigger?name=ENTRY_NAME` and `Authorization: Bearer KEY`. Administrators and Maintainers can copy the complete key from the key-management area; Operators and Viewers cannot view it. HTTP and HTTPS are both supported; HTTP sends the Key and request content in plaintext. Keep the Key in a secret store, prefer HTTPS or a trusted network, and disable or rotate it when the calling system no longer needs access.

Website Monitoring entries use `GET` instead of `POST`. To view one ScriptBoard instance from another, copy the complete call URL and Key, open Monitor → Websites on the receiving instance, and choose Connect ScriptBoard. Remote monitors appear in a separate read-only ledger; the receiving instance cannot check, pause, edit, reorder, or delete them. The remote Key is encrypted in the receiving instance's State Root.

### Host security

Monitoring → Host Security brings together Windows login events or Linux SSH login records, remote-login configuration, and firewall status. On Windows, it can manage Windows Defender Firewall rules. On Linux, it can install Fail2Ban and UFW, inspect or remove SSH bans, and synchronize UFW rules and default policies after a change review.

Every role can inspect the detected state; only Administrators and Maintainers can change host defenses. Firewall, remote-login, and ban operations can interrupt access to the host. Confirm that the service runs with Administrator or root privileges, preserve an allow rule for the active management port, and keep an out-of-band recovery path.

### MySQL backup and restore

Administrators and Maintainers can register local or remote MySQL/MariaDB instances under Resources → Databases, inspect databases and core status, run manual or five-field Cron logical backups, and restore `.sql` or `.sql.gz` files. Each connection can explicitly disable TLS, prefer TLS, require TLS, or verify the certificate and host identity; disabling TLS sends credentials and database traffic in plaintext. ScriptBoard does not bundle database clients; install `mysqldump` and `mysql` on the host PATH or configure their absolute paths in the page.

Each database is stored in a separate `.sql.gz` file with a SHA-256 digest. Replacing or deleting a database requires a successful safety backup and full-name confirmation; a failed replacement automatically attempts rollback. Artifacts default to `state_root/database-backups/mysql`, and a custom directory is also protected from host-file operations. Keep an independent off-host copy for disaster recovery.

### AI assistant

AI is disabled by default. An Administrator can install the matching Pi Runtime under System Settings → AI, then add an OpenAI, Anthropic, or OpenAI-compatible provider. Model endpoints support HTTP and HTTPS; HTTP sends the API Key, prompts, and model responses in plaintext.

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

ScriptBoard listens only on `127.0.0.1:8787` by default. You can explicitly select another address through configuration, environment variables, or `--listen`. A non-loopback listener exposes the privileged management interface to the network; in production, configure TLS or restrict access through a trusted VPN, zero-trust network, or HTTPS reverse proxy.

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

Back up before upgrading from an older version. The current release uses database schema 38 and can migrate schemas 20–37 automatically; older databases and legacy configuration files are not migrated automatically.

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
