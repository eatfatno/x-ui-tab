param([switch]$PrepareOnly)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$sourceDirectory = Join-Path $projectRoot 'artifacts\github\source'
if (-not (Test-Path -LiteralPath (Join-Path $sourceDirectory 'scripts\install-online.sh'))) {
    throw 'First run scripts/prepare-github.py in the WSL project environment.'
}
if (-not (Get-Command git -ErrorAction SilentlyContinue)) { throw 'Install Git for Windows first.' }
$expectedRemote = 'https://github.com/eatfatno/x-ui-tab.git'
$gitTransport = @()
# Git does not automatically inherit the Windows browser's local proxy.
$systemProxy = Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
if ($systemProxy.ProxyEnable -eq 1 -and $systemProxy.ProxyServer -match '^(127\.0\.0\.1|localhost):[0-9]+$') {
    $gitTransport = @('-c', ('http.proxy=http://' + $systemProxy.ProxyServer))
}
function Invoke-ProjectGit {
    param([string[]]$GitArguments)
    & git @gitTransport -C $sourceDirectory @GitArguments
    if ($LASTEXITCODE -ne 0) { throw 'Git command failed. No force push was performed; review its error above.' }
}
if (-not (Test-Path -LiteralPath (Join-Path $sourceDirectory '.git'))) {
    Invoke-ProjectGit -GitArguments @('init', '-b', 'main')
    Invoke-ProjectGit -GitArguments @('config', 'user.name', 'eatfatno')
    Invoke-ProjectGit -GitArguments @('config', 'user.email', '172374293+eatfatno@users.noreply.github.com')
    Invoke-ProjectGit -GitArguments @('add', '--all')
    Invoke-ProjectGit -GitArguments @('commit', '-m', 'Release 3x-ui traffic monitor and Docker installer')
    Invoke-ProjectGit -GitArguments @('remote', 'add', 'origin', $expectedRemote)
}
$actualRemote = & git -C $sourceDirectory remote get-url origin
if ($LASTEXITCODE -ne 0 -or $actualRemote -ne $expectedRemote) { throw 'Unexpected Git remote. Check the source snapshot repository.' }
Invoke-ProjectGit -GitArguments @('status', '--short')
if ($PrepareOnly) {
    Write-Host "Local source commit prepared in $sourceDirectory. Nothing uploaded."
    exit 0
}
Write-Host 'Uploading the prepared source to eatfatno/x-ui-tab. Complete GitHub sign-in in the browser if prompted.'
Invoke-ProjectGit -GitArguments @('push', '-u', 'origin', 'main')
Write-Host 'Source uploaded. Create v1.0.2 at https://github.com/eatfatno/x-ui-tab/releases/new and attach the two files described in docs/github-publishing.md.'
