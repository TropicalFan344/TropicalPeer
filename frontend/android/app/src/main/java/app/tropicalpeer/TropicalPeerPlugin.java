package app.tropicalpeer;

import android.Manifest;
import android.annotation.SuppressLint;
import android.app.DownloadManager;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.database.Cursor;
import android.net.Uri;
import android.os.Build;
import android.provider.DocumentsContract;
import android.provider.MediaStore;
import android.provider.OpenableColumns;
import android.provider.Settings;

import androidx.activity.result.ActivityResult;
import androidx.activity.result.contract.ActivityResultContracts;

import com.getcapacitor.JSArray;
import com.getcapacitor.JSObject;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PermissionState;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.ActivityCallback;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

/**
 * The frontend's way into the Go engine on Android (frontend/src/lib/backend.ts).
 * call() runs any app method by name, events arrive as "event" with the
 * desktop event names, and the rest are Android stand-ins for desktop
 * dialogs and the file manager.
 */
@CapacitorPlugin(
    name = "TropicalPeer",
    permissions = {
        @Permission(alias = "notifications", strings = { Manifest.permission.POST_NOTIFICATIONS }),
        @Permission(alias = "media", strings = { Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO }),
        @Permission(alias = "storage", strings = { Manifest.permission.READ_EXTERNAL_STORAGE }),
    }
)
public class TropicalPeerPlugin extends Plugin {

    @Override
    public void load() {
        try {
            Engine.start(getContext(), (name, json) -> {
                JSObject data = new JSObject();
                data.put("name", name);
                data.put("json", json);
                // Retained until the frontend subscribes, so startup events aren't lost.
                notifyListeners("event", data, true);
            });
        } catch (Exception e) {
            JSObject data = new JSObject();
            data.put("name", "node:error");
            data.put("json", JSObject.quote(e.getMessage() != null ? e.getMessage() : "can't start"));
            notifyListeners("event", data, true);
        }
        Engine.setForeground(true);
        // Opened from another app's share menu.
        handleShare(getActivity().getIntent());
    }

    @Override
    protected void handleOnResume() {
        Engine.setForeground(true);
    }

    @Override
    protected void handleOnPause() {
        Engine.setForeground(false);
    }

    @Override
    protected void handleOnDestroy() {
        // The screen is gone; with "Stay ready" on the engine lives on without it.
        Engine.setForeground(false);
        Engine.detach();
    }

    @Override
    protected void handleOnNewIntent(Intent intent) {
        handleShare(intent);
    }

    // ---- Share menu: "Send with TropicalPeer" ----

    /** Files shared from another app → "share" { files: [{ uri, name, size }] }; the UI asks which device. */
    private void handleShare(Intent intent) {
        if (intent == null) return;
        String action = intent.getAction();
        if (!Intent.ACTION_SEND.equals(action) && !Intent.ACTION_SEND_MULTIPLE.equals(action)) return;
        JSArray files = new JSArray();
        try {
            List<Uri> uris = new ArrayList<>();
            if (Intent.ACTION_SEND_MULTIPLE.equals(action)) {
                List<Uri> list = intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM);
                if (list != null) uris.addAll(list);
            } else {
                Uri one = intent.getParcelableExtra(Intent.EXTRA_STREAM);
                if (one != null) uris.add(one);
            }
            for (Uri u : uris) {
                if (Engine.mayRead(u)) files.put(describe(u));
                else android.util.Log.w(Engine.TAG, "ignored a shared file this app may not send: " + u);
            }
            // Shared text (a link, a note) with no file: send it as a small text file.
            CharSequence text = intent.getCharSequenceExtra(Intent.EXTRA_TEXT);
            if (uris.isEmpty() && text != null && text.length() > 0) files.put(textFile(text.toString()));
        } catch (Exception e) {
            android.util.Log.w(Engine.TAG, "couldn't read what was shared", e);
        }
        // Handled: don't offer the same files again after a rotation or restart.
        getActivity().setIntent(new Intent(Intent.ACTION_MAIN));
        JSObject data = new JSObject();
        data.put("files", files);
        notifyListeners("share", data, true);
    }

    private JSObject textFile(String text) throws Exception {
        String trimmed = text.trim();
        boolean link = trimmed.matches("(?s)^https?://\\S+$");
        File dir = new File(getContext().getCacheDir(), "shared");
        //noinspection ResultOfMethodCallIgnored
        dir.mkdirs();
        File f = new File(dir, (link ? "Link " : "Note ") + System.currentTimeMillis() % 100000 + ".txt");
        try (FileOutputStream out = new FileOutputStream(f)) {
            out.write(text.getBytes(StandardCharsets.UTF_8));
        }
        JSObject o = new JSObject();
        o.put("uri", Uri.fromFile(f).toString());
        o.put("name", link ? "Link.txt" : "Note.txt");
        o.put("size", f.length());
        return o;
    }

    // ---- Download folder ----

    /** → { name, custom } */
    @PluginMethod
    public void getDownloadFolder(PluginCall call) {
        call.resolve(folderState());
    }

    /** Opens Android's folder picker; → { name, custom } (unchanged if cancelled). */
    @PluginMethod
    public void chooseDownloadFolder(PluginCall call) {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION
                | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION);
        startActivityForResult(call, intent, "folderChosen");
    }

    @ActivityCallback
    private void folderChosen(PluginCall call, ActivityResult result) {
        Intent data = result.getData();
        if (result.getResultCode() == android.app.Activity.RESULT_OK && data != null && data.getData() != null) {
            Uri tree = data.getData();
            try {
                // Keep write access across restarts.
                getContext().getContentResolver().takePersistableUriPermission(tree,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
                releaseFolder();
                Engine.setDownloadTree(tree);
            } catch (SecurityException e) {
                call.reject(Notifier.t("android.folderFailed"));
                return;
            }
        }
        call.resolve(folderState());
    }

    /** Back to Download/TropicalPeer. */
    @PluginMethod
    public void resetDownloadFolder(PluginCall call) {
        releaseFolder();
        Engine.setDownloadTree(null);
        call.resolve(folderState());
    }

    private void releaseFolder() {
        Uri old = Engine.downloadTree();
        if (old == null) return;
        try {
            getContext().getContentResolver().releasePersistableUriPermission(old,
                Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
        } catch (SecurityException ignored) {
            // Already gone.
        }
    }

    private JSObject folderState() {
        JSObject o = new JSObject();
        o.put("name", Engine.downloadFolderName());
        o.put("custom", Engine.downloadTree() != null);
        return o;
    }

    // ---- Stay ready to receive ----

    /** → { on, notifications } */
    @PluginMethod
    public void getReady(PluginCall call) {
        call.resolve(readyState());
    }

    /** { on } → { on, notifications }. Turning it on asks for notification permission first. */
    @PluginMethod
    public void setReady(PluginCall call) {
        boolean on = Boolean.TRUE.equals(call.getBoolean("on", false));
        if (on && Build.VERSION.SDK_INT >= 33 && getPermissionState("notifications") != PermissionState.GRANTED) {
            requestPermissionForAlias("notifications", call, "readyPermission");
            return;
        }
        Engine.setReady(on);
        call.resolve(readyState());
    }

    @PermissionCallback
    private void readyPermission(PluginCall call) {
        // On either way: staying online works without notifications, offers just can't reach you outside the app.
        Engine.setReady(true);
        call.resolve(readyState());
    }

    private JSObject readyState() {
        JSObject o = new JSObject();
        o.put("on", Engine.ready());
        o.put("notifications", Notifier.allowed(getContext()));
        return o;
    }

    /** Opens TropicalPeer's notification settings. */
    @PluginMethod
    public void openNotificationSettings(PluginCall call) {
        try {
            Intent i = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                .putExtra(Settings.EXTRA_APP_PACKAGE, getContext().getPackageName())
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            getContext().startActivity(i);
            call.resolve();
        } catch (RuntimeException e) {
            call.reject(Notifier.t("android.openNotifSettings"));
        }
    }

    /** { method, args: JSON array string } → { json } */
    @PluginMethod
    public void call(PluginCall call) {
        String method = call.getString("method", "");
        String args = call.getString("args", "[]");
        Engine.call(method, args, new Engine.Callback<String>() {
            @Override
            public void ok(String json) {
                JSObject res = new JSObject();
                res.put("json", json);
                call.resolve(res);
            }

            @Override
            public void fail(String message) {
                call.reject(message);
            }
        });
    }

    /** Opens the system document picker → { files: [{ uri, name, size }] } (empty if cancelled). */
    @PluginMethod
    public void pickFiles(PluginCall call) {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT)
            .addCategory(Intent.CATEGORY_OPENABLE)
            .setType("*/*")
            .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        startActivityForResult(call, intent, "picked");
    }

    @ActivityCallback
    private void picked(PluginCall call, ActivityResult result) {
        JSArray files = new JSArray();
        Intent data = result.getData();
        if (result.getResultCode() == android.app.Activity.RESULT_OK && data != null) {
            List<Uri> uris = new ArrayList<>();
            if (data.getClipData() != null) {
                for (int i = 0; i < data.getClipData().getItemCount(); i++) uris.add(data.getClipData().getItemAt(i).getUri());
            } else if (data.getData() != null) {
                uris.add(data.getData());
            }
            for (Uri uri : uris) {
                // Keep access across restarts so a failed send can be retried.
                try {
                    getContext().getContentResolver().takePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION);
                } catch (SecurityException ignored) {
                    // Not every provider grants persistable access; this session still can read it.
                }
                files.put(describe(uri));
            }
        }
        JSObject res = new JSObject();
        res.put("files", files);
        call.resolve(res);
    }

    /**
     * Opens the system photo picker (photos and videos, no camera) →
     * { files: [{ uri, name, size }] }. Where there is no photo picker, the
     * document picker limited to images and videos stands in.
     */
    @PluginMethod
    public void pickMedia(PluginCall call) {
        // The picker hands out numbers ("1000018599.jpg") for names; reading
        // the photo library turns them back into real ones. Asked once: a
        // refusal only costs the names.
        String alias = mediaAlias();
        if (getPermissionState(alias) == PermissionState.PROMPT) {
            requestPermissionForAlias(alias, call, "mediaPermission");
            return;
        }
        openMediaPicker(call);
    }

    @PermissionCallback
    private void mediaPermission(PluginCall call) {
        openMediaPicker(call);
    }

    private static String mediaAlias() {
        return Build.VERSION.SDK_INT >= 33 ? "media" : "storage";
    }

    @SuppressLint("NewApi") // isPhotoPickerAvailable checks the SDK extension the picker needs
    private void openMediaPicker(PluginCall call) {
        Intent intent;
        if (ActivityResultContracts.PickVisualMedia.isPhotoPickerAvailable(getContext())) {
            intent = new Intent(MediaStore.ACTION_PICK_IMAGES)
                .putExtra(MediaStore.EXTRA_PICK_IMAGES_MAX, MediaStore.getPickImagesMaxLimit());
        } else {
            intent = new Intent(Intent.ACTION_OPEN_DOCUMENT)
                .addCategory(Intent.CATEGORY_OPENABLE)
                .setType("*/*")
                .putExtra(Intent.EXTRA_MIME_TYPES, new String[] { "image/*", "video/*" })
                .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        }
        startActivityForResult(call, intent, "picked");
    }

    /** Opens Android's folder picker → { files: [{ uri, name: "Folder/sub/file", size }] } (empty if cancelled). */
    @PluginMethod
    public void pickFolder(PluginCall call) {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION);
        startActivityForResult(call, intent, "folderPicked");
    }

    @ActivityCallback
    private void folderPicked(PluginCall call, ActivityResult result) {
        Intent data = result.getData();
        if (result.getResultCode() != android.app.Activity.RESULT_OK || data == null || data.getData() == null) {
            JSObject res = new JSObject();
            res.put("files", new JSArray());
            call.resolve(res);
            return;
        }
        Uri tree = data.getData();
        try {
            // Keep access across restarts so a failed send can be retried.
            getContext().getContentResolver().takePersistableUriPermission(tree, Intent.FLAG_GRANT_READ_URI_PERMISSION);
        } catch (SecurityException ignored) {
            // This session still can read it.
        }
        // A big folder takes a while to list; not on the main thread.
        new Thread(() -> {
            try {
                String root = DocumentsContract.getTreeDocumentId(tree);
                JSArray files = new JSArray();
                listFolder(tree, root, folderName(tree, root), files);
                JSObject res = new JSObject();
                res.put("files", files);
                call.resolve(res);
            } catch (Exception e) {
                call.reject(e.getMessage() != null ? e.getMessage() : "can't read the folder");
            }
        }).start();
    }

    private String folderName(Uri tree, String id) {
        try (Cursor c = getContext().getContentResolver().query(DocumentsContract.buildDocumentUriUsingTree(tree, id),
            new String[] { DocumentsContract.Document.COLUMN_DISPLAY_NAME }, null, null, null)) {
            if (c != null && c.moveToFirst() && !c.isNull(0)) return c.getString(0);
        }
        return "Folder";
    }

    /** Adds the files under the folder id to out, named by their path from the chosen folder. */
    private void listFolder(Uri tree, String id, String path, JSArray out) {
        Uri children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, id);
        try (Cursor c = getContext().getContentResolver().query(children, new String[] {
            DocumentsContract.Document.COLUMN_DOCUMENT_ID, DocumentsContract.Document.COLUMN_DISPLAY_NAME,
            DocumentsContract.Document.COLUMN_MIME_TYPE, DocumentsContract.Document.COLUMN_SIZE,
        }, null, null, null)) {
            while (c != null && c.moveToNext()) {
                String child = c.getString(0);
                String name = c.isNull(1) ? "file" : c.getString(1);
                if (DocumentsContract.Document.MIME_TYPE_DIR.equals(c.getString(2))) {
                    listFolder(tree, child, path + "/" + name, out);
                    continue;
                }
                JSObject f = new JSObject();
                f.put("uri", DocumentsContract.buildDocumentUriUsingTree(tree, child).toString());
                f.put("name", path + "/" + name);
                f.put("size", c.isNull(3) ? -1 : c.getLong(3));
                out.put(f);
            }
        }
    }

    /** The real name of a photo picker item, if the photo library may be read; else null. */
    private String mediaName(Uri uri) {
        if (!"media".equals(uri.getAuthority()) || !uri.getPath().contains("picker")) return null;
        if (getPermissionState(mediaAlias()) != PermissionState.GRANTED) return null;
        try {
            long id = Long.parseLong(uri.getLastPathSegment());
            try (Cursor c = getContext().getContentResolver().query(MediaStore.Files.getContentUri("external"),
                new String[] { MediaStore.MediaColumns.DISPLAY_NAME }, MediaStore.MediaColumns._ID + "=?",
                new String[] { String.valueOf(id) }, null)) {
                if (c != null && c.moveToFirst() && !c.isNull(0)) return c.getString(0);
            }
        } catch (RuntimeException e) {
            // Not a number, or the library won't say: keep the picker's name.
        }
        return null;
    }

    private JSObject describe(Uri uri) {
        String name = "file";
        long size = -1;
        try (Cursor c = getContext().getContentResolver().query(uri,
            new String[] { OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE }, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                if (!c.isNull(0)) name = c.getString(0);
                if (!c.isNull(1)) size = c.getLong(1);
            }
        } catch (RuntimeException e) {
            // A provider that won't describe the file: the engine reads the size itself.
        }
        String real = mediaName(uri);
        if (real != null) name = real;
        JSObject f = new JSObject();
        f.put("uri", uri.toString());
        f.put("name", name);
        f.put("size", size);
        return f;
    }



    /** Opens a received file in whatever app handles it. */
    @PluginMethod
    public void showFile(PluginCall call) {
        Uri uri = Engine.publicCopy(call.getString("path", ""));
        // The copy can be deleted or moved from Files at any time; Android then
        // refuses to share it, so check before handing it to another app.
        if (uri == null || !exists(uri)) {
            call.reject(Notifier.t("android.fileGone"));
            return;
        }
        try {
            String type = getContext().getContentResolver().getType(uri);
            Intent view = new Intent(Intent.ACTION_VIEW)
                .setDataAndType(uri, type != null ? type : "*/*")
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
            getContext().startActivity(Intent.createChooser(view, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
            call.resolve();
        } catch (ActivityNotFoundException e) {
            call.reject(Notifier.t("android.noViewer"));
        } catch (RuntimeException e) {
            // e.g. SecurityException if the file vanished between the check and here.
            call.reject(Notifier.t("android.fileGone"));
        }
    }

    private boolean exists(Uri uri) {
        // No projection: MediaStore rows and document-tree entries name their id columns differently.
        try (Cursor c = getContext().getContentResolver().query(uri, null, null, null, null)) {
            return c != null && c.moveToFirst();
        } catch (RuntimeException e) {
            return false;
        }
    }

    /** Opens the folder received files go to: the chosen one, or the Downloads screen. */
    /** Opens Android's share menu for text, such as a link (share by link). */
    @PluginMethod
    public void shareText(PluginCall call) {
        String text = call.getString("text", "");
        Intent send = new Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text);
        String title = call.getString("title");
        if (title != null) send.putExtra(Intent.EXTRA_SUBJECT, title);
        try {
            getActivity().startActivity(Intent.createChooser(send, null));
            call.resolve();
        } catch (RuntimeException e) {
            call.reject(e.getMessage());
        }
    }

    @PluginMethod
    public void openFolder(PluginCall call) {
        Uri tree = Engine.downloadTree();
        if (tree != null) {
            try {
                Uri dir = DocumentsContract.buildDocumentUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree));
                getContext().startActivity(new Intent(Intent.ACTION_VIEW)
                    .setDataAndType(dir, DocumentsContract.Document.MIME_TYPE_DIR)
                    .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK));
                call.resolve();
                return;
            } catch (RuntimeException ignored) {
                // No app opens folders directly on this phone: show Downloads instead.
            }
        }
        try {
            getContext().startActivity(new Intent(DownloadManager.ACTION_VIEW_DOWNLOADS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK));
            call.resolve();
        } catch (RuntimeException e) {
            call.reject(Notifier.t("android.openFilesApp"));
        }
    }
}
