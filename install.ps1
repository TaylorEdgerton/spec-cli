$ErrorActionPreference = "Stop"

function ConvertTo-SpecVersion {
	param([string]$Value)
	try {
		$core = (($Value -replace '^v', '') -split '-', 2)[0]
		return [version]$core
	} catch {
		return $null
	}
}

$Repository = if ($env:SPEC_REPOSITORY) { $env:SPEC_REPOSITORY } else { "TaylorEdgerton/spec-cli" }
$Bin = "spec"
$Dir = "$env:LOCALAPPDATA\Spec\bin"
$Asset = "$Bin-windows-amd64.exe"
$PathMarker = Join-Path $Dir ".spec-path-added"
$ConfigDir = if ($env:SPEC_CONFIG_HOME) {
	$env:SPEC_CONFIG_HOME
} else {
	Join-Path ([Environment]::GetFolderPath("ApplicationData")) "spec"
}

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

$Headers = @{
	"Accept" = "application/vnd.github+json"
	"X-GitHub-Api-Version" = "2026-03-10"
	"User-Agent" = "spec-cli-installer"
}
if ($env:GITHUB_TOKEN) {
	$Headers["Authorization"] = "Bearer $env:GITHUB_TOKEN"
}
$Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repository/releases/latest" -Headers $Headers
$LatestVersion = [string]$Release.tag_name
$LatestParsed = ConvertTo-SpecVersion $LatestVersion
if (-not $LatestVersion -or $null -eq $LatestParsed) {
	throw "could not determine the latest spec release"
}

$Target = Join-Path $Dir "$Bin.exe"
$InstallRelease = $true
if (Test-Path $Target -PathType Leaf) {
	$CurrentVersion = "unknown"
	try {
		$CurrentOutput = & $Target --version 2>$null | Select-Object -First 1
		if ($LASTEXITCODE -eq 0 -and $CurrentOutput) {
			$CurrentVersion = (($CurrentOutput -split '\s+') | Select-Object -Last 1)
		}
	} catch {
		$CurrentVersion = "unknown"
	}
	$CurrentParsed = ConvertTo-SpecVersion $CurrentVersion
	if ($null -eq $CurrentParsed -or $LatestParsed -gt $CurrentParsed) {
		$Approved = $env:SPEC_INSTALL_YES -match '^(1|true|yes)$'
		if (-not $Approved) {
			$Answer = Read-Host "spec $CurrentVersion is installed; $LatestVersion is available. Update? [y/N]"
			$Approved = $Answer -match '^(y|yes)$'
		}
		if (-not $Approved) {
			$InstallRelease = $false
			Write-Host "update cancelled"
		}
	} elseif ($CurrentParsed -gt $LatestParsed) {
		$InstallRelease = $false
		Write-Host "installed spec $CurrentVersion is newer than latest release $LatestVersion; no update needed"
	} else {
		$InstallRelease = $false
		Write-Host "spec $CurrentVersion is already the latest release"
	}
}

if ($InstallRelease) {
	$url = "https://github.com/$Repository/releases/download/$LatestVersion/$Asset"
	$TempFile = Join-Path $Dir ([System.IO.Path]::GetRandomFileName())
	Write-Host "downloading $url"
	try {
		Invoke-WebRequest -Uri $url -OutFile $TempFile
		Move-Item -Force $TempFile $Target
	} finally {
		if (Test-Path $TempFile) {
			Remove-Item -Force $TempFile
		}
	}
}

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$userPathEntries = @($userPath -split ";" | Where-Object { $_ })
if ($userPathEntries -notcontains $Dir) {
	$newUserPath = if ($userPath) { "$userPath;$Dir" } else { $Dir }
	[Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
	[System.IO.File]::WriteAllText($PathMarker, "This PATH entry is managed by spec-cli.`n")
	Write-Host "added $Dir to user PATH; restart your shell"
}

if (Test-Path $Target -PathType Leaf) {
	& $Target --version
}
Write-Host "configuration folder: $ConfigDir"
