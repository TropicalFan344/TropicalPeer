# Writes the icon SVGs: the mark is shared, backgrounds differ per platform.
FILE = 'M-90 -125 h110 l70 70 v180 a24 24 0 0 1 -24 24 h-156 a24 24 0 0 1 -24 -24 v-226 a24 24 0 0 1 24 -24z'
FOLD = 'M20 -125 v46 a24 24 0 0 0 24 24 h46'
def mark(mono=False):
    w, mint, ring = ('#fff', '#fff', '#fff') if mono else ('#fff', '#6fdba6', '#fff')
    dots = ''  # hidden under the file at this size
    _ = ''.join(f'<circle cx="{345+i*(672-345)/6:.0f}" cy="{679-i*(679-352)/6:.0f}" r="15" fill="{ring}" fill-opacity=".45"/>' for i in (1, 5))
    return f'''
<circle cx="292" cy="732" r="74" fill="none" stroke="{ring}" stroke-opacity="{1 if mono else .7}" stroke-width="36"/>
<circle cx="736" cy="288" r="98" fill="{mint}"/>
{dots}
<g transform="translate(514 510) rotate(-12) scale(1.42)">
  {'' if mono else f'<path d="{FILE}" transform="translate(6 16)" fill="#04150f" fill-opacity=".35" filter="url(#s)"/>'}
  <path d="{FILE}" fill="{w}"/>
  {'' if mono else f'<path d="{FOLD}" fill="#bfe3d1"/>'}
</g>'''
DEFS = '''<defs>
<linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#22966a"/><stop offset=".55" stop-color="#136247"/><stop offset="1" stop-color="#0a3024"/></linearGradient>
<linearGradient id="hl" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#fff" stop-opacity=".18"/><stop offset=".5" stop-color="#fff" stop-opacity="0"/></linearGradient>
<filter id="s" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="14"/></filter>
<filter id="ds" x="-20%" y="-20%" width="140%" height="140%"><feGaussianBlur stdDeviation="12"/></filter>
</defs>'''
def tile(x, r, shadow=False):
    s = x - 2*0
    size = 1024 - 2*x
    sh = f'<rect x="{x}" y="{x+12}" width="{size}" height="{size}" rx="{r}" fill="#000" fill-opacity=".3" filter="url(#ds)"/>' if shadow else ''
    return sh + f'<rect x="{x}" y="{x}" width="{size}" height="{size}" rx="{r}" fill="url(#g)"/><rect x="{x}" y="{x}" width="{size}" height="{size}" rx="{r}" fill="url(#hl)"/>'
def svg(body, scale=1.0):
    t = f'translate({512*(1-scale):.1f} {512*(1-scale):.1f}) scale({scale})'
    return f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024">{DEFS}{body[0]}<g transform="{t}">{body[1]}</g></svg>'
out = {
  # macOS grid: 824 tile, 100 margin, soft drop shadow.
  'mac.svg': svg((tile(100, 185, True), mark()), 824/1024),
  # Windows/Linux: nearly full bleed.
  'win.svg': svg((tile(24, 210), mark()), 976/1024),
  # Android adaptive: background full bleed, mark inside the 66% safe circle.
  'android_bg.svg': svg(('<rect width="1024" height="1024" fill="url(#g)"/><rect width="1024" height="1024" fill="url(#hl)"/>', '')),
  'android_fg.svg': svg(('', mark()), 0.70),
  'android_mono.svg': svg(('', mark(True)), 0.70),
  'android_legacy.svg': svg(('<circle cx="512" cy="512" r="500" fill="url(#g)"/>', mark()), 0.9),
}
for k, v in out.items(): open(k, 'w').write(v)
