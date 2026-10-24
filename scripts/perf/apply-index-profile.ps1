#requires -Version 7.2
# Explicit local-only DDL. The application does not auto-apply these migrations.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidateSet('scoped','search','rollback')][string]$Profile,
    [string]$EnvFile='.env.optimization',
    [ValidatePattern('^(asset_perf|asset_opt_(test|index)_[a-z0-9_]+)$')][string]$Database='asset_perf',
    [Parameter(Mandatory)][string]$EvidenceDir
)
$ErrorActionPreference='Stop'
$repo=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repo
$EvidenceDir=[IO.Path]::GetFullPath($EvidenceDir)
if(-not $EvidenceDir.StartsWith((Join-Path $repo 'data/perf/optimization-'),[StringComparison]::OrdinalIgnoreCase)){throw 'DDL evidence must be in an optimization archive'}
if(@(Get-Process -Name k6 -ErrorAction SilentlyContinue).Count){throw 'No index changes while any local k6 load is active'}
$values=@{}
Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$values[$matches[1]]=$matches[2]}}
if($values.POSTGRES_DB -ne 'asset_perf' -or $values.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated optimization database'}
$project='asset-amqp-opt'
if($Database -eq 'asset_perf'){
    $running=@(& docker ps --filter "label=com.docker.compose.project=$project" --format '{{.Label "com.docker.compose.service"}}')
    if($LASTEXITCODE -ne 0){throw 'Could not verify stopped application writers'}
    if(@($running|Where-Object{$_ -notin @('postgres','rabbitmq','minio')}).Count){throw 'Stop all optimization application services before DDL'}
}
New-Item -ItemType Directory -Force -Path $EvidenceDir|Out-Null
$dest=Join-Path $EvidenceDir ('migration-'+$Profile+'-'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff'))
New-Item -ItemType Directory -Path $dest|Out-Null
$compose=@('compose','--env-file',$EnvFile,'-p',$project)
$files=if($Profile -eq 'rollback'){@('rollback_query_indexes.sql')}elseif($Profile -eq 'scoped'){@('004_query_scope_indexes.sql')}else{@('004_query_scope_indexes.sql','005_query_search_indexes.sql')}
$state=@{started_utc=[DateTimeOffset]::UtcNow.ToString('o');project=$project;database=$Database;profile=$Profile;files=@();status='applying'}
try {
    foreach($file in $files){
        $source=Join-Path $repo "deploy/postgres/migrations/asset/$file"
        Copy-Item -LiteralPath $source -Destination (Join-Path $dest $file)
        & docker cp $source "${project}-postgres-1:/tmp/optimization-$file" *> (Join-Path $dest "$file-copy.log")
        if($LASTEXITCODE -ne 0){throw 'Could not copy the explicit migration'}
        & docker @compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U $values.POSTGRES_USER -d $Database -f "/tmp/optimization-$file" *> (Join-Path $dest "$file.log")
        if($LASTEXITCODE -ne 0){throw "Migration failed: $file"}
        $state.files+=@{name=$file;sha256=(Get-FileHash -LiteralPath $source).Hash}
    }
    $query="SELECT json_build_object('indexes',(SELECT json_agg(row_to_json(i)) FROM (SELECT c.relname AS name,pg_get_indexdef(c.oid) AS definition,pg_relation_size(c.oid) AS bytes,x.indisvalid,x.indisready FROM pg_index x JOIN pg_class c ON c.oid=x.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='asset' AND c.relname LIKE '%_live_%_idx' ORDER BY c.relname) i),'pg_trgm_schema',(SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_trgm'));"
    $raw=& docker @compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U $values.POSTGRES_USER -d $Database -Atc $query
    if($LASTEXITCODE -ne 0){throw 'Index verification query failed'}
    $metadata=($raw -join "`n")|ConvertFrom-Json
    $metadata|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $dest 'indexes.json')
    $expected=if($Profile -eq 'scoped'){21}elseif($Profile -eq 'search'){22}else{0}
    $actualIndexes=@($metadata.indexes|Where-Object{$null -ne $_})
    if($actualIndexes.Count -ne $expected -or @($actualIndexes|Where-Object{-not $_.indisvalid -or -not $_.indisready}).Count){throw 'Index count/validity differs from the explicit profile'}
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o')
} catch {$state.status='failed';$state.error=$_.Exception.Message;throw}
finally {$state|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $dest 'state.json')}
Write-Output "Index profile verified: $Profile; database=$Database; evidence=$dest"
