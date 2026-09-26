#requires -Version 5.1
<#
.SYNOPSIS
    Safely configure and inspect the private Syncthing/Tailscale mirror for Naruto Timer.

.DESCRIPTION
    This script deliberately does not copy files itself. Syncthing performs block-level,
    bidirectional incremental synchronization after the folder has been configured on
    both owner-controlled machines.

    The normal project tree is F:\计时器 on Windows and
    /ssd/work/naruto-substitute-timer-sync on Linux. The owner explicitly chose to
    synchronize the whole shared development workspace: source, configuration, build
    outputs, temporary work, backups, reverse-engineering material, and evidence all
    remain eligible. The only project-side exception is the generated Fyne Junction
    pointing outside the tree to the Windows Go module cache. The owner deliberately
    selected bidirectional synchronization, so do not make conflicting edits on both
    machines at once; Syncthing preserves a sync-conflict copy for manual resolution.

    Default mode is read-only status. -Configure performs only Syncthing configuration;
    it never mirrors/deletes project files. -Check validates selected source, runtime,
    configuration, build, backup, and temporary-workspace paths after Syncthing reaches idle.

.NOTES
    Use -Configure once from F:\计时器 after reviewing the paths below. Run the same
    configuration once on Linux (the printed command tells you exactly what to run).
    Do not run this script against a public or untrusted Syncthing peer.
#>

[CmdletBinding()]
param(
    [switch]$Configure,
    [switch]$Check,
    [switch]$ShowLinuxCommand,
    [string]$FolderId = 'naruto-substitute-timer-workspace',
    [string]$LinuxDeviceId = 'Q7HHGUL-FIJHAQH-U2N6ML7-FD5Y234-H2FXOPB-DUSM2ZK-XF7MXSC-OM5RWQZ',
    [string]$LinuxFolderPath = '/ssd/work/naruto-substitute-timer-sync',
    [string]$SyncthingBinary = 'E:\Syncthing\bin\syncthing-windows-amd64-v2.1.3\syncthing.exe',
    [string]$SyncthingHome = 'E:\Syncthing\config'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$projectRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$ignoreFile = Join-Path $projectRoot '.stignore'
$markerFile = Join-Path $projectRoot '.stfolder'

function Invoke-SyncthingCli {
    param([Parameter(Mandatory)][string[]]$Arguments)

    if (-not (Test-Path -LiteralPath $SyncthingBinary -PathType Leaf)) {
        throw "Syncthing executable not found: $SyncthingBinary"
    }
    if (-not (Test-Path -LiteralPath $SyncthingHome -PathType Container)) {
        throw "Syncthing home not found: $SyncthingHome"
    }

    $output = & $SyncthingBinary -H $SyncthingHome cli @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "Syncthing CLI failed ($LASTEXITCODE): $($output -join [Environment]::NewLine)"
    }
    return $output
}

function Get-FolderConfig {
    $ids = @(Invoke-SyncthingCli -Arguments @('config', 'folders', 'list'))
    if ($ids -notcontains $FolderId) {
        return $null
    }
    $json = Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'dump-json')
    return ($json -join [Environment]::NewLine | ConvertFrom-Json)
}

function Get-UnpackVisionDocRelativePath {
    # Windows PowerShell 5.1 reads UTF-8 files without a BOM using the active
    # ANSI code page. Build this one Chinese filename from code points so -Check
    # remains reliable regardless of that host setting.
    $name = -join @([char]0x89E3, [char]0x5305, [char]0x4E0E, [char]0x89C6, [char]0x89C9, [char]0x6570, [char]0x636E)
    return 'docs/' + $name + '.md'
}

function Get-RequiredWorkspacePaths {
    return @(
        'internal/ninja/templates/itachi_hyakusen.png',
        'internal/ninja/templates/minato_kyubi.png',
        'internal/ninja/templates/hashirama_edo.png',
        'internal/ocr/neuraldata/recognizer.onnx',
        'internal/ocr/neuraldata/onnxruntime.dll',
        'assets/game/substitutes.json',
        'internal/hudtext/data/vision_catalog.json',
        'config.json',
        'bin/timer.exe',
        'backups/before-v0.2.5-restore-20260921-192529/untracked-source.zip',
        'tmp/user-frames/minato-144.png',
        (Get-UnpackVisionDocRelativePath),
        'internal/ninja/templates/hashirama_edo.source.png'
    )
}

function Assert-LocalBoundary {
    if (-not (Test-Path -LiteralPath $ignoreFile -PathType Leaf)) {
        throw "Required ignore file missing: $ignoreFile"
    }

    foreach ($relativePath in Get-RequiredWorkspacePaths) {
        if (-not (Test-Path -LiteralPath (Join-Path $projectRoot $relativePath) -PathType Leaf)) {
            throw "Required shared-workspace file is missing: $relativePath"
        }
    }

}

function Assert-ConfigurationPreconditions {
    if (Test-Path -LiteralPath $markerFile) {
        throw "Refusing to configure: source folder already contains .stfolder marker: $markerFile"
    }
}

function Test-SyncthingFileState {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][bool]$ShouldBeIndexed
    )

    $output = Invoke-SyncthingCli -Arguments @('debug', 'file', $FolderId, $Path)
    $text = $output -join "`n"
    $isIgnored = $text -match '"ignored"\s*:\s*true|ignored.*true'
    $isDeleted = $text -match '"deleted"\s*:\s*true|deleted.*true'
    $hasFile = $text -match '"name"\s*:\s*"'

    if ($ShouldBeIndexed -and ($isIgnored -or $isDeleted -or -not $hasFile)) {
        throw "Expected Syncthing to index required path: $Path`n$text"
    }
    if (-not $ShouldBeIndexed -and -not $isIgnored) {
        throw "Expected Syncthing to ignore excluded path: $Path`n$text"
    }
}


function Show-Status {
    $folder = Get-FolderConfig
    if ($null -eq $folder) {
        Write-Host "Folder '$FolderId' is not configured on Windows." -ForegroundColor Yellow
        return
    }

    $devices = @($folder.devices | ForEach-Object { $_.deviceID }) -join ', '
    [pscustomobject]@{
        FolderId = $folder.id
        Label = $folder.label
        WindowsPath = $folder.path
        Mode = $folder.type
        Versioning = $folder.versioning.type
        IgnorePerms = $folder.ignorePerms
        JunctionsAsDirs = $folder.junctionsAsDirs
        FSWatcherEnabled = $folder.fsWatcherEnabled
        Peers = $devices
    } | Format-List

    Write-Host 'Connection status:' -ForegroundColor Cyan
    Invoke-SyncthingCli -Arguments @('show', 'connections') | Write-Output
    Write-Host 'Pending folders:' -ForegroundColor Cyan
    Invoke-SyncthingCli -Arguments @('show', 'pending', 'folders') | Write-Output
    Write-Host 'Config restart requirement:' -ForegroundColor Cyan
    Invoke-SyncthingCli -Arguments @('show', 'config-status') | Write-Output
}

function Configure-WindowsFolder {
    Assert-LocalBoundary
    Assert-ConfigurationPreconditions
    $existing = Get-FolderConfig
    if ($null -ne $existing) {
        if ($existing.path -ne $projectRoot) {
            throw "Folder ID '$FolderId' already belongs to a different path: $($existing.path)"
        }
        Write-Host "Folder '$FolderId' already exists; no Windows configuration changed." -ForegroundColor Yellow
        return
    }

    Invoke-SyncthingCli -Arguments @(
        'config', 'folders', 'add',
        '--id', $FolderId,
        '--label', 'Naruto Substitute Timer Development (Tailscale)',
        '--path', $projectRoot,
        '--type', 'sendreceive',
        '--rescan-intervals', '3600',
        '--fswatcher-enabled',
        '--fswatcher-delays', '10',
        '--ignore-perms',
        '--auto-normalize',
        '--max-conflicts', '10',
        '--marker-name', '.stfolder',
        '--block-indexing'
    ) | Out-Null
    try {
        Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'devices', 'add', '--device-id', $LinuxDeviceId) | Out-Null
        Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'versioning', 'type', 'set', 'staggered') | Out-Null
        Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'versioning', 'params', 'set', 'cleanoutDays', '365') | Out-Null
        Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'versioning', 'params', 'set', 'maxAge', '31536000') | Out-Null
        Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'versioning', 'cleanup-intervals', 'set', '3600') | Out-Null
    } catch {
        Invoke-SyncthingCli -Arguments @('config', 'folders', $FolderId, 'delete') | Out-Null
        throw
    }
    Write-Host "Configured Windows folder '$FolderId'." -ForegroundColor Green
}

function Show-LinuxConfigurationCommand {
    @"
# Linux preflight: run only after the Windows folder has been configured.
set -eu
B=/home/x/.local/bin/syncthing
STHOME=/home/x/.local/share/syncthing
ROOT='$LinuxFolderPath'
FOLDER='$FolderId'
WINDOWS='YKELXII-4WHWPVF-BZGRUCS-DNN3BZW-XRL4JCU-WOZEEN2-UHIMLEC-RFONCQE'
test "`$(findmnt -no TARGET /ssd)" = /ssd
test "`$(readlink -f "`$ROOT")" = "`$ROOT"
test ! -L "`$ROOT"
test -s "`$ROOT/.stignore"
"`$B" --home "`$STHOME" cli config folders add --id "`$FOLDER" --label 'Naruto Substitute Timer Development (Tailscale)' --path "`$ROOT" --type sendreceive --rescan-intervals 3600 --fswatcher-enabled --fswatcher-delays 10 --ignore-perms --auto-normalize --max-conflicts 10 --marker-name .stfolder --block-indexing
"`$B" --home "`$STHOME" cli config folders "`$FOLDER" devices add --device-id "`$WINDOWS"
"`$B" --home "`$STHOME" cli config folders "`$FOLDER" versioning type set staggered
"`$B" --home "`$STHOME" cli config folders "`$FOLDER" versioning params set cleanoutDays 365
"`$B" --home "`$STHOME" cli config folders "`$FOLDER" versioning params set maxAge 31536000
"`$B" --home "`$STHOME" cli config folders "`$FOLDER" versioning cleanup-intervals set 3600
"`$B" --home "`$STHOME" cli show config-status
"@ | Write-Output
}

Assert-LocalBoundary

if ($Configure) {
    Configure-WindowsFolder
}

Show-Status

if ($Check) {
    $folder = Get-FolderConfig
    if ($null -eq $folder) {
        throw "Cannot check file state: folder '$FolderId' is not configured."
    }
    foreach ($relativePath in Get-RequiredWorkspacePaths) {
        Test-SyncthingFileState -Path $relativePath -ShouldBeIndexed $true
    }
    Write-Host 'Syncthing shared-workspace checks passed.' -ForegroundColor Green
}

if ($ShowLinuxCommand -or $Configure) {
    Write-Host 'Linux configuration command:' -ForegroundColor Cyan
    Show-LinuxConfigurationCommand
}
