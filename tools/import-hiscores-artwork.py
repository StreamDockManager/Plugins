#!/usr/bin/env python3
"""Explicit artwork maintenance; normal builds use only checked-in local assets."""
import hashlib
import json
from pathlib import Path
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / 'internal/catalog/artwork/hiscores'


def read(url):
    request = urllib.request.Request(url, headers={
        'User-Agent': 'stream-dock-manager-plugins/0.1.1 (skill artwork maintenance)',
    })
    with urllib.request.urlopen(request, timeout=40) as response:
        return response.read(8 * 1024 * 1024)


def main():
    sources = []
    previous = OUTPUT / 'sources.json'
    cached = {image['asset']: image for image in json.loads(previous.read_text())['images']} if previous.exists() else {}
    for game, domain in [('osrs', 'oldschool.runescape.wiki'), ('runescape', 'runescape.wiki')]:
        skills = json.loads((ROOT / f'internal/plugins/{game}-hiscores/skills.json').read_text())
        requested = {}
        for skill in skills:
            name = skill['name']
            cape = 'Max cape' if skill['id'] == '0' else name + (
                ' master cape' if game == 'runescape' and skill['cap'] == 120 else ' cape')
            if game == 'osrs' and name == 'Construction':
                cape = 'Construct. cape'
            if game == 'osrs' and name == 'Ranged':
                cape = 'Ranging cape'
            requested[skill['cape']] = [f'File:{cape} detail.png']
            requested[skill['icon']] = [(
                'File:Stats icon.png' if game == 'osrs' else 'File:Skills icon.png'
            ) if skill['id'] == '0' else f'File:{name} icon.png']
            if game == 'runescape' and skill['id'] != '0':
                requested[skill['icon']].insert(0, f'File:{name}-icon.png')
                requested[skill['icon']].insert(0, f'File:{name}.png')
        info = {}
        titles = list(dict.fromkeys(title for candidates in requested.values() for title in candidates))
        for start in range(0, len(titles), 40):
            query = urllib.parse.urlencode({
                'action': 'query', 'format': 'json', 'prop': 'imageinfo',
                'titles': '|'.join(titles[start:start + 40]), 'iiprop': 'url',
                'iiurlwidth': 256, 'iiurlheight': 256,
            })
            payload = json.loads(read(f'https://{domain}/api.php?{query}'))
            for page in payload['query']['pages'].values():
                if 'imageinfo' in page:
                    info[page['title']] = page['imageinfo'][0]
        missing = [candidates for candidates in requested.values() if not any(title in info for title in candidates)]
        if missing:
            raise RuntimeError(f'{game}: missing artwork: {sorted(missing)}')
        for asset, candidates in requested.items():
            title = next(title for title in candidates if title in info)
            image = info[title]
            url = image.get('thumburl', image['url'])
            target = OUTPUT / Path(asset).relative_to('hiscores').with_suffix('.png')
            record = cached.get(str(target.relative_to(OUTPUT)), {})
            data = target.read_bytes() if target.exists() else b''
            if record.get('download') != url or record.get('sha256') != hashlib.sha256(data).hexdigest():
                data = read(url)
            if not data.startswith(b'\x89PNG\r\n\x1a\n'):
                raise RuntimeError(f'Not PNG: {url}')
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
            sources.append({
                'asset': str(target.relative_to(OUTPUT)),
                'source': image['descriptionurl'], 'download': url,
                'sha256': hashlib.sha256(data).hexdigest(),
            })
        print(f'{game}: imported {len(requested)} cape and skill images', flush=True)
    (OUTPUT / 'sources.json').write_text(json.dumps({
        'retrieved': '2026-10-30',
        'copyright': 'Game artwork © Jagex Limited. Wiki file pages identify image provenance.',
        'notice': "Created using intellectual property belonging to Jagex Limited under the terms of Jagex's Fan Content Policy. This content is not endorsed by or affiliated with Jagex.",
        'policy': 'https://legal.jagex.com/docs/policies/fan-content-policy',
        'images': sources,
    }, indent=4) + '\n')


if __name__ == '__main__':
    main()
