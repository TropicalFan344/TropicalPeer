import os, glob
from PIL import Image
P = r'F:\Projects\TropicalPeer'
R = P + r'\frontend\android\app\src\main\res'
L = lambda n: Image.open(n + '.png').convert('RGBA')
def fit(im, s): return im.resize((s, s), Image.LANCZOS)

# Desktop: Wails builds the Mac .icns from build/appicon.png; Windows uses icon.ico.
L('mac').save(P + r'\build\appicon.png')
L('win').save(P + r'\build\windows\icon.ico', sizes=[(s, s) for s in (16, 20, 24, 32, 40, 48, 64, 96, 128, 256)])

# Android launcher icons.
dens = {'mdpi': 1, 'hdpi': 1.5, 'xhdpi': 2, 'xxhdpi': 3, 'xxxhdpi': 4}
for d, k in dens.items():
    out = f'{R}\mipmap-{d}'
    os.makedirs(out, exist_ok=True)
    fit(L('android_legacy'), int(48 * k)).save(out + r'\ic_launcher.png')
    fit(L('android_legacy'), int(48 * k)).save(out + r'\ic_launcher_round.png')
    for layer in ('fg', 'bg', 'mono'):
        name = {'fg': 'ic_launcher_foreground', 'bg': 'ic_launcher_background', 'mono': 'ic_launcher_monochrome'}[layer]
        fit(L('android_' + layer), int(108 * k)).save(f'{out}\{name}.png')

# Launch screen (Android 11 and older): rail green with the icon in the middle.
rail = (15, 59, 45, 255)
icon = L('android_legacy')
for f in glob.glob(R + r'\drawable*\splash.png'):
    w, h = Image.open(f).size
    im = Image.new('RGBA', (w, h), rail)
    s = int(min(w, h) * 0.28)
    im.alpha_composite(fit(icon, s), ((w - s) // 2, (h - s) // 2))
    im.convert('RGB').save(f)
print('ok')
