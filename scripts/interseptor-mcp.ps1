# Launch `interseptor mcp` (stdio MCP) using the best available binary.
# Resolution order: INTERSEPTOR_BIN → interseptor on PATH → $(go env GOPATH)/bin/interseptor[.exe]
# Use this for clients that only support stdio (not Streamable HTTP).
# For Cursor, prefer `.cursor/mcp.json` with url http://127.0.0.1:9966/mcp instead — it
# always matches the running Interseptor and updates when you restart after `interseptor update`.

$ErrorActionPreference = 'Stop'

function Resolve-InterseptorBin {
    if ($env:INTERSEPTOR_BIN -and (Test-Path -LiteralPath $env:INTERSEPTOR_BIN)) {
        return (Resolve-Path -LiteralPath $env:INTERSEPTOR_BIN).Path
    }
    $cmd = Get-Command interseptor -ErrorAction SilentlyContinue
    if ($cmd -and $cmd.Source) { return $cmd.Source }
    try {
        $go = (& go env GOPATH 2>$null).Trim()
        if ($go) {
            $c = Join-Path $go 'bin\interseptor.exe'
            if (Test-Path -LiteralPath $c) { return (Resolve-Path -LiteralPath $c).Path }
        }
    } catch {}
    return $null
}

$bin = Resolve-InterseptorBin
if (-not $bin) {
    Write-Error 'interseptor binary not found — install with `go install github.com/Veyal/interseptor/cmd/interseptor@latest` or set INTERSEPTOR_BIN'
    exit 1
}

if (-not $env:INTERSEPTOR_CONTROL_URL) {
    $env:INTERSEPTOR_CONTROL_URL = 'http://127.0.0.1:9966'
}

& $bin mcp @args
