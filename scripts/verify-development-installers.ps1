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
$contracts = @(
    @{ Name = "scriptboard-development-windows-amd64-setup.exe"; Files = @("scriptboard.exe", "scriptboard-broker.exe", "scriptboard-ai-host.exe", "scriptboard-runner.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe", "RELEASE.json") },
    @{ Name = "scriptboard-development-windows-arm64-setup.exe"; Files = @("scriptboard.exe", "scriptboard-broker.exe", "scriptboard-ai-host.exe", "scriptboard-runner.exe", "scriptboard-tray.exe", "scriptboard-tray-launcher.exe", "scriptboard-updater.exe", "RELEASE.json") },
    @{ Name = "scriptboard-development-linux-amd64.run"; Files = @("scriptboard", "scriptboard-broker", "scriptboard-ai-host", "scriptboard-runner", "scriptboard-updater", "RELEASE.json") },
    @{ Name = "scriptboard-development-linux-arm64.run"; Files = @("scriptboard", "scriptboard-broker", "scriptboard-ai-host", "scriptboard-runner", "scriptboard-updater", "RELEASE.json") }
)

foreach ($contract in $contracts) {
    $path = Join-Path $dist $contract.Name
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing $($contract.Name)" }
    $archive = [IO.Compression.ZipFile]::OpenRead($path)
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
