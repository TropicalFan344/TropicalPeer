import type { CapacitorConfig } from '@capacitor/cli'

// The Android app: this frontend in a WebView, with the Go engine
// (build/android/tropicalpeer.aar) behind the TropicalPeer plugin in
// android/app/src/main/java/app/tropicalpeer/. See docs/plan-android.md.
const config: CapacitorConfig = {
  appId: 'app.tropicalpeer',
  appName: 'TropicalPeer',
  webDir: 'dist',
  android: {
    // Plain HTTP stays off; the WebView only loads the bundled app.
    allowMixedContent: false,
  },
  plugins: {
    // Insets reach CSS as --safe-area-inset-* (see app.css).
    SystemBars: { insetsHandling: 'css' },
  },
}

export default config
