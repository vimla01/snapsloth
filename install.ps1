# SnapSloth installer for Windows (PowerShell):
#   irm https://raw.githubusercontent.com/vimla01/snapsloth/main/install.ps1 | iex
# Installs Ollama (if missing), pulls the model, and puts snapsloth.exe on your PATH.
# Set $env:SNAPSLOTH_MODEL = "gemma4:e2b" first for the smaller, faster model.
$ErrorActionPreference = "Stop"

$Repo = "vimla01/snapsloth"
$Model = if ($env:SNAPSLOTH_MODEL) { $env:SNAPSLOTH_MODEL } else { "gemma4:e4b" }
$BinDir = Join-Path $env:LOCALAPPDATA "snapsloth"

function Say($msg) { Write-Host "🦥 $msg" -ForegroundColor Green }
function Test-Ollama {
    try { Invoke-RestMethod -Uri "http://localhost:11434/api/version" -TimeoutSec 2 | Out-Null; $true } catch { $false }
}

# 1. Ollama, the local model runner.
if (Get-Command ollama -ErrorAction SilentlyContinue) {
    Say "Ollama is already installed"
} else {
    Say "Installing Ollama..."
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw "winget not found. Install Ollama from https://ollama.com/download/windows, then run this again."
    }
    winget install -e --id Ollama.Ollama --accept-source-agreements --accept-package-agreements
    $env:Path += ";" + (Join-Path $env:LOCALAPPDATA "Programs\Ollama")
}

# Wait for the Ollama server; start it ourselves if the app isn't running yet.
if (-not (Test-Ollama)) {
    Start-Process ollama -ArgumentList "serve" -WindowStyle Hidden
    $i = 0
    while (-not (Test-Ollama)) {
        $i++
        if ($i -gt 30) { throw "Ollama didn't start. Open the Ollama app once, then run this again." }
        Start-Sleep -Seconds 1
    }
}

# 2. The model (a few GB, only the first time).
Say "Downloading the model $Model (a few GB the first time, then it's offline)..."
ollama pull $Model

# 3. SnapSloth itself.
$Arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
Say "Installing snapsloth to $BinDir..."
Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest/download/snapsloth_windows_$Arch.exe" `
    -OutFile (Join-Path $BinDir "snapsloth.exe")

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($UserPath -split ";") -notcontains $BinDir) {
    [Environment]::SetEnvironmentVariable("Path", "$UserPath;$BinDir", "User")
    $env:Path += ";$BinDir"
}

Say "All set! Start the app with:"
Write-Host ""
Write-Host "    snapsloth ui"
Write-Host ""
Write-Host "(If 'snapsloth' isn't found, open a new PowerShell window.)"
