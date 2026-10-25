# Regenerates docs/medium/*.md from docs/*.md. Run from the repository root: python3 docs/medium/convert.py
import re, sys

def cells(line):
    line = line.strip()
    if line.startswith('|'): line = line[1:]
    if line.endswith('|'): line = line[:-1]
    return [c.strip() for c in re.split(r'(?<!\\)\|', line)]

def end(s):
    s = s.strip()
    return s if s and s[-1] in '.?!:)' else s + '.'

RENAME = {'One generic `act`': 'Generic `act`', 'One tool per verb': 'Per-verb tools'}

def table_to_bullets(rows):
    head = [RENAME.get(h.strip(), h.strip()) for h in cells(rows[0])]
    out = []
    if head[0] == 'Tool':  # the tool list reads better as a sentence than as labelled fields
        for r in rows[2:]:
            c = cells(r)
            out.append(f"- **{c[0].strip('*')}**: {end(c[1]).rstrip('.')} ({c[2]}).")
        return out
    for r in rows[2:]:
        c = cells(r)
        if len(head) == 2:
            first = c[0].strip('*')
            out.append(f"- **{first}:** {c[1]}")
        else:
            parts = [f"{head[j].strip()}: {end(c[j])}" if head[j].strip() else end(c[j]) for j in range(1, len(c))]
            first = c[0].strip('*')
            out.append(f"- **{first}.** " + " ".join(parts))
    return out

def convert(src, note):
    lines = open(src).read().split('\n')
    out, i, fence = [], 0, False
    while i < len(lines):
        l = lines[i]
        if l.startswith('```'):
            fence = not fence; out.append(l); i += 1; continue
        if fence:
            out.append(l); i += 1; continue
        if l.startswith('|'):
            j = i
            while j < len(lines) and lines[j].startswith('|'): j += 1
            out += table_to_bullets(lines[i:j]); i = j; continue
        m = re.match(r'^### (.*)$', l)
        if m:
            out.append(f"**{m.group(1)}**"); i += 1; continue
        l = re.sub(r'<(https?://[^>]+)>', r'\1', l)
        out.append(l); i += 1
    text = '\n'.join(out)
    # insert note after the title
    first_nl = text.index('\n')
    return text[:first_nl] + '\n\n' + note + text[first_nl:]

note = ("*This copy is formatted for Medium: tables are lists, and sub-headings are bold lines. The repository has the original, "
        "with tables: https://github.com/lehmann314159/PuzzleBoxApp. File paths in the text refer to that repository.*")
for name in ['HOW-IT-WORKS.md', 'TESTING.md']:
    open(f'docs/medium/{name}', 'w').write(convert(f'docs/{name}', note) + '\n')
