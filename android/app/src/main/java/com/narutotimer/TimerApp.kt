package com.narutotimer

import android.app.Application
import android.os.Handler
import android.os.Looper
import android.util.Log
import com.narutotimer.tracker.Session
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors

/**
 * 进程级对象图（architect 所有）。
 *
 *  - [settings]：持久化设置。
 *  - [session]：对局状态机（tracker/Session，Go ui/run.go session 的非 UI 部分），全进程唯一。
 *  - native 引擎：第一次需要时（主界面观察状态 [addNativeListener]、FrameLoop 启动）才在
 *    nt-assets 线程跑 [AssetLoader.loadAll]；无障碍服务绑定拉起进程时不解码资源（AND-16）。
 *    完成后 [nativeStatus] = READY，并在主线程回调监听者。READY 之前 FrameLoop 必须丢弃所有帧。
 *    重新加载串行执行，只有最新一次能发布状态（[NewestWins]，AND-12）。
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

    /** Once per process: an Activity recreation must not re-run the startup check. */
    @Volatile var autoUpdateChecked = false

    private val nativeListeners = CopyOnWriteArrayList<() -> Unit>()
    private val main = Handler(Looper.getMainLooper())
    /** Serial: each run reads the newest calibration / names, so the last one always wins. */
    private val calibrationWorker = Executors.newSingleThreadExecutor { r -> Thread(r, "nt-calibration").apply { isDaemon = true } }
    private val applyCalibration = Runnable {
        calibrationWorker.execute {
            if (!NativeCore.setCalibration(settings)) Log.w(TAG, "calibration: ${NativeCore.lastError()}")
            // 同一画面换了读法：像换采集源一样只作废视觉基线。
            session.resyncGeometry()
        }
    }
    /** Engine loads, one at a time; a load superseded by a newer request does not publish. */
    private val assetWorker = Executors.newSingleThreadExecutor { r -> Thread(r, "nt-assets").apply { isDaemon = true } }
    private val loads = NewestWins(assetWorker)
    @Volatile private var loadRequested = false

    override fun onCreate() {
        super.onCreate()
        instance = this
        settings = Settings(this)
        // 设置页改我方名字后立刻用于认边（Go identity.SetMineNames）。Session 在持有自己的锁时
        // 调用这里：交给 nt-calibration 线程，不在主线程/会话锁里等 native 锁（AND-10）。native
        // 跨引擎重建保留名字，加载中也可以设置；读取当时的设置，最后一次总是最新的（AND-08）。
        session = Session(settings) { _ ->
            calibrationWorker.execute { AssetLoader.applyPlayerNames(settings) }
        }
        // 区域校准保存后立即生效（docs/区域校准.md）。一次保存会连写几个键，合并成一次重建；
        // 引擎尚未建好时 native 只记下，由 nativeFinishAssets 应用。
        settings.addListener { key ->
            if (key == Settings.KEY_CALIB_PROFILE || key in Settings.KEY_CALIB_RECTS) {
                main.removeCallbacks(applyCalibration)
                main.postDelayed(applyCalibration, CALIBRATION_DEBOUNCE_MS)
            }
        }
    }

    /**
     * Loads the engine unless that was already requested: called by whatever needs it (the main
     * screen showing its status, FrameLoop starting). Any thread.
     */
    fun ensureNativeLoaded() {
        if (loadRequested) return
        synchronized(this) {
            if (loadRequested) return
            loadRequested = true
        }
        reloadNative()
    }

    /** 重新加载全部资源并重建引擎（例如用户导入了新的账号名图片）。后台线程执行。 */
    fun reloadNative() {
        loadRequested = true
        loads.submit(
            begin = { nativeStatus = NativeStatus.LOADING },
            work = { loadNative() },
            publish = { (report, message) ->
                assetReport = report
                nativeMessage = message
                nativeStatus = if (report?.ready == true) NativeStatus.READY else NativeStatus.FAILED
                notifyNative()
            },
        )
        notifyNative()
    }

    /** nt-assets thread. Never throws: a failure is reported as FAILED with its message. */
    private fun loadNative(): Pair<AssetLoader.Report?, String> = try {
        val report = AssetLoader.loadAll(this, settings.playerNames, settings)
        report to (if (report.ready) "" else report.nativeError.ifEmpty { report.errors.firstOrNull() ?: "unknown" })
    } catch (t: Throwable) {
        Log.e(TAG, "native load failed", t)
        null to (t.message ?: t.javaClass.simpleName)
    }

    private fun notifyNative() {
        main.post { for (l in nativeListeners) l() }
    }

    /** Registers a status listener (main thread); whoever shows the engine status wants it loaded. */
    fun addNativeListener(l: () -> Unit) {
        nativeListeners += l
        ensureNativeLoaded()
    }
    fun removeNativeListener(l: () -> Unit) { nativeListeners -= l }

    /** True once the service can safely access settings/session during boot. */
    fun isReady(): Boolean = ::settings.isInitialized && ::session.isInitialized

    companion object {
        private const val TAG = "TimerApp"
        private const val CALIBRATION_DEBOUNCE_MS = 100L
        lateinit var instance: TimerApp
            private set
    }
}
