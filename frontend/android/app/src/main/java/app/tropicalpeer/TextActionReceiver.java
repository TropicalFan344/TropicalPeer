package app.tropicalpeer;

import android.content.BroadcastReceiver;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.widget.Toast;

import org.json.JSONArray;

/** Copy tapped on a text's notification: the text goes to the clipboard, and counts as read. */
public class TextActionReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        String id = intent.getStringExtra("id");
        String body = intent.getStringExtra("body");
        if (id == null || body == null) return;
        ClipboardManager cm = context.getSystemService(ClipboardManager.class);
        if (cm != null) cm.setPrimaryClip(ClipData.newPlainText("TropicalPeer", body));
        // Android 13 and later confirm a copy themselves.
        if (Build.VERSION.SDK_INT < 33) Toast.makeText(context, Notifier.t("common.copied"), Toast.LENGTH_SHORT).show();
        Notifier.cancelText(context, id);
        PendingResult pending = goAsync();
        Engine.call("MarkTextRead", new JSONArray().put(id).toString(), new Engine.Callback<String>() {
            @Override
            public void ok(String value) {
                pending.finish();
            }

            @Override
            public void fail(String message) {
                pending.finish(); // the app was restarted and no longer has it
            }
        });
    }
}
