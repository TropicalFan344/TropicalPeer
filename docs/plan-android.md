# Android app plan

The Go engine (pairing, NAT traversal, relay, transfers) is shared with the
desktop app and built into an Android library with gomobile. The UI is the
Svelte frontend inside a Capacitor shell, with Kotlin for what only Android
can do.

## Status

| Step | What | Status |
|---|---|---|
| 0 | App layer moved out of the Wails `main` package into `internal/appcore`; desktop `app.go` is a thin shell | Done |
| 1 | Sending reads from `transfer.Source` (name, size, open), not only paths | Done |
| 2 | `mobile` package: `Start`, `Call(method, argsJSON)`, `Listener.OnEvent`, `Documents.OpenFD`, `SetLocalIPs`, `NetworkChanged`; `mobile\build.ps1` builds `build\android\tropicalpeer.aar` | Done |
| 3 | Bare Kotlin test app (`mobile/android-probe`): start, pair with the PC, send and receive across two networks | Done |
| 4 | Capacitor shell (`frontend/android`, app id `app.tropicalpeer`) with the Svelte UI; `backend.ts` routes calls to Wails or the plugin; phone layout (DESIGN.md, Phone) | Done |
| 5 | Share target (files and text), foreground service while transferring, "Stay ready to receive" (Settings, off by default), offer notifications with Accept/Decline, "Received" notifications, router discovery from Android's addresses | Done |
| 6 | Sideloaded APK, then Play Store (foreground service type declaration) | |

## How the pieces meet

- **Calls.** `Mobile.call("SendDocuments", "[peerId, [{uri, name, size}]]")`
  returns JSON or throws with a message worded for people. Methods and
  argument order match the desktop App, so `api.ts` can route
  `Go.X(...args)` to `call("X", JSON.stringify(args))`. Differences:
  `SendDocuments` replaces `SendFiles`; there's no `PickFiles`,
  `ChooseFolder`, `ShowFile` or `OpenFolder`; the download folder and
  per-peer folders are fixed by the app.
- **Events.** `Listener.onEvent(name, json)` carries the desktop event names
  and payloads (`transfer:update`, `offer:new`, ...). It runs on a Go thread.
- **Picked files.** Kotlin's `Documents.openFD(uri)` returns
  `ParcelFileDescriptor.detachFd()`; Go owns and closes it. Size `-1` means
  unknown and is read from the descriptor. Retry opens the URI again, so the
  app should take a persistable URI permission for documents it may retry.
- **Received files** land in the `downloadDir` passed to `start`
  (`getExternalFilesDir(DIRECTORY_DOWNLOADS)`); Kotlin copies them into the
  user's Downloads through MediaStore on `transfer:update` with status `done`.
- **Network.** Android 11+ blocks Go from listing interface addresses, so
  Kotlin passes `LinkProperties` addresses to `setLocalIPs` at start and on
  every change, then calls `networkChanged()`. Without them, two devices on
  the same Wi-Fi can't connect directly. UPnP discovery needs a
  `WifiManager.MulticastLock`; without one, port forwarding reports
  "Not available" and everything else still works.

## Background behaviour (step 5)

- `TransferService` is a foreground service that runs while any transfer is
  active (type dataSync) and, if "Stay ready to receive" is on, all the time
  (type specialUse, since staying reachable has no natural end; Android 15
  caps dataSync at six hours a day). Its notification shows progress, or
  "Ready to receive". It stops when neither applies.
- "Stay ready to receive" was decided as a Settings switch, off by default:
  off, files arrive only while the app is open; on, the quiet notification
  keeps it online. Turning it on asks for notification permission (Android
  13+); without it the service still runs, and Settings offers "Allow
  notifications". Push through Google's FCM stays ruled out: it routes
  through Google, against "self-hosted & private".
- Offers arrive as high-priority notifications with Decline and Accept
  (`OfferActionReceiver`) only while the app is off screen; on screen the
  offer strip handles them. They time out with the offer. Files received off
  screen post a "Received" notification that opens the file.
- Share menu: `ACTION_SEND`/`SEND_MULTIPLE` for any type open the app with a
  "Send …" sheet listing paired devices. Shared text with no file is sent as
  `Link.txt` or `Note.txt`.
- Port forwarding: goupnp's own discovery and NAT-PMP's gateway lookup are
  both blocked on Android 11+, so `portmap` searches from the addresses
  Android reports (`SetPlatformNetwork`) and uses the default route's
  gateway that Kotlin passes in `setLocalIPs(ips, gateway)`.

## Verified on a POCO X7 Pro (Android 16, HyperOS)

Offer notification and Accept while the app had been on the home screen for
45 s with Stay ready on; Received notification; a 100 MB receive finishing
after leaving the app mid-transfer with Stay ready off (dataSync service,
stopped afterwards); sharing text and a 20 MB file from outside the app;
the Android-supplied router search found the PC's UPnP router (run on the PC
with supplied addresses only).

## Known gaps

- The UI is English only; the test phone runs Traditional Chinese.
- No app icon yet (Capacitor's default).
- HyperOS and other vendor skins may still stop the service under aggressive
  battery settings; "No restrictions" for TropicalPeer in battery settings
  avoids that. Not yet surfaced in the app.
- The service doesn't come back after the process is killed or the phone
  restarts; reopening the app starts it again.

## Building

The app:

```powershell
cd frontend
npm run build; npx cap sync android
cd android; $env:JAVA_HOME = "$env:USERPROFILE\.jdks\openjdk-23.0.2"; .\gradlew assembleDebug
adb install -r app\build\outputs\apk\debug\app-debug.apk
```

Capacitor 8 needs JDK 21 or newer with `jlink` (the JetBrains Runtime in `.jdks` lacks it). Both
Gradle wrappers point at gradle-8.14-bin, which is already unpacked under `F:\.gradle`.

The Go engine library:

```powershell
powershell -File mobile\build.ps1
```

Needs the Android SDK with an NDK (27.2.12479018 is installed), JDK 17 and
`gomobile`. `go.mod` records `golang.org/x/mobile/cmd/gobind` as a tool
dependency, which `gomobile bind` requires.
