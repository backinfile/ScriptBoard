param(
    [Parameter(Mandatory = $true)][string]$ScriptBoardVersion,
    [string]$Output = "dist"
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$outputRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot $Output))
$lockPath = Join-Path $repoRoot "runtime\pi-runtime-lock.json"
$extensionPath = Join-Path $repoRoot "runtime\scriptboard-extension.ts"
$downloadRoot = Join-Path $repoRoot ".runtime-downloads"
if (-not $outputRoot.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar) -or -not $downloadRoot.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar)) {
    throw "Runtime release paths must stay inside the repository"
}
if ($ScriptBoardVersion -notmatch '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') {
    throw "ScriptBoardVersion must be a stable vX.Y.Z tag"
}
foreach ($required in @("SCRIPTBOARD_UPDATE_KEY_ID", "SCRIPTBOARD_UPDATE_PUBLIC_KEY", "SCRIPTBOARD_UPDATE_SIGNING_KEY")) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($required))) {
        throw "$required is required for signed Runtime assets"
    }
}

$lock = Get-Content -LiteralPath $lockPath -Raw -Encoding UTF8 | ConvertFrom-Json
if ($lock.schema -ne 1 -or $lock.tag -ne "v$($lock.version)" -or $lock.assets.Count -ne 4) {
    throw "Pinned Pi runtime lock is invalid"
}
New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
if (Test-Path -LiteralPath $downloadRoot) { Remove-Item -LiteralPath $downloadRoot -Recurse -Force }
New-Item -ItemType Directory -Path $downloadRoot | Out-Null

function Receive-LockedFile([string]$Url, [string]$Destination, [long]$Size, [string]$SHA256) {
    & curl.exe --fail --silent --show-error --location --proto '=https' --tlsv1.2 --max-redirs 5 --output $Destination $Url
    if ($LASTEXITCODE -ne 0) { throw "Download failed: $Url" }
    $file = Get-Item -LiteralPath $Destination
    if ($file.Length -ne $Size) { throw "Downloaded file size does not match the runtime lock: $($file.Name)" }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $Destination).Hash.ToLowerInvariant()
    if ($actual -ne $SHA256) { throw "Downloaded file SHA-256 does not match the runtime lock: $($file.Name)" }
}

try {
    Push-Location -LiteralPath $repoRoot
    $licensePath = Join-Path $downloadRoot "PI-LICENSE"
    Receive-LockedFile $lock.license.url $licensePath $lock.license.size $lock.license.sha256
    foreach ($asset in $lock.assets) {
        $upstreamPath = Join-Path $downloadRoot $asset.name
        $url = "https://github.com/$($lock.repository)/releases/download/$($lock.tag)/$($asset.name)"
        Receive-LockedFile $url $upstreamPath $asset.size $asset.sha256
        $archiveExtension = if ($asset.os -eq "windows") { "zip" } else { "tar.gz" }
        $runtimeName = "scriptboard-pi-runtime-$($lock.version)-$($asset.os)-$($asset.arch).$archiveExtension"
        $runtimePath = Join-Path $outputRoot $runtimeName
        if (Test-Path -LiteralPath $runtimePath) { Remove-Item -LiteralPath $runtimePath -Force }
        & go run ./cmd/scriptboard-release runtime-package `
            --lock $lockPath --upstream $upstreamPath --license $licensePath --extension $extensionPath `
            --os $asset.os --arch $asset.arch --output $runtimePath
        if ($LASTEXITCODE -ne 0) { throw "Packaging Runtime $($asset.os)/$($asset.arch) failed" }
    }
    & go run ./cmd/scriptboard-release runtime-manifest `
        --scriptboard-version $ScriptBoardVersion.Substring(1) --scriptboard-tag $ScriptBoardVersion `
        --pi-version $lock.version --assets $outputRoot
    if ($LASTEXITCODE -ne 0) { throw "Generating the signed Runtime manifest failed" }
} finally {
    Pop-Location
    if (Test-Path -LiteralPath $downloadRoot) { Remove-Item -LiteralPath $downloadRoot -Recurse -Force }
}
