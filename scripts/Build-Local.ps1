# Local Windows build and checks for Claude Switch (ajjs1ajjs/claude-account-switcher).
# Usage:
#   powershell -NoProfile -File .\scripts\Build-Local.ps1
#   powershell -NoProfile -File .\scripts\Build-Local.ps1 -SkipTests
param(
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$RepoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $RepoRoot
try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'go not found in PATH'
    }

    $mainGo = [IO.File]::ReadAllText((Join-Path $RepoRoot 'main.go'))
    if ($mainGo -notmatch '(?m)^(?:const|var)\s+version\s*=\s*"([^"]+)"') {
        throw 'Could not parse version from main.go'
    }
    $version = $Matches[1]

    if (-not $SkipTests) {
        & go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "go vet failed ($LASTEXITCODE)" }
        & go test ./...
        if ($LASTEXITCODE -ne 0) { throw "go test failed ($LASTEXITCODE)" }
    }

    & go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64 --product-version "$version.0" --file-version "$version.0"
    if ($LASTEXITCODE -ne 0) { throw "go-winres failed ($LASTEXITCODE)" }

    $distDir = Join-Path $RepoRoot 'dist'
    New-Item -ItemType Directory -Path $distDir -Force | Out-Null
    $outName = 'claude-account-switcher-windows-amd64.exe'
    $outPath = Join-Path $distDir $outName

    & go build -ldflags "-H=windowsgui -s -w -X main.version=$version" -o $outPath .
    if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }

    $hash = (Get-FileHash -LiteralPath $outPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $shaPath = $outPath + '.sha256'
    [IO.File]::WriteAllText($shaPath, "$hash  $outName`n", [Text.UTF8Encoding]::new($false))

    Write-Host "Built Claude Switch v$version -> $outPath"
    Write-Host "SHA256: $hash  $outName"
} finally {
    Pop-Location
}
