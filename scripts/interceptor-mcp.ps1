# Launch `interceptor mcp` (stdio MCP) using the best available binary.
# Resolution order: INTERCEPTOR_BIN → interceptor on PATH → $(go env GOPATH)/bin/interceptor[.exe]
# Use this for clients that only support stdio (not Streamable HTTP).
# For Cursor, prefer `.cursor/mcp.json` with url http://127.0.0.1:9966/mcp instead — it
# always matches the running Interceptor and updates when you restart after `interceptor update`.

$ErrorActionPreference = 'Stop'

function Resolve-InterceptorBin {
    if ($env:INTERCEPTOR_BIN -and (Test-Path -LiteralPath $env:INTERCEPTOR_BIN)) {
        return (Resolve-Path -LiteralPath $env:INTERCEPTOR_BIN).Path
    }
    $cmd = Get-Command interceptor -ErrorAction SilentlyContinue
    if ($cmd -and $cmd.Source) { return $cmd.Source }
    try {
        $go = (& go env GOPATH 2>$null).Trim()
        if ($go) {
            $c = Join-Path $go 'bin\interceptor.exe'
            if (Test-Path -LiteralPath $c) { return (Resolve-Path -LiteralPath $c).Path }
        }
    } catch {}
    return $null
}

$bin = Resolve-InterceptorBin
if (-not $bin) {
    Write-Error 'interceptor binary not found — install with `go install github.com/Veyal/interceptor/cmd/interceptor@latest` or set INTERCEPTOR_BIN'
    exit 1
}

if (-not $env:INTERCEPTOR_CONTROL_URL) {
    $env:INTERCEPTOR_CONTROL_URL = 'http://127.0.0.1:9966'
}

& $bin mcp @args
