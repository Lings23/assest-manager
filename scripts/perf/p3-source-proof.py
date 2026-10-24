"""Offline proof that P3 kept the frozen P2 application and baseline workload."""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile


def digest(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--checkpoint', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[2]
    differences = []
    count = 0
    with zipfile.ZipFile(root / 'data/perf/optimization-p2/source-p2-observe.zip') as archive:
        for entry in archive.infolist():
            name = entry.filename.replace('\\', '/')
            if not name.startswith('platform/') or entry.is_dir():
                continue
            count += 1
            path = root / name
            if not path.is_file() or digest(path.read_bytes()) != digest(archive.read(entry)):
                differences.append(name)
    def containers(path):
        return [json.loads(line) for line in path.read_text(encoding='utf-8-sig').splitlines() if line.strip()]
    previous = containers(root / 'data/perf/optimization-p2/checkpoint-20261018T034834327/containers.jsonl')
    current = containers(args.checkpoint / 'containers.jsonl')
    image_proof = []
    for service in ('gateway', 'asset-service', 'iam-service', 'governance-service', 'task-report-service', 'task-publisher', 'task-worker'):
        name = f'/asset-amqp-opt-{service}-1'
        old = next(item for item in previous if item['name'] == name)
        now = next(item for item in current if item['name'] == name)
        image_proof.append({'service': service, 'image_id': now['image_id'], 'same_as_p2': now['image_id'] == old['image_id']})
    api_hash = digest((root / 'scripts/perf/api.js').read_bytes())
    d1_hash = digest((root / 'data/perf/20261016-local-baseline/d1.dump').read_bytes())
    s7_hash = digest((root / 'data/perf/20261016-local-baseline/s7base.dump').read_bytes())
    proof = {'frozen_platform_files': count, 'differences': differences,
             'baseline_api_sha256': api_hash, 'd1_sha256': d1_hash, 's7_sha256': s7_hash,
             's7_copy_same': s7_hash == digest((root / 'data/perf/optimization-p3/s7base.dump').read_bytes()),
             'application_images': image_proof, 'checkpoint': str(args.checkpoint)}
    proof['passed'] = (count == 110 and not differences and all(row['same_as_p2'] for row in image_proof)
        and api_hash == '9cec947dbfea428e3983fdc9710f2b312d0f4f23aa0071a6123b3cfba176f7e7'
        and d1_hash == 'f4e53123c4a642140cfe77799ba58046706a524b5c34002cff2acf1fc36d2e13'
        and proof['s7_copy_same'])
    if args.output.exists():
        raise ValueError('Preserve earlier source proof; choose a new output')
    args.output.write_text(json.dumps(proof, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'passed': proof['passed'], 'platform_files': count, 'image_services': len(image_proof)}))
    if not proof['passed']:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
