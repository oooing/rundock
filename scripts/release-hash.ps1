# Use the runtime directly: Get-FileHash is a script-module function on Windows
# PowerShell 5.1 and may be unavailable in an inherited desktop environment.
function Get-ReleaseFileHash([string]$LiteralPath) {
    $stream = $null
    $sha = $null
    try {
        $stream = [IO.File]::OpenRead($LiteralPath)
        $sha = [Security.Cryptography.SHA256]::Create()
        return [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant()
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}
