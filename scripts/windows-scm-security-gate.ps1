param(
    [string]$WorkingRoot = "",
    [int]$Port = 0
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw "The Windows SCM security gate must run on Windows"
}
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "The Windows SCM security gate requires an elevated Administrator token"
}

$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
if ([string]::IsNullOrWhiteSpace($WorkingRoot)) {
    $base = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }
    $WorkingRoot = Join-Path $base ("scriptboard-windows-scm-gate-" + [Guid]::NewGuid().ToString("N"))
}
$gateRoot = [IO.Path]::GetFullPath($WorkingRoot)
if ([IO.Path]::GetPathRoot($gateRoot) -eq $gateRoot -or (Test-Path -LiteralPath $gateRoot)) {
    throw "WorkingRoot must be a new, non-root directory"
}

$releaseRoot = Join-Path $gateRoot "release"
$programFilesRoot = Join-Path $gateRoot "program-files"
$stateRoot = Join-Path $gateRoot "state"
$configPath = Join-Path $gateRoot "config.yaml"
$passwordPath = Join-Path $gateRoot "admin-password"
$brokerSecrets = Join-Path $gateRoot "broker-secrets"
$relayTokenPath = Join-Path $brokerSecrets "mail-relay-token"
$windowsTemp = [IO.Path]::GetFullPath((Join-Path $env:windir "Temp"))
$runWorkRoot = Join-Path $windowsTemp ("scriptboard-scm-gate-" + [Guid]::NewGuid().ToString("N"))
$serviceNames = @("ScriptBoard", "ScriptBoardBroker", "ScriptBoardRunner", "ScriptBoardAI")
$installed = $false

function Invoke-Checked([string]$FilePath, [string[]]$Arguments) {
    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}

function Write-UTF8NoBOM([string]$Path, [string]$Value) {
    [IO.File]::WriteAllText($Path, $Value, [Text.UTF8Encoding]::new($false))
}

function New-RandomBase64([int]$ByteCount) {
    $bytes = [byte[]]::new($ByteCount)
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return [Convert]::ToBase64String($bytes)
}

function Wait-ServiceState([string]$Name, [string]$State, [int]$TimeoutSeconds = 45) {
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    do {
        $service = Get-CimInstance Win32_Service -Filter "Name='$Name'" -ErrorAction SilentlyContinue
        if ($service -and $service.State -eq $State) { return $service }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    $snapshot = Get-CimInstance Win32_Service -Filter "Name='$Name'" -ErrorAction SilentlyContinue
    Write-Warning ("Service timeout snapshot: " + ($snapshot | Select-Object Name, State, Status, ExitCode, ProcessId | ConvertTo-Json -Compress))
    $serviceLog = Join-Path $stateRoot "logs\service.log"
    if (Test-Path -LiteralPath $serviceLog) {
        Write-Warning "Last service.log lines:"
        Get-Content -LiteralPath $serviceLog -Tail 80 | ForEach-Object { Write-Warning $_ }
    }
    throw "Service $Name did not reach $State"
}

function Invoke-CheckedTimed([string]$FilePath, [string[]]$Arguments, [int]$TimeoutSeconds = 60) {
    $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -PassThru -WindowStyle Hidden
    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        throw "$FilePath timed out after $TimeoutSeconds seconds"
    }
    if ($process.ExitCode -ne 0) {
        throw "$FilePath failed with exit code $($process.ExitCode)"
    }
}

function Wait-NewServiceProcess([string]$Name, [uint32]$PreviousPID) {
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    do {
        $service = Get-CimInstance Win32_Service -Filter "Name='$Name'" -ErrorAction SilentlyContinue
        if ($service -and $service.State -eq "Running" -and $service.ProcessId -ne 0 -and $service.ProcessId -ne $PreviousPID) {
            return $service
        }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Service $Name did not recover with a new process"
}

function Wait-Pipe([string]$Pattern) {
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        $pipe = Get-ChildItem -LiteralPath "\\.\pipe\" | Where-Object Name -Like $Pattern | Select-Object -First 1
        if ($pipe) { return $pipe.Name }
        Start-Sleep -Milliseconds 100
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "Named Pipe $Pattern was not created"
}

function Assert-PipeDenied([string]$PipeName) {
    $client = [IO.Pipes.NamedPipeClientStream]::new(
        ".", $PipeName, [IO.Pipes.PipeDirection]::InOut,
        [IO.Pipes.PipeOptions]::None, [Security.Principal.TokenImpersonationLevel]::Identification
    )
    try {
        $client.Connect(2000)
        throw "Administrator unexpectedly connected to protected pipe $PipeName"
    } catch [UnauthorizedAccessException] {
        return
    } finally {
        $client.Dispose()
    }
}

function Assert-ServiceDefinition([string]$Name, [string]$StartName, [string]$StartMode) {
    $service = Get-CimInstance Win32_Service -Filter "Name='$Name'"
    if (-not $service -or $service.StartName -ne $StartName -or $service.StartMode -ne $StartMode) {
        throw "Unexpected service definition for ${Name}: $($service | ConvertTo-Json -Compress)"
    }
}

function Get-ServiceSID([string]$Name) {
    $output = & sc.exe showsid $Name
    if ($LASTEXITCODE -ne 0) { throw "Unable to resolve service SID for $Name" }
    $match = [Regex]::Match(($output -join "`n"), 'S-1-5-80-(?:\d+-){4}\d+')
    if (-not $match.Success) { throw "SCM did not return a service SID for $Name" }
    return $match.Value
}

function Assert-PrivateBrokerPath([string]$Path, [string]$WebSID) {
    $sddl = (Get-Acl -LiteralPath $Path).Sddl
    foreach ($forbidden in @(";;;WD)", ";;;BU)", ";;;AU)", ";;;LS)", $WebSID)) {
        if ($sddl -match [Regex]::Escape($forbidden)) {
            throw "Broker-only path $Path grants a Web or broad trustee: $sddl"
        }
    }
}

try {
    New-Item -ItemType Directory -Path $releaseRoot, $programFilesRoot, $stateRoot, $brokerSecrets, $runWorkRoot | Out-Null
    if ($Port -eq 0) {
        $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
        $listener.Start()
        $Port = ([Net.IPEndPoint]$listener.LocalEndpoint).Port
        $listener.Stop()
    }
    if ($Port -lt 1024 -or $Port -gt 65535) { throw "Port must be between 1024 and 65535" }

    $version = "0.0.0"
    $tag = "v$version"
    $commit = (git -C $repoRoot rev-parse HEAD).Trim().ToLowerInvariant()
    if ($commit -notmatch '^[0-9a-f]{40}$') { throw "Unable to resolve the Git commit" }
    $builtAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $ldflags = @(
        "-X", "scriptboard/internal/buildinfo.Version=$version",
        "-X", "scriptboard/internal/buildinfo.Tag=$tag",
        "-X", "scriptboard/internal/buildinfo.Commit=$commit",
        "-X", "scriptboard/internal/buildinfo.BuiltAt=$builtAt",
        "-X", "scriptboard/internal/buildinfo.ReleaseBuildValue=true"
    ) -join " "
    foreach ($binary in @(
        @{ Name = "scriptboard.exe"; Package = "./cmd/scriptboard"; GUI = $false },
        @{ Name = "scriptboard-broker.exe"; Package = "./cmd/scriptboard-broker"; GUI = $false },
        @{ Name = "scriptboard-runner.exe"; Package = "./cmd/scriptboard-runner"; GUI = $false },
        @{ Name = "scriptboard-ai-host.exe"; Package = "./cmd/scriptboard-ai-host"; GUI = $false },
        @{ Name = "scriptboard-updater.exe"; Package = "./cmd/scriptboard-updater"; GUI = $false },
        @{ Name = "scriptboard-tray.exe"; Package = "./cmd/scriptboard-tray"; GUI = $true },
        @{ Name = "scriptboard-tray-launcher.exe"; Package = "./cmd/scriptboard-tray-launcher"; GUI = $true }
    )) {
        $binaryFlags = if ($binary.GUI) { "$ldflags -H=windowsgui" } else { $ldflags }
        Invoke-Checked "go" @("build", "-trimpath", "-ldflags", $binaryFlags, "-o", (Join-Path $releaseRoot $binary.Name), $binary.Package)
    }
    Invoke-Checked "go" @(
        "run", "./cmd/scriptboard-release", "info",
        "--version", $version,
        "--tag", $tag,
        "--commit", $commit,
        "--built-at", $builtAt,
        "--release",
        "--output", (Join-Path $releaseRoot "RELEASE.json")
    )

    $adminPassword = New-RandomBase64 24
    $adminPassword | Set-Content -LiteralPath $passwordPath -Encoding ascii -NoNewline
    New-RandomBase64 32 | Set-Content -LiteralPath $relayTokenPath -Encoding ascii -NoNewline
    $configBody = @"
state_root: '$($stateRoot.Replace("'", "''"))'
listen: '127.0.0.1:$Port'
admin_username: 'admin'
admin_password_file: '$($passwordPath.Replace("'", "''"))'
notification_email_relay_endpoint: 'https://mail.invalid/v1/scriptboard'
notification_email_relay_token_file: '$($relayTokenPath.Replace("'", "''"))'
notification_email_recipient: 'security@example.invalid'
"@
    Write-UTF8NoBOM $configPath $configBody

    foreach ($name in $serviceNames) {
        if (Get-Service -Name $name -ErrorAction SilentlyContinue) { throw "Service $name already exists" }
    }
    $oldProgramFiles = $env:ProgramFiles
    $env:ProgramFiles = $programFilesRoot
    try {
        Invoke-Checked (Join-Path $releaseRoot "scriptboard.exe") @("service", "install", "--config", $configPath)
    } finally {
        $env:ProgramFiles = $oldProgramFiles
    }
    $installed = $true
    Invoke-Checked (Join-Path $releaseRoot "scriptboard.exe") @("service", "verify", "--config", $configPath)

    Assert-ServiceDefinition "ScriptBoard" "NT AUTHORITY\LocalService" "Auto"
    Assert-ServiceDefinition "ScriptBoardBroker" "LocalSystem" "Auto"
    Assert-ServiceDefinition "ScriptBoardRunner" "NT AUTHORITY\LocalService" "Manual"
    Assert-ServiceDefinition "ScriptBoardAI" "NT AUTHORITY\LocalService" "Manual"

    Write-Host ("STATE_ROOT_ACL: " + (Get-Acl -LiteralPath $stateRoot).Sddl)
    Write-Host ("EXTERNAL_SECRETS_ACL: " + (Get-Acl -LiteralPath (Join-Path $gateRoot "secrets")).Sddl)

    $runnerSID = Get-ServiceSID "ScriptBoardRunner"
    # Use the numeric service SID so the gate never blocks on account-name resolution.
    Invoke-Checked "icacls.exe" @($runWorkRoot, "/grant", "*${runnerSID}:(OI)(CI)M")
    Write-Host "RUNNER_WORKSPACE_ACL: VERIFIED"

    Write-Host "SERVICE_START_COMMAND: BEGIN"
    Invoke-CheckedTimed (Join-Path $releaseRoot "scriptboard.exe") @("service", "start")
    Write-Host "SERVICE_START_COMMAND: RETURNED"
    $web = Wait-ServiceState "ScriptBoard" "Running"
    $broker = Wait-ServiceState "ScriptBoardBroker" "Running"
    Wait-ServiceState "ScriptBoardRunner" "Stopped" | Out-Null
    Wait-ServiceState "ScriptBoardAI" "Stopped" | Out-Null
    $deadline = [DateTime]::UtcNow.AddSeconds(45)
    do {
        try { $response = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/login" -TimeoutSec 2 } catch { $response = $null }
        if ($response -and $response.StatusCode -eq 200) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $response -or $response.StatusCode -ne 200) { throw "Managed Web did not become HTTP ready" }
    Write-Host "MANAGED_WEB_HTTP: READY"

    Assert-PipeDenied (Wait-Pipe "scriptboard-privileged-broker-*")

    $baseURL = "http://127.0.0.1:$Port"
    $loginPage = Invoke-WebRequest -Uri "$baseURL/login" -SessionVariable webSession
    $loginToken = [Regex]::Match($loginPage.Content, 'name="csrf_token" value="([^"]+)"').Groups[1].Value
    if ([string]::IsNullOrWhiteSpace($loginToken)) { throw "Login CSRF token was not rendered" }
    Invoke-WebRequest -Uri "$baseURL/login" -Method Post -WebSession $webSession -Body @{
        username = "admin"; password = $adminPassword; csrf_token = $loginToken
    } | Out-Null
    $taskPage = Invoke-WebRequest -Uri "$baseURL/config/quick-runs/one-time/new" -WebSession $webSession
    $taskToken = [Regex]::Match($taskPage.Content, 'name="csrf_token" value="([^"]+)"').Groups[1].Value
    if ([string]::IsNullOrWhiteSpace($taskToken)) { throw "One-time Run CSRF token was not rendered" }
    $markerPath = Join-Path $runWorkRoot "demand-start-ok.txt"
    $source = "@echo off`r`necho SCM_DEMAND_START_OK>`"$markerPath`"`r`n"
    try {
        $runSubmission = Invoke-WebRequest -Uri "$baseURL/config/quick-runs/one-time" -Method Post -WebSession $webSession -MaximumRedirection 0 -TimeoutSec 15 -Body @{
            csrf_token = $taskToken; working_directory = $runWorkRoot; language = "batch"
            source = $source; timeout_seconds = "30"; arguments = ""
        }
    } catch {
        $runSubmission = $_.Exception.Response
        if (-not $runSubmission) { throw }
    }
    $runSubmissionStatus = [int]$runSubmission.StatusCode
    $runSubmissionLocation = $runSubmission.Headers.Location
    if ($runSubmissionStatus -ne 303 -or -not $runSubmissionLocation) {
        throw "One-time Run submission did not return a redirect: status=$runSubmissionStatus"
    }
    Write-Host ("RUN_SUBMISSION: status=" + $runSubmissionStatus + " location=" + $runSubmissionLocation)
    $submittedRunID = [IO.Path]::GetFileName($runSubmissionLocation.ToString().TrimEnd('/'))
    $runner = Wait-ServiceState "ScriptBoardRunner" "Running"
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        if ((Test-Path -LiteralPath $markerPath) -and (Get-Content -Raw -LiteralPath $markerPath) -match "SCM_DEMAND_START_OK") { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not (Test-Path -LiteralPath $markerPath)) {
        $runnerSnapshot = Get-CimInstance Win32_Service -Filter "Name='ScriptBoardRunner'" -ErrorAction SilentlyContinue
        Write-Warning ("Runner snapshot: " + ($runnerSnapshot | Select-Object Name, State, Status, ExitCode, ProcessId | ConvertTo-Json -Compress))
        $diagnosticLogs = @(
            Get-ChildItem -LiteralPath (Join-Path $stateRoot "runs\$submittedRunID") -File -Recurse -ErrorAction SilentlyContinue
            Get-Item -LiteralPath (Join-Path $stateRoot "logs\service.log") -ErrorAction SilentlyContinue
        ) |
            Sort-Object LastWriteTimeUtc -Descending | Select-Object -First 5
        foreach ($diagnosticLog in $diagnosticLogs) {
            Write-Warning ("Last lines from " + $diagnosticLog.FullName + ":")
            Get-Content -LiteralPath $diagnosticLog.FullName -Tail 80 -ErrorAction SilentlyContinue | ForEach-Object { Write-Warning $_ }
        }
        throw "Web could not demand-start Runner and complete a managed Run"
    }

    Start-Service -Name "ScriptBoardAI"
    $ai = Wait-ServiceState "ScriptBoardAI" "Running"
    Assert-PipeDenied (Wait-Pipe "scriptboard-runner-*")
    Assert-PipeDenied (Wait-Pipe "scriptboard-ai-runtime-*")

    $webSID = Get-ServiceSID "ScriptBoard"
    Assert-PrivateBrokerPath $brokerSecrets $webSID
    Assert-PrivateBrokerPath $relayTokenPath $webSID

    foreach ($running in @($web, $broker, $runner, $ai)) {
        Stop-Process -Id $running.ProcessId -Force
        Wait-NewServiceProcess $running.Name $running.ProcessId | Out-Null
    }
    Write-Host "SERVICE_RECOVERY: VERIFIED"
    Invoke-Checked (Join-Path $releaseRoot "scriptboard.exe") @("service", "verify", "--config", $configPath)

    Invoke-Checked (Join-Path $releaseRoot "scriptboard.exe") @("service", "stop")
    foreach ($name in $serviceNames) { Wait-ServiceState $name "Stopped" | Out-Null }
    Write-Host "SERVICE_STOP_COMMAND: VERIFIED"
    Invoke-CheckedTimed (Join-Path $releaseRoot "scriptboard.exe") @("service", "start")
    Wait-ServiceState "ScriptBoard" "Running" | Out-Null
    Wait-ServiceState "ScriptBoardBroker" "Running" | Out-Null
    Write-Host "SERVICE_RESTART_COMMAND: VERIFIED"

    Invoke-Checked (Join-Path $releaseRoot "scriptboard.exe") @("service", "uninstall")
    $installed = $false
    foreach ($name in $serviceNames) {
        if (Get-Service -Name $name -ErrorAction SilentlyContinue) { throw "Service $name remains after uninstall" }
    }
    Write-Host "WINDOWS_SCM_SECURITY_GATE: PASSED"
} finally {
    $managedDefinitionExists = $serviceNames | Where-Object { Get-Service -Name $_ -ErrorAction SilentlyContinue } | Select-Object -First 1
    if (($installed -or $managedDefinitionExists) -and (Test-Path -LiteralPath (Join-Path $releaseRoot "scriptboard.exe"))) {
        try { & (Join-Path $releaseRoot "scriptboard.exe") service uninstall } catch { Write-Warning $_ }
    }
    if (Test-Path -LiteralPath $gateRoot) {
        Remove-Item -LiteralPath $gateRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    if (Test-Path -LiteralPath $runWorkRoot) {
        Remove-Item -LiteralPath $runWorkRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
}
