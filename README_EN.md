# ScriptBoard

[简体中文](./README.md) | English

> Manage, run, and schedule trusted scripts on one machine from your browser.

ScriptBoard is a self-hosted script console for a single Windows or Linux machine. Put your existing scripts in a managed directory, then use the browser to manage files, enter arguments, follow live output, save recurring operations, create schedules, and trace changes and runs.

It is designed for personal servers, home labs, small workstations, and script hosts maintained by one administrator. Scripts do not need to be registered individually, and there is no agent cluster, message queue, or production Node.js runtime to operate.

[Download the latest release](https://github.com/backinfile/ScriptBoard/releases/latest) · [5-minute quick start](#quick-start) · [Install as a system service](#install-as-a-system-service) · [Troubleshooting](#troubleshooting)

> [!WARNING]
> ScriptBoard is not a security sandbox. Scripts inherit the operating-system identity, permissions, and environment of the ScriptBoard process. Only run scripts you fully trust, and do not give untrusted users access to ScriptBoard.

![ScriptBoard Quick Runs](./integration/browser/snapshots/readme-quick-runs-en.png)

## What you can do

| Use case | What ScriptBoard provides |
| --- | --- |
| Manage scripts centrally | Browse, search, upload, download, move, rename, preview, and edit managed files |
| Run scripts manually | Run PowerShell, Python, shell, batch, and CMD scripts with live stdout and stderr |
| Reuse common operations | Save scripts, argument templates, and timeouts as Quick Runs, then group and order them |
| Run on a schedule | Create five-field Cron schedules, preview trigger times, and configure timeout and overlap behavior |
| Investigate problems | Review run results, audit records, and run history that cannot be deleted from the Web interface |
| Recover from mistakes | Restore files from the application trash or enable optional local Git version protection |
| Observe the host | View CPU, memory, storage, disk I/O, network, and ScriptBoard process status |

The Web interface supports Simplified Chinese and American English. It follows the browser language by default and can be switched at any time. Both desktop and mobile browsers are supported.

## Is ScriptBoard right for you?

ScriptBoard is a good fit when:

- you maintain a collection of trusted scripts on one fixed machine;
- you want to use a browser instead of repeatedly opening a remote desktop or entering commands;
- you need live logs, run history, schedules, and basic file recovery;
- one trusted administrator is responsible for the machine.

ScriptBoard is not intended for multiple users, per-script permissions, untrusted-code isolation, multi-host orchestration, job queues, public APIs, notifications, interactive terminals, or high-availability deployments. It also does not provide an official Docker deployment or automatic updates.

## Supported platforms

| Operating system | Architecture | Release package |
| --- | --- | --- |
| Windows 10/11 and Windows Server 2019+ | amd64, arm64 | ZIP containing the service and tray executables |
| Linux with systemd | amd64, arm64 | tar.gz containing the service executable |

Download the archive for your system from [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest). Use the release's `SHA256SUMS` file to verify its integrity.

The host must also have an interpreter for each script type you want to run, such as PowerShell, Python, or Bash. ScriptBoard automatically selects an interpreter that is actually available.

## Quick start

The following steps use `managed` and `state` directories beside the executable. This trial setup does not install a system service. `managed` contains the files you want to manage, while `state` contains the ScriptBoard database, logs, and credentials.

### Windows

Extract the Windows release, open PowerShell in the extracted directory, and run:

```powershell
New-Item -ItemType Directory -Force .\managed, .\state
.\scriptboard.exe serve --managed-root "$PWD\managed" --state-root "$PWD\state"
```

Keep that window running. Open another PowerShell window in the same directory and read the initial password:

```powershell
Get-Content .\state\secrets\initial-admin-password
```

### Linux

Extract the Linux release, open a terminal in the extracted directory, and run:

```bash
chmod +x ./scriptboard
mkdir -p ./managed ./state
./scriptboard serve \
  --managed-root "$PWD/managed" \
  --state-root "$PWD/state"
```

Keep that terminal running. Open another terminal in the same directory and read the initial password:

```bash
cat ./state/secrets/initial-admin-password
```

### Sign in and run your first script

1. Open <http://127.0.0.1:8787>.
2. Sign in with the username `admin` and the initial password from the previous step.
3. Open Account after signing in and replace the initial password with your own.
4. Upload a script from the Files page, or copy an existing script directly into the `managed` directory.
5. Open the script, select Run, enter its arguments and timeout, and start it.
6. Follow the live output, stop the run when necessary, or save the configuration as a Quick Run.

The argument field uses simplified shellwords syntax: whitespace separates arguments, and single or double quotes preserve spaces inside an argument. ScriptBoard does not expand pipelines, redirects, wildcards, or command substitutions.

## Everyday use

### Manage files

- Upload one or more files, create directories, search, and sort from the Files page.
- Preview text, Markdown, source code, and common raster images.
- Edit UTF-8 text files up to 1 MiB in the browser.
- Deleted files first enter the application trash, where they can be restored or permanently removed.
- ScriptBoard does not follow symbolic links, Windows junctions, or cross-volume mount boundaries.

Scripts run in place, with the script's directory as the working directory. Changes made directly on the host are reflected in the Web interface.

### Save Quick Runs

Save recurring operations as Quick Runs from a file or a previous run. A Quick Run stores the script path, argument template, and timeout, and can be grouped, ordered, copied, or soft-locked.

Variables can be reused in argument templates, but their values are stored as plaintext in SQLite. Marking a variable as a password only hides it by default in the interface. It does not encrypt the value or turn ScriptBoard into a secret vault.

### Create schedules

Schedules use standard five-field Cron expressions:

```text
minute hour day-of-month month day-of-week
```

For example, `0 2 * * *` runs every day at 02:00. When you create or edit a schedule, the interface shows a readable summary and the next five trigger times.

Each schedule can define its own timeout and whether to skip a trigger when the same script is already running. Triggers missed while the service is stopped are not replayed. ScriptBoard does not read or modify the system crontab.

### Recover files

Use the application trash to recover files deleted or replaced through the Web interface. For a more complete change history, enable local Git version protection under Settings → Version Protection:

- automatically create checkpoints for managed files;
- inspect the history of an individual file;
- restore a selected version through a new local commit;
- never run `push`, `pull`, `fetch`, or other remote operations.

Version protection helps recover accidental changes; it is not an off-machine backup. Important data should still be protected by your own backup system.

## Install as a system service

After confirming the trial setup works, install ScriptBoard as a system service so it starts automatically. Service installation records the absolute paths of the current executable and configuration file, so move the executable to its permanent location first.

### Windows service

Save the following configuration as `C:\ProgramData\ScriptBoard\config.yaml`:

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

Run these commands from an elevated PowerShell window:

```powershell
.\scriptboard.exe config validate --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service install --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service start
.\scriptboard.exe service status
```

The Windows service runs as `LocalSystem` by default. Scripts inherit that identity's permissions and environment.

The tray application included in the Windows release shows service and HTTP readiness, starts and stops the service, and opens the management page or data directories:

```powershell
.\scriptboard-tray.exe --config C:\ProgramData\ScriptBoard\config.yaml
```

Closing the tray application does not stop the ScriptBoard service.

### Linux systemd service

Install the executable first:

```bash
sudo install -m 0755 ./scriptboard /usr/local/bin/scriptboard
```

Save the following configuration as `/etc/scriptboard/config.yaml`:

```yaml
managed_root: /var/lib/scriptboard/managed
state_root: /var/lib/scriptboard/state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

Install and start the systemd service:

```bash
sudo scriptboard config validate --config /etc/scriptboard/config.yaml
sudo scriptboard service install --config /etc/scriptboard/config.yaml
sudo scriptboard service start
sudo scriptboard service status
```

The systemd service runs as `root` by default. Uninstalling the service does not remove the configuration, managed files, database, logs, or local Git history.

## Configuration

Configuration precedence, from lowest to highest, is:

```text
built-in defaults → YAML configuration → SCRIPTBOARD_* environment variables → command-line flags
```

Common settings:

| Setting | Default | Purpose |
| --- | --- | --- |
| `managed_root` | Windows: `C:\ProgramData\ScriptBoard\managed`<br>Linux: `/var/lib/scriptboard/managed` | The only directory that can be managed from the browser |
| `state_root` | Windows: `C:\ProgramData\ScriptBoard\state`<br>Linux: `/var/lib/scriptboard/state` | Database, logs, sessions, and private application state |
| `listen` | `127.0.0.1:8787` | HTTP or HTTPS listen address |
| `tls_cert`, `tls_key` | Empty | TLS certificate and key; required for non-loopback listeners |
| `trusted_proxies` | Empty | Trusted proxy IP addresses or CIDR ranges allowed to provide forwarding headers |
| `git_executable` | Auto-detected | Absolute path to the system Git CLI |
| `run_timeout_grace_seconds` | `30` | Grace period before a timed-out process tree is forcibly terminated |
| `admin_username` | Empty | Administrator username override applied at startup |
| `admin_password_file` | Empty | Read the startup password from a permission-restricted file |
| `executor_chains` | Platform defaults | Override interpreters by script extension |

YAML fields are validated strictly. An unknown setting causes validation or startup to fail. After changing the configuration, run:

```text
scriptboard config validate --config CONFIG_PATH
scriptboard doctor --config CONFIG_PATH
```

<details>
<summary>View a complete configuration example</summary>

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787

tls_cert: C:\ProgramData\ScriptBoard\tls\server.crt
tls_key: C:\ProgramData\ScriptBoard\tls\server.key
trusted_proxies:
  - 127.0.0.1/32

git_executable: C:\Program Files\Git\cmd\git.exe
run_timeout_grace_seconds: 30

admin_username: admin
admin_password_file: C:\ProgramData\ScriptBoard\secrets\admin-password

executor_chains:
  .py:
    - C:\Python313\python.exe
```

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
SCRIPTBOARD_ADMIN_USERNAME
SCRIPTBOARD_ADMIN_PASSWORD
SCRIPTBOARD_ADMIN_PASSWORD_FILE
```

</details>

### Default script interpreters

| Platform | Extension | Interpreter candidates |
| --- | --- | --- |
| Windows | `.ps1` | `pwsh.exe` → `powershell.exe` |
| Windows | `.py` | `py.exe -3` → `python.exe` |
| Windows | `.bat`, `.cmd` | `cmd.exe` |
| Windows | `.sh` | `bash.exe` |
| Linux | `.sh` | `bash` → `sh` |
| Linux | `.py` | `python3` → `python` |
| Linux | `.ps1` | `pwsh` |

Interpreter fallback only occurs before the script starts. Once an interpreter successfully launches the script, ScriptBoard does not retry the run with another interpreter if the script later fails.

## Networking and security

- ScriptBoard listens on `127.0.0.1:8787` by default and is not directly exposed to the LAN or internet.
- Plain HTTP can only listen on a loopback address. A non-loopback listener requires both `tls_cert` and `tls_key`.
- When using a same-host HTTPS reverse proxy, explicitly configure it through `trusted_proxies`.
- ScriptBoard has one administrator account and does not provide multiple users, RBAC, or per-script permissions.
- Every script inherits the service process identity. ScriptBoard does not switch identities or provide container isolation.
- Administrator passwords are stored using Argon2id, but argument variables are plaintext data.
- Only one ScriptBoard instance can use a particular `state_root` at a time.

Do not expose ScriptBoard directly to the internet. For remote access, use a trusted VPN, zero-trust network, or correctly configured HTTPS reverse proxy, and restrict which sources can connect.

## Troubleshooting

### Lost administrator password

Stop the ScriptBoard service, then reset the administrator using the original configuration:

```powershell
.\scriptboard.exe admin reset --config C:\ProgramData\ScriptBoard\config.yaml
```

On Linux:

```bash
sudo scriptboard admin reset --config /etc/scriptboard/config.yaml
```

The new initial password is written to `secrets/initial-admin-password` under `state_root`.

### A script does not start

Run the read-only diagnostics:

```text
scriptboard doctor --config CONFIG_PATH
```

Check that the required interpreter is installed, that the service account can read the script and its working directory, and that the script extension is supported. Custom entries in `executor_chains` must use absolute paths.

### The page does not open

- Confirm that the process is still running and check `scriptboard service status`.
- Confirm that the browser address matches `listen`.
- Check whether another program is already using port `8787`.
- Remote connections cannot use plain HTTP; configure TLS or an HTTPS reverse proxy.

### A file operation or run is rejected

ScriptBoard rejects new writes or runs when the managed or state volume has less than 100 MiB available. While a script is running, its file and parent directories also cannot be moved, modified, or deleted through the Web interface.

## Data and upgrades

Include both `managed_root` and `state_root` in your backup:

| Data | Location | Purpose |
| --- | --- | --- |
| Managed files | `managed_root` | Scripts and other user files |
| Application state | `state_root` | SQLite database, run logs, sessions, audit records, and internal state |
| Configuration | Windows: `C:\ProgramData\ScriptBoard\config.yaml`<br>Linux: `/etc/scriptboard/config.yaml` | Service startup configuration |

To upgrade:

1. Stop the service.
2. Back up `managed_root`, `state_root`, and the configuration file.
3. Replace the existing executable with the new version.
4. Start the service and check it with `service status` or `doctor`.

```text
scriptboard service stop
scriptboard service start
scriptboard service status
```

ScriptBoard applies forward-only SQLite migrations at startup and creates an internal snapshot before migrating an older database. Do not open an upgraded `state_root` with an older ScriptBoard version.

## Command reference

```text
scriptboard serve
scriptboard service install|uninstall|start|stop|restart|status
scriptboard admin reset
scriptboard config validate
scriptboard doctor
scriptboard version
```

Run `scriptboard help` to see the common command-line flags.

## For developers

Building from source requires Go 1.26:

```powershell
go test ./... -count=1
go build ./cmd/scriptboard
```

Build the Windows tray application:

```powershell
go build ./cmd/scriptboard-tray
```

Build portable Windows/Linux packages for amd64 and arm64:

```powershell
./scripts/build-release.ps1 -Version development
```

Project design and development documentation:

- [Product requirements (Chinese)](./docs/PRD.md)
- [Acceptance criteria (Chinese)](./docs/ACCEPTANCE.md)
- [Data model and state machines (Chinese)](./docs/DATA-MODEL.md)
- [Domain language](./CONTEXT.md)
- [Product and interface principles (Chinese)](./PRODUCT.md)
- [Design system (Chinese)](./DESIGN.md)
- [Architecture decisions](./docs/adr/README.md)
- [Chromium browser gate](./integration/browser/README.md)
