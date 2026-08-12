param([string]$Version = "development", [string]$Output = "dist")
$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$outputRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot $Output))
$stageRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot ".dist-stage"))
if (-not $outputRoot.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar) -or -not $stageRoot.StartsWith($repoRoot + [IO.Path]::DirectorySeparatorChar)) {
    throw "Release paths must stay inside the repository"
}

$formalRelease = $Version -match '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
if (-not $formalRelease -and $Version -ne "development") {
    throw "Version must be development or a stable vX.Y.Z tag"
}
$normalizedVersion = if ($formalRelease) { $Version.Substring(1) } else { "development" }
$tag = if ($formalRelease) { $Version } else { "" }
$commit = (git rev-parse HEAD).Trim().ToLowerInvariant()
if ($commit -notmatch '^[0-9a-f]{40}$') { throw "Unable to resolve a full Git commit SHA" }
$builtAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
$releaseValue = if ($formalRelease) { "true" } else { "false" }
if ($formalRelease) {
    $worktreeChanges = @(git status --porcelain --untracked-files=normal)
    if ($LASTEXITCODE -ne 0 -or $worktreeChanges.Count -ne 0) {
        throw "Formal releases require a clean Git worktree"
    }
    $tagCommitOutput = git rev-parse "${Version}^{commit}" 2>$null
    if ($LASTEXITCODE -ne 0) {
        throw "Formal release tag $Version does not exist"
    }
    $tagCommit = ([string]$tagCommitOutput).Trim().ToLowerInvariant()
    if ($tagCommit -ne $commit) {
        throw "Formal release tag $Version must resolve to HEAD"
    }
    foreach ($required in @("SCRIPTBOARD_UPDATE_KEY_ID", "SCRIPTBOARD_UPDATE_PUBLIC_KEY", "SCRIPTBOARD_UPDATE_SIGNING_KEY")) {
        if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($required))) {
            throw "$required is required for a formal release"
        }
    }
    if ($env:SCRIPTBOARD_UPDATE_KEY_ID -notmatch '^[A-Za-z0-9._-]{1,64}$') {
        throw "SCRIPTBOARD_UPDATE_KEY_ID has an invalid format"
    }
    try { $currentPublicKeyBytes = [Convert]::FromBase64String($env:SCRIPTBOARD_UPDATE_PUBLIC_KEY) } catch { throw "SCRIPTBOARD_UPDATE_PUBLIC_KEY is not valid base64" }
    if ($currentPublicKeyBytes.Length -ne 32) { throw "SCRIPTBOARD_UPDATE_PUBLIC_KEY must be a 32-byte Ed25519 public key" }
    $hasNextKeyID = -not [string]::IsNullOrWhiteSpace($env:SCRIPTBOARD_UPDATE_NEXT_KEY_ID)
    $hasNextPublicKey = -not [string]::IsNullOrWhiteSpace($env:SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY)
    if ($hasNextKeyID -ne $hasNextPublicKey) {
        throw "SCRIPTBOARD_UPDATE_NEXT_KEY_ID and SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY must be set together"
    }
    if ($hasNextKeyID -and $env:SCRIPTBOARD_UPDATE_NEXT_KEY_ID -eq $env:SCRIPTBOARD_UPDATE_KEY_ID) {
        throw "Current and next update key IDs must be different"
    }
    if ($hasNextKeyID) {
        if ($env:SCRIPTBOARD_UPDATE_NEXT_KEY_ID -notmatch '^[A-Za-z0-9._-]{1,64}$') {
            throw "SCRIPTBOARD_UPDATE_NEXT_KEY_ID has an invalid format"
        }
        try { $nextPublicKeyBytes = [Convert]::FromBase64String($env:SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY) } catch { throw "SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY is not valid base64" }
        if ($nextPublicKeyBytes.Length -ne 32) { throw "SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY must be a 32-byte Ed25519 public key" }
    }
    $hasNextSigningKey = -not [string]::IsNullOrWhiteSpace($env:SCRIPTBOARD_UPDATE_NEXT_SIGNING_KEY)
    if ($hasNextSigningKey -and -not $hasNextKeyID) {
        throw "SCRIPTBOARD_UPDATE_NEXT_SIGNING_KEY requires the next key ID and public key"
    }
    $revokedIDs = @()
    if (-not [string]::IsNullOrWhiteSpace($env:SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS)) {
        $revokedIDs = @($env:SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS.Split(',') | ForEach-Object { $_.Trim() })
        if ($revokedIDs.Count -ne (@($revokedIDs | Sort-Object -Unique)).Count -or @($revokedIDs | Where-Object { $_ -notmatch '^[A-Za-z0-9._-]{1,64}$' }).Count -ne 0) {
            throw "SCRIPTBOARD_UPDATE_REVOKED_KEY_IDS must be a unique comma-separated key ID list"
        }
        if ($revokedIDs -contains $env:SCRIPTBOARD_UPDATE_KEY_ID -or ($hasNextKeyID -and $revokedIDs -contains $env:SCRIPTBOARD_UPDATE_NEXT_KEY_ID)) {
            throw "An embedded trusted update key cannot also be revoked"
        }
    }
}
$publicKeyID = if ($formalRelease) { $env:SCRIPTBOARD_UPDATE_KEY_ID } else { "" }
$publicKey = if ($formalRelease) { $env:SCRIPTBOARD_UPDATE_PUBLIC_KEY } else { "" }
$nextKeyID = if ($formalRelease) { $env:SCRIPTBOARD_UPDATE_NEXT_KEY_ID } else { "" }
$nextPublicKey = if ($formalRelease) { $env:SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY } else { "" }
$revokedKeyIDs = if ($formalRelease) { $revokedIDs -join "," } else { "" }
$commonLDFlags = @(
    "-s", "-w",
    "-X", "scriptboard/internal/buildinfo.Version=$normalizedVersion",
    "-X", "scriptboard/internal/buildinfo.Tag=$tag",
    "-X", "scriptboard/internal/buildinfo.Commit=$commit",
    "-X", "scriptboard/internal/buildinfo.BuiltAt=$builtAt",
    "-X", "scriptboard/internal/buildinfo.ReleaseBuildValue=$releaseValue",
    "-X", "scriptboard/internal/buildinfo.UpdatePublicKeyID=$publicKeyID",
    "-X", "scriptboard/internal/buildinfo.UpdatePublicKeyBase64=$publicKey",
    "-X", "scriptboard/internal/buildinfo.UpdateNextKeyID=$nextKeyID",
    "-X", "scriptboard/internal/buildinfo.UpdateNextKeyBase64=$nextPublicKey",
    "-X", "scriptboard/internal/buildinfo.UpdateRevokedKeyIDs=$revokedKeyIDs"
) -join " "

if (Test-Path -LiteralPath $outputRoot) { Remove-Item -LiteralPath $outputRoot -Recurse -Force }
if (Test-Path -LiteralPath $stageRoot) { Remove-Item -LiteralPath $stageRoot -Recurse -Force }
New-Item -ItemType Directory -Path $outputRoot, $stageRoot | Out-Null

function Write-ReleaseInfo([string]$Stage) {
    $targetGOOS = $env:GOOS
    $targetGOARCH = $env:GOARCH
    $env:GOOS = $originalGOOS
    $env:GOARCH = $originalGOARCH
    $arguments = @(
        "run", "./cmd/scriptboard-release", "info",
        "--version", $normalizedVersion,
        "--commit", $commit,
        "--built-at", $builtAt,
        "--output", (Join-Path $Stage "RELEASE.json")
    )
    if ($formalRelease) { $arguments += @("--tag", $tag) }
    if ($formalRelease) { $arguments += "--release" }
    try {
        & go @arguments
        if ($LASTEXITCODE -ne 0) { throw "Generating RELEASE.json failed" }
    } finally {
        $env:GOOS = $targetGOOS
        $env:GOARCH = $targetGOARCH
    }
}

function Compress-ReleaseArchive([string]$Stage, [string]$Destination) {
    for ($attempt = 1; $attempt -le 5; $attempt++) {
        try {
            Compress-Archive -Path (Join-Path $Stage "*") -DestinationPath $Destination -ErrorAction Stop
            return
        } catch {
            if ($attempt -eq 5) { throw }
            Start-Sleep -Milliseconds 500
        }
    }
}

$originalGOOS = $env:GOOS
$originalGOARCH = $env:GOARCH
try {
    foreach ($arch in @("amd64", "arm64")) {
        $env:GOOS = "windows"; $env:GOARCH = $arch
        $name = "scriptboard-$Version-windows-$arch"
        $stage = Join-Path $stageRoot $name
        New-Item -ItemType Directory -Path $stage | Out-Null
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard.exe") ./cmd/scriptboard
        if ($LASTEXITCODE -ne 0) { throw "Building Windows $arch service failed" }
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard-broker.exe") ./cmd/scriptboard-broker
        if ($LASTEXITCODE -ne 0) { throw "Building Windows $arch privileged Broker failed" }
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard-ai-host.exe") ./cmd/scriptboard-ai-host
        if ($LASTEXITCODE -ne 0) { throw "Building Windows $arch AI Runtime Host failed" }
        go build -trimpath -ldflags "$commonLDFlags -H=windowsgui" -o (Join-Path $stage "scriptboard-tray.exe") ./cmd/scriptboard-tray
        if ($LASTEXITCODE -ne 0) { throw "Building Windows $arch tray failed" }
        go build -trimpath -ldflags "$commonLDFlags -H=windowsgui" -o (Join-Path $stage "scriptboard-tray-launcher.exe") ./cmd/scriptboard-tray-launcher
        if ($LASTEXITCODE -ne 0) { throw "Building Windows $arch tray launcher failed" }
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard-updater.exe") ./cmd/scriptboard-updater
        if ($LASTEXITCODE -ne 0) { throw "Building Windows $arch updater failed" }
        Write-ReleaseInfo $stage
        Copy-Item README.md, README_EN.md, LICENSE* -Destination $stage -ErrorAction SilentlyContinue
        Compress-ReleaseArchive $stage (Join-Path $outputRoot "$name.zip")
    }
    foreach ($arch in @("amd64", "arm64")) {
        $env:GOOS = "linux"; $env:GOARCH = $arch
        $name = "scriptboard-$Version-linux-$arch"
        $stage = Join-Path $stageRoot $name
        New-Item -ItemType Directory -Path $stage | Out-Null
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard") ./cmd/scriptboard
        if ($LASTEXITCODE -ne 0) { throw "Building Linux $arch service failed" }
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard-broker") ./cmd/scriptboard-broker
        if ($LASTEXITCODE -ne 0) { throw "Building Linux $arch privileged Broker failed" }
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard-ai-host") ./cmd/scriptboard-ai-host
        if ($LASTEXITCODE -ne 0) { throw "Building Linux $arch AI Runtime Host failed" }
        go build -trimpath -ldflags $commonLDFlags -o (Join-Path $stage "scriptboard-updater") ./cmd/scriptboard-updater
        if ($LASTEXITCODE -ne 0) { throw "Building Linux $arch updater failed" }
        Write-ReleaseInfo $stage
        Copy-Item README.md, README_EN.md, LICENSE* -Destination $stage -ErrorAction SilentlyContinue
        tar -czf (Join-Path $outputRoot "$name.tar.gz") -C $stage .
        if ($LASTEXITCODE -ne 0) { throw "Packaging Linux $arch archive failed" }
    }
    if ($formalRelease) {
        # The archive loops leave GOOS/GOARCH on the final Linux target. This
        # child script uses `go run` for host-side packaging tools, so restore
        # the caller's host target before PowerShell tries to execute them.
        $env:GOOS = $originalGOOS
        $env:GOARCH = $originalGOARCH
        & (Join-Path $PSScriptRoot "build-assistant-runtime.ps1") -ScriptBoardVersion $Version -Output $Output
        if ($LASTEXITCODE -ne 0) { throw "Building signed assistant Runtime assets failed" }
    }

    Get-ChildItem -LiteralPath $outputRoot -File | Sort-Object Name | ForEach-Object {
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
        "$hash  $($_.Name)"
    } | Set-Content -Encoding ascii (Join-Path $outputRoot "SHA256SUMS")

    if ($formalRelease) {
        $env:GOOS = $originalGOOS
        $env:GOARCH = $originalGOARCH
        go run ./cmd/scriptboard-release manifest --version $normalizedVersion --tag $tag --commit $commit --published-at $builtAt --assets $outputRoot
        if ($LASTEXITCODE -ne 0) { throw "Generating signed release manifest failed" }
    }
} finally {
    $env:GOOS = $originalGOOS
    $env:GOARCH = $originalGOARCH
    if (Test-Path -LiteralPath $stageRoot) { Remove-Item -LiteralPath $stageRoot -Recurse -Force }
}
