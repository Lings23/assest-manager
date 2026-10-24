#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$OutDir='data/perf/20261016-local-baseline',
    [string]$EnvFile='.env.perf',
    [ValidatePattern('^asset_perf_explain_[0-9]{8}$')][string]$DiagnosticDatabase='asset_perf_explain_20261017'
)
$ErrorActionPreference='Stop'
$repoRoot=Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location -LiteralPath $repoRoot
$OutDir=[IO.Path]::GetFullPath($OutDir)
$allowed=[IO.Path]::GetFullPath((Join-Path $repoRoot 'data/perf'))+[IO.Path]::DirectorySeparatorChar
if(-not $OutDir.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)){throw 'OutDir must be inside ignored data/perf'}
$final=Get-Content -LiteralPath (Join-Path $OutDir 'finalization-status.json') -Raw|ConvertFrom-Json
if($final.status -ne 'archived'){throw 'Formal tests and archival must finish before diagnostic SQL'}
$config=@{}
Get-Content -LiteralPath $EnvFile|ForEach-Object{if($_ -match '^([^#=]+)=(.*)$'){$config[$matches[1]]=$matches[2]}}
if($config.POSTGRES_DB -ne 'asset_perf' -or $config.POSTGRES_PORT -ne '59432'){throw 'Unexpected isolated database configuration'}
$dump=Join-Path $OutDir 'd1.dump'
$manifest=Get-Content -LiteralPath (Join-Path $OutDir 'seed-d1/seed-manifest.json') -Raw|ConvertFrom-Json
if($manifest.state -ne 'completed' -or $manifest.dataset -ne 'D1'){throw 'Completed D1 required'}
$dest=Join-Path $OutDir 'sql-diagnostic'
if(Test-Path -LiteralPath $dest){throw 'Diagnostic evidence directory exists; do not overwrite'}
$null=New-Item -ItemType Directory -Path $dest
Copy-Item -LiteralPath $PSCommandPath -Destination (Join-Path $dest 'explain-after.ps1')
$state=@{status='starting';started_utc=[DateTimeOffset]::UtcNow.ToString('o');database=$DiagnosticDatabase;dump_sha256=(Get-FileHash -LiteralPath $dump).Hash;source='D1 snapshot restored to a new database after all measurements';limitations='Literal standalone plans; not application prepared-statement or concurrent HTTP timings. ANALYZE affects only the diagnostic copy. No cold-cache claim. No index or application changes.';queries=@();error=$null}
$state.script_sha256=(Get-FileHash -LiteralPath $PSCommandPath).Hash
$state.query_source_sha256=(Get-FileHash -LiteralPath 'platform/internal/asset/store.go').Hash
$statePath=Join-Path $dest 'state.json'
function Save-State {$state|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $statePath -Encoding utf8}
$compose=@('compose','--env-file',$EnvFile,'-p','asset-amqp-perf')
function Psql([string]$Sql,[string]$Database=$DiagnosticDatabase){
    $output=& docker @compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U $config.POSTGRES_USER -d $Database -Atq -c $Sql
    if($LASTEXITCODE -ne 0){throw 'Diagnostic SQL command failed'}
    return ($output -join "`n")
}
function Explain([string]$Name,[string]$Sql){
    $Sql|Set-Content -LiteralPath (Join-Path $dest "$Name.sql") -Encoding utf8
    $raw=Psql "BEGIN READ ONLY; SET LOCAL statement_timeout='60s'; EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) $Sql; COMMIT;"
    $plan=$raw|ConvertFrom-Json
    $raw|Set-Content -LiteralPath (Join-Path $dest "$Name.json") -Encoding utf8
    $state.queries+=@{name=$Name;execution_ms=$plan[0].'Execution Time';planning_ms=$plan[0].'Planning Time';top_node=$plan[0].Plan.'Node Type';rows=$plan[0].Plan.'Actual Rows'}
    Save-State
}
Save-State
try{
    & docker @compose up --no-build -d --wait postgres *> (Join-Path $dest 'postgres-start.log')
    if($LASTEXITCODE -ne 0){throw 'Could not start isolated PostgreSQL'}
    $existing=Psql "SELECT count(*) FROM pg_database WHERE datname='$DiagnosticDatabase';" 'postgres'
    if([int]$existing -ne 0){throw 'Diagnostic database already exists; no restore or removal permitted'}
    $null=Psql "CREATE DATABASE $DiagnosticDatabase;" 'postgres'
    & docker cp $dump 'asset-amqp-perf-postgres-1:/tmp/perf-diagnostic-d1.dump' *> (Join-Path $dest 'copy.log')
    if($LASTEXITCODE -ne 0){throw 'Diagnostic dump copy failed'}
    & docker @compose exec -T postgres pg_restore -U $config.POSTGRES_USER -d $DiagnosticDatabase --exit-on-error /tmp/perf-diagnostic-d1.dump *> (Join-Path $dest 'restore.log')
    if($LASTEXITCODE -ne 0){throw 'Diagnostic copy restore failed'}
    Psql "SELECT json_build_object('server',version(),'settings',(SELECT json_object_agg(name,setting) FROM pg_settings WHERE name IN ('shared_buffers','work_mem','effective_cache_size','random_page_cost','max_connections','jit')),'indexes',(SELECT json_agg(row_to_json(i)) FROM (SELECT tablename,indexname,indexdef FROM pg_indexes WHERE schemaname='asset' AND tablename IN ('responsible_department_assets','system_info_assets') ORDER BY tablename,indexname) i));"|Set-Content -LiteralPath (Join-Path $dest 'metadata.json')
    $null=Psql 'ANALYZE asset.responsible_department_assets; ANALYZE asset.system_info_assets;'
    $counts=Psql 'SELECT json_build_object(''responsible_active'',(SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL),''system_active'',(SELECT count(*) FROM asset.system_info_assets WHERE deleted_at IS NULL));'|ConvertFrom-Json
    if($counts.responsible_active -ne 38010 -or $counts.system_active -ne 9499){throw 'Unexpected D1 diagnostic population'}
    $counts|ConvertTo-Json|Set-Content -LiteralPath (Join-Path $dest 'counts.json')
    foreach($limit in @(20,100)){foreach($offset in @(0,1000,10000,38000)){
        Explain "responsible-l$limit-o$offset" "SELECT to_jsonb(t) FROM asset.responsible_department_assets AS t WHERE t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id ASC LIMIT $limit OFFSET $offset"
    }}
    foreach($case in $manifest.search_cases){
        $query=([string]$case.query).Replace("'","''")
        $where="t.deleted_at IS NULL AND (t.department_name::text ILIKE '%$query%' OR t.department_code::text ILIKE '%$query%' OR t.department_head::text ILIKE '%$query%')"
        $actual=[int](Psql "SELECT count(*) FROM asset.responsible_department_assets t WHERE $where;")
        if($actual -ne [int]$case.expected_min -or $actual -ne [int]$case.expected_max){throw 'Diagnostic search selectivity mismatch'}
        Explain "search-$($case.label)" "SELECT to_jsonb(t) FROM asset.responsible_department_assets AS t WHERE $where ORDER BY t.updated_at DESC,t.id ASC LIMIT 20 OFFSET 0"
    }
    Explain 'system-l20-o0' 'SELECT to_jsonb(t) FROM asset.system_info_assets AS t WHERE t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id ASC LIMIT 20 OFFSET 0'
    Explain 'reporting-responsible-page5000-o0' 'SELECT to_jsonb(t) FROM asset.responsible_department_assets AS t WHERE t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id ASC LIMIT 5000 OFFSET 0'
    Explain 'reporting-responsible-page5000-o35000' 'SELECT to_jsonb(t) FROM asset.responsible_department_assets AS t WHERE t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id ASC LIMIT 5000 OFFSET 35000'
    Explain 'reporting-system-page5000-o0' 'SELECT to_jsonb(t) FROM asset.system_info_assets AS t WHERE t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id ASC LIMIT 5000 OFFSET 0'
    Explain 'responsible-id-projection-control' 'SELECT t.id FROM asset.responsible_department_assets AS t WHERE t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id ASC LIMIT 20 OFFSET 38000'
    $s7Path=Join-Path $OutDir 'interference/s7-20261017/state.json'
    if(Test-Path -LiteralPath $s7Path){
        $s7=Get-Content -LiteralPath $s7Path -Raw|ConvertFrom-Json
        $owner=[string]$s7.plan.accounts.online.id
        if($owner -notmatch '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'){throw 'Invalid own-scope diagnostic owner ID'}
        $actual=[int](Psql "SELECT count(*) FROM asset.responsible_department_assets WHERE deleted_at IS NULL AND owner_id='$owner';")
        $expected=[int]$manifest.per_user_counts.$owner.'responsible-department'
        if($actual -ne $expected){throw 'D1 own-scope diagnostic population mismatch'}
        $state.own_scope=@{owner_id=$owner;active_rows=$actual;scope='D1 copy only; does not reproduce S7base or the table after a background import'}
        Explain 'd1-own-responsible-l20' "SELECT to_jsonb(t) FROM asset.responsible_department_assets AS t WHERE t.deleted_at IS NULL AND t.owner_id='$owner' ORDER BY t.updated_at DESC,t.id ASC LIMIT 20 OFFSET 0"
        Explain 'd1-own-search-broad' "SELECT to_jsonb(t) FROM asset.responsible_department_assets AS t WHERE t.deleted_at IS NULL AND t.owner_id='$owner' AND (t.department_name::text ILIKE '%HALF-A%' OR t.department_code::text ILIKE '%HALF-A%' OR t.department_head::text ILIKE '%HALF-A%') ORDER BY t.updated_at DESC,t.id ASC LIMIT 20 OFFSET 0"
    }
    $state.status='completed';$state.finished_utc=[DateTimeOffset]::UtcNow.ToString('o');Save-State
}catch{$state.status='failed';$state.error=$_.Exception.Message;Save-State;throw}
finally{
    & docker @compose down *> (Join-Path $dest 'teardown.log')
    if($LASTEXITCODE -ne 0){$state.teardown_error='Could not stop isolated diagnostic project';Save-State}
}
