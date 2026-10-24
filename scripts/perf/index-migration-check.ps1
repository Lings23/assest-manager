#requires -Version 7.2
# Test repeatability, rollback and an existing extension in a non-public schema.
[CmdletBinding()]
param([string]$EnvFile='.env.optimization')
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$out=Join-Path $repo 'data/perf/optimization-p1-sql'
if(@(Get-Process -Name k6 -ErrorAction SilentlyContinue).Count){throw 'Migration checks must not overlap load'}
$dest=Join-Path $out ('migration-check-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
New-Item -ItemType Directory -Path $dest|Out-Null
$values=@{};Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database'}
$database='asset_opt_index_'+[DateTime]::UtcNow.ToString('yyyyMMddHHmmssfff')
$compose=@('compose','--env-file',$EnvFile,'-p','asset-amqp-opt')
$state=@{database=$database;started_utc=[DateTimeOffset]::UtcNow.ToString('o');profiles=@();passed=$false}
function Assert-Exit([string]$Step){if($LASTEXITCODE -ne 0){throw "$Step failed"}}
try {
    & docker @compose exec -T postgres createdb -U $values.POSTGRES_USER -T template0 $database *> (Join-Path $dest 'create.log')
    Assert-Exit 'Disposable diagnostic database creation'
    & docker cp (Join-Path $out 'd1.dump') 'asset-amqp-opt-postgres-1:/tmp/index-check-d1.dump' *> (Join-Path $dest 'copy.log')
    Assert-Exit 'D1 copy'
    & docker @compose exec -T postgres pg_restore -U $values.POSTGRES_USER -d $database --exit-on-error /tmp/index-check-d1.dump *> (Join-Path $dest 'restore.log')
    Assert-Exit 'Disposable D1 restore'
    & docker @compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U $values.POSTGRES_USER -d $database -c 'CREATE SCHEMA optimization_extensions; CREATE EXTENSION pg_trgm WITH SCHEMA optimization_extensions;' *> (Join-Path $dest 'extension.log')
    Assert-Exit 'Non-public pg_trgm setup'
    foreach($profile in @('scoped','scoped','search','search','rollback','rollback','search')){
        & (Join-Path $PSScriptRoot 'apply-index-profile.ps1') -Profile $profile -EnvFile $EnvFile -Database $database -EvidenceDir $dest *> (Join-Path $dest ("$($state.profiles.Count)-$profile.log"))
        $state.profiles+=@{profile=$profile;passed=$true}
    }
    $sql="SELECT json_build_object('extension_schema',(SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_trgm'),'active_rows',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),'version_rows',(SELECT count(*) FROM asset.asset_versions));"
    $raw=& docker @compose exec -T postgres psql -X -U $values.POSTGRES_USER -d $database -Atc $sql
    Assert-Exit 'Post-migration correctness'
    $check=($raw -join "`n")|ConvertFrom-Json
    if($check.extension_schema -ne 'optimization_extensions' -or $check.active_rows -ne 38010 -or $check.version_rows -ne 145002){throw 'Migration changed D1 data or the existing extension schema'}
    $state.correctness=$check;$state.passed=$true
} catch {$state.error=$_.Exception.Message;throw}
finally {
    $state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
    $state|ConvertTo-Json -Depth 10|Set-Content -LiteralPath (Join-Path $dest 'result.json')
    # Leave the explicitly named diagnostic database and evidence for review.
}
Write-Output "Migration compatibility verified: $dest"
