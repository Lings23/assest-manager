#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$ResultsDir,
    [string]$OutputJson='',
    [string]$OutputMarkdown=''
)
$ErrorActionPreference='Stop'
$ResultsDir=(Resolve-Path -LiteralPath $ResultsDir).Path
$api=[Collections.Generic.List[object]]::new()
$jobs=[Collections.Generic.List[object]]::new()
$interruptions=[Collections.Generic.List[object]]::new()
$readErrors=[Collections.Generic.List[object]]::new()
$runRecords=@{}
$interruptionsByID=@{}
$seenSummaryIDs=@{}
$protectedInputs=[Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
function Read-Result([string]$Path) {
    [void]$protectedInputs.Add([IO.Path]::GetFullPath($Path))
    try { return Get-Content -LiteralPath $Path -Raw | ConvertFrom-Json -AsHashtable }
    catch { $readErrors.Add(@{path=$Path;error='Result JSON could not be parsed'}); return $null }
}
function Median($Values) {
    $sorted=@($Values | Where-Object {$null -ne $_} | Sort-Object)
    if(-not $sorted.Count){return $null}
    $middle=[int][Math]::Floor($sorted.Count/2)
    if($sorted.Count%2){return $sorted[$middle]}
    return ($sorted[$middle-1]+$sorted[$middle])/2
}
function Number-Range($Values) {
    $items=@($Values|Where-Object{$null -ne $_})
    if(-not $items.Count){return @{n=0;min=$null;median=$null;max=$null}}
    $m=$items|Measure-Object -Minimum -Maximum
    return @{n=$items.Count;min=$m.Minimum;median=(Median $items);max=$m.Maximum}
}
function Escape-Cell($Text) { return ([string]$Text).Replace('|','\|').Replace("`r",' ').Replace("`n",' ') }
function Format-Number($Value) {
    if($null -eq $Value){return '—'}
    return ([double]$Value).ToString('0.###',[Globalization.CultureInfo]::InvariantCulture)
}
function Format-Verdict($Value){if($null -eq $Value){return '未核验'};if($Value){return '通过'};return '未通过'}
function Resolve-EvidencePath([string]$Path,[string]$ParentFile) {
    if(-not $Path){return $null}
    if([IO.Path]::IsPathRooted($Path)){return [IO.Path]::GetFullPath($Path)}
    return [IO.Path]::GetFullPath((Join-Path (Split-Path -Parent $ParentFile) $Path))
}
# Final run records are written after k6 exit and SQL reconciliation. A raw k6
# summary alone cannot establish successful orchestration or write correctness.
foreach($file in Get-ChildItem -LiteralPath $ResultsDir -Filter '*-runs.json' -File -Recurse|Sort-Object FullName){
    $data=Read-Result $file.FullName
    foreach($record in @($data)){
        if(-not $record -or $record -isnot [Collections.IDictionary] -or -not $record.run_id){continue}
        if(-not $runRecords.ContainsKey($record.run_id)){$runRecords[$record.run_id]=[Collections.Generic.List[object]]::new()}
        $runRecords[$record.run_id].Add(@{source=$file.FullName;data=$record})
    }
}
foreach($file in Get-ChildItem -LiteralPath $ResultsDir -Filter '*-interrupted.json' -File -Recurse|Sort-Object FullName){
    $data=Read-Result $file.FullName
    if($data){
        $entry=@{run_id=$data.run_id;utc=$data.utc;source=$file.FullName;error=$data.error}
        $interruptions.Add($entry)
        if($data.run_id){
            if(-not $interruptionsByID.ContainsKey($data.run_id)){$interruptionsByID[$data.run_id]=[Collections.Generic.List[object]]::new()}
            $interruptionsByID[$data.run_id].Add($entry)
        }
    }
}
function Get-FinalVerdict($Raw,[string]$SummaryPath){
    $result=@{status='unverified_missing_run_record';verified=$false;load_point_passed=$null;baseline_eligible=$null;run_record=$null;write_audit=$null;issues=@();interruptions=@()}
    if($interruptionsByID.ContainsKey($Raw.run_id)){$result.interruptions=$interruptionsByID[$Raw.run_id].ToArray()}
    $entries=@(if($runRecords.ContainsKey($Raw.run_id)){$runRecords[$Raw.run_id].ToArray()})
    if($entries.Count -eq 0){$result.issues+=,'No final orchestration record exists.'}
    elseif($entries.Count -ne 1){$result.status='unverified_ambiguous_run_record';$result.issues+=,'Multiple final records share this run_id.'}
    else{
        $entry=$entries[0];$record=$entry.data
        $result.run_record=@{source=$entry.source;run_id=$record.run_id;exit_code=$record.exit_code;load_point_passed=$record.load_point_passed;baseline_eligible=$record.baseline_eligible;summary_path=$record.summary_path}
        $target=Resolve-EvidencePath $record.summary_path $entry.source
        if(-not $target -or -not [string]::Equals($target,$SummaryPath,[StringComparison]::OrdinalIgnoreCase)){
            $result.status='unverified_summary_path_mismatch';$result.issues+=,'Final record refers to another summary path.'
        }elseif($null -eq $record.exit_code -or -not $record.ContainsKey('load_point_passed')){
            $result.status='unverified_incomplete_run_record';$result.issues+=,'Final record lacks exit code or load verdict.'
        }else{
            $result.verified=$true
            $result.load_point_passed=([int]$record.exit_code -eq 0 -and [bool]$record.load_point_passed -and [bool]$Raw.load_point_passed)
            $result.baseline_eligible=($result.load_point_passed -and [bool]$record.baseline_eligible -and [bool]$Raw.baseline_eligible)
            $result.status=if($result.load_point_passed){'verified_pass'}else{'verified_failure'}
        }
        if($Raw.mode -in @('mixed','burst','soak')){
            $auditPath=Resolve-EvidencePath $record.write_audit $entry.source
            $allowedAuditRoot=$ResultsDir.TrimEnd('\','/')+[IO.Path]::DirectorySeparatorChar
            if(-not $auditPath -or -not $auditPath.StartsWith($allowedAuditRoot,[StringComparison]::OrdinalIgnoreCase) -or
                -not $auditPath.EndsWith('-write-audit.json',[StringComparison]::OrdinalIgnoreCase) -or -not(Test-Path -LiteralPath $auditPath)){
                if($result.status -ne 'verified_failure'){$result.verified=$false;$result.load_point_passed=$null;$result.baseline_eligible=$null;$result.status='unverified_missing_write_audit'}
                $result.issues+=,'A write workload has no valid final SQL reconciliation evidence inside ResultsDir.'
            }else{
                $audit=Read-Result $auditPath
                $auditValid=$audit -and $audit.run_id -eq $Raw.run_id -and $null -ne $audit.before.assets -and $null -ne $audit.after.assets -and
                    $null -ne $audit.before.versions -and $null -ne $audit.after.versions -and $null -ne $audit.expected_added_assets -and $null -ne $audit.expected_added_versions
                $countMatch=$auditValid -and ([long]$audit.after.assets-[long]$audit.before.assets -eq [long]$audit.expected_added_assets) -and
                    ([long]$audit.after.versions-[long]$audit.before.versions -eq [long]$audit.expected_added_versions)
                $ownerMatch=$auditValid -and [string]$audit.before.owner_id -eq [string]$audit.owner_id -and [string]$audit.after.owner_id -eq [string]$audit.owner_id
                if($Raw.filters.username -and $Raw.filters.username -ne 'all'){$ownerMatch=$ownerMatch -and $audit.owner_id -match '^[a-fA-F0-9-]{36}$'}
                $expectationsMatch=$Raw.write_effects -and [long]$audit.expected_added_assets -eq ([long]$Raw.write_effects.fixture_creates+[long]$Raw.write_effects.successful_creates_including_warmup) -and
                    [long]$audit.expected_added_versions -eq [long]$Raw.write_effects.expected_added_versions
                $auditPass=($auditValid -and [bool]$audit.passed -and $countMatch -and $ownerMatch -and $expectationsMatch)
                $result.write_audit=@{source=$auditPath;run_id=$audit.run_id;owner_id=$audit.owner_id;claimed_passed=$audit.passed;count_deltas_match=$countMatch;owner_scope_consistent=$ownerMatch;expected_deltas_match_raw_write_counters=$expectationsMatch;passed=$auditPass;
                    expected_added_assets=$audit.expected_added_assets;expected_added_versions=$audit.expected_added_versions;before=$audit.before;after=$audit.after}
                if(-not $auditPass){$result.verified=$true;$result.load_point_passed=$false;$result.baseline_eligible=$false;$result.status='verified_write_audit_failure';$result.issues+=,'SQL reconciliation failed or does not match the run/count/owner evidence.'}
            }
        }
    }
    if($result.interruptions.Count){$result.status='interrupted';$result.verified=$true;$result.load_point_passed=$false;$result.baseline_eligible=$false;$result.issues+=,'An interruption record prevents this run from being counted as passed.'}
    return $result
}
function Get-ScenarioEvidence($Raw){
    if($Raw.mode -eq 'soak'){
        $available=$Raw.auth_requests -is [Collections.IDictionary] -and $Raw.auth_requests.ContainsKey('refresh')
        $observed=if($available){[long]$Raw.auth_requests.refresh -gt 0}else{$null}
        return @{kind='soak';refresh_counter_available=$available;refresh_requests=$Raw.auth_requests.refresh;refresh_observed=$observed;
            refresh_status=if(-not $available){'unavailable'}elseif($observed){'observed'}else{'not_observed'};
            planned_steady_seconds=$Raw.steady_seconds;full_60m_planned=([double]$Raw.steady_seconds -ge 3600);auth_failure_rate=$Raw.auth_requests.failure_rate;
            note='Refresh is established only by an observed nonzero refresh counter. A passing throughput verdict alone does not prove refresh or a full 60-minute soak.'}
    }
    if($Raw.mode -ne 'burst'){return $null}
    $allWindows=@($Raw.windows|Where-Object{$null -ne $_})
    $segments=@{}
    foreach($segment in @('steady_before','burst','steady_after')){
        $windows=@($allWindows|Where-Object{$_.segment -eq $segment}|Sort-Object index)
        $attempts=($windows|Measure-Object attempts -Sum).Sum
        $positive=@($windows|Where-Object{[long]$_.attempts -gt 0})
        $knownErrors=@($positive|Where-Object{$null -ne $_.error_rate -and [double]$_.error_rate -gt 0}).Count -gt 0
        $observedErrors=if(-not $windows.Count -or -not $attempts){$null}elseif($knownErrors){$true}elseif(@($positive|Where-Object{$null -eq $_.error_rate}).Count){$null}else{$false}
        $segments[$segment]=@{windows=$windows;attempts=$attempts;errors_observed=$observedErrors;window_count=$windows.Count}
    }
    $before=@($segments.steady_before.windows|Where-Object full_window_observed|Select-Object -Last 1)
    $burstEnd=@($segments.burst.windows|ForEach-Object{[double]$_.start_seconds+[double]$_.scheduled_seconds}|Sort-Object|Select-Object -Last 1)
    $recovery=@();$firstQualifiedEnd=$null
    foreach($window in $segments.steady_after.windows){
        $perOperation=@{};$enough=$true;$within=$true
        foreach($operation in $Raw.operations.Keys){
            $values=$window.operations[$operation]
            $reference=if($before.Count){$before[0].operations[$operation]}else{$null}
            $sampleOK=([long]$values.count -ge 100 -and $null -ne $window.error_rate -and [double]$window.error_rate -eq 0)
            $limit=if($operation -in @('list','detail','versions')){500}else{1000}
            $operationWithin=($sampleOK -and $null -ne $values.'p(95)' -and [double]$values.'p(95)' -le $limit)
            $growth=if($null -ne $reference -and [double]$reference.'p(95)' -gt 0){[double]$values.'p(95)'/[double]$reference.'p(95)'-1}else{$null}
            $perOperation[$operation]=@{response_count=$values.count;p95_ms=$values.'p(95)';max_ms=$values.max;p95_sample_sufficient=$sampleOK;reference_line_p95_ms=$limit;
                p95_growth_from_last_before_window=$growth;growth_comparison_sample_sufficient=($sampleOK -and [long]$reference.count -ge 100 -and [double]$before[0].error_rate -eq 0)}
            $enough=$enough -and $sampleOK;$within=$within -and $operationWithin
        }
        $arrivalOK=([double]$window.target_rps -gt 0 -and [Math]::Abs([double]$window.actual_rps-[double]$window.target_rps) -le [double]$window.target_rps*0.05)
        $qualifies=([bool]$window.full_window_observed -and $enough -and $within -and $arrivalOK)
        $endAfter=if($burstEnd.Count){[double]$window.start_seconds+[double]$window.scheduled_seconds-[double]$burstEnd[0]}else{$null}
        if($qualifies -and $null -eq $firstQualifiedEnd){$firstQualifiedEnd=$endAfter}
        $recovery+=@{window_index=$window.index;end_seconds_after_burst=$endAfter;full_window_observed=$window.full_window_observed;error_rate=$window.error_rate;
            actual_rps=$window.actual_rps;arrival_within_5_percent=$arrivalOK;all_operation_p95_samples_sufficient=$enough;reference_line_window_qualified=$qualifies;operations=$perOperation}
    }
    return @{kind='burst';window_evidence_available=($allWindows.Count -gt 0);segments=$segments;recovery_windows=$recovery;
        first_qualified_window_end_seconds_after_burst=$firstQualifiedEnd;
        note='Descriptive, separately segmented observations. A qualifying recovery window requires a full window, zero observed errors, >=100 responses for each operation, reference p95 limits and arrival within 5%. This is a window-end observation bound, not an exact recovery time. Small-sample percentiles and before/after growth remain descriptive; steady thresholds do not certify the burst segment.'}
}
# Narrow filename patterns avoid opening seed manifests or credential files.
foreach($file in Get-ChildItem -LiteralPath $ResultsDir -Filter '*-summary.json' -File -Recurse | Sort-Object FullName) {
    $data=Read-Result $file.FullName
    if(-not $data -or -not $data.ContainsKey('operations') -or -not $data.ContainsKey('target_rps')){continue}
    $operations=[ordered]@{}
    $seenSummaryIDs[$data.run_id]=$true
    $verdict=Get-FinalVerdict $data $file.FullName
    foreach($name in $data.operations.Keys|Sort-Object) {
        $op=$data.operations[$name]
        $operations[$name]=@{attempts=$op.attempts;successes=$op.successes;error_rate=$op.errors;actual_rps=$op.actual_rps;
            p50_ms=$op.duration_ms.med;p95_ms=$op.duration_ms.'p(95)';p99_ms=$op.duration_ms.'p(99)';max_ms=$op.duration_ms.max;
            p95_evidence_sufficient=([long]$op.successes -ge 100);p99_evidence_sufficient=([long]$op.successes -ge 1000)}
    }
    $repetition=$null
    if($data.run_id -match '-r([0-9]+)$'){$repetition=[int]$matches[1]}
    $family=$data.run_id -replace '-r[0-9]+$',''
    $key=@($family,$data.mode,$data.dataset.dataset,$data.target_rps,$data.steady_seconds,$data.filters.scope,$data.filters.role,$data.filters.username,$data.filters.asset_type) -join '|'
    $correctnessCount=0
    if($data.metrics.ContainsKey('correctness_errors')){$correctnessCount=[long]$data.metrics.correctness_errors.values.count}
    $api.Add(@{run_id=$data.run_id;source=$file.FullName;comparison_key=$key;repetition=$repetition;
        mode=$data.mode;dataset=$data.dataset.dataset;scope=$data.filters.scope;role=$data.filters.role;username=$data.filters.username;asset_type=$data.filters.asset_type;
        target_rps=$data.target_rps;actual_rps=$data.actual_steady_rps;steady_seconds=$data.steady_seconds;
        dropped_iterations=$data.dropped_iterations;thresholds_passed=$data.thresholds_passed;load_point_passed=$verdict.load_point_passed;
        baseline_eligible=$verdict.baseline_eligible;verification_status=$verdict.status;verification=$verdict;
        raw_verdict=@{load_point_passed=$data.load_point_passed;baseline_eligible=$data.baseline_eligible;thresholds_passed=$data.thresholds_passed};
        correctness_errors=$correctnessCount;operations=$operations;breakdowns=$data.breakdowns;
        auth_requests=$data.auth_requests;write_effects=$data.write_effects;windows=@($data.windows|Where-Object{$null -ne $_});scenario_evidence=(Get-ScenarioEvidence $data)})
}
foreach($file in Get-ChildItem -LiteralPath $ResultsDir -Filter '*.manifest.json' -File -Recurse | Sort-Object FullName) {
    $data=Read-Result $file.FullName
    if(-not $data -or -not $data.ContainsKey('task_denominator') -or -not $data.ContainsKey('tasks')){continue}
    $tasks=@(foreach($task in $data.tasks){
        @{task_id=$task.task_id;kind=$task.kind;state=$task.state;passed=$task.passed;rows=$task.rows;attempt=$task.attempt;
            db_verified=$task.db_verified;timing=$task.timing;poll_count=$task.poll_count;
            download_rows=$task.download.rows;download_bytes=$task.download.bytes;error=$task.error}
    })
    $timings=[ordered]@{}
    $timingNames=@($tasks|ForEach-Object{$_.timing.Keys}|Sort-Object -Unique)
    foreach($name in $timingNames){$timings[$name]=Number-Range @($tasks|ForEach-Object{$_.timing[$name]})}
    $jobs.Add(@{run_id=$data.run_id;source=$file.FullName;mode=$data.mode;asset_type=$data.asset_type;profile=$data.profile;
        rows=$data.rows;concurrency=$data.concurrency;repetitions=$data.repetitions;task_denominator=$data.task_denominator;
        passed=$data.passed;failed_or_unfinished=$data.failed_or_unfinished;database_verification_skipped=$data.database_verification_skipped;
        batches=$data.batches;tasks=$tasks;timing_ranges=$timings;
        statistical_note='Only observed raw values, minimum, median and maximum; task repetitions do not establish p95/p99.'})
}
$repeated=[Collections.Generic.List[object]]::new()
foreach($group in $api|Group-Object -Property comparison_key) {
    $items=@($group.Group)
    $uniqueRepetitions=@($items|Where-Object{$null -ne $_.repetition}|ForEach-Object{$_.repetition}|Sort-Object -Unique)
    $allPassed=@($items|Where-Object{-not $_.load_point_passed}).Count -eq 0
    $allEligible=@($items|Where-Object{-not $_.baseline_eligible}).Count -eq 0
    $perOperation=[ordered]@{}
    foreach($operation in $items[0].operations.Keys) {
        $perOperation[$operation]=@{p95_ms_range=(Number-Range @($items|ForEach-Object{$_.operations[$operation].p95_ms}));
            actual_rps_range=(Number-Range @($items|ForEach-Object{$_.operations[$operation].actual_rps}));
            note='These are ranges across per-run results; they are not pooled latency percentiles.'}
    }
    $repeated.Add(@{comparison_key=$group.Name;run_ids=@($items|ForEach-Object run_id);observed_runs=$items.Count;
        unique_repetitions=$uniqueRepetitions;all_load_points_passed=$allPassed;all_baseline_eligible=$allEligible;
        unverified_runs=@($items|Where-Object{$null -eq $_.load_point_passed}).Count;failed_or_interrupted_runs=@($items|Where-Object{$_.load_point_passed -eq $false}).Count;
        three_run_candidate=($uniqueRepetitions.Count -ge 3 -and $allPassed -and $allEligible);
        environment_and_snapshot_comparability_verified=$false;
        actual_rps_range=(Number-Range @($items|ForEach-Object actual_rps));operations=$perOperation})
}
$withoutSummary=@(foreach($id in $runRecords.Keys){if(-not $seenSummaryIDs.ContainsKey($id)){foreach($entry in $runRecords[$id]){@{run_id=$id;source=$entry.source;exit_code=$entry.data.exit_code;summary_path=$entry.data.summary_path;status='unverified_missing_raw_summary'}}}})
$measurementNotes=@()
$notesPath=Join-Path $ResultsDir 'measurement-notes.json'
if(Test-Path -LiteralPath $notesPath){$measurementNotes=@(Read-Result $notesPath)}
$report=[ordered]@{
    schema_version=2;generated_at_utc=[DateTimeOffset]::UtcNow.ToString('o');results_dir=$ResultsDir;
    scope='Observed result files only. No untested capacity, missing scenario success, cross-run snapshot equivalence, or production SLA is inferred.';
    api_runs=$api.ToArray();api_repeat_groups=$repeated.ToArray();job_runs=$jobs.ToArray();interruptions=$interruptions.ToArray();read_errors=$readErrors.ToArray();run_records_without_summary=$withoutSummary;measurement_notes=$measurementNotes;
    limitations=@('No global percentile is calculated by averaging endpoint or run percentiles.',
        'Effective API verdicts require a unique matching final run record, zero process exit, no interruption, and matching SQL audit for write workloads. Raw verdicts are retained separately; missing records remain unverified.',
        'No R* is declared automatically; three-run candidates require matching code, resources, data snapshot, permissions and correctness reconciliation.',
        'Window error summaries are retrospective. External monitoring must enforce resource and consecutive-window stop rules during load.',
        'Historical summaries without windows or refresh counters cannot establish burst recovery time or prove session refresh occurred.')
}
foreach($output in @($OutputJson,$OutputMarkdown)){if($output -and $protectedInputs.Contains([IO.Path]::GetFullPath($output))){throw 'Output path would overwrite an input evidence file'}}
if($OutputJson){[IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputJson),($report|ConvertTo-Json -Depth 40),[Text.UTF8Encoding]::new($false))}
if($OutputMarkdown) {
    $lines=[Collections.Generic.List[string]]::new()
    $lines.Add('以下仅汇总实际结果文件；未测场景、容量上限、快照可比性及生产 SLA 均不作推断。')
    foreach($note in $measurementNotes){$lines.Add('');$lines.Add("测量说明 $(Escape-Cell $note.run_id) [$($note.classification)]：$(Escape-Cell $note.reason)（原始判定保留；说明不覆盖退出码、对账与阈值）。")}
    $lines.Add('')
    $lines.Add('| run_id | 场景/数据 | 操作 | 目标/实际RPS | 成功/请求 | p50/p95/p99 ms | p95/p99样本足够 | 错误率 | 掉迭代 | 最终核验 |')
    $lines.Add('| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |')
    foreach($run in $api) {foreach($opName in $run.operations.Keys){
        $op=$run.operations[$opName]
        $lines.Add('| '+(@((Escape-Cell $run.run_id),(Escape-Cell ($run.mode+'/'+$run.dataset)),$opName,
            ((Format-Number $run.target_rps)+'/'+(Format-Number $run.actual_rps)),"$($op.successes)/$($op.attempts)",
            ((Format-Number $op.p50_ms)+'/'+(Format-Number $op.p95_ms)+'/'+(Format-Number $op.p99_ms)),
            "$($op.p95_evidence_sufficient)/$($op.p99_evidence_sufficient)",(Format-Number $op.error_rate),$run.dropped_iterations,((Format-Verdict $run.load_point_passed)+' / '+$run.verification_status)) -join ' | ')+' |')
    }}
    $lines.Add('')
    $lines.Add('| 任务run_id | 类型/规模/在途 | 成功/总数 | 失败或未完成 | 数据库核验跳过 |')
    $lines.Add('| --- | --- | --- | --- | --- |')
    foreach($run in $jobs){$lines.Add("| $(Escape-Cell $run.run_id) | $($run.mode)/$($run.rows)/$($run.concurrency) | $($run.passed)/$($run.task_denominator) | $($run.failed_or_unfinished) | $($run.database_verification_skipped) |")}
    foreach($run in @($api|Where-Object{$_.mode -eq 'soak'})){
        $lines.Add('');$lines.Add("Soak $(Escape-Cell $run.run_id)：计划稳态 $($run.steady_seconds) 秒；实际刷新请求 $(Format-Number $run.scenario_evidence.refresh_requests)，刷新证据 $($run.scenario_evidence.refresh_status)。吞吐通过不单独证明会话刷新。")
    }
    foreach($run in @($api|Where-Object{$_.mode -eq 'burst'})){
        $lines.Add('');$lines.Add("Burst $(Escape-Cell $run.run_id)：峰值阶段错误观测 $($run.scenario_evidence.segments.burst.errors_observed)；首个满足样本量、参考线和到达率条件的完整恢复窗口末端位于峰值结束后 $(Format-Number $run.scenario_evidence.first_qualified_window_end_seconds_after_burst) 秒。未满足样本条件时仅描述原始窗口，不给出精确恢复时间。")
    }
    $lines.Add('')
    $lines.Add("中断记录：$($interruptions.Count)；无法解析的结果文件：$($readErrors.Count)。任务三轮只报告明细、中位数和范围，不报告可靠的p95/p99。")
    [IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputMarkdown),($lines -join "`n"),[Text.UTF8Encoding]::new($false))
}
return [pscustomobject]$report
