# Stop Discord Rich Presence daemon (Windows), by hand
#
# The plugin hooks don't run this script: they run stop.sh, through Git Bash
# on Windows. This one stops the daemon whatever sessions are running; it
# isn't needed when Claude Code quits, as the daemon then exits by itself.

# Configuration
$ClaudeDir = Join-Path $env:USERPROFILE ".claude"
$PidFile = Join-Path $ClaudeDir "discord-presence.pid"

# Stop the daemon
if (Test-Path $PidFile) {
    $ProcessId = Get-Content $PidFile -ErrorAction SilentlyContinue
    if ($ProcessId) {
        $Process = Get-Process -Id $ProcessId -ErrorAction SilentlyContinue | Where-Object { $_.ProcessName -like "cc-discord-presence*" }
        if ($Process) {
            Stop-Process -Id $ProcessId -Force -ErrorAction SilentlyContinue
            Write-Host "Discord Rich Presence stopped (PID: $ProcessId)"
        }
    }
    Remove-Item $PidFile -Force -ErrorAction SilentlyContinue
} else {
    # Try to find and kill by process name
    $Processes = Get-Process -Name "cc-discord-presence*" -ErrorAction SilentlyContinue
    if ($Processes) {
        $Processes | Stop-Process -Force
        Write-Host "Discord Rich Presence stopped"
    }
}
