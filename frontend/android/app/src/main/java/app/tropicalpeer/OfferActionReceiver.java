package app.tropicalpeer;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

import org.json.JSONArray;

/** Accept or Decline tapped on an incoming-offer notification. */
public class OfferActionReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context context, Intent intent) {
        String id = intent.getStringExtra("id");
        if (id == null) return;
        boolean accept = intent.getBooleanExtra("accept", false);
        Notifier.cancelOffer(context, id);
        PendingResult pending = goAsync();
        String args = new JSONArray().put(id).put(accept).toString();
        Engine.call("RespondOffer", args, new Engine.Callback<String>() {
            @Override
            public void ok(String value) {
                pending.finish();
            }

            @Override
            public void fail(String message) {
                pending.finish(); // the offer had already expired or been answered
            }
        });
    }
}
