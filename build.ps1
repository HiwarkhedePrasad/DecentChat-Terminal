# Load the environment variables from .env
if (Test-Path ".env") {
    Get-Content .env | Where-Object { $_ -match '=' -and $_ -notmatch '^#' } | ForEach-Object {
        $name, $value = $_ -split '=', 2
        Set-Variable -Name $name.Trim() -Value $value.Trim()
    }
}

if (-not $SUPABASE_URL -or -not $SUPABASE_KEY) {
    Write-Host "Error: SUPABASE_URL or SUPABASE_KEY not found in .env" -ForegroundColor Red
    exit 1
}

Write-Host "Building decentchat.exe with embedded credentials..."
go build -ldflags="-X 'decentchat/internal/config.DefaultSupabaseURL=$SUPABASE_URL' -X 'decentchat/internal/config.DefaultSupabaseKey=$SUPABASE_KEY'" -o decentchat.exe ./cmd

if ($LASTEXITCODE -eq 0) {
    Write-Host "Build successful! Standard executable created: decentchat.exe" -ForegroundColor Green
} else {
    Write-Host "Build failed." -ForegroundColor Red
}
