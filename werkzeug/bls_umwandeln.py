#!/usr/bin/env python3
"""BLS 4.0 (Excel) -> daten/naehrstoffe.tsv + daten/bls.tsv.gz fuer Foodr.

Aufruf: python3 werkzeug/bls_umwandeln.py <Ordner mit den BLS-xlsx> daten
"""
import gzip
import html
import re
import sys
import zipfile

def lies(pfad):
    z = zipfile.ZipFile(pfad)
    ss = []
    if 'xl/sharedStrings.xml' in z.namelist():
        x = z.read('xl/sharedStrings.xml').decode()
        for si in re.findall(r'<si>(.*?)</si>', x, re.S):
            ss.append(html.unescape(''.join(re.findall(r'<t[^>]*>(.*?)</t>', si, re.S))))
    blatt = sorted(n for n in z.namelist() if n.startswith('xl/worksheets/sheet'))[0]
    x = z.read(blatt).decode()
    zeilen = []
    for row in re.findall(r'<row[^>]*>(.*?)</row>', x, re.S):
        z_ = {}
        for ref, attrs, inner in re.findall(r'<c r="([A-Z]+)\d+"([^>]*?)(?:/>|>(.*?)</c>)', row, re.S):
            v = re.search(r'<v>(.*?)</v>', inner or '')
            t = re.search(r't="(\w+)"', attrs)
            if t and t.group(1) == 's' and v: val = ss[int(v.group(1))]
            elif t and t.group(1) == 'inlineStr':
                val = html.unescape(''.join(re.findall(r'<t[^>]*>(.*?)</t>', inner, re.S)))
            else: val = html.unescape(v.group(1)) if v else ''
            n = 0
            for ch in ref: n = n*26 + ord(ch)-64
            z_[n-1] = val
        if z_:
            zeilen.append([z_.get(i, '') for i in range(max(z_)+1)])
    return zeilen
quelle, ziel = sys.argv[1], sys.argv[2]
komp = lies(quelle + '/BLS_4_0_Components_DE_EN.xlsx')[1:]
komp = [k for k in komp if len(k) > 6 and k[1].strip()]
codes = [k[1].strip() for k in komp]
with open(ziel + '/naehrstoffe.tsv', 'w') as f:
    for i, k in enumerate(komp):
        gruppe = k[5].split(';')[0].strip()
        f.write('\t'.join([k[1].strip(), k[2].strip(), k[3].strip(), k[4].strip(), gruppe]) + '\n')
daten = lies(quelle + '/BLS_4_0_Daten_2025_DE.xlsx')
kopf = daten[0]
spalte = {}
for i, h in enumerate(kopf):
    m = re.match(r'^(\S+) .*\[', h)
    if m and m.group(1) in codes:
        spalte[m.group(1)] = i
fehlt = [c for c in codes if c not in spalte]
assert not fehlt, fehlt
n = 0
with gzip.open(ziel + '/bls.tsv.gz', 'wt', compresslevel=9) as f:
    for z in daten[1:]:
        if not z or not z[0].strip():
            continue
        z = z + [''] * (len(kopf) - len(z))
        werte = []
        for c in codes:
            v = z[spalte[c]].strip().replace(',', '.')
            if v in ('TR', '<LOD', '<LOQ', '<LOD or <LOQ'):
                v = '~'  # Spuren, unter der Nachweisgrenze
            else:
                try:
                    v = '%.4g' % float(v)
                except ValueError:
                    v = ''  # '-' = nicht bestimmt
            werte.append(v)
        f.write('\t'.join([z[0].strip(), z[1].strip().replace('\t', ' '), z[2].strip().replace('\t', ' ')] + werte) + '\n')
        n += 1
print(n, 'Lebensmittel,', len(codes), 'Naehrstoffe')
