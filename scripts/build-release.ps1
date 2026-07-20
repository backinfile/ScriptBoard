param([string]$Version = "development", [string]$Output = "dist")
$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$outputRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot $Output))
$stageRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot ".dist-stage"))
if (-not $outputRoot.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar) -or -not $stageRoot.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar)) {
    throw "Release paths must stay inside the repository"
}
if (Test-Path -LiteralPath $outputRoot) { Remove-Item -LiteralPath $outputRoot -Recurse -Force }
if (Test-Path -LiteralPath $stageRoot) { Remove-Item -LiteralPath $stageRoot -Recurse -Force }
New-Item -ItemType Directory -Path $outputRoot, $stageRoot | Out-Null

$originalGOOS = $env:GOOS
$originalGOARCH = $env:GOARCH
try {
    foreach ($arch in @("amd64", "arm64")) {
        $env:GOOS = "windows"; $env:GOARCH = $arch
        $name = "scriptboard-$Version-windows-$arch"
        $stage = Join-Path $stageRoot $name
        New-Item -ItemType Directory -Path $stage | Out-Null
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $stage "scriptboard.exe") ./cmd/scriptboard
        go build -trimpath -ldflags "-s -w -H=windowsgui" -o (Join-Path $stage "scriptboard-tray.exe") ./cmd/scriptboard-tray
        Copy-Item README.md, LICENSE* -Destination $stage -ErrorAction SilentlyContinue
        Compress-Archive -Path (Join-Path $stage "*") -DestinationPath (Join-Path $outputRoot "$name.zip")
    }
    foreach ($arch in @("amd64", "arm64")) {
        $env:GOOS = "linux"; $env:GOARCH = $arch
        $name = "scriptboard-$Version-linux-$arch"
        $stage = Join-Path $stageRoot $name
        New-Item -ItemType Directory -Path $stage | Out-Null
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $stage "scriptboard") ./cmd/scriptboard
        Copy-Item README.md, LICENSE* -Destination $stage -ErrorAction SilentlyContinue
        tar -czf (Join-Path $outputRoot "$name.tar.gz") -C $stage .
    }
    Get-ChildItem -LiteralPath $outputRoot -File | Sort-Object Name | ForEach-Object {
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
        "$hash  $($_.Name)"
    } | Set-Content -Encoding ascii (Join-Path $outputRoot "SHA256SUMS")
} finally {
    $env:GOOS = $originalGOOS; $env:GOARCH = $originalGOARCH
    if (Test-Path -LiteralPath $stageRoot) { Remove-Item -LiteralPath $stageRoot -Recurse -Force }
}
