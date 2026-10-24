"""Package reviewed backend/performance sources and reports without private envs."""
import hashlib
import json
from pathlib import Path
import zipfile


def main():
    root = Path(__file__).resolve().parents[2]
    out = root / 'data/perf/optimization-p3'
    acceptance = json.loads((out / 'acceptance.json').read_text(encoding='utf-8-sig'))
    assert acceptance['status'] == 'completed'
    target = out / 'performance-source-and-reports.zip'
    if target.exists():
        raise ValueError('Preserve prior delivery package')
    sources = []
    for directory in ('platform', 'scripts/perf', 'docs/testing', 'docs/interview', 'deploy/postgres', 'deploy/rabbitmq', 'deploy/test'):
        for path in (root / directory).rglob('*'):
            if not path.is_file() or any(part in ('__pycache__', 'node_modules', '.git', '.cache') for part in path.parts):
                continue
            if path.suffix in ('.pyc', '.exe', '.dump', '.zip', '.log', '.env') or path.name.startswith('.env'):
                continue
            sources.append(path)
    sources += [root / name for name in ('compose.yaml', '.env.example', 'README.md')]
    for directory in (out / 'plots', root / 'data/perf/optimization-p2/preauthenticated-20261018/plots'):
        sources += [p for p in directory.iterdir() if p.is_file() and p.suffix in ('.png', '.svg', '.json')]
    sources += [out / name for name in ('acceptance.json', 'p3-summary.json', 'single-instance-user-estimates.json', 'post-scaling-quiescence.json', 'post-scaling-source-proof.json', 'evidence-redaction.json')]
    records = []
    with zipfile.ZipFile(target, 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        for path in sorted(set(sources)):
            data = path.read_bytes()
            name = path.relative_to(root).as_posix()
            archive.writestr(name, data)
            records.append({'path': name, 'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data)})
    manifest = {'package': str(target), 'bytes': target.stat().st_size,
                'sha256': hashlib.sha256(target.read_bytes()).hexdigest(), 'files': records,
                'scope': 'Backend/performance sources, migrations, reports and derived figures; use the existing full repository for frontend and the archived local datasets. Private env/credential files, raw database dumps and log streams are excluded.'}
    (out / 'delivery-package-manifest.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    with zipfile.ZipFile(target) as archive:
        assert archive.testzip() is None
        assert 'docs/interview/single-instance-concurrency-and-performance.md' in archive.namelist()
        assert not any(name.endswith('.env.optimization') or 'credentials' in Path(name).name.lower() for name in archive.namelist())
    print(json.dumps({'files': len(records), 'bytes': manifest['bytes'], 'sha256': manifest['sha256']}))


if __name__ == '__main__':
    main()
