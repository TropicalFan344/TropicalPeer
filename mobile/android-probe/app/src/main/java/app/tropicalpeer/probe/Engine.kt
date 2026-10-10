package app.tropicalpeer.probe

import android.content.ContentValues
import android.content.Context
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import android.provider.Settings
import android.util.Log
import app.tropicalpeer.mobile.Documents
import app.tropicalpeer.mobile.Listener
import app.tropicalpeer.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

const val TAG = "TPProbe"

/**
 * The Go engine for the whole process: started once, events fanned out to
 * whichever screen is showing. This is the part the real app keeps; only
 * the UI around it is throwaway.
 */
object Engine {
    private val main = Handler(Looper.getMainLooper())
    private val work = Executors.newCachedThreadPool()
    private lateinit var app: Context
    private var started = false
    private var lastIPs: String? = null
    private val published = mutableSetOf<String>()

    /** Called on the main thread for every engine event. */
    var onEvent: ((name: String, json: String) -> Unit)? = null

    fun start(context: Context) {
        if (started) return
        app = context.applicationContext
        val dataDir = app.filesDir.path
        val downloads = (app.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: File(app.filesDir, "downloads")).path
        val name = Settings.Global.getString(app.contentResolver, "device_name") ?: Build.MODEL

        val cm = app.getSystemService(ConnectivityManager::class.java)
        cm.getLinkProperties(cm.activeNetwork)?.let { reportIPs(it, initial = true) }

        Mobile.start(dataDir, downloads, name, listener, documents)
        started = true
        Log.i(TAG, "engine started: data=$dataDir downloads=$downloads name=$name")

        cm.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) = reportIPs(lp, initial = false)
        })
        // UPnP discovery needs multicast on Wi-Fi.
        app.getSystemService(WifiManager::class.java)
            .createMulticastLock("tropicalpeer-upnp").apply { setReferenceCounted(false); acquire() }
    }

    /** Android 11+ won't let Go list addresses, so pass them in; a change means a new network. */
    private fun reportIPs(lp: LinkProperties, initial: Boolean) {
        val ips = lp.linkAddresses.mapNotNull { it.address.hostAddress?.substringBefore('%') }.sorted().joinToString("\n")
        if (ips == lastIPs) return
        lastIPs = ips
        Mobile.setLocalIPs(ips)
        Log.i(TAG, "local IPs: ${ips.replace('\n', ' ')}")
        if (!initial && started) Mobile.networkChanged()
    }

    /** Runs an engine method off the main thread; the callback gets JSON or an error message. */
    fun call(method: String, vararg args: Any?, done: (result: String?, error: String?) -> Unit = { _, _ -> }) {
        val json = JSONArray().apply { args.forEach { put(it ?: JSONObject.NULL) } }.toString()
        work.execute {
            try {
                val res = Mobile.call(method, json)
                Log.i(TAG, "call $method -> $res")
                main.post { done(res, null) }
            } catch (e: Exception) {
                Log.w(TAG, "call $method failed: ${e.message}")
                main.post { done(null, e.message ?: "failed") }
            }
        }
    }

    private val listener = object : Listener {
        override fun onEvent(name: String, json: String) {
            Log.i(TAG, "event $name $json")
            if (name == "transfer:update") publishReceived(json)
            main.post { onEvent?.invoke(name, json) }
        }
    }

    private val documents = object : Documents {
        override fun openFD(uri: String): Long {
            val pfd = app.contentResolver.openFileDescriptor(Uri.parse(uri), "r")
                ?: throw IllegalStateException("can't open $uri")
            return pfd.detachFd().toLong()
        }
    }

    /** Received files land in the app's folder; copy finished ones into Downloads/TropicalPeer. */
    private fun publishReceived(json: String) {
        val t = JSONObject(json)
        if (t.optString("direction") != "receive" || t.optString("status") != "done") return
        if (Build.VERSION.SDK_INT < 29 || !published.add(t.getString("id"))) return
        val saved = t.optJSONArray("saved") ?: return
        work.execute {
            for (i in 0 until saved.length()) {
                val f = File(saved.getString(i))
                try {
                    val values = ContentValues().apply {
                        put(MediaStore.Downloads.DISPLAY_NAME, f.name)
                        put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/TropicalPeer")
                        put(MediaStore.Downloads.IS_PENDING, 1)
                    }
                    val resolver = app.contentResolver
                    val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: continue
                    resolver.openOutputStream(uri)!!.use { out -> f.inputStream().use { it.copyTo(out) } }
                    values.clear()
                    values.put(MediaStore.Downloads.IS_PENDING, 0)
                    resolver.update(uri, values, null, null)
                    f.delete()
                    Log.i(TAG, "published ${f.name} to Downloads/TropicalPeer")
                    main.post { onEvent?.invoke("probe:published", JSONObject().put("name", f.name).toString()) }
                } catch (e: Exception) {
                    Log.w(TAG, "couldn't publish ${f.name}: ${e.message}")
                }
            }
        }
    }
}
