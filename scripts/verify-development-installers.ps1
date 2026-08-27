param([string]$Distribution = "dist")
$ErrorActionPreference = "Stop"
if (Test-Path variable:PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$dist = [IO.Path]::GetFullPath((Join-Path $root $Distribution))
if (-not $dist.StartsWith($root + [IO.Path]::DirectorySeparatorChar)) {
    throw "Development installer path must stay inside the repository"
}

Add-Type -AssemblyName System.IO.Compression.FileSystem

function Open-SelfExtractingArchive([string]$Path) {
    $bytes = [IO.File]::ReadAllBytes($Path)
    $minimumOffset = [Math]::Max(0, $bytes.Length - 65557)
    $endOfCentralDirectory = -1
    for ($offset = $bytes.Length - 22; $offset -ge $minimumOffset; $offset--) {
        if ([BitConverter]::ToUInt32($bytes, $offset) -eq 0x06054b50) {
            $endOfCentralDirectory = $offset
            break
        }
    }
    if ($endOfCentralDirectory -lt 0) { throw "$Path does not contain a ZIP directory" }

    $centralDirectorySize = [BitConverter]::ToUInt32($bytes, $endOfCentralDirectory + 12)
    $centralDirectoryOffset = [BitConverter]::ToUInt32($bytes, $endOfCentralDirectory + 16)
    $payloadOffset = [int64]$endOfCentralDirectory - [int64]$centralDirectorySize - [int64]$centralDirectoryOffset
    if ($payloadOffset -lt 0 -or $payloadOffset -gt [int]::MaxValue) { throw "$Path has an invalid ZIP payload offset" }

    # 自解包启动器直接在 EXE 后追加 ZIP，目录偏移仍以 ZIP 载荷起点为基准；切出载荷后再交给 ZipArchive 验证。
    $payload = [IO.MemoryStream]::new($bytes, [int]$payloadOffset, $bytes.Length - [int]$payloadOffset, $false, $true)
    return [IO.Compression.ZipArchive]::new($payload, [IO.Compression.ZipArchiveMode]::Read, $false)
}

$contracts = @(
    @{ Name = "scriptboard-development-windows-amd64-setup.exe"; Files = @("scriptboard.exe", "scriptboard-broker.exe", "scriptboard-runner.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe", "RELEASE.json") },
    @{ Name = "scriptboard-development-windows-arm64-setup.exe"; Files = @("scriptboard.exe", "scriptboard-broker.exe", "scriptboard-runner.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe", "RELEASE.json") },
    @{ Name = "scriptboard-development-linux-amd64.run"; Files = @("scriptboard", "scriptboard-broker", "scriptboard-runner", "scriptboard-updater", "RELEASE.json") },
    @{ Name = "scriptboard-development-linux-arm64.run"; Files = @("scriptboard", "scriptboard-broker", "scriptboard-runner", "scriptboard-updater", "RELEASE.json") }
)

foreach ($contract in $contracts) {
    $path = Join-Path $dist $contract.Name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing $($contract.Name)" }
    $archive = Open-SelfExtractingArchive $path
    try {
        $entries = @{}
        foreach ($entry in $archive.Entries) { $entries[$entry.FullName.Replace('\', '/')] = $entry }
        foreach ($required in $contract.Files) {
            if (-not $entries.ContainsKey($required) -or $entries[$required].Length -le 0) {
                throw "$($contract.Name) has an invalid $required payload entry"
            }
        }
        $reader = [IO.StreamReader]::new($entries["RELEASE.json"].Open())
        try { $release = $reader.ReadToEnd() | ConvertFrom-Json } finally { $reader.Dispose() }
        if ($release.version -ne "development" -or $release.release_build) {
            throw "$($contract.Name) has invalid development release metadata"
        }
    } finally {
        $archive.Dispose()
    }
}

$native = Join-Path $dist "scriptboard-development-windows-amd64-setup.exe"
$info = & $native --version-json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0 -or $info.version -ne "development" -or $info.release_build) {
    throw "Development installer metadata contract changed"
}
& $native *> $null
if ($LASTEXITCODE -eq 0) { throw "Development installer accepted a no-argument install" }
$extractTarget = Join-Path $root ".development-installer-contract-extract"
& $native --extract-to $extractTarget *> $null
if ($LASTEXITCODE -eq 0 -or (Test-Path -LiteralPath $extractTarget)) {
    throw "Development installer accepted --extract-to"
}
