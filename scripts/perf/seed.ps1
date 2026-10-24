param(
    [ValidateRange(100, 1000000)][int]$Rows = 100000,
    [ValidateRange(0, 2147483647)][int]$Seed = 20261015,
    [string]$BaseUrl = 'http://127.0.0.1:59080/api/v1',
    [string]$EnvFile = '.env.perf',
    [string]$OutDir = 'data/perf/seed',
    [ValidateRange(-1, 1000000)][int]$VersionedRows = -1,
    [ValidateRange(1, 32)][int]$PreparationConcurrency = 2,
    [ValidateRange(1, 250)][int]$SystemImportChunkRows = 250,
    [switch]$GenerateOnly
)
# This script writes fixtures only through the public Gateway HTTP API. Passwords
# stay in ignored data/perf; no access or refresh token is written to disk.
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$base = $BaseUrl.TrimEnd('/')
if ($base -notmatch '/api/v1$') { throw 'BaseUrl must end in /api/v1' }
if (-not [IO.Path]::IsPathRooted($OutDir)) { $OutDir = Join-Path $root $OutDir }
$OutDir = [IO.Path]::GetFullPath($OutDir)
$allowedRoot = [IO.Path]::GetFullPath((Join-Path $root 'data/perf')) + [IO.Path]::DirectorySeparatorChar
if (-not $OutDir.StartsWith($allowedRoot, [StringComparison]::OrdinalIgnoreCase)) { throw 'OutDir must be inside the ignored repository data/perf directory' }
[void][IO.Directory]::CreateDirectory($OutDir)
$credentialPath = Join-Path $OutDir 'credentials.json'
$manifestPath = Join-Path $OutDir 'seed-manifest.json'
$assetsPath = Join-Path $OutDir 'assets.ndjson'
$prefix = "P$Seed-$Rows"
$types = @('data', 'hardware-software', 'responsible-department', 'software-statistics', 'supply-chain', 'system-info', 'vulnerability')
$targetByType = [ordered]@{}
foreach ($type in $types) { $targetByType[$type] = [int][Math]::Floor($Rows / 10) }
$targetByType['responsible-department'] = $Rows - (6 * [int][Math]::Floor($Rows / 10))
$targetVersioned = [int][Math]::Floor($Rows / 10)
if ($VersionedRows -ge 0) { $targetVersioned = [Math]::Min($targetVersioned, $VersionedRows) }
$targetDeleted = [int][Math]::Floor($Rows / 20)
$dataset = if ($Rows -eq 10000) { 'D0' } elseif ($Rows -eq 100000) { 'D1' } elseif ($Rows -eq 300000) { 'D2' } else { "custom-$Rows" }
$state = [ordered]@{
    schema_version = 1; dataset = $dataset; seed = $Seed; rows = $Rows; prefix = $prefix
    base_url = $base; credentials_file = $credentialPath; asset_types = $types
    started_at = [DateTimeOffset]::UtcNow.ToString('o'); state = 'preparing'
    requested_counts = $targetByType; target_versioned_rows = $targetVersioned
    plan_versioned_rows = [int][Math]::Floor($Rows / 10); target_deleted_rows = $targetDeleted
    deviations = @(); jobs = @(); departments = @(); users = @(); assets = @()
    samples = [ordered]@{}; expected_counts = [ordered]@{}; per_user_counts = [ordered]@{}
    assets_file = $assetsPath; preparation_concurrency = $PreparationConcurrency; system_import_chunk_rows = $SystemImportChunkRows; baseline_deleted_assets = @()
}
if ($targetVersioned -lt $state.plan_versioned_rows) { $state.deviations += "Versioned records explicitly capped at $targetVersioned; plan requires $($state.plan_versioned_rows)." }
function Save-Json($Value, [string]$Path) { [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 40), [Text.UTF8Encoding]::new($false)) }
function Save-State { Save-Json $state $manifestPath }
if (Test-Path -LiteralPath $manifestPath) {
    $previous = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json -AsHashtable
    if ($previous.state -eq 'completed') { throw 'A completed seed exists in OutDir. Restore its snapshot or use a new independent Compose project and output directory.' }
    if ($previous.state -ne 'dry_run' -and ($previous.seed -ne $Seed -or $previous.rows -ne $Rows -or $previous.target_versioned_rows -ne $targetVersioned)) {
        throw 'Resuming a partial seed requires its original Seed, Rows and VersionedRows parameters'
    }
    if($previous.state -ne 'dry_run' -and (($previous.system_import_chunk_rows -and $previous.system_import_chunk_rows -ne $SystemImportChunkRows) -or @($previous.jobs|Where-Object{$_.asset_type -eq 'system-info' -and $_.rows -gt $SystemImportChunkRows}).Count)){
        throw 'System import chunking changed; archive the failed fixture and restore the empty snapshot before preparing it again'
    }
}
if ($GenerateOnly) {
    $state.state = 'dry_run'
    $estimatedImportJobs = 0
    foreach ($type in $types) {
        for($ownerIndex=0;$ownerIndex -lt 20;$ownerIndex++){
            $ownerRows=[int][Math]::Floor($targetByType[$type]/20)+$(if($ownerIndex -lt ($targetByType[$type]%20)){1}else{0})
            $estimatedImportJobs += $(if($type -eq 'system-info'){[int][Math]::Ceiling($ownerRows/[double]$SystemImportChunkRows)}else{[int]($ownerRows -gt 0)})
        }
    }
    $state.estimated_import_jobs = $estimatedImportJobs
    $state.estimated_version_patches = $targetVersioned * 4
    $state.estimated_deletes = $targetDeleted
    $state.deviations += 'GenerateOnly: no HTTP requests, accounts, files or database fixtures were created.'
    Save-State
    Write-Output "Seed dry run: dataset=$dataset rows=$Rows import_jobs=$estimatedImportJobs patches=$($targetVersioned * 4) deletes=$targetDeleted manifest=$manifestPath"
    return
}
if (-not [IO.Path]::IsPathRooted($EnvFile)) { $EnvFile = Join-Path $root $EnvFile }
$values = @{}
Get-Content -LiteralPath $EnvFile | ForEach-Object { if ($_ -match '^([^#=]+)=(.*)$') { $values[$matches[1].Trim()] = $matches[2].Trim().Trim('"').Trim("'") } }
if (-not $values.IAM_BOOTSTRAP_ADMIN_PASSWORD) { throw 'IAM_BOOTSTRAP_ADMIN_PASSWORD is missing' }
$credentials = @{ schema_version = 1; users = @(); admin = @{ username = ($values.IAM_BOOTSTRAP_ADMIN_USERNAME ?? 'admin'); password = $values.IAM_BOOTSTRAP_ADMIN_PASSWORD } }
if (Test-Path -LiteralPath $credentialPath) { $credentials = Get-Content -LiteralPath $credentialPath -Raw | ConvertFrom-Json -AsHashtable }
$handler = [Net.Http.HttpClientHandler]::new()
$handler.UseCookies = $false
$http = [Net.Http.HttpClient]::new($handler)
$http.Timeout = [TimeSpan]::FromSeconds(60)
$sessions = @{}
function Invoke-Json([string]$Method, [string]$Path, $Body = $null, [string]$Token = '') {
    $stopFile = Join-Path (Split-Path -Parent $OutDir) 'telemetry/abort.json'
    if (Test-Path -LiteralPath $stopFile) { throw 'Resource collector requested a stop; no further seed requests will be submitted' }
    $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method), $base + $Path)
    if ($Token) { $request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $Token) }
    if ($null -ne $Body) { $request.Content = [Net.Http.StringContent]::new(($Body | ConvertTo-Json -Depth 20 -Compress), [Text.Encoding]::UTF8, 'application/json') }
    try {
        $response = $http.SendAsync($request).GetAwaiter().GetResult()
        try {
            $raw = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
            if (-not $response.IsSuccessStatusCode) { throw "HTTP $([int]$response.StatusCode) $Method $Path (response body omitted to protect credentials)" }
            if ($raw) { return $raw | ConvertFrom-Json -AsHashtable }
        } finally { $response.Dispose() }
    } finally { $request.Dispose() }
}
function Login-Account($Account) {
    # Every account receives its own login/session. We do not persist or share
    # refresh cookies; this preparatory script renews by login before expiry.
    $pair = Invoke-Json POST '/auth/login' @{ username = $Account.username; password = $Account.password }
    if ($pair.user.must_change_password) {
        $newPassword = $Account.password + '-Ready!'
        $oldPassword = $Account.password
        $Account.password = $newPassword
        Save-Json $credentials $credentialPath
        try { Invoke-Json POST '/auth/change-password' @{ current_password = $oldPassword; new_password = $newPassword } $pair.access_token | Out-Null }
        catch { $Account.password = $oldPassword; Save-Json $credentials $credentialPath; throw }
        $pair = Invoke-Json POST '/auth/login' @{ username = $Account.username; password = $Account.password }
    }
    if ($pair.user.must_change_password) { throw 'Password initialization did not finish' }
    $Account.id = $pair.user.user_id
    $Account.user_id = $pair.user.user_id
    $sessions[$Account.username] = @{ token = $pair.access_token; expires = [DateTimeOffset]::UtcNow.AddSeconds([int]$pair.expires_in - 60) }
    return $pair.user
}
function Get-Token($Account) {
    if (-not $sessions.ContainsKey($Account.username) -or $sessions[$Account.username].expires -le [DateTimeOffset]::UtcNow) { Login-Account $Account | Out-Null }
    return $sessions[$Account.username].token
}
function Public-User($Account) {
    return @{ id = $Account.id; user_id = $Account.id; username = $Account.username; roles = @($Account.roles); data_scope = $Account.data_scope; department_id = $Account.department_id }
}
function Quote-CSV($Value) {
    if ($null -eq $Value) { return '' }
    $textValue = if ($Value -is [bool]) { $Value.ToString().ToLowerInvariant() } else { [Convert]::ToString($Value, [Globalization.CultureInfo]::InvariantCulture) }
    return '"' + $textValue.Replace('"', '""') + '"'
}
function Find-FixtureMarker($Fields, [string]$FixturePrefix) {
    foreach ($value in $Fields.Values) {
        if ($value -is [string] -and $value.StartsWith($FixturePrefix + '-T', [StringComparison]::Ordinal) -and $value -match '-T[0-9]+-R[0-9]{7}(?:\D|$)') { return $value }
    }
    return $null
}
function Get-FixtureSummary($Records, $AssetTypes) {
    # Aggregate the compact identity index once. The visible-count calculation
    # below then visits only a user's owner/department counters, not every row.
    $summary = @{
        expected_counts = [ordered]@{}; samples = [ordered]@{}
        owner_counts = @{}; department_counts = @{}
        versioned_rows = 0; deleted_rows = 0; search_rare = 0; search_broad = 0
    }
    foreach ($type in $AssetTypes) {
        $summary.expected_counts[$type] = @{ total = 0; active = 0; deleted = 0 }
        $summary.samples[$type] = [Collections.Generic.List[object]]::new()
    }
    foreach ($record in $Records) {
        $type = $record.asset_type
        $counts = $summary.expected_counts[$type]
        $counts.total++
        if ($record.deleted) { $counts.deleted++; $summary.deleted_rows++; continue }
        $counts.active++
        if ($record.version -ge 5) { $summary.versioned_rows++ }
        if (-not $summary.owner_counts.ContainsKey($record.owner_id)) { $summary.owner_counts[$record.owner_id] = @{} }
        if (-not $summary.department_counts.ContainsKey($record.department_id)) { $summary.department_counts[$record.department_id] = @{} }
        $summary.owner_counts[$record.owner_id][$type] = [int]$summary.owner_counts[$record.owner_id][$type] + 1
        $summary.department_counts[$record.department_id][$type] = [int]$summary.department_counts[$record.department_id][$type] + 1
        if ($summary.samples[$type].Count -lt 20) {
            $summary.samples[$type].Add(@{ id = $record.id; asset_type = $type; version = $record.version; owner_id = $record.owner_id; department_id = $record.department_id; deleted = $false })
        }
        if ($type -eq 'responsible-department') {
            if (($record.fixture_index % 100) -eq 0) { $summary.search_rare++ }
            if (($record.fixture_index % 2) -eq 0) { $summary.search_broad++ }
        }
    }
    return $summary
}
function Get-FixtureVisibleCounts($Account, $Departments, $Summary, $AssetTypes) {
    $visibleDepartments = [Collections.Generic.HashSet[string]]::new()
    [void]$visibleDepartments.Add([string]$Account.department_id)
    if ($Account.data_scope -eq 'descendants') {
        do {
            $before = $visibleDepartments.Count
            foreach ($department in $Departments) {
                if ($visibleDepartments.Contains([string]$department.parent_id)) { [void]$visibleDepartments.Add([string]$department.id) }
            }
        } while ($visibleDepartments.Count -gt $before)
    }
    $counts = [ordered]@{}
    foreach ($type in $AssetTypes) {
        $counts[$type] = 0
        switch ($Account.data_scope) {
            'global' { $counts[$type] = $Summary.expected_counts[$type].active }
            'own' { if ($Summary.owner_counts.ContainsKey($Account.id)) { $counts[$type] = [int]$Summary.owner_counts[$Account.id][$type] } }
            { $_ -in @('department', 'descendants') } {
                foreach ($departmentID in $visibleDepartments) {
                    if ($Summary.department_counts.ContainsKey($departmentID)) { $counts[$type] += [int]$Summary.department_counts[$departmentID][$type] }
                }
            }
            default { throw "Unsupported fixture data scope $($Account.data_scope)" }
        }
    }
    return $counts
}
function Assert-ImportPreflight($Task, [int]$ExpectedRows) {
    if ($Task.result.total -ne $ExpectedRows -or $Task.result.valid -ne $ExpectedRows -or $Task.result.error_count -ne 0) {
        throw "Seed preflight mismatch for task=$($Task.id) valid=$($Task.result.valid)/$ExpectedRows errors=$($Task.result.error_count)"
    }
}
function Field-Value($Field, [int]$Index, [int]$TypeIndex) {
    # Optional blank CSV cells become absent values; conditional fields remain
    # populated. Values are deterministic and deliberately cover CSV escaping.
    if (-not $Field.Required -and -not $Field.RequiredWhen -and $Field.Type -ne 'boolean' -and ($Index % 11) -eq 0) { return $null }
    if ($null -ne $Field.Enum -and @($Field.Enum).Count -gt 0) { return $Field.Enum[($Index + $Seed) % @($Field.Enum).Count].Value }
    switch ($Field.Type) {
        'boolean' { return (($Index % 2) -eq 0) }
        'date' { return ([datetime]'2025-01-01').AddDays(($Index + ($Seed % 300)) % 365).ToString('yyyy-MM-dd') }
        'integer' {
            $minimum = if ($null -ne $Field.Minimum) { [double]$Field.Minimum } else { 0 }
            $maximum = if ($null -ne $Field.Maximum) { [double]$Field.Maximum } else { $minimum + 100 }
            return [long]($minimum + (($Index + $Seed) % ([int]([Math]::Min(100, $maximum - $minimum)) + 1)))
        }
        'number' { return [double]([Math]::Max(0, [double]$Field.Minimum) + (($Index + $Seed) % 1000) + 0.25) }
        'string' {
            $half = if (($Index % 2) -eq 0) { 'HALF-A' } else { 'HALF-B' }
            $rare = if (($Index % 100) -eq 0) { ' RARE1' } else { '' }
            $value = "$prefix-T$TypeIndex-R$($Index.ToString('D7')) $half$rare 中文"
            if ($Field.Name -in @('remarks', 'modification_log')) { $value += ",引用`"内容`"`n第二行" }
            if ($Field.MaxLength -gt 0 -and $value.Length -gt $Field.MaxLength) { $value = $value.Substring(0, $Field.MaxLength) }
            return $value
        }
        default { throw "Unsupported schema field type $($Field.Type)" }
    }
}
function Wait-Imports($Jobs, $Accounts) {
    $pending = [Collections.Generic.List[object]]::new()
    foreach ($job in $Jobs) { $pending.Add($job) }
    $deadline = [DateTimeOffset]::UtcNow.AddMinutes(20)
    while ($pending.Count -gt 0) {
        for ($j = $pending.Count - 1; $j -ge 0; $j--) {
            $job = $pending[$j]
            $account = $Accounts[$job.owner_index]
            $task = (Invoke-Json GET ('/tasks/' + $job.task_id) $null (Get-Token $account)).task
            $job.state = $task.state; $job.attempt = $task.attempt; $job.result = $task.result
            if ($task.state -eq 'awaiting_confirmation') {
                # A replay can observe a preflight that was still running when
                # the original seed process lost its HTTP response.
                Assert-ImportPreflight $task $job.rows
                Invoke-Json POST ('/import-jobs/' + $job.task_id + '/confirm') @{} (Get-Token $account) | Out-Null
            } elseif ($task.state -eq 'succeeded') {
                if ($task.result.committed -ne $job.rows -or $task.result.created -ne $job.rows -or $task.result.skipped -ne 0) { throw "Seed import count mismatch: $($job.task_id)" }
                $pending.RemoveAt($j)
            } elseif ($task.state -in @('dead_letter', 'cancelled', 'failed')) { throw "Seed import failed: $($job.task_id) state=$($task.state)" }
        }
        Save-State
        if ([DateTimeOffset]::UtcNow -gt $deadline) { throw 'Seed import queue did not drain within 20 minutes' }
        if ($pending.Count -gt 0) { Start-Sleep -Milliseconds 1000 }
    }
}
function Run-Mutations($Records, [string]$Mode, [int]$TargetVersion = 0) {
    $done = 0
    for ($start = 0; $start -lt $Records.Count; $start += $PreparationConcurrency) {
        $stopFile = Join-Path (Split-Path -Parent $OutDir) 'telemetry/abort.json'
        if (Test-Path -LiteralPath $stopFile) { throw 'Resource collector requested a stop during fixture preparation' }
        $token = Get-Token $credentials.admin
        $inflight = [Collections.Generic.List[object]]::new()
        try {
            for ($n = $start; $n -lt [Math]::Min($start + $PreparationConcurrency, $Records.Count); $n++) {
                $record = $Records[$n]
                if ($Mode -eq 'update' -and $record.version -ge $TargetVersion) { continue }
                if ($Mode -eq 'delete' -and $record.deleted) { continue }
                $path = '/assets/' + $record.asset_type + '/' + $record.id
                $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($(if ($Mode -eq 'delete') { 'DELETE' } else { 'PATCH' })), $base + $path + $(if ($Mode -eq 'delete') { '?version=' + $record.version } else { '' }))
                $request.Headers.Authorization = [Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $token)
                if ($Mode -eq 'update') {
                    $fieldName = if ($record.asset_type -eq 'software-statistics') { 'modification_log' } elseif ($record.asset_type -eq 'vulnerability') { 'risk_description' } else { 'remarks' }
                    $payload = @{ version = $record.version; fields = @{ $fieldName = "$prefix version $TargetVersion 中文" } }
                    $request.Content = [Net.Http.StringContent]::new(($payload | ConvertTo-Json -Compress -Depth 5), [Text.Encoding]::UTF8, 'application/json')
                }
                $inflight.Add(@{ request = $request; task = $http.SendAsync($request); record = $record })
            }
            foreach ($item in $inflight) {
                try { $response = $item.task.GetAwaiter().GetResult() }
                catch { throw "Seed $Mode HTTP transport failed: $($_.Exception.ToString())" }
                try {
                    if (-not $response.IsSuccessStatusCode) { throw "Seed $Mode failed: HTTP $([int]$response.StatusCode), type=$($item.record.asset_type), id=$($item.record.id)" }
                    if ($Mode -eq 'update') {
                        $payload = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json -AsHashtable
                        $item.record.version = $payload.asset.version
                    } else { $item.record.version++; $item.record.deleted = $true }
                    $done++
                } finally { $response.Dispose() }
            }
        } finally {
            # Drain started HTTP requests before disposing them, even on failure.
            foreach ($item in $inflight) { try { $res = $item.task.GetAwaiter().GetResult(); $res.Dispose() } catch {}; $item.request.Dispose() }
        }
        if (($start % 1000) -lt $PreparationConcurrency) { Write-Output "Seed $Mode target_version=$TargetVersion processed=$([Math]::Min($start + $PreparationConcurrency, $Records.Count))/$($Records.Count)" }
    }
    return $done
}
try {
    $adminPrincipal = Login-Account $credentials.admin
    $credentials.admin.roles = @($adminPrincipal.roles); $credentials.admin.data_scope = $adminPrincipal.data_scope; $credentials.admin.department_id = $adminPrincipal.department_id
    Save-Json $credentials $credentialPath
    Save-State
    # A functional smoke may leave a soft-deleted fixture in the otherwise empty
    # database. Preserve and report that physical baseline; it is not D0/D1 data.
    foreach ($type in $types) {
        $baselineOffset = 0
        do {
            $existingPage = Invoke-Json GET ("/assets/$type`?include_deleted=true&limit=100&offset=$baselineOffset") $null (Get-Token $credentials.admin)
            foreach ($record in $existingPage.items) {
                $isFixture = $null -ne (Find-FixtureMarker $record.fields $prefix)
                if (-not $isFixture) {
                    if ($null -eq $record.deleted_at) { throw 'Visible pre-existing assets found: restore an empty baseline before seeding' }
                    $state.baseline_deleted_assets += @{ id = $record.id; asset_type = $type; version = $record.version; owner_id = $record.owner_id; department_id = $record.department_id }
                }
            }
            $baselineOffset += @($existingPage.items).Count
        } while (@($existingPage.items).Count -eq 100)
    }
    if ($state.baseline_deleted_assets.Count -gt 0) { $state.deviations += "The baseline contains $($state.baseline_deleted_assets.Count) pre-existing soft-deleted assets; physical table rows exceed the dataset by that amount. They remain excluded from active counts." }
    $departments = @((Invoke-Json GET '/departments' $null (Get-Token $credentials.admin)).departments)
    $departmentSpecs = @(
        @{ key = 'root'; parent = ''; name = "$prefix 总部" },
        @{ key = 'a'; parent = 'root'; name = "$prefix 一级甲" },
        @{ key = 'b'; parent = 'root'; name = "$prefix 一级乙" },
        @{ key = 'a1'; parent = 'a'; name = "$prefix 二级甲一" },
        @{ key = 'a2'; parent = 'a'; name = "$prefix 二级甲二" },
        @{ key = 'b1'; parent = 'b'; name = "$prefix 二级乙一" },
        @{ key = 'b2'; parent = 'b'; name = "$prefix 二级乙二" }
    )
    $departmentIDs = @{}
    foreach ($spec in $departmentSpecs) {
        $existing = @($departments | Where-Object name -eq $spec.name)
        $department = if ($existing.Count -eq 1) { $existing[0] } else { (Invoke-Json POST '/departments' @{ name = $spec.name; parent_id = $(if ($spec.parent) { $departmentIDs[$spec.parent] } else { '' }) } (Get-Token $credentials.admin)).department }
        $departmentIDs[$spec.key] = $department.id
        $state.departments += $department
    }
    $accounts = [Collections.Generic.List[object]]::new()
    $allUsers = @((Invoke-Json GET '/users' $null (Get-Token $credentials.admin)).users)
    $scopeNames = @('own', 'department', 'descendants', 'global')
    $roleNames = @('reporter', 'reviewer', 'auditor', 'admin')
    for ($i = 0; $i -lt 20; $i++) {
        $username = "perf-$Seed-$Rows-" + $i.ToString('D2')
        $known = @($credentials.users | Where-Object username -eq $username)
        $account = if ($known.Count -eq 1) { $known[0] } else { @{ username = $username; password = 'Perf!' + [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(16)) } }
        $scope = $scopeNames[$i % 4]
        $departmentKey = if ($scope -eq 'descendants') { @('root', 'a', 'b')[[int][Math]::Floor($i / 4) % 3] } else { @('a1', 'a2', 'b1', 'b2')[[int][Math]::Floor($i / 4) % 4] }
        $departmentID = $departmentIDs[$departmentKey]
        $finalRole = $roleNames[[int][Math]::Floor($i / 4) % 4]
        $existing = @($allUsers | Where-Object username -eq $username)
        if ($existing.Count -eq 0) {
            $user = (Invoke-Json POST '/users' @{ username = $username; display_name = "压测账号 $i"; password = $account.password; department_id = $departmentID; roles = @('reporter'); data_scope = $scope; must_change_password = $true } (Get-Token $credentials.admin)).user
            $credentials.users += $account
            Save-Json $credentials $credentialPath
        } else {
            if ($known.Count -ne 1) { throw 'Existing fixture account has no saved credential; use a fresh isolated project' }
            $user = (Invoke-Json PATCH ('/users/' + $existing[0].id + '/access') @{ enabled = $true; department_id = $departmentID; roles = @('reporter'); data_scope = $scope } (Get-Token $credentials.admin)).user
        }
        $account.id = $user.id; $account.user_id = $user.id; $account.department_id = $departmentID; $account.data_scope = $scope; $account.roles = @($finalRole)
        Login-Account $account | Out-Null
        $accounts.Add($account)
        Save-Json $credentials $credentialPath
    }
    $state.users = @($accounts | ForEach-Object { Public-User $_ })
    $state.write_template = @{ type = 'responsible-department'; department_id = $departmentIDs.root; fields = @{ department_name = "$prefix-write"; department_code = 'PERF-WRITE' }; mutable_field = 'department_code' }
    Save-State
    for ($typeIndex = 0; $typeIndex -lt $types.Count; $typeIndex++) {
        $type = $types[$typeIndex]
        $schema = (Invoke-Json GET ('/asset-types/' + $type + '/schema') $null (Get-Token $credentials.admin)).schema
        $columns = @($schema.Fields | Where-Object Import)
        $localCount = [int]$targetByType[$type]
        $cursor = 0
        for ($ownerIndex = 0; $ownerIndex -lt 20; $ownerIndex++) {
            $owner = $accounts[$ownerIndex]
            $ownerRows = [int][Math]::Floor($localCount / 20) + $(if ($ownerIndex -lt ($localCount % 20)) { 1 } else { 0 })
            if ($ownerRows -eq 0) { continue }
            # The unchanged asset service caps HTTP bodies at 1 MiB. A full
            # system-info row has many fields; seed it through smaller public
            # imports, retaining identical row values, ownership and totals.
            $chunkRows=if($type -eq 'system-info'){[Math]::Min($ownerRows,$SystemImportChunkRows)}else{$ownerRows}
            for($partStart=0;$partStart -lt $ownerRows;$partStart+=$chunkRows){
            $rowCount=[Math]::Min($chunkRows,$ownerRows-$partStart)
            $partSuffix=if($ownerRows -gt $chunkRows){'-p'+([int]($partStart/$chunkRows)).ToString('D3')}else{''}
            $csv = [Text.StringBuilder]::new()
            [void]$csv.AppendLine(($columns.Name -join ','))
            for ($line = 0; $line -lt $rowCount; $line++) {
                $cells = foreach ($field in $columns) { Quote-CSV (Field-Value $field $cursor $typeIndex) }
                [void]$csv.AppendLine(($cells -join ','))
                $cursor++
            }
            $csvPath = Join-Path $OutDir ("seed-$type-" + $ownerIndex.ToString('D2') + $partSuffix + '.csv')
            [IO.File]::WriteAllText($csvPath, $csv.ToString(), [Text.UTF8Encoding]::new($false))
            $key = "$prefix-seed-$type-$ownerIndex$partSuffix"
            $created = Invoke-Json POST '/import-jobs' @{ asset_type = $type; idempotency_key = $key; department_id = $owner.department_id; csv = $csv.ToString() } (Get-Token $owner)
            if ($created.task.state -eq 'awaiting_confirmation') { Assert-ImportPreflight $created.task $rowCount }
            elseif ($created.task.state -notin @('pending', 'running', 'succeeded')) { throw "Seed import is not resumable: task=$($created.task.id) state=$($created.task.state)" }
            $job = @{ task_id = $created.task.id; asset_type = $type; owner_index = $ownerIndex; owner_id = $owner.id; department_id = $owner.department_id; rows = $rowCount; state = $created.task.state; csv_file = $csvPath; sha256 = (Get-FileHash -LiteralPath $csvPath -Algorithm SHA256).Hash.ToLowerInvariant(); idempotency_key = $key }
            $state.jobs += $job
            Save-State
            if ($created.task.state -eq 'awaiting_confirmation') { Invoke-Json POST ('/import-jobs/' + $job.task_id + '/confirm') @{} (Get-Token $owner) | Out-Null }
            }
        }
        Write-Output "Seed imports submitted: type=$type rows=$localCount jobs=$($state.jobs.Count)"
    }
    Wait-Imports $state.jobs $accounts
    $state.state = 'collecting_assets'; Save-State
    $records = [Collections.Generic.List[object]]::new()
    foreach ($type in $types) {
        $offset = 0
        $fixtureCount = 0
        do {
            $page = Invoke-Json GET ("/assets/$type`?include_deleted=true&limit=100&offset=$offset") $null (Get-Token $credentials.admin)
            foreach ($record in $page.items) {
                $marker = Find-FixtureMarker $record.fields $prefix
                if ($null -eq $marker) {
                    if ($null -ne $record.deleted_at -and @($state.baseline_deleted_assets | Where-Object id -eq $record.id).Count -eq 1) { continue }
                    throw 'Unexpected non-fixture asset found during seed preparation'
                }
                if (-not $record.owner_id -or -not $record.department_id) { throw 'Fixture ownership or IAM department is empty' }
                if ($marker -notmatch '-T([0-9]+)-R([0-9]+)') { throw 'Fixture marker does not contain its deterministic row index' }
                $fixtureIndex = [int]$matches[2]
                $rank = (([long]$fixtureIndex * 1103515245) + ([long]$matches[1] * 12345) + $Seed) % 2147483647
                # Keep the complete fixture values in the already saved CSVs;
                # retain identity/ownership/version only in the in-memory index.
                $records.Add(@{ id = $record.id; asset_type = $type; fixture_index = $fixtureIndex; selection_rank = $rank; version = [int]$record.version; owner_id = $record.owner_id; department_id = $record.department_id; deleted = ($null -ne $record.deleted_at) })
                $fixtureCount++
            }
            $offset += @($page.items).Count
        } while (@($page.items).Count -eq 100)
        if ($fixtureCount -ne $targetByType[$type]) { throw "Seed asset count mismatch for $type : $fixtureCount/$($targetByType[$type])" }
    }
    if ($records.Count -ne $Rows) { throw 'Total fixture asset count mismatch' }
    # A fixed arithmetic permutation of the CSV row index spreads preparation
    # across owners/types while selecting identical fixture content after reseed.
    $records = @($records | Sort-Object selection_rank, asset_type, fixture_index)
    $versionRecords = @($records | Where-Object { -not $_.deleted } | Select-Object -First $targetVersioned)
    $deleteRecords = @($records | Select-Object -Skip $targetVersioned -First $targetDeleted)
    $state.selection_method = '(fixture_index*1103515245 + type_index*12345 + seed) mod 2147483647; first target_versioned rows, next target_deleted rows (disjoint); independent of generated UUIDs'
    $state.state = 'preparing_versions'; Save-State
    for ($version = 2; $version -le 5; $version++) { Run-Mutations $versionRecords 'update' $version | Out-Null }
    $state.state = 'preparing_soft_deletes'; Save-State
    Run-Mutations $deleteRecords 'delete' | Out-Null
    foreach ($account in $accounts) {
        Invoke-Json PATCH ('/users/' + $account.id + '/access') @{ enabled = $true; department_id = $account.department_id; roles = @($account.roles); data_scope = $account.data_scope } (Get-Token $credentials.admin) | Out-Null
        $sessions.Remove($account.username)
        Login-Account $account | Out-Null
    }
    Save-Json $credentials $credentialPath
    $state.users = @($accounts | ForEach-Object { Public-User $_ })
    $allAccounts = @($credentials.admin) + @($accounts)
    $summary = Get-FixtureSummary $records $types
    $state.expected_counts = $summary.expected_counts
    foreach ($type in $types) { $state.samples[$type] = @($summary.samples[$type].ToArray()) }
    $state.search_prefix = $prefix
    $state.search_cases = @(
        @{ asset_type = 'responsible-department'; query = "$prefix-NOT-EXIST"; label = 'none'; expected_min = 0; expected_max = 0 },
        # Marker strings are generated from fixture_index. The in-memory index
        # deliberately omits wide field maps; derive expected search counts from
        # the same deterministic rule instead of an absent fields property.
        @{ asset_type = 'responsible-department'; query = 'RARE1'; label = 'rare'; expected_min = $summary.search_rare },
        @{ asset_type = 'responsible-department'; query = 'HALF-A'; label = 'broad'; expected_min = $summary.search_broad }
    )
    foreach ($case in $state.search_cases) { $case.expected_max = $case.expected_min }
    foreach ($account in $allAccounts) {
        $counts = Get-FixtureVisibleCounts $account $state.departments $summary $types
        $state.per_user_counts[$account.id] = $counts
        $reported = Invoke-Json GET '/reporting/asset-counts' $null (Get-Token $account)
        $reportedTypes = [Collections.Generic.HashSet[string]]::new()
        foreach ($item in $reported.items) {
            if (-not $counts.Contains($item.asset_type) -or -not $reportedTypes.Add([string]$item.asset_type)) { throw 'Reporting returned an unknown or duplicate asset type' }
            if ([int]$item.total -ne $counts[$item.asset_type]) { throw "Visible count reconciliation failed for user=$($account.username) type=$($item.asset_type)" }
        }
        if ($reportedTypes.Count -ne $types.Count) { throw 'Reporting did not return all seven asset types' }
    }
    $stream = [IO.StreamWriter]::new($assetsPath, $false, [Text.UTF8Encoding]::new($false))
    try { foreach ($record in $records) { $stream.WriteLine(($record | ConvertTo-Json -Depth 10 -Compress)) } } finally { $stream.Dispose() }
    # Reuse the same compact records instead of constructing 100000 duplicate
    # Hashtables. selection_rank is retained as inspectable fixture metadata.
    $state.assets = $records
    $state.actual_versioned_rows = $summary.versioned_rows
    $state.actual_deleted_rows = $summary.deleted_rows
    if ($state.actual_versioned_rows -ne $targetVersioned -or $state.actual_deleted_rows -ne $targetDeleted) { throw 'Version/deletion fixture counts do not match targets' }
    $state.assets_sha256 = (Get-FileHash -LiteralPath $assetsPath -Algorithm SHA256).Hash.ToLowerInvariant()
    $state.completed_at = [DateTimeOffset]::UtcNow.ToString('o')
    $state.state = 'completed'; Save-State
    Write-Output "Seed completed: dataset=$dataset rows=$Rows active=$($Rows - $targetDeleted) versioned=$($state.actual_versioned_rows) deleted=$($state.actual_deleted_rows) users=$($accounts.Count) jobs=$($state.jobs.Count) manifest=$manifestPath"
} catch {
    $state.state = 'failed'; $state.failed_at = [DateTimeOffset]::UtcNow.ToString('o'); $state.failure = $_.Exception.Message
    Save-State
    throw
} finally { $http.Dispose() }
