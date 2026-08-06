# notion-manager restart script
# usage: .\restart-notion-manager.ps1 [-NoBuild] [-WaitSeconds 5]
# checks health, rebuilds, restarts, verifies

param(
    [switch]$NoBuild,
    [int]$WaitSeconds = 5
)

$exePath = "C:\Users\Admin\Downloads\notion_manager\notion-manager.exe"
$repoDir = "C:\Users\Admin\Downloads\notion_manager"
$port = 8081
$healthUrl = "http://localhost:$port/health"

Write-Host "=== notion-manager restart ===" -ForegroundColor Cyan

# 1. check if running
$proc = Get-Process notion-manager -EA SilentlyContinue
if ($proc) {
    Write-Host "stopping notion-manager (PID $($proc.Id))..." -ForegroundColor Yellow
    Stop-Process -Id $proc.Id -Force -EA SilentlyContinue
    Start-Sleep 1
    $stillRunning = Get-Process notion-manager -EA SilentlyContinue
    if ($stillRunning) {
        Write-Error "failed to stop notion-manager"
        exit 1
    }
    Write-Host "stopped." -ForegroundColor Green
} else {
    Write-Host "notion-manager was not running." -ForegroundColor Gray
}

# 2. build (unless skipped)
if (-not $NoBuild) {
    Write-Host "building from source..." -ForegroundColor Yellow
    Set-Location $repoDir
    $buildOutput = go build -o notion-manager.exe ./cmd/notion-manager 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Error "build failed:`n$buildOutput"
        exit 1
    }
    Write-Host "built successfully." -ForegroundColor Green
} else {
    Write-Host "skipping build (use -NoBuild to skip)." -ForegroundColor Gray
}

# 3. verify binary exists
if (-not (Test-Path $exePath)) {
    Write-Error "binary not found at $exePath"
    exit 1
}

# 4. start detached
Write-Host "starting notion-manager on port $port..." -ForegroundColor Yellow
$stdoutLog = Join-Path $repoDir "nm-out.log"
$stderrLog = Join-Path $repoDir "nm-err.log"
Start-Process -FilePath $exePath -WorkingDirectory $repoDir -WindowStyle Minimized `
    -RedirectStandardOutput $stdoutLog -RedirectStandardError $stderrLog
Write-Host "stdout: $stdoutLog" -ForegroundColor Gray
Write-Host "stderr: $stderrLog" -ForegroundColor Gray
Start-Sleep $WaitSeconds

# 5. verify via health check
try {
    $resp = Invoke-RestMethod -Uri $healthUrl -Method Get -TimeoutSec 10
    $status = $resp.status
    $accounts = $resp.accounts
    $available = $resp.available
    Write-Host "health check: $status | accounts: $accounts | available: $available" -ForegroundColor Green
    Write-Host "restart complete." -ForegroundColor Cyan
} catch {
    Write-Warning "health check failed: $($_.Exception.Message)"
    Write-Warning "server may still be starting; check the health endpoint manually."
}
