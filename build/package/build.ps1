[CmdletBinding()]
param(
    [string]$Output = "bin/clip-translator.exe"
)

$ErrorActionPreference = "Stop"

# PSScriptRoot is build/package; the repository root is two levels up.
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Push-Location $root
try {
    $rsrcCmd = Get-Command rsrc -ErrorAction SilentlyContinue
    if ($rsrcCmd) {
        $rsrcPath = $rsrcCmd.Source
    }
    else {
        $rsrcPath = Join-Path (go env GOPATH) "bin\rsrc.exe"
        if (-not (Test-Path -LiteralPath $rsrcPath)) {
            Write-Host "rsrc not found. Installing github.com/akavel/rsrc ..."
            go install github.com/akavel/rsrc@latest
            if ($LASTEXITCODE -ne 0) {
                throw "go install rsrc failed with exit code $LASTEXITCODE"
            }
        }
        if (-not (Test-Path -LiteralPath $rsrcPath)) {
            throw "rsrc was installed but not found at $rsrcPath"
        }
    }

    $manifest = Join-Path $PSScriptRoot "clip-translator.exe.manifest"
    $syso = Join-Path $root "cmd\clip-translator\rsrc.syso"

    Write-Host "Generating resources from $manifest ..."
    & $rsrcPath -manifest $manifest -o $syso
    if ($LASTEXITCODE -ne 0) {
        throw "rsrc failed with exit code $LASTEXITCODE"
    }

    $outPath = Join-Path $root $Output
    $outDir = Split-Path -Parent $outPath
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null

    Write-Host "Building $Output ..."
    & go build -trimpath -ldflags="-H=windowsgui -s -w" -o $outPath ./cmd/clip-translator
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE"
    }

    Write-Host "Built $outPath"
}
finally {
    Pop-Location
}
