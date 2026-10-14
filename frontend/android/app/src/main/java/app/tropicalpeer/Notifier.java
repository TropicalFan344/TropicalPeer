package app.tropicalpeer;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;

import androidx.core.app.NotificationCompat;
import androidx.core.app.NotificationManagerCompat;

import org.json.JSONArray;
import org.json.JSONObject;

import app.tropicalpeer.mobile.Mobile;

/**
 * Every notification TropicalPeer shows. Offers and finished transfers only
 * appear while the app isn't on screen; inside the app the offer strip and
 * toasts already say it.
 */
final class Notifier {
    static final String CH_OFFERS = "offers";
    static final String CH_ACTIVE = "active";
    static final String CH_RECEIVED = "received";
    /** The foreground service's notification: transfer progress, or "Ready to receive". */
    static final int ID_SERVICE = 1;

    private Notifier() {}

    /** Text in the app's language, from the shared language files (locales/*.json). */
    static String t(String key, Object... params) {
        JSONObject p = new JSONObject();
        try {
            for (int i = 0; i + 1 < params.length; i += 2) p.put((String) params[i], params[i + 1]);
        } catch (Exception ignored) {
            // Keys are literals here.
        }
        return Mobile.text(key, p.toString());
    }

    static void createChannels(Context c) {
        if (Build.VERSION.SDK_INT < 26) return;
        NotificationManager nm = c.getSystemService(NotificationManager.class);
        // Called again when the language changes: same ids, so Android renames them.
        NotificationChannel offers = new NotificationChannel(CH_OFFERS, t("android.chOffers"), NotificationManager.IMPORTANCE_HIGH);
        offers.setDescription(t("android.chOffersDesc"));
        NotificationChannel active = new NotificationChannel(CH_ACTIVE, t("android.chActive"), NotificationManager.IMPORTANCE_LOW);
        active.setDescription(t("android.chActiveDesc"));
        active.setShowBadge(false);
        NotificationChannel received = new NotificationChannel(CH_RECEIVED, t("android.chReceived"), NotificationManager.IMPORTANCE_DEFAULT);
        received.setDescription(t("android.chReceivedDesc"));
        nm.createNotificationChannel(offers);
        nm.createNotificationChannel(active);
        nm.createNotificationChannel(received);
    }

    static boolean allowed(Context c) {
        return NotificationManagerCompat.from(c).areNotificationsEnabled();
    }

    private static PendingIntent openApp(Context c) {
        Intent i = new Intent(c, MainActivity.class).setFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        return PendingIntent.getActivity(c, 0, i, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
    }

    private static NotificationCompat.Builder base(Context c, String channel) {
        return new NotificationCompat.Builder(c, channel)
            .setSmallIcon(R.drawable.ic_stat_tropicalpeer)
            .setColor(0xFF146C4C) // jungle leaf
            .setContentIntent(openApp(c));
    }

    /** The foreground service's notification. active holds the running transfers. */
    static Notification service(Context c, JSONObject[] active) {
        NotificationCompat.Builder b = base(c, CH_ACTIVE).setOngoing(true).setOnlyAlertOnce(true).setSilent(true);
        if (active.length == 0 && Engine.linkCount() > 0) {
            int dl = Engine.linkDownloads();
            return b.setContentTitle(t("android.linksTitle", "count", Engine.linkCount()))
                .setContentText(dl > 0 ? t("android.linksDownloading", "count", dl) : t("android.linksWaiting"))
                .build();
        }
        if (active.length == 0) {
            return b.setContentTitle(t("android.readyTitle"))
                .setContentText(t("android.readyText"))
                .build();
        }
        long done = 0, total = 0;
        for (JSONObject x : active) {
            done += x.optLong("done");
            total += x.optLong("total");
        }
        int pct = total > 0 ? (int) (done * 100 / total) : 0;
        String title;
        if (active.length == 1) {
            JSONObject x = active[0];
            boolean sending = "send".equals(x.optString("direction"));
            String what = what(x.optJSONArray("files"));
            title = t(sending ? "android.sending" : "android.receiving", "what", what);
            String status = x.optString("status");
            String peer = x.optString("peerName");
            String text = "connecting".equals(status) ? t("transfer.findingPath", "name", peer)
                : "waiting".equals(status) ? t("transfer.waitingAccept", "name", peer)
                : t(sending ? "android.to" : "android.from", "name", peer, "pct", pct);
            b.setContentText(text);
            boolean moving = "sending".equals(status) || "receiving".equals(status);
            b.setProgress(100, pct, !moving);
        } else {
            title = t("android.transfers", "count", active.length);
            b.setContentText(pct + "%").setProgress(100, pct, false);
        }
        return b.setContentTitle(title).build();
    }

    static void offer(Context c, JSONObject o) {
        if (!allowed(c)) return;
        String id = o.optString("id");
        JSONArray files = o.optJSONArray("files");
        Intent accept = new Intent(c, OfferActionReceiver.class).putExtra("id", id).putExtra("accept", true);
        Intent decline = new Intent(c, OfferActionReceiver.class).putExtra("id", id).putExtra("accept", false);
        int code = id.hashCode();
        long left = o.optLong("expiresAt") - System.currentTimeMillis();
        NotificationCompat.Builder b = base(c, CH_OFFERS)
            .setContentTitle(t("offer.wants", "name", o.optString("peerName"), "what", what(files)))
            .setContentText(size(o.optLong("total")))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .addAction(0, t("offer.decline"), PendingIntent.getBroadcast(c, code * 2, decline, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT))
            .addAction(0, t("offer.accept"), PendingIntent.getBroadcast(c, code * 2 + 1, accept, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT));
        if (left > 0) b.setTimeoutAfter(left); // the offer declines itself then
        notify(c, offerTag(id), b.build());
    }

    static void cancelOffer(Context c, String id) {
        NotificationManagerCompat.from(c).cancel(offerTag(id), 0);
    }

    static void received(Context c, String name, String peer, Uri file) {
        if (!allowed(c)) return;
        NotificationCompat.Builder b = base(c, CH_RECEIVED)
            .setContentTitle(t("android.receivedTitle", "name", name))
            .setContentText(t("android.receivedFrom", "name", peer))
            .setAutoCancel(true);
        if (file != null) {
            Intent view = new Intent(Intent.ACTION_VIEW).setDataAndType(file, c.getContentResolver().getType(file))
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
            b.setContentIntent(PendingIntent.getActivity(c, file.hashCode(), view, PendingIntent.FLAG_IMMUTABLE));
        }
        notify(c, "received:" + name, b.build());
    }

    @SuppressWarnings("MissingPermission") // allowed() checked by callers
    private static void notify(Context c, String tag, Notification n) {
        try {
            NotificationManagerCompat.from(c).notify(tag, 0, n);
        } catch (SecurityException ignored) {
            // Notifications were turned off between the check and here.
        }
    }

    private static String offerTag(String id) {
        return "offer:" + id;
    }

    private static String what(JSONArray files) {
        int n = files == null ? 0 : files.length();
        if (n == 1) return files.optJSONObject(0).optString("name");
        return t("common.files", "count", n);
    }

    static String size(long b) {
        String[] units = { "B", "KB", "MB", "GB", "TB" };
        double v = b;
        int u = 0;
        while (v >= 1024 && u < units.length - 1) {
            v /= 1024;
            u++;
        }
        // Same as the app's formatSize (format.ts).
        return (v >= 100 || v == Math.rint(v) ? String.format("%.0f", v) : String.format("%.1f", v)) + " " + units[u];
    }
}
