package com.narutotimer

import android.app.Application
import android.os.Handler
import android.os.Looper
import android.util.Log
import com.narutotimer.tracker.Session
import java.util.concurrent.CopyOnWriteArrayList

/**
 * 进程级对象图（architect 所有）。
 *
 *  - [settings]：持久化设置。
 *  - [session]：对局状态机（tracker/Session，Go ui/run.go session 的非 UI 部分），全进程唯一。
 *  - native 引擎：onCreate 在后台线程跑 [AssetLoader.loadAll]，完成后 [nativeStatus] = READY，
 *    并在主线程回调 [addNativeListener] 注册的监听者。READY 之前 FrameLoop 必须丢弃所有帧。
 */
class TimerApp : Application() {
    enum class NativeStatus { LOADING, READY, FAILED }

    lateinit var settings: Settings
        private set
    lateinit var session: Session
        private set

    @Volatile var nativeStatus: NativeStatus = NativeStatus.LOADING
        private set
    @Volatile var nativeMessage: String = ""
        private set
    @Volatile var assetReport: AssetLoader.Report? = null
        private set

    private val nativeListeners = CopyOnWriteArrayList<() -> Unit>()
    private val main = Handler(Looper.getMainLooper())

    override fun onCreate() {
        super.onCreate()
        instance = this
        settings = Settings(this)
        // 设置页改我方名字后立刻用于认边（Go identity.SetMineNames）。
        session = Session(settings) { names ->
            if (nativeStatus == NativeStatus.READY) NativeCore.setPlayerNames(names)
        }
        Thread({ loadNative() }, "nt-assets").start()
    }

    /** 重新加载全部资源并重建引擎（例如用户导入了新的账号名图片）。后台线程执行。 */
    fun reloadNative() {
        nativeStatus = NativeStatus.LOADING
        notifyNative()
        Thread({ loadNative() }, "nt-assets").start()
    }

    private fun loadNative() {
        try {
            val report = AssetLoader.loadAll(this, settings.playerNames)
            assetReport = report
            nativeMessage = if (report.ready) "" else report.nativeError.ifEmpty { report.errors.firstOrNull() ?: "unknown" }
            nativeStatus = if (report.ready) NativeStatus.READY else NativeStatus.FAILED
        } catch (t: Throwable) {
            Log.e(TAG, "native load failed", t)
            nativeMessage = t.message ?: t.javaClass.simpleName
            nativeStatus = NativeStatus.FAILED
        }
        notifyNative()
    }

    private fun notifyNative() {
        main.post { for (l in nativeListeners) l() }
    }

    fun addNativeListener(l: () -> Unit) { nativeListeners += l }
    fun removeNativeListener(l: () -> Unit) { nativeListeners -= l }

    /** True once the service can safely access settings/session during boot. */
    fun isReady(): Boolean = ::settings.isInitialized && ::session.isInitialized

    companion object {
        private const val TAG = "TimerApp"
        lateinit var instance: TimerApp
            private set
    }
}
