$ErrorActionPreference = "Stop"

$Repository = if ($env:SPEC_REPOSITORY) { $env:SPEC_REPOSITORY } else { "TaylorEdgerton/spec-cli" }
$Bin = "spec"
$Dir = "$env:LOCALAPPDATA\Spec\bin"
$Asset = "$Bin-windows-amd64.exe"
$PathMarker = Join-Path $Dir ".spec-path-added"

$url = "https://github.com/$Repository/releases/latest/download/$Asset"
New-Item -ItemType Directory -Force -Path $Dir | Out-Null
$TempFile = Join-Path $Dir ([System.IO.Path]::GetRandomFileName())
Write-Host "downloading $url"
try {
	Invoke-WebRequest -Uri $url -OutFile $TempFile
	Move-Item -Force $TempFile "$Dir\$Bin.exe"
} finally {
	if (Test-Path $TempFile) {
		Remove-Item -Force $TempFile
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

& "$Dir\$Bin.exe" --version
