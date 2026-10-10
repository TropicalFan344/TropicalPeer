package app.tropicalpeer;

import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;

import androidx.core.app.NotificationManagerCompat;
import androidx.core.app.ServiceCompat;

/**
 * Keeps TropicalPeer online while it isn't on screen, which Android allows
 * only with a visible notification: while files are moving (so a transfer
 * survives leaving the app or turning the screen off), and all the time
 * while "Stay ready to receive" is on.
 */
public class TransferService extends Service {
    private static volatile boolean running;

    /** Starts, updates or stops the service to match what the engine needs now. */
    static void sync(Context c) {
        boolean want = Engine.ready() || Engine.activeCount() > 0;
        if (want && !running) {
            try {
                if (Build.VERSION.SDK_INT >= 26) c.startForegroundService(new Intent(c, TransferService.class));
                else c.startService(new Intent(c, TransferService.class));
                running = true;
            } catch (RuntimeException e) {
                // Android refuses to start a foreground service from the background;
                // the next start from the foreground will catch up.
                Log.w(Engine.TAG, "couldn't start the transfer service", e);
            }
        } else if (!want && running) {
            c.stopService(new Intent(c, TransferService.class));
            running = false;
        } else if (running) {
            update(c);
        }
    }

    @SuppressWarnings("MissingPermission")
    private static void update(Context c) {
        try {
            NotificationManagerCompat.from(c).notify(Notifier.ID_SERVICE, Notifier.service(c, Engine.active()));
        } catch (SecurityException ignored) {
            // No notification permission: the service still runs, unseen.
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        int type = 0;
        if (Build.VERSION.SDK_INT >= 34) {
            // Staying ready has no natural end (special use); a transfer does (data sync).
            type = Engine.ready() ? ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE : ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC;
        } else if (Build.VERSION.SDK_INT >= 29) {
            type = ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC;
        }
        try {
            ServiceCompat.startForeground(this, Notifier.ID_SERVICE, Notifier.service(this, Engine.active()), type);
        } catch (RuntimeException e) {
            Log.w(Engine.TAG, "couldn't go foreground", e);
            running = false;
            stopSelf();
        }
        // Without the app's process the engine is gone too; don't come back empty.
        return START_NOT_STICKY;
    }

    /** Android 15 caps data-sync services at six hours a day. */
    @Override
    public void onTimeout(int startId, int fgsType) {
        running = false;
        stopSelf();
    }

    @Override
    public void onDestroy() {
        running = false;
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
