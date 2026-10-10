package app.tropicalpeer.probe

import android.app.Application
import android.util.Log

class ProbeApp : Application() {
    override fun onCreate() {
        super.onCreate()
        try {
            Engine.start(this)
        } catch (e: Exception) {
            Log.e(TAG, "engine failed to start", e)
        }
    }
}
