package app.tropicalpeer;

import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.content.SharedPreferences;
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.LinkProperties;
import android.net.Network;
import android.net.RouteInfo;
import android.net.Uri;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.os.ParcelFileDescriptor;
import android.provider.DocumentsContract;
import android.provider.MediaStore;
import android.webkit.MimeTypeMap;
import android.provider.Settings;
import android.util.Log;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.File;
import java.io.FileInputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.Inet4Address;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import app.tropicalpeer.mobile.Documents;
import app.tropicalpeer.mobile.Listener;
import app.tropicalpeer.mobile.Mobile;

/**
 * The Go engine (package mobile) for the whole process: started once, with
 * the Android duties Go can't do itself. Received files are copied into
 * Downloads/TropicalPeer; network changes and the device's addresses are
 * reported to Go (Android 11+ won't let Go list them).
 */
final class Engine {
    static final String TAG = "TropicalPeer";

    interface EventSink {
        void onEvent(String name, String json);
    }

    private static Context app;
    private static boolean started;
    private static String lastIPs;
    private static volatile EventSink sink;
    private static final ExecutorService work = Executors.newCachedThreadPool();
    private static final Set<String> published = Collections.newSetFromMap(new ConcurrentHashMap<>());
    /** Where each received file ended up in MediaStore, by its original path. */
    private static final Map<String, Uri> publicCopies = new ConcurrentHashMap<>();
    /** Running transfers by id, for the foreground service's notification. */
    private static final Map<String, JSONObject> active = new ConcurrentHashMap<>();
    private static final Handler main = new Handler(Looper.getMainLooper());
    private static volatile boolean foreground;
    private static boolean syncQueued;
    private static SharedPreferences prefs;

    private Engine() {}

    static synchronized void start(Context context, EventSink eventSink) throws Exception {
        sink = eventSink;
        if (started) return;
        app = context.getApplicationContext();
        prefs = app.getSharedPreferences("tropicalpeer", Context.MODE_PRIVATE);
        Notifier.createChannels(app);
        String dataDir = app.getFilesDir().getPath();
        File dl = app.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS);
        String downloads = (dl != null ? dl : new File(app.getFilesDir(), "downloads")).getPath();
        String name = Settings.Global.getString(app.getContentResolver(), "device_name");
        if (name == null || name.isEmpty()) name = Build.MODEL;

        ConnectivityManager cm = app.getSystemService(ConnectivityManager.class);
        LinkProperties lp = cm.getLinkProperties(cm.getActiveNetwork());
        if (lp != null) reportIPs(lp, true);

        Mobile.start(dataDir, downloads, name, listener, documents);
        started = true;

        cm.registerDefaultNetworkCallback(new ConnectivityManager.NetworkCallback() {
            @Override
            public void onLinkPropertiesChanged(Network network, LinkProperties props) {
                reportIPs(props, false);
            }
        });
        // UPnP router discovery is multicast on Wi-Fi.
        WifiManager.MulticastLock lock = app.getSystemService(WifiManager.class).createMulticastLock("tropicalpeer-upnp");
        lock.setReferenceCounted(false);
        lock.acquire();

        // Started from the activity, so a "Stay ready" service may start now.
        TransferService.sync(app);
    }

    // ---- What the foreground service and notifications need to know ----

    // ---- Where received files go ----

    /** The folder the user chose (a document tree), or null for Download/TropicalPeer. */
    static Uri downloadTree() {
        String s = prefs == null ? null : prefs.getString("downloadTree", null);
        return s == null ? null : Uri.parse(s);
    }

    static void setDownloadTree(Uri tree) {
        if (tree == null) prefs.edit().remove("downloadTree").apply();
        else prefs.edit().putString("downloadTree", tree.toString()).apply();
    }

    /** "Download/TropicalPeer", or the chosen folder as a readable path ("Download/Shared"). */
    static String downloadFolderName() {
        Uri tree = downloadTree();
        if (tree == null) return Environment.DIRECTORY_DOWNLOADS + "/TropicalPeer";
        String id = DocumentsContract.getTreeDocumentId(tree); // e.g. "primary:Download/Shared"
        int colon = id.indexOf(':');
        String path = colon >= 0 ? id.substring(colon + 1) : id;
        if (!path.isEmpty()) return path;
        try (android.database.Cursor c = app.getContentResolver().query(
            DocumentsContract.buildDocumentUriUsingTree(tree, id),
            new String[] { DocumentsContract.Document.COLUMN_DISPLAY_NAME }, null, null, null)) {
            if (c != null && c.moveToFirst()) return c.getString(0);
        } catch (RuntimeException ignored) {
            // Fall through to the raw id.
        }
        return id;
    }

    /** Stops sending events to a screen that has closed. */
    static void detach() {
        sink = null;
    }

    static boolean ready() {
        return prefs != null && prefs.getBoolean("ready", false);
    }

    static void setReady(boolean on) {
        prefs.edit().putBoolean("ready", on).apply();
        TransferService.sync(app);
    }

    static int activeCount() {
        return active.size();
    }

    static JSONObject[] active() {
        return active.values().toArray(new JSONObject[0]);
    }

    /** Whether the app is on screen: offers and arrivals then show in the app, not as notifications. */
    static void setForeground(boolean on) {
        foreground = on;
        if (on && app != null) Installer.resume(app);
    }

    static boolean isForeground() {
        return foreground;
    }

    /** Progress arrives several times a second; refresh the notification at most twice. */
    private static synchronized void queueSync() {
        if (syncQueued) return;
        syncQueued = true;
        main.postDelayed(() -> {
            synchronized (Engine.class) {
                syncQueued = false;
            }
            TransferService.sync(app);
        }, 500);
    }

    /** Runs an engine method off the main thread. */
    static void call(String method, String argsJSON, Callback<String> done) {
        if ("RespondOffer".equals(method)) {
            try {
                Notifier.cancelOffer(app, new JSONArray(argsJSON).getString(0));
            } catch (Exception ignored) {
                // Malformed arguments: the engine will say so.
            }
        }
        work.execute(() -> {
            try {
                String result = Mobile.call(method, argsJSON);
                // Notification channel names follow the app's language.
                if ("SetLanguage".equals(method)) Notifier.createChannels(app);
                done.ok(result);
            } catch (Exception e) {
                done.fail(e.getMessage() != null ? e.getMessage() : "Something went wrong");
            }
        });
    }

    interface Callback<T> {
        void ok(T value);
        void fail(String message);
    }

    /** The MediaStore copy of a received file, if it was published. */
    static Uri publicCopy(String path) {
        return publicCopies.get(path);
    }

    private static synchronized void reportIPs(LinkProperties lp, boolean initial) {
        List<String> ips = new ArrayList<>();
        for (LinkAddress a : lp.getLinkAddresses()) {
            String s = a.getAddress().getHostAddress();
            if (s != null) ips.add(s.split("%")[0]);
        }
        Collections.sort(ips);
        // The router: the default route's IPv4 gateway (NAT-PMP port forwarding asks it).
        String gateway = "";
        for (RouteInfo r : lp.getRoutes()) {
            if (r.isDefaultRoute() && r.getGateway() instanceof Inet4Address) {
                gateway = r.getGateway().getHostAddress();
                break;
            }
        }
        String joined = String.join("\n", ips) + "|" + gateway;
        if (joined.equals(lastIPs)) return;
        lastIPs = joined;
        Mobile.setLocalIPs(String.join("\n", ips), gateway);
        if (!initial && started) Mobile.networkChanged();
    }

    private static final Listener listener = new Listener() {
        @Override
        public void onEvent(String name, String json) {
            switch (name) {
                case "update:install":
                    try {
                        Installer.request(app, new JSONObject(json).getString("path"));
                    } catch (Exception e) {
                        Log.w(TAG, "couldn't read the update to install", e);
                    }
                    break;
                case "transfer:update":
                    track(json);
                    publishReceived(json);
                    break;
                case "offer:new":
                    if (!foreground) {
                        try {
                            Notifier.offer(app, new JSONObject(json));
                        } catch (Exception ignored) {
                            // Not an offer we can show.
                        }
                    }
                    break;
                case "offer:closed":
                    try {
                        Notifier.cancelOffer(app, new JSONObject(json).optString("id"));
                    } catch (Exception ignored) {
                        // Nothing to cancel.
                    }
                    break;
            }
            EventSink s = sink;
            if (s != null) s.onEvent(name, json);
        }
    };

    /**
     * Whether a file another app handed us (share sheet, picker) may be read
     * and sent. Opening a URI uses this app's own permissions, so without
     * this check another app could "share" TropicalPeer's private files, its
     * identity key included, and have them sent on. Allowed: other apps'
     * content providers, and the text notes this app writes for shared text.
     */
    static boolean mayRead(Uri uri) {
        if (uri == null || uri.getScheme() == null) return false;
        switch (uri.getScheme()) {
            case ContentResolver.SCHEME_CONTENT: {
                String authority = uri.getAuthority();
                // Our own FileProvider serves this app's files: never from outside.
                return authority != null && !authority.startsWith(app.getPackageName());
            }
            case ContentResolver.SCHEME_FILE: {
                try {
                    String path = new File(uri.getPath()).getCanonicalPath();
                    String notes = new File(app.getCacheDir(), "shared").getCanonicalPath() + File.separator;
                    return path.startsWith(notes);
                } catch (Exception e) {
                    return false;
                }
            }
            default:
                return false;
        }
    }

    private static final Documents documents = new Documents() {
        @Override
        public long openFD(String uri) throws Exception {
            Uri u = Uri.parse(uri);
            if (!mayRead(u)) throw new SecurityException("that file can't be sent");
            ParcelFileDescriptor pfd = app.getContentResolver().openFileDescriptor(u, "r");
            if (pfd == null) throw new Exception("can't open the file");
            return pfd.detachFd();
        }
    };

    private static void track(String json) {
        try {
            JSONObject t = new JSONObject(json);
            String status = t.optString("status");
            boolean running = status.equals("connecting") || status.equals("waiting") || status.equals("sending") || status.equals("receiving");
            if (running) active.put(t.getString("id"), t);
            else if (active.remove(t.getString("id")) == null) return;
            queueSync();
        } catch (Exception e) {
            Log.w(TAG, "couldn't read transfer update", e);
        }
    }

    /** Copies f into the chosen folder; null if that fails. The provider renames on a name clash. */
    private static Uri publishToTree(File f, Uri tree) {
        ContentResolver resolver = app.getContentResolver();
        Uri doc = null;
        try {
            Uri parent = DocumentsContract.buildDocumentUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree));
            String ext = MimeTypeMap.getFileExtensionFromUrl(Uri.fromFile(f).toString());
            String mime = ext == null ? null : MimeTypeMap.getSingleton().getMimeTypeFromExtension(ext.toLowerCase());
            doc = DocumentsContract.createDocument(resolver, parent, mime != null ? mime : "application/octet-stream", f.getName());
            if (doc == null) return null;
            try (InputStream in = new FileInputStream(f); OutputStream out = resolver.openOutputStream(doc)) {
                if (out == null) throw new Exception("no output stream");
                byte[] buf = new byte[256 * 1024];
                for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
            }
            return doc;
        } catch (Exception e) {
            Log.w(TAG, "couldn't save " + f.getName() + " to the chosen folder", e);
            if (doc != null) {
                try {
                    DocumentsContract.deleteDocument(resolver, doc);
                } catch (Exception ignored) {
                    // Leave the partial copy; nothing more to do.
                }
            }
            return null;
        }
    }

    /** Copies a finished incoming transfer into Downloads/TropicalPeer, where people look for it. */
    private static void publishReceived(String json) {
        if (Build.VERSION.SDK_INT < 29) return; // older Androids: files stay in the app's folder
        try {
            JSONObject t = new JSONObject(json);
            if (!"receive".equals(t.optString("direction")) || !"done".equals(t.optString("status"))) return;
            if (!published.add(t.getString("id"))) return;
            JSONArray saved = t.optJSONArray("saved");
            if (saved == null) return;
            String peer = t.optString("peerName");
            boolean announce = !foreground;
            for (int i = 0; i < saved.length(); i++) {
                String path = saved.getString(i);
                work.execute(() -> {
                    Uri uri = publish(path);
                    if (announce) Notifier.received(app, new File(path).getName(), peer, uri);
                });
            }
        } catch (Exception e) {
            Log.w(TAG, "couldn't read transfer update", e);
        }
    }

    private static Uri publish(String path) {
        File f = new File(path);
        Uri tree = downloadTree();
        if (tree != null) {
            Uri uri = publishToTree(f, tree);
            if (uri != null) {
                publicCopies.put(path, uri);
                //noinspection ResultOfMethodCallIgnored
                f.delete();
                return uri;
            }
            // The folder is gone or access was revoked: fall back to Downloads.
        }
        ContentResolver resolver = app.getContentResolver();
        ContentValues values = new ContentValues();
        values.put(MediaStore.Downloads.DISPLAY_NAME, f.getName());
        values.put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/TropicalPeer");
        values.put(MediaStore.Downloads.IS_PENDING, 1);
        Uri uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values);
        if (uri == null) return null;
        try (InputStream in = new FileInputStream(f); OutputStream out = resolver.openOutputStream(uri)) {
            if (out == null) throw new Exception("no output stream");
            byte[] buf = new byte[256 * 1024];
            for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
        } catch (Exception e) {
            Log.w(TAG, "couldn't copy " + f.getName() + " to Downloads", e);
            resolver.delete(uri, null, null);
            return null;
        }
        values.clear();
        values.put(MediaStore.Downloads.IS_PENDING, 0);
        resolver.update(uri, values, null, null);
        publicCopies.put(path, uri);
        //noinspection ResultOfMethodCallIgnored
        f.delete();
        return uri;
    }
}
