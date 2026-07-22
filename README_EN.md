# ScriptBoard

[简体中文](./README.md) | [English](./README_EN.md)

ScriptBoard is a trusted-script manager for a single machine and a single administrator. It provides browser-based file management, script execution, live logs, quick runs, schedules, audit records, and optional local Git-backed version protection.

> [!WARNING]
> ScriptBoard is not a sandbox. Scripts inherit the operating-system identity and environment of the ScriptBoard service. Only run scripts you fully trust.

## Highlights

- Upload, download, move, rename, preview, and edit managed files in a browser
- Run PowerShell, Python, shell, and batch scripts with live output
- Preserve run history and audit records that cannot be deleted from the UI
- Save common tasks as Quick Runs or schedule them with five-field Cron expressions
- Two-stage stopping, execution timeouts, and descendant-process cleanup on Windows and Linux
- Optional local Git version protection, file history, and single-file restore
- Native Windows service and tray controller; native Linux systemd service
- Responsive web interface for desktop and mobile browsers

> [!NOTE]
> The application UI, tray menus, errors, and CLI help are currently available in Simplified Chinese only.

## Supported platforms

| Operating system | Architecture | Package |
| --- | --- | --- |
| Windows 10/11 and Windows Server 2019+ | amd64, arm64 | ZIP with the service and tray executables |
| Linux with systemd | amd64, arm64 | tar.gz with the service executable |

Download the archive for your system from [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest). Use `SHA256SUMS` to verify its integrity.

## Quick start

### Windows

Extract the release archive and run this command in PowerShell:

```powershell
.\scriptboard.exe serve
```

Open <http://127.0.0.1:8787>. The default username is `admin`. The initial one-time password is stored at:

```text
C:\ProgramData\ScriptBoard\state\secrets\initial-admin-password
```

You must choose a new password after the first sign-in. Keep the PowerShell window open in direct-run mode, or install the Windows service as described below.

### Linux

Extract the archive and install the binary:

```bash
tar -xzf scriptboard-v1.0.0-linux-amd64.tar.gz
chmod +x scriptboard
sudo install -m 0755 scriptboard /usr/local/bin/scriptboard
sudo scriptboard serve
```

Open <http://127.0.0.1:8787>. The default username is `admin`. The initial one-time password is stored at:

```text
/var/lib/scriptboard/state/secrets/initial-admin-password
```

## Install as a system service

Service installation records the current executable and configuration paths. Move the executable to its permanent location and create the configuration file before installing the service.

Minimal configuration:

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

On Windows, save it as `C:\ProgramData\ScriptBoard\config.yaml`, then use an elevated PowerShell window:

```powershell
.\scriptboard.exe config validate --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service install --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service start
.\scriptboard.exe service status
```

After installing the service, you can start the tray controller:

```powershell
.\scriptboard-tray.exe --config C:\ProgramData\ScriptBoard\config.yaml
```

Closing the tray application does not stop the ScriptBoard service.

On Linux, save this configuration as `/etc/scriptboard/config.yaml`:

```yaml
managed_root: /var/lib/scriptboard/managed
state_root: /var/lib/scriptboard/state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

Then install and start the systemd service:

```bash
sudo scriptboard config validate --config /etc/scriptboard/config.yaml
sudo scriptboard service install --config /etc/scriptboard/config.yaml
sudo scriptboard service start
sudo scriptboard service status
```

## Configuration

Configuration precedence, from lowest to highest, is: built-in defaults, YAML, `SCRIPTBOARD_*` environment variables, and command-line flags.

Full example:

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
git_executable: C:\Program Files\Git\cmd\git.exe
run_timeout_grace_seconds: 30
trusted_proxies:
  - 127.0.0.1/32
admin_password_file: C:\ProgramData\ScriptBoard\secrets\admin-password
executor_chains:
  .py:
    - C:\Python313\python.exe
```

Common commands:

```text
scriptboard serve
scriptboard service install|uninstall|start|stop|restart|status
scriptboard admin reset
scriptboard config validate
scriptboard doctor
scriptboard version
```

If the administrator password is lost, stop the service and reset it using the same configuration:

```powershell
.\scriptboard.exe admin reset --config C:\ProgramData\ScriptBoard\config.yaml
```

The new initial password is written to `secrets/initial-admin-password` under the State Root.

## Networking and security

- ScriptBoard listens on `127.0.0.1:8787` by default and is not directly exposed to the LAN or internet.
- A non-loopback listener requires both `tls_cert` and `tls_key`; ScriptBoard refuses to start without them.
- When using a local reverse proxy, configure `trusted_proxies`. Forwarded headers are accepted only from trusted proxies.
- ScriptBoard supports one administrator and does not provide multiple users, RBAC, script isolation, or a public API.
- The Windows service runs as LocalSystem by default, and the Linux systemd service runs as root by default. You can reduce privileges through the operating system's service configuration.

## Upgrade

Stop the service, replace the existing executable with the new version, and start it again:

```text
scriptboard service stop
scriptboard service start
```

ScriptBoard applies forward-only database migrations at startup. Do not open an upgraded State Root with an older ScriptBoard version.

## Build from source

Go 1.26 is required:

```powershell
go test ./...
go build ./cmd/scriptboard
./scripts/build-release.ps1 -Version development
```

The release script writes portable Windows/Linux archives for amd64/arm64 and `SHA256SUMS` to `dist/`.

## Project documentation

- [Product requirements (Chinese)](./docs/PRD.md)
- [Acceptance criteria (Chinese)](./docs/ACCEPTANCE.md)
- [Data model and state machines (Chinese)](./docs/DATA-MODEL.md)
- [Domain language](./CONTEXT.md)
- [Architecture decisions](./docs/adr/README.md)
