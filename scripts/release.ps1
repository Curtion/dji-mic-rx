#Requires -Version 5.1
<#
发布新版本：提升 mygo.json 的版本号、提交、打标签并推送，之后 CI 接手构建与发布：

  scripts\release.ps1 patch      :: 1.0.6 -> 1.0.7（默认）
  scripts\release.ps1 minor      :: 1.0.6 -> 1.1.0
  scripts\release.ps1 major      :: 1.0.6 -> 2.0.0
  scripts\release.ps1 1.2.3      :: 升到指定版本

前提：工作区干净、在 main 且与 origin/main 同步、目标标签不存在。
推送后 GitHub Actions 自动构建并发布 Release（.github/workflows/release.yml），
它会再校验一遍标签与 mygo.json 版本一致。
#>
[CmdletBinding()]
param(
  [Parameter(Position = 0)]
  [ValidatePattern('^(patch|minor|major|\d+\.\d+\.\d+)$')]
  [string]$Bump = 'patch'
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Push-Location $root
try {
  # 透传 git 参数：位置参数会被 $args 收齐，具名数组参数会静默丢掉后面的项。
  function Invoke-Git {
    & git @args
    if ($LASTEXITCODE -ne 0) { throw "git $($args -join ' ') 失败（退出码 $LASTEXITCODE）" }
  }

  if (git status --porcelain) { throw '工作区有未提交的改动，先提交或暂存' }
  Invoke-Git fetch origin --quiet
  if ((git branch --show-current) -ne 'main') { throw '发布请切到 main 分支' }
  if ((git rev-parse HEAD) -ne (git rev-parse origin/main)) { throw '本地 main 与 origin/main 不同步，先对齐' }

  $jsonPath = Join-Path $root 'mygo.json'
  $jsonText = [System.IO.File]::ReadAllText($jsonPath)
  if ($jsonText -notmatch '"version"\s*:\s*"(\d+\.\d+\.\d+)"') { throw 'mygo.json 里找不到版本号' }
  $current = [version]$Matches[1]

  switch ($Bump) {
    'patch' { $next = [version]::new($current.Major, $current.Minor, $current.Build + 1) }
    'minor' { $next = [version]::new($current.Major, $current.Minor + 1, 0) }
    'major' { $next = [version]::new($current.Major + 1, 0, 0) }
    default {
      $next = [version]$Bump
      if ($next -le $current) { throw "目标版本 $next 不大于当前版本 $current" }
    }
  }
  $tag = "v$next"
  if (git tag --list $tag) { throw "本地已有标签 $tag" }
  if (git ls-remote --tags origin $tag) { throw "远端已有标签 $tag" }

  Write-Host "版本：$current -> $next（标签 $tag）" -ForegroundColor Cyan
  $newText = $jsonText -replace '"version"\s*:\s*"\d+\.\d+\.\d+"', """version"": ""$next"""
  [System.IO.File]::WriteAllText($jsonPath, $newText, (New-Object System.Text.UTF8Encoding($false)))

  Invoke-Git add mygo.json
  Invoke-Git commit -m "chore: 版本号 $next"
  Invoke-Git tag $tag
  Invoke-Git push origin main $tag

  Write-Host "已推送，CI 开始构建。盯进度：" -ForegroundColor Green
  Write-Host "  gh run watch (gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
} finally { Pop-Location }
