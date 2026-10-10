# App icon

A file passing from one device (the ring) to another (the mint dot): what TropicalPeer does.

- `mark.py` writes the SVGs: `mac.svg` (Apple's 824/1024 grid with shadow), `win.svg`
  (Windows, near full bleed), and the Android adaptive layers (`android_bg`, `android_fg`,
  `android_mono` for themed icons, `android_legacy` for Android 7).
- Render each SVG to a 1024px PNG (any browser: open it, screenshot at 1024x1024 with a
  transparent background), then `install.py` writes `build/appicon.png`,
  `build/windows/icon.ico`, the Android mipmaps and the Android launch screen.
- The Android notification icon is drawn by hand in
  `frontend/android/app/src/main/res/drawable/ic_stat_tropicalpeer.xml` from the same shapes.
