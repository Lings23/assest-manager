#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://127.0.0.1:59080/api/v1',
    [Parameter(Mandatory)][string]$EnvFile,
    [Parameter(Mandatory)][string]$OutDir,
    [string]$RunId = ('perf-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfff')),
    [ValidateSet('smoke','import','export','correctness')][string]$Mode = 'smoke',
    [ValidateSet(1000,10000,100000)][int]$Rows = 1000,
    [ValidateSet(1,2,4)][int]$Concurrency = 1,
    [ValidateRange(1,10)][int]$Repetitions = 3,
    [string]$AssetType = 'responsible-department',
    [ValidateSet('F1','F2')][string]$Profile = 'F1',
    [string]$ComposeProject = 'asset-amqp-perf',
    [switch]$SkipDatabaseVerify,
    [long]$ExpectedRows = -1,
    [string]$ExpectedCsvPath = '',
    [string]$RequiredOwnerId = '',
    [ValidateRange(10,1800)][int]$WaitSeconds = 1200
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$EnvFile = (Resolve-Path -LiteralPath $EnvFile).Path
$OutDir = [IO.Path]::GetFullPath($OutDir)
[void][IO.Directory]::CreateDirectory($OutDir)
$phaseDirectory=[IO.DirectoryInfo]$OutDir
while($phaseDirectory -and -not $phaseDirectory.Name.StartsWith('optimization-')){$phaseDirectory=$phaseDirectory.Parent}
$resourceAbort=if($phaseDirectory){Join-Path $phaseDirectory.FullName 'telemetry/abort.json'}else{''}
if($resourceAbort -and (Test-Path -LiteralPath $resourceAbort)){throw 'Performance resource stop prevents another file task'}
if ($RunId -notmatch '^[a-zA-Z0-9_.-]{1,80}$') { throw 'RunId must be 1-80 ASCII letters, digits, dot, underscore or hyphen' }
if (-not $SkipDatabaseVerify -and $ComposeProject -notmatch '^asset-amqp-[a-zA-Z0-9_-]+$') { throw 'Database verification requires an explicitly isolated asset-amqp-* project' }
$gateway = [Uri]$BaseUrl
if ($gateway.Host -notin @('127.0.0.1','localhost','::1') -or $gateway.AbsolutePath.TrimEnd('/') -ne '/api/v1') { throw 'BaseUrl must be the local Gateway /api/v1 address' }
if ($ExpectedCsvPath) { $ExpectedCsvPath = (Resolve-Path -LiteralPath $ExpectedCsvPath).Path }
$values = @{}
foreach ($line in [IO.File]::ReadAllLines($EnvFile)) {
    if ($line -match '^\s*([^#=]+)=(.*)$') { $values[$matches[1].Trim()] = $matches[2].Trim().Trim('"').Trim("'") }
}
if (-not $values.IAM_BOOTSTRAP_ADMIN_PASSWORD) { throw 'IAM_BOOTSTRAP_ADMIN_PASSWORD is required in EnvFile' }
$username = if ($values.PERF_ADMIN_USERNAME) { $values.PERF_ADMIN_USERNAME } elseif ($values.IAM_BOOTSTRAP_ADMIN_USERNAME) { $values.IAM_BOOTSTRAP_ADMIN_USERNAME } else { 'admin' }
$password = if ($values.PERF_ADMIN_PASSWORD) { $values.PERF_ADMIN_PASSWORD } else { $values.IAM_BOOTSTRAP_ADMIN_PASSWORD }

# Functions are injected into each runspace; each worker owns its cookie jar and refresh token.
$library = {
    function Save-Json($Value, [string]$Path) {
        [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 30), [Text.UTF8Encoding]::new($false))
    }
    function Read-CsvSummary([string]$Path, [string[]]$Columns = @(), [string]$OwnerId = '') {
        $parser = [Microsoft.VisualBasic.FileIO.TextFieldParser]::new($Path, [Text.Encoding]::UTF8, $true)
        $parser.SetDelimiters(','); $parser.HasFieldsEnclosedInQuotes = $true; $parser.TrimWhiteSpace = $false
        try {
            $header = $parser.ReadFields()
            if (-not $header -or @($header | Select-Object -Unique).Count -ne $header.Count) { throw 'CSV has an empty or duplicate header' }
            if (-not $Columns -or $Columns.Count -eq 0) { $Columns = [string[]]$header.Clone() }
            [Array]::Sort($Columns, [StringComparer]::Ordinal)
            $indexes = foreach ($column in $Columns) {
                $index = [Array]::IndexOf($header, $column)
                if ($index -lt 0) { throw "CSV lacks expected column: $column" }
                $index
            }
            $ownerIndex = [Array]::IndexOf($header, 'owner_id')
            if ($OwnerId -and $ownerIndex -lt 0) { throw 'CSV lacks owner_id' }
            $hashes = [Collections.Generic.List[string]]::new()
            $samples = [Collections.Generic.List[object]]::new()
            $badOwners = 0
            while (-not $parser.EndOfData) {
                $fields = $parser.ReadFields()
                if ($fields.Count -ne $header.Count) { throw 'CSV row has inconsistent column count' }
                if ($OwnerId -and $fields[$ownerIndex] -ne $OwnerId) { $badOwners++ }
                $row = [Text.StringBuilder]::new()
                foreach ($i in $indexes) {
                    $value = $fields[$i].Trim().Replace("`r`n", "`n")
                    [void]$row.Append($value.Length).Append(':').Append($value)
                }
                $bytes = [Text.Encoding]::UTF8.GetBytes($row.ToString())
                $hashes.Add([Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($bytes)))
                if ($samples.Count -lt 3) {
                    $sample = [ordered]@{}
                    foreach ($i in $indexes) { $sample[$header[$i]] = $fields[$i] }
                    $samples.Add($sample)
                }
            }
            $orderedHashes = $hashes.ToArray(); [Array]::Sort($orderedHashes, [StringComparer]::Ordinal)
            $hasher = [Security.Cryptography.IncrementalHash]::CreateHash([Security.Cryptography.HashAlgorithmName]::SHA256)
            try {
                $hasher.AppendData([Text.Encoding]::UTF8.GetBytes(($Columns -join "`0")))
                foreach ($digest in $orderedHashes) { $hasher.AppendData([Text.Encoding]::ASCII.GetBytes($digest)) }
                $canonical = [Convert]::ToHexString($hasher.GetHashAndReset()).ToLowerInvariant()
            } finally { $hasher.Dispose() }
            return [ordered]@{ rows=$hashes.Count; bytes=([IO.FileInfo]$Path).Length; columns=$header; digest_columns=$Columns; sha256=(Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant(); normalized_content_sha256=$canonical; unexpected_owners=$badOwners; sample=$samples.ToArray() }
        } finally { $parser.Dispose() }
    }
    function New-PerfSession([string]$Base, [string]$UserName, [string]$Password, [string]$RequestLog) {
        $handler = [Net.Http.HttpClientHandler]::new(); $handler.CookieContainer = [Net.CookieContainer]::new()
        $client = [Net.Http.HttpClient]::new($handler); $client.Timeout = [TimeSpan]::FromSeconds(45)
        $session = @{ client=$client; base=$Base.TrimEnd('/'); token=''; refresh_at=[DateTime]::MaxValue; request_log=$RequestLog }
        $login = Invoke-PerfHttp $session POST '/auth/login' @{username=$UserName;password=$Password} 'auth_login' -NoRefresh
        if ($login.status -ne 200 -or -not $login.data.access_token) { $client.Dispose(); throw 'Login failed; see request log (credentials omitted)' }
        if ($login.data.user.must_change_password) { $client.Dispose(); throw 'Initialize the account password before performance tests' }
        $session.token = $login.data.access_token
        $session.refresh_at = [DateTime]::UtcNow.AddSeconds([Math]::Max(10, [int]$login.data.expires_in - 60 - (Get-Random -Minimum 0 -Maximum 20)))
        $session.principal = $login.data.user
        return $session
    }
    function Invoke-PerfHttp($Session, [string]$Method, [string]$Path, $Body, [string]$Phase, [string]$DownloadPath='', [switch]$NoRefresh) {
        if (-not $NoRefresh -and [DateTime]::UtcNow -ge $Session.refresh_at) {
            $refresh = Invoke-PerfHttp $Session POST '/auth/refresh' @{} 'auth_refresh' -NoRefresh
            if ($refresh.status -ne 200 -or -not $refresh.data.access_token) { throw 'Session refresh failed; see request log' }
            $Session.token = $refresh.data.access_token
            $Session.refresh_at = [DateTime]::UtcNow.AddSeconds([Math]::Max(10, [int]$refresh.data.expires_in - 60 - (Get-Random -Minimum 0 -Maximum 20)))
        }
        $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method), $Session.base + $Path)
        if ($Session.token) { $request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $Session.token) }
        if ($null -ne $Body) {
            $json = $Body | ConvertTo-Json -Depth 20 -Compress
            $request.Content = [Net.Http.StringContent]::new($json, [Text.Encoding]::UTF8, 'application/json')
        }
        $utc = [DateTime]::UtcNow.ToString('o'); $watch = [Diagnostics.Stopwatch]::StartNew()
        $response = $null; $status = 0; $requestId = ''; $data = $null; $bytes = @(); $transportError = $false
        try {
            $response = $Session.client.SendAsync($request, [Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
            $status = [int]$response.StatusCode
            $bytes = $response.Content.ReadAsByteArrayAsync().GetAwaiter().GetResult()
            $watch.Stop()
            if ($response.Headers.Contains('X-Request-ID')) { $requestId = [string]::Join(',', $response.Headers.GetValues('X-Request-ID')) }
            if ($DownloadPath -and $status -eq 200) { [IO.File]::WriteAllBytes($DownloadPath, $bytes) }
            elseif ($bytes.Length -gt 0) {
                try { $data = [Text.Encoding]::UTF8.GetString($bytes) | ConvertFrom-Json -Depth 30 } catch { }
                if (-not $requestId -and $data.request_id) { $requestId=$data.request_id }
                if (-not $requestId -and $data.error.request_id) { $requestId=$data.error.request_id }
            }
        } catch { $transportError = $true; $watch.Stop() }
        finally { if ($response) { $response.Dispose() }; $request.Dispose() }
        # Never persist request/response auth bodies, headers, cookies, or exception text.
        $sample = [ordered]@{utc=$utc;phase=$Phase;method=$Method;path=$Path;status=$status;duration_ms=$watch.Elapsed.TotalMilliseconds;received_bytes=$bytes.Length;request_id=$requestId;transport_error=$transportError}
        [IO.File]::AppendAllText($Session.request_log, (($sample | ConvertTo-Json -Compress) + "`n"))
        return @{status=$status;data=$data;duration_ms=$watch.Elapsed.TotalMilliseconds;bytes=$bytes.Length;request_id=$requestId;transport_error=$transportError}
    }
    function Assert-Status($Response, [int[]]$Expected, [string]$Action) {
        if ($Response.status -notin $Expected) { throw "$Action returned HTTP $($Response.status); request_id=$($Response.request_id)" }
    }
    function Get-DatabaseEvidence($Config, [string]$TaskId, [string]$OutputPath) {
        if ($Config.skip_db) { return $null }
        if ($TaskId -notmatch '^[a-fA-F0-9-]{36}$') { throw 'Invalid task ID for SQL verification' }
        $arguments = @('compose','-f',(Join-Path $Config.root 'compose.yaml'),'--env-file',$Config.env_file,'-p',$Config.project,'exec','-T','postgres','psql','-X','-q','--csv','-v','ON_ERROR_STOP=1','-U',$Config.db_user,'-d',$Config.db_name,'-v',('task_ids_json=["'+$TaskId+'"]'))
        $sql = [IO.File]::ReadAllText((Join-Path $Config.root 'scripts/perf/verify.sql'))
        $raw = $sql | & docker @arguments 2>&1
        if ($LASTEXITCODE -ne 0) { throw 'Read-only database verification failed' }
        $text = $raw -join "`n"; [IO.File]::WriteAllText($OutputPath, $text)
        $items = @($text | ConvertFrom-Csv)
        if ($items.Count -ne 1 -or $items[0].task_id -ne $TaskId) { throw 'Database evidence did not return exactly the requested task' }
        return $items[0]
    }
    function Assert-ImportEvidence($Evidence, [int]$Expected) {
        if ($null -eq $Evidence) { return }
        foreach ($field in @('import_keys','parsed_rows','committed_rows','active_assets','import_versions','reported_committed','reported_created')) {
            if ([long]$Evidence.$field -ne $Expected) { throw "Database import count mismatch in $field" }
        }
        if ([int]$Evidence.same_key_tasks -ne 1 -or [int]$Evidence.reported_skipped -ne 0 -or [int]$Evidence.dead_letters -ne 0) { throw 'Database task idempotency/status mismatch' }
    }
}.ToString()
. ([scriptblock]::Create($library))

$worker = {
    param($Library, $Config, $Item)
    . ([scriptblock]::Create($Library))
    $session = $null; $task = $null; $timer = $null; $confirmStart = $null; $barrierSignaled=$false
    $itemPath = Join-Path $Config.out_dir ($Item.label + '.json')
    $result = [ordered]@{label=$Item.label;kind=$Item.kind;run_id=$Config.run_id;asset_type=$Config.asset_type;rows=$Item.rows;profile=$Config.profile;idempotency_key=$Item.key;task_id=$null;state='not_started';passed=$false;timing=[ordered]@{};utc_started=[DateTime]::UtcNow.ToString('o');poll_count=0;db_verified=$false;error=$null}
    try {
        $session = New-PerfSession $Config.base $Config.username $Config.password (Join-Path $Config.out_dir ($Item.label + '.requests.jsonl'))
        $body = @{asset_type=$Config.asset_type;idempotency_key=$Item.key}
        if ($Item.kind -eq 'import') {
            $body.csv = [IO.File]::ReadAllText($Item.csv)
            $result.source_csv = $Item.csv
            $result.source = Read-CsvSummary $Item.csv
            if ($result.source.rows -ne $Item.rows) { throw 'Generated source CSV row count mismatch' }
        }
        if ($Item.gate) { [void]$Item.ready.Signal(); $barrierSignaled=$true; if (-not $Item.gate.Wait(60000)) { throw 'Concurrent request start barrier timed out' } }
        $result.utc_create_started = [DateTime]::UtcNow.ToString('o')
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $created = Invoke-PerfHttp $session POST ('/'+$Item.kind+'-jobs') $body ($Item.kind+'_create')
        $createReturnedMs = $timer.Elapsed.TotalMilliseconds
        $result.timing.create_ms = $created.duration_ms
        Assert-Status $created @(200,201,202) 'Task creation'
        $task = $created.data.task
        if (-not $task.id) { throw 'Task creation response lacks task ID' }
        $result.task_id = $task.id; $result.created = $created.data.created; $result.state = $task.state
        Save-Json $result $itemPath
        if ($Item.request_only) { $result.passed=$true; return [pscustomobject]$result }
        if ($Item.kind -eq 'import') {
            $preflightDeadline = [DateTime]::UtcNow.AddSeconds($Config.wait_seconds)
            while ($task.state -ne 'awaiting_confirmation') {
                if ($task.state -in @('dead_letter','cancelled') -or [DateTime]::UtcNow -gt $preflightDeadline) { throw 'Async import preflight failed or timed out' }
                Start-Sleep -Milliseconds 250
                $preflightPoll = Invoke-PerfHttp $session GET ('/tasks/'+$task.id) $null 'preflight_poll'
                Assert-Status $preflightPoll @(200) 'Preflight polling'
                $task = $preflightPoll.data.task
            }
            $result.timing.preflight_ms = $timer.Elapsed.TotalMilliseconds
            $result.timing.preflight_mode = 'async'

            if ($task.state -ne 'awaiting_confirmation' -or $task.result.total -ne $Item.rows -or $task.result.valid -ne $Item.rows -or $task.result.error_count -ne 0) { throw 'Import preflight counts/state mismatch' }
            $result.timing.create_to_confirm_gap_ms = $timer.Elapsed.TotalMilliseconds - $createReturnedMs
            $result.timing.upload_prepare_and_client_overhead_ms = $createReturnedMs - $created.duration_ms
            $confirmStart = $timer.Elapsed.TotalMilliseconds
            $confirm = Invoke-PerfHttp $session POST ('/import-jobs/'+$task.id+'/confirm') @{} 'import_confirm'
            $result.timing.confirm_ms = $confirm.duration_ms
            Assert-Status $confirm @(200,202) 'Import confirmation'
            $result.state = 'confirmed'; Save-Json $result $itemPath
        }
        $deadline = [DateTime]::UtcNow.AddSeconds($Config.wait_seconds)
        while ($true) {
            if ($Config.resource_abort -and (Test-Path -LiteralPath $Config.resource_abort)) { throw 'Performance collector requested pressure stop; inspect the active task before resuming' }
            $poll = Invoke-PerfHttp $session GET ('/tasks/'+$task.id) $null 'task_poll'
            $result.poll_count++
            if ($poll.status -eq 200 -and $poll.data.task) {
                $task = $poll.data.task; $result.state = $task.state
                if ($task.state -eq 'running' -and $task.attempt -ge 1 -and -not $result.timing.Contains('first_running_observed_ms')) { $result.timing.first_running_observed_ms=$timer.Elapsed.TotalMilliseconds }
                if ($task.state -in @('succeeded','dead_letter','cancelled')) { break }
            }
            # Failed is retryable; poll errors remain in the request log and the task denominator.
            if ([DateTime]::UtcNow -ge $deadline) { $result.state='client_timeout'; throw 'Task exceeded client wait budget; server task may still be running' }
            Start-Sleep -Milliseconds (Get-Random -Minimum 1750 -Maximum 2251)
        }
        $result.timing.create_to_complete_ms = $timer.Elapsed.TotalMilliseconds
        $result.utc_completion_observed = [DateTime]::UtcNow.ToString('o')
        if ($null -ne $confirmStart) { $result.timing.confirm_to_complete_ms = $timer.Elapsed.TotalMilliseconds - $confirmStart }
        $result.attempt = $task.attempt; $result.server_result=$task.result
        if ($task.state -ne 'succeeded' -or $task.attempt -ne 1) { throw 'Normal task did not succeed in one attempt' }
        if ($Item.kind -eq 'import') {
            if ($task.result.committed -ne $Item.rows -or $task.result.created -ne $Item.rows -or $task.result.skipped -ne 0) { throw 'Import completion counts mismatch' }
            $before = Get-DatabaseEvidence $Config $task.id (Join-Path $Config.out_dir ($Item.label+'.db-before.csv'))
            Assert-ImportEvidence $before $Item.rows
            $replay = Invoke-PerfHttp $session POST '/import-jobs' $body 'import_samekey_replay'
            Assert-Status $replay @(200,201,202) 'Idempotent replay'
            if ($replay.data.created -ne $false -or $replay.data.task.id -ne $task.id) { throw 'Same-key replay created a different task' }
            $after = Get-DatabaseEvidence $Config $task.id (Join-Path $Config.out_dir ($Item.label+'.db-after.csv'))
            Assert-ImportEvidence $after $Item.rows
            foreach ($sample in $result.source.sample) {
                $lookup = Invoke-PerfHttp $session GET ('/assets/'+$Config.asset_type+'?limit=20&search='+[Uri]::EscapeDataString($sample.department_code)) $null 'import_sample_verify'
                Assert-Status $lookup @(200) 'Imported content read'
                $matching = @($lookup.data.items | Where-Object { $_.fields.department_code -eq $sample.department_code })
                if ($matching.Count -ne 1) { throw 'Imported deterministic sample was not found exactly once' }
                foreach ($column in $sample.Keys) {
                    if ([string]$matching[0].fields.$column -ne [string]$sample[$column].Trim().Replace("`r`n","`n")) { throw "Imported deterministic sample content differs in $column" }
                }
            }
            $result.replay_passed=$true; $result.db_verified=($null -ne $after)
            $result.timing.e2e_ms=$result.timing.create_to_complete_ms
        } else {
            $downloadPath = Join-Path $Config.out_dir ($Item.label+'.csv')
            $download = Invoke-PerfHttp $session GET ('/export-jobs/'+$task.id+'/download') $null 'export_download' $downloadPath
            Assert-Status $download @(200) 'Export download'
            $result.utc_download_completed = [DateTime]::UtcNow.ToString('o')
            $result.timing.download_ms=$download.duration_ms
            # Record the wall interval, including the observed completion to download gap.
            $result.timing.e2e_ms=$timer.Elapsed.TotalMilliseconds
            $result.timing.download_mib_per_second = if ($download.duration_ms -gt 0) { $download.bytes/1MB/($download.duration_ms/1000) } else { $null }
            $projection=@(); $expected=$null
            if ($Config.expected_csv) { $expected=Read-CsvSummary $Config.expected_csv; $projection=[string[]]$expected.digest_columns }
            $result.download=Read-CsvSummary $downloadPath $projection $Config.required_owner
            if ($result.download.rows -ne $task.result.rows -or ($Config.expected_rows -ge 0 -and $result.download.rows -ne $Config.expected_rows)) { throw 'Parsed export row count mismatch' }
            if ($result.download.unexpected_owners -ne 0) { throw 'Export includes an unexpected owner' }
            if ($expected -and ($expected.rows -ne $result.download.rows -or $expected.normalized_content_sha256 -ne $result.download.normalized_content_sha256)) { throw 'Export normalized content differs from frozen expected CSV' }
            $result.content_verified=($null -ne $expected)
        }
        $result.passed=$true
    } catch {
        $result.error=$_.Exception.Message
        if ($result.state -eq 'not_started') { $result.state='client_error' }
    } finally {
        if ($Item.ready -and -not $barrierSignaled) { [void]$Item.ready.Signal() }
        if ($timer) { $timer.Stop() }
        if ($session) { $session.client.Dispose() }
        $result.utc_finished=[DateTime]::UtcNow.ToString('o')
        Save-Json $result $itemPath
    }
    return [pscustomobject]$result
}.ToString()

$config = @{root=$root;env_file=$EnvFile;out_dir=$OutDir;run_id=$RunId;project=$ComposeProject;base=$BaseUrl;username=$username;password=$password;asset_type=$AssetType;profile=$Profile;wait_seconds=$WaitSeconds;skip_db=[bool]$SkipDatabaseVerify;db_user=$values.POSTGRES_USER;db_name=$values.POSTGRES_DB;expected_rows=$ExpectedRows;expected_csv=$ExpectedCsvPath;required_owner=$RequiredOwnerId;resource_abort=$resourceAbort}
if (-not $config.db_user) { $config.db_user='asset' }; if (-not $config.db_name) { $config.db_name='asset_platform' }

function New-SourceCsv([string]$Label, [int]$Count) {
    if ($AssetType -ne 'responsible-department') { throw 'Generated F1/F2 source supports responsible-department; extend with schema-backed fields for another asset type' }
    $path=Join-Path $OutDir ($Label+'.source.csv')
    $writer=[IO.StreamWriter]::new($path,$false,[Text.UTF8Encoding]::new($false))
    try {
        if ($Profile -eq 'F1') { $writer.WriteLine('department_name,department_code') }
        else { $writer.WriteLine('department_name,department_code,department_head,head_phone,department_fax,remarks') }
        $prefix = 'P' + (Get-FileHash -InputStream ([IO.MemoryStream]::new([Text.Encoding]::UTF8.GetBytes($RunId+'-'+$Label))) -Algorithm SHA256).Hash.Substring(0,16)
        for ($index=0; $index -lt $Count; $index++) {
            # Fixed-width suffixes prevent the exact code of sample 1 matching
            # thousands of later rows (10, 100, ...) in the public ILIKE search.
            $rowNumber = $index.ToString('D6', [Globalization.CultureInfo]::InvariantCulture)
            if ($Profile -eq 'F1') { $writer.WriteLine("压测部门$prefix-$rowNumber,$prefix-$rowNumber") }
            else {
                $remarks=('合法中文备注，含逗号、"引号"与换行。' + "`n" + ('宽字段样本' * 24)).Replace('"','""')
                $writer.WriteLine(('压测部门{0}-{1},{0}-{1},测试负责人,010-55550000,010-55550001,"{2}"' -f $prefix,$rowNumber,$remarks))
            }
        }
    } finally { $writer.Dispose() }
    return $path
}
function Invoke-Batch($Items, [int]$Slots) {
    $pool=[RunspaceFactory]::CreateRunspacePool(1,$Slots); $pool.Open()
    $handles=[Collections.Generic.List[object]]::new()
    $watch=[Diagnostics.Stopwatch]::StartNew()
    try {
        foreach ($item in $Items) {
            $shell=[PowerShell]::Create(); $shell.RunspacePool=$pool
            [void]$shell.AddScript($worker).AddArgument($library).AddArgument($config).AddArgument($item)
            $handles.Add(@{shell=$shell;async=$shell.BeginInvoke();item=$item})
        }
        if ($Items.Count -gt 0 -and $Items[0].gate) {
            if (-not $Items[0].ready.Wait(60000)) { throw 'Concurrent session login barrier timed out' }
            $Items[0].gate.Set()
        }
        $output=[Collections.Generic.List[object]]::new()
        foreach ($handle in $handles) {
            try {
                $returned=@($handle.shell.EndInvoke($handle.async))
                if ($returned.Count -ne 1) { throw 'Worker did not return one task result' }
                $output.Add($returned[0])
            } catch {
                $failure=[pscustomobject]@{label=$handle.item.label;kind=$handle.item.kind;task_id=$null;state='runspace_error';passed=$false;error='Runspace failed; inspect individual evidence file';rows=$handle.item.rows}
                $output.Add($failure)
                Save-Json $failure (Join-Path $OutDir ($handle.item.label+'.runspace-error.json'))
            }
        }
        $watch.Stop()
        return @{items=$output.ToArray();wall_ms=$watch.Elapsed.TotalMilliseconds}
    } finally {
        if ($Items.Count -gt 0 -and $Items[0].gate) { $Items[0].gate.Set() }
        foreach ($handle in $handles) { $handle.shell.Dispose() }; $pool.Dispose()
    }
}

$results=[Collections.Generic.List[object]]::new(); $batches=[Collections.Generic.List[object]]::new()
$summaryPath=Join-Path $OutDir ($Mode+'-'+$RunId+'.manifest.json')
function Save-Manifest {
    $passed=@($results | Where-Object passed).Count
    $summary=[ordered]@{run_id=$RunId;mode=$Mode;asset_type=$AssetType;profile=$Profile;rows=$Rows;concurrency=$Concurrency;repetitions=$Repetitions;task_denominator=$results.Count;passed=$passed;failed_or_unfinished=$results.Count-$passed;database_verification_skipped=[bool]$SkipDatabaseVerify;expected_rows=$ExpectedRows;expected_csv=$ExpectedCsvPath;batches=$batches.ToArray();tasks=$results.ToArray()}
    Save-Json $summary $summaryPath
}

if ($Mode -in @('import','export','smoke')) {
    $stopSubmission=$false
    $rounds=if ($Mode -eq 'smoke') {1} else {$Repetitions}
    $slots=if ($Mode -eq 'smoke') {1} else {$Concurrency}
    foreach ($kind in $(if ($Mode -eq 'smoke') {@('import','export')} else {@($Mode)})) {
        if ($stopSubmission) { break }
        for ($round=1;$round -le $rounds;$round++) {
            $items=for ($slot=1;$slot -le $slots;$slot++) {
                $label="$Mode-$kind-$Profile-$Rows-c$slots-r$round-s$slot-$RunId"
                $count=if ($Mode -eq 'smoke') {1} else {$Rows}
                @{label=$label;kind=$kind;rows=$count;key=$label;csv=$(if ($kind -eq 'import') {New-SourceCsv $label $count} else {''});request_only=$false}
            }
            $batch=Invoke-Batch @($items) $slots
            foreach ($result in $batch.items) { $results.Add($result) }
            $success=@($batch.items | Where-Object passed)
            $processed=if ($kind -eq 'import') {($success | Measure-Object rows -Sum).Sum} else {($success | ForEach-Object {$_.download.rows} | Measure-Object -Sum).Sum}
            $windowStart=($batch.items | Where-Object utc_create_started | ForEach-Object {[DateTimeOffset]::Parse($_.utc_create_started)} | Sort-Object | Select-Object -First 1)
            $windowEnd=($batch.items | Where-Object utc_completion_observed | ForEach-Object {[DateTimeOffset]::Parse($_.utc_completion_observed)} | Sort-Object | Select-Object -Last 1)
            $processingMs=if ($windowStart -and $windowEnd) {($windowEnd-$windowStart).TotalMilliseconds} else {$null}
            $downloadEnd=($batch.items | Where-Object utc_download_completed | ForEach-Object {[DateTimeOffset]::Parse($_.utc_download_completed)} | Sort-Object | Select-Object -Last 1)
            $downloadWindowMs=if ($windowStart -and $downloadEnd) {($downloadEnd-$windowStart).TotalMilliseconds} else {$null}
            $batches.Add(@{kind=$kind;round=$round;task_count=$slots;successful=$success.Count;failed_or_unfinished=$slots-$success.Count;orchestration_wall_ms=$batch.wall_ms;processing_wall_ms=$processingMs;complete_with_download_wall_ms=$downloadWindowMs;rows_per_second=$(if ($processingMs -gt 0) {$processed/($processingMs/1000)} else {$null});includes_login_and_validation=$false;clock_note='Task durations use Stopwatch; batch windows use UTC timestamps'})
            Save-Manifest
            if ($success.Count -ne $slots) { $stopSubmission=$true; Write-Warning 'A task failed; stopping additional task submission'; break }
        }
    }
} else {
    # Ten separate sessions enter POST at the same barrier. Only one may report created=true.
    $gate=[Threading.ManualResetEventSlim]::new($false);$ready=[Threading.CountdownEvent]::new(10)
    $label="correctness-samekey-$RunId";$csv=New-SourceCsv $label 1
    $items=for ($i=1;$i -le 10;$i++) {@{label="$label-$i";kind='import';rows=1;key=$label;csv=$csv;request_only=$true;gate=$gate;ready=$ready}}
    $batch=Invoke-Batch @($items) 10
    foreach ($result in $batch.items) {$results.Add($result)}
    $ids=@($batch.items | Where-Object passed | Select-Object -ExpandProperty task_id -Unique)
    $creators=@($batch.items | Where-Object created).Count
    $sameKeyOK=(@($batch.items | Where-Object passed).Count -eq 10 -and $ids.Count -eq 1 -and $creators -eq 1)
    $batches.Add(@{kind='concurrent_samekey';requests=10;unique_tasks=$ids.Count;created_true=$creators;passed=$sameKeyOK})
    Save-Manifest
    if ($sameKeyOK) {
        # Reopening the same key only returns the preflighted task; confirm once, then verify side effects.
        $follow=Invoke-Batch @(@{label="$label-complete";kind='import';rows=1;key=$label;csv=$csv;request_only=$false}) 1
        foreach ($result in $follow.items) {$results.Add($result)}
    }
    $gate.Dispose();$ready.Dispose()
    $session=New-PerfSession $BaseUrl $username $password (Join-Path $OutDir ('correctness-version-'+$RunId+'.requests.jsonl'))
    try {
        $created=Invoke-PerfHttp $session POST ('/assets/'+$AssetType) @{fields=@{department_name="并发版本$RunId";department_code=('V'+[Guid]::NewGuid().ToString('N'))}} 'version_asset_create'
        Assert-Status $created @(201) 'Version test asset creation'
        $asset=$created.data.asset; $requests=[Collections.Generic.List[object]]::new()
        $versionWatch=[Diagnostics.Stopwatch]::StartNew()
        for ($i=0;$i -lt 10;$i++) {
            $request=[Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::Patch,($BaseUrl.TrimEnd('/')+'/assets/'+$AssetType+'/'+$asset.id))
            $request.Headers.Authorization=[Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer',$session.token)
            $request.Content=[Net.Http.StringContent]::new((@{version=$asset.version;fields=@{department_name="并发版本$RunId-$i"}}|ConvertTo-Json -Compress),[Text.Encoding]::UTF8,'application/json')
            $requests.Add(@{request=$request;pending=$session.client.SendAsync($request);started_ms=$versionWatch.Elapsed.TotalMilliseconds})
        }
        $samples=@();$success=0;$conflicts=0
        foreach ($sent in $requests) {
            try {
                $response=$sent.pending.GetAwaiter().GetResult();$text=$response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
                $status=[int]$response.StatusCode;$requestId=if($response.Headers.Contains('X-Request-ID')){[string]::Join(',',$response.Headers.GetValues('X-Request-ID'))}else{''}
                if($status -eq 200){$success++};if($status -eq 409){$conflicts++}
                $samples+=@{status=$status;request_id=$requestId;duration_upper_bound_ms=$versionWatch.Elapsed.TotalMilliseconds-$sent.started_ms}
                $response.Dispose()
            } catch {$samples+=@{status=0;transport_error=$true}} finally {$sent.request.Dispose()}
        }
        $got=Invoke-PerfHttp $session GET ('/assets/'+$AssetType+'/'+$asset.id) $null 'version_asset_verify'
        Assert-Status $got @(200) 'Version final read'
        $versions=Invoke-PerfHttp $session GET ('/assets/'+$AssetType+'/'+$asset.id+'/versions') $null 'version_history_verify'
        Assert-Status $versions @(200) 'Version history read'
        $versionOK=($success -eq 1 -and $conflicts -eq 9 -and $got.data.asset.version -eq ($asset.version+1) -and @($versions.data.versions).Count -eq 2)
        $versionResult=@{kind='concurrent_sameversion';asset_id=$asset.id;initial_version=$asset.version;final_version=$got.data.asset.version;success=$success;conflicts=$conflicts;requests=$samples;passed=$versionOK}
        Save-Json $versionResult (Join-Path $OutDir ('correctness-version-'+$RunId+'.json'))
        $batches.Add($versionResult)
    } finally {$session.client.Dispose()}
    Save-Manifest
    if(-not $sameKeyOK -or -not $versionOK){throw 'Concurrency correctness failed; evidence is preserved in manifest'}
}
Save-Manifest
Write-Output "Jobs complete: mode=$Mode total=$($results.Count) passed=$(@($results | Where-Object passed).Count) manifest=$summaryPath"
if (@($results | Where-Object {-not $_.passed}).Count -gt 0) { throw 'Some jobs failed or remain unfinished; inspect the manifest' }
