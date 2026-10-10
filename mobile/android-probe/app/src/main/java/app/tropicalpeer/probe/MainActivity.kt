package app.tropicalpeer.probe

import android.app.Activity
import android.content.Intent
import android.graphics.Typeface
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import android.text.InputType
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.text.DecimalFormat

/**
 * A plain diagnostic screen: enough to pair, send and receive. Intent
 * extras drive it from adb as well (see handle()).
 */
class MainActivity : Activity() {
    private lateinit var device: TextView
    private lateinit var server: EditText
    private lateinit var network: TextView
    private lateinit var code: TextView
    private lateinit var joinCode: EditText
    private lateinit var peersView: TextView
    private lateinit var sendButton: Button
    private lateinit var offerBox: LinearLayout
    private lateinit var offerText: TextView
    private lateinit var transfersView: TextView
    private lateinit var log: TextView

    private var peers = JSONArray()
    private var offerId: String? = null
    private val transfers = linkedMapOf<String, String>()
    private val logLines = ArrayDeque<String>()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        buildUi()
        Engine.onEvent = ::onEvent
        refresh()
        handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    override fun onDestroy() {
        if (Engine.onEvent == ::onEvent) Engine.onEvent = null
        super.onDestroy()
    }

    /** adb shell am start -n app.tropicalpeer.probe/.MainActivity --es server HOST:PORT | --es join CODE | --ez invite true | --ez accept true | --es sendPath /path */
    private fun handle(i: Intent) {
        i.getStringExtra("server")?.let { saveServer(it) }
        i.getStringExtra("join")?.let { join(it) }
        if (i.getBooleanExtra("invite", false)) invite()
        if (i.getBooleanExtra("accept", false)) respond(true)
        i.getStringExtra("sendPath")?.let { path ->
            val f = File(path)
            send(listOf(JSONObject().put("uri", Uri.fromFile(f).toString()).put("name", f.name).put("size", f.length())))
        }
    }

    private fun refresh() {
        Engine.call("GetDevice") { r, _ -> r?.let { showDevice(JSONObject(it)) } }
        Engine.call("GetSettings") { r, _ ->
            r?.let { val s = JSONObject(it); server.setText("${s.getString("serverHost")}:${s.getInt("serverPort")}") }
        }
        Engine.call("GetNetwork") { r, _ -> r?.let { showNetwork(JSONObject(it)) } }
        Engine.call("ListPeers") { r, _ -> r?.let { showPeers(JSONArray(it)) } }
        Engine.call("ListOffers") { r, _ ->
            r?.let { val a = JSONArray(it); if (a.length() > 0) showOffer(a.getJSONObject(0)) }
        }
    }

    private fun onEvent(name: String, json: String) {
        when (name) {
            "device:changed" -> showDevice(JSONObject(json))
            "network:changed" -> showNetwork(JSONObject(json))
            "server:status" -> {
                val s = JSONObject(json)
                note(if (s.getBoolean("online")) "server online" else "server offline ${s.optString("error")}")
            }
            "peers:changed" -> showPeers(JSONArray(json))
            "pair:done" -> { code.text = "Paired with ${JSONObject(json).getString("name")}"; note("paired") }
            "pair:failed" -> { code.text = json.trim('"'); note("pairing failed") }
            "offer:new" -> showOffer(JSONObject(json))
            "offer:closed" -> { offerBox.visibility = View.GONE; offerId = null; note(JSONObject(json).optString("reason")) }
            "transfer:update" -> showTransfer(JSONObject(json))
            "transfers:cleared" -> { transfers.clear(); transfersView.text = "" }
            "probe:published" -> note("saved to Downloads/TropicalPeer: ${JSONObject(json).getString("name")}")
            "node:error" -> note("engine error: $json")
        }
    }

    // ---- Actions ----

    private fun saveServer(addr: String) {
        val host = addr.substringBeforeLast(':').trim()
        val port = addr.substringAfterLast(':', "7777").trim().toIntOrNull() ?: 7777
        Engine.call("GetSettings") { r, _ ->
            val s = JSONObject(r ?: return@call).put("serverHost", host).put("serverPort", port)
            Engine.call("SaveSettings", s) { _, err -> note(err ?: "server set to $host:$port") }
        }
    }

    private fun invite() {
        code.text = "Getting a code…"
        Engine.call("StartInvite") { r, err ->
            code.text = if (r != null) JSONObject(r).getString("code") else err
        }
    }

    private fun join(c: String) {
        Engine.call("JoinWithCode", c.trim()) { r, err ->
            note(if (r != null) "paired with ${JSONObject(r).getString("name")}" else "join failed: $err")
        }
    }

    private fun respond(accept: Boolean) {
        val id = offerId ?: return note("no offer waiting")
        Engine.call("RespondOffer", id, accept)
        offerBox.visibility = View.GONE
        offerId = null
    }

    private fun send(docs: List<JSONObject>) {
        val peer = if (peers.length() > 0) peers.getJSONObject(0) else return note("pair with a device first")
        Engine.call("SendDocuments", peer.getString("id"), JSONArray(docs)) { _, err -> err?.let { note("send failed: $it") } }
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != PICK || resultCode != RESULT_OK || data == null) return
        val uris = buildList {
            data.clipData?.let { c -> for (i in 0 until c.itemCount) add(c.getItemAt(i).uri) }
            data.data?.let { if (isEmpty()) add(it) }
        }
        send(uris.map { uri ->
            var name = "file"
            var size = -1L
            contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
                if (c.moveToFirst()) {
                    name = c.getString(0) ?: name
                    if (!c.isNull(1)) size = c.getLong(1)
                }
            }
            JSONObject().put("uri", uri.toString()).put("name", name).put("size", size)
        })
    }

    // ---- Display ----

    private fun showDevice(d: JSONObject) {
        device.text = "${d.getString("name")}\n${d.getString("id")}"
    }

    private fun showNetwork(n: JSONObject) {
        val lines = mutableListOf(
            "Server: " + if (n.getBoolean("serverOnline")) "connected · ${n.getInt("rttMs")} ms" else "unreachable ${n.optString("serverError")}",
            "Public address: ${n.optString("publicAddr").ifEmpty { "—" }}",
            "Router: ${n.getString("router")}" + if (n.getBoolean("checking")) " (checking…)" else "",
            "Port forwarding: " + (n.optString("forwarded").ifEmpty { n.optString("mappingErr").ifEmpty { "—" } }),
            "Relay: " + if (n.getBoolean("relay")) "available" else "off",
        )
        network.text = lines.joinToString("\n")
    }

    private fun showPeers(list: JSONArray) {
        peers = list
        peersView.text = if (list.length() == 0) "No paired devices yet."
        else (0 until list.length()).joinToString("\n") { list.getJSONObject(it).let { p -> "• ${p.getString("name")} (${p.getString("policy")})" } }
        sendButton.isEnabled = list.length() > 0
        sendButton.text = if (list.length() > 0) "Send files to ${list.getJSONObject(0).getString("name")}" else "Send files"
    }

    private fun showOffer(o: JSONObject) {
        offerId = o.getString("id")
        val files = o.getJSONArray("files")
        offerText.text = "${o.getString("peerName")} wants to send ${files.length()} file(s), ${size(o.getLong("total"))}:\n" +
            (0 until files.length()).joinToString("\n") { "  ${files.getJSONObject(it).getString("name")}" }
        offerBox.visibility = View.VISIBLE
    }

    private fun showTransfer(t: JSONObject) {
        val dir = if (t.getString("direction") == "send") "→ ${t.getString("peerName")}" else "← ${t.getString("peerName")}"
        val files = t.getJSONArray("files")
        val first = if (files.length() > 0) files.getJSONObject(0).getString("name") else ""
        val total = t.getLong("total")
        val pct = if (total > 0) t.getLong("done") * 100 / total else 0
        val path = when {
            t.getBoolean("upgraded") -> " · went direct"
            t.getBoolean("relayed") -> " · relayed"
            else -> ""
        }
        val speed = t.getDouble("speed").let { if (it > 0) " · ${size(it.toLong())}/s" else "" }
        val err = t.optString("error").let { if (it.isNotEmpty()) "\n   $it" else "" }
        transfers[t.getString("id")] = "$dir  $first  ${t.getString("status")} $pct%$speed$path$err"
        transfersView.text = transfers.values.reversed().joinToString("\n")
    }

    private fun note(s: String) {
        logLines.addFirst(s)
        while (logLines.size > 12) logLines.removeLast()
        log.text = logLines.joinToString("\n")
    }

    private fun size(b: Long): String {
        val units = listOf("B", "KB", "MB", "GB")
        var v = b.toDouble()
        var u = 0
        while (v >= 1024 && u < units.lastIndex) { v /= 1024; u++ }
        return DecimalFormat("#.#").format(v) + " " + units[u]
    }

    // ---- Layout (code only, no resources) ----

    private fun buildUi() {
        val pad = (16 * resources.displayMetrics.density).toInt()
        val col = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(pad, pad * 2, pad, pad) }
        fun heading(s: String) = TextView(this).apply {
            text = s; textSize = 13f; setTypeface(typeface, Typeface.BOLD); setPadding(0, pad, 0, pad / 4)
        }.also { col.addView(it) }
        fun body(mono: Boolean = false) = TextView(this).apply {
            textSize = 14f; setTextIsSelectable(true); if (mono) typeface = Typeface.MONOSPACE
        }.also { col.addView(it) }
        fun row(vararg views: View) = LinearLayout(this).apply {
            views.forEach { addView(it, LinearLayout.LayoutParams(0, WRAP_CONTENT, if (it is EditText) 1f else 0f)) }
        }.also { col.addView(it) }
        fun button(s: String, onClick: () -> Unit) = Button(this).apply { text = s; isAllCaps = false; setOnClickListener { onClick() } }

        col.addView(TextView(this).apply { text = "TropicalPeer probe"; textSize = 22f })
        device = body(mono = true)

        heading("Rendezvous server")
        server = EditText(this).apply { inputType = InputType.TYPE_TEXT_VARIATION_URI; isSingleLine = true }
        row(server, button("Save") { saveServer(server.text.toString()) })
        network = body()
        col.addView(button("Check again") { Engine.call("RecheckNetwork") })

        heading("Pair")
        col.addView(button("Show a pairing code") { invite() })
        code = TextView(this).apply { textSize = 24f; setTextIsSelectable(true) }.also { col.addView(it) }
        joinCode = EditText(this).apply { hint = "7-orchid-tiger"; isSingleLine = true }
        row(joinCode, button("Join") { join(joinCode.text.toString()) })
        peersView = body()

        heading("Transfers")
        sendButton = button("Send files") {
            startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                type = "*/*"; addCategory(Intent.CATEGORY_OPENABLE); putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
            }, PICK)
        }.also { col.addView(it) }
        offerBox = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; visibility = View.GONE }
        offerText = TextView(this).apply { textSize = 15f }
        offerBox.addView(offerText)
        offerBox.addView(LinearLayout(this).apply {
            addView(button("Accept") { respond(true) })
            addView(button("Decline") { respond(false) })
        })
        col.addView(offerBox)
        transfersView = body(mono = true)

        heading("Log")
        log = body()

        setContentView(ScrollView(this).apply { fitsSystemWindows = true; addView(col, MATCH_PARENT, WRAP_CONTENT) })
    }

    companion object {
        private const val PICK = 1
    }
}
