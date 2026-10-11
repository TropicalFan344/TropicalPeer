package app.tropicalpeer;

import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.net.Uri;
import android.os.Build;
import android.provider.Settings;
import android.util.Log;

import java.io.File;
import java.io.FileInputStream;
import java.io.InputStream;
import java.io.OutputStream;

/**
 * Installs an update the engine downloaded and verified ("update:install").
 * An app that isn't from an app store can't update itself silently: Android
 * shows its own "Update this app?" dialog, and the first time it also asks
 * to allow TropicalPeer to install apps. Both need the app on screen, so a
 * request that arrives in the background waits for the next time it is.
 * Android only accepts an APK signed with the same key as the installed app.
 */
final class Installer {
    private Installer() {}

    /** The downloaded APK waiting to be installed, if any. */
    private static String pending;
    /** We sent the person to "Install unknown apps" and wait for them to come back. */
    private static boolean askedPermission;

    /** An update to install (automatically, or the person pressed Install). */
    static synchronized void request(Context ctx, String path) {
        pending = path;
        askedPermission = false;
        if (Engine.isForeground()) begin(ctx);
    }

    /** The app came on screen: carry on with a waiting install. */
    static synchronized void resume(Context ctx) {
        if (pending != null) begin(ctx);
    }

    private static void begin(Context ctx) {
        if (Build.VERSION.SDK_INT >= 26 && !ctx.getPackageManager().canRequestPackageInstalls()) {
            if (askedPermission) return; // they declined; ask again only when they press Install
            askedPermission = true;
            Intent settings = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:" + ctx.getPackageName()));
            settings.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            ctx.startActivity(settings);
            return;
        }
        String path = pending;
        pending = null;
        askedPermission = false;
        try {
            File apk = new File(path);
            PackageInstaller installer = ctx.getPackageManager().getPackageInstaller();
            PackageInstaller.SessionParams params = new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
            params.setAppPackageName(ctx.getPackageName());
            if (Build.VERSION.SDK_INT >= 31) {
                params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED);
            }
            int id = installer.createSession(params);
            try (PackageInstaller.Session session = installer.openSession(id)) {
                try (InputStream in = new FileInputStream(apk); OutputStream out = session.openWrite("TropicalPeer.apk", 0, apk.length())) {
                    byte[] buf = new byte[256 * 1024];
                    for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
                    session.fsync(out);
                }
                Intent done = new Intent(ctx, InstallResultReceiver.class);
                int flags = PendingIntent.FLAG_UPDATE_CURRENT | (Build.VERSION.SDK_INT >= 31 ? PendingIntent.FLAG_MUTABLE : 0);
                PendingIntent callback = PendingIntent.getBroadcast(ctx, id, done, flags);
                session.commit(callback.getIntentSender());
            }
        } catch (Exception e) {
            Log.w(Engine.TAG, "couldn't start installing the update", e);
        }
    }
}
