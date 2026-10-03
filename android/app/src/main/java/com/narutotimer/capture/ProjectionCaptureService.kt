package com.narutotimer.capture

import android.app.Activity
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.PixelFormat
import android.media.ImageReader
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import android.view.Display
import android.view.WindowManager
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import com.narutotimer.service.TimerAccessibilityService
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.math.max

class ProjectionCaptureService : Service() {
    private var projection: MediaProjection? = null
    private var display: VirtualDisplay? = null
    private var reader: ImageReader? = null
    private var worker: HandlerThread? = null
    private var handler: Handler? = null
    private var callback: MediaProjection.Callback? = null
    private val stopping = AtomicBoolean(false)
    private var captureGeneration = 0L
    private var readerGeneration = 0L
    private var width = CAPTURE_WIDTH
    private var height = DEFAULT_CAPTURE_HEIGHT
    private var densityDpi = 0

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        val code = intent?.getIntExtra(EXTRA_RESULT_CODE, Activity.RESULT_CANCELED)
            ?: return START_NOT_STICKY
        val data = if (Build.VERSION.SDK_INT >= 33) {
            intent.getParcelableExtra(EXTRA_RESULT_DATA, Intent::class.java)
        } else {
            @Suppress("DEPRECATION")
            intent.getParcelableExtra<Intent>(EXTRA_RESULT_DATA)
        } ?: return START_NOT_STICKY
        runCatching { startCapture(code, data) }
            .onFailure { FrameLoop.offerFailure(it.message ?: "projection failed", NativeMethod.PROJECTION) }
        return START_STICKY
    }

    override fun onDestroy() {
        stopCapture()
        super.onDestroy()
    }

    private fun startCapture(code: Int, data: Intent) {
        stopCapture()
        stopping.set(false)
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(
                NotificationChannel(NOTIFICATION_CHANNEL, "屏幕识别", NotificationManager.IMPORTANCE_LOW)
            )
        }
        val notification = Notification.Builder(this, NOTIFICATION_CHANNEL)
            .setContentTitle("替身计时器")
            .setContentText("正在识别模拟器画面")
            .setSmallIcon(com.narutotimer.R.drawable.ic_launcher)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }

        val mgr = getSystemService(MediaProjectionManager::class.java)
        val p = mgr.getMediaProjection(code, data)
        projection = p
        val metrics = getSystemService(WindowManager::class.java).maximumWindowMetrics
        width = CAPTURE_WIDTH
        height = (width.toLong() * metrics.bounds.height() / max(1, metrics.bounds.width())).toInt().coerceAtLeast(1)
        densityDpi = resources.displayMetrics.densityDpi
        val run = ++captureGeneration
        readerGeneration = 0L
        worker = HandlerThread("nt-capture-$run").also { it.start() }
        handler = Handler(worker!!.looper)
        callback = object : MediaProjection.Callback() {
            override fun onCapturedContentResize(newWidth: Int, newHeight: Int) {
                // Android 14+ single-app capture reports content changes here. Keep
                // this projection token and resize its existing VirtualDisplay.
                if (run != captureGeneration || stopping.get()) return
                resizeCapture(newWidth, newHeight)
            }

            override fun onStop() {
                if (run != captureGeneration || !stopping.compareAndSet(false, true)) return
                stopCapture()
            }
        }
        p.registerCallback(callback!!, handler)
        createReader(width, height, run)
        display = p.createVirtualDisplay(
            "NarutoTimer",
            width,
            height,
            densityDpi,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
            reader!!.surface,
            null,
            handler,
        )
        running = true
        FrameLoop.start(this)
        TimerAccessibilityService.instance?.stopScreenshotFallback()
    }

    private fun resizeCapture(newWidth: Int, newHeight: Int) {
        val p = projection ?: return
        val vd = display ?: return
        val h = handler ?: return
        val w = newWidth.coerceAtLeast(1)
        val hh = newHeight.coerceAtLeast(1)
        if (w == width && hh == height) return
        width = w
        height = hh
        val oldReader = reader
        val run = captureGeneration
        val readerRun = ++readerGeneration
        val nextReader = ImageReader.newInstance(w, hh, PixelFormat.RGBA_8888, MAX_IMAGES)
        nextReader.setOnImageAvailableListener({ source -> onImageAvailable(source, run, readerRun) }, h)
        // Resize/rebind the existing display; never request another projection token.
        vd.resize(w, hh, densityDpi)
        vd.setSurface(nextReader.surface)
        reader = nextReader
        runCatching { oldReader?.close() }
    }

    private fun createReader(w: Int, h: Int, run: Long) {
        val next = ImageReader.newInstance(w, h, PixelFormat.RGBA_8888, MAX_IMAGES)
        val captureHandler = handler ?: error("capture handler unavailable")
        val readerRun = ++readerGeneration
        next.setOnImageAvailableListener({ source -> onImageAvailable(source, run, readerRun) }, captureHandler)
        reader = next
    }

    private fun onImageAvailable(source: ImageReader, run: Long, readerRun: Long) {
        if (run != captureGeneration || readerRun != readerGeneration || stopping.get()) {
            drain(source)
            return
        }
        val image = try {
            source.acquireLatestImage()
        } catch (_: IllegalStateException) {
            // The producer may signal while a resize/close is in progress. Do not
            // retain an image or let the ImageReader callback kill the service.
            drain(source)
            return
        } ?: return
        var hb: android.hardware.HardwareBuffer? = null
        var handedOff = false
        try {
            hb = image.hardwareBuffer
            if (hb == null) return
            val timestamp = image.timestamp.takeIf { it > 0 } ?: System.nanoTime()
            // CapturedFrame owns both objects until the analysis thread has finished.
            val owned = hb
            FrameLoop.offer(CapturedFrame(owned, timestamp, NativeMethod.PROJECTION) {
                runCatching { owned.close() }
                runCatching { image.close() }
            })
            handedOff = true
        } finally {
            if (!handedOff) {
                runCatching { hb?.close() }
                runCatching { image.close() }
            }
        }
    }

    private fun drain(source: ImageReader) {
        // acquireLatestImage already releases older queued images. If one is
        // available during teardown, close it explicitly so maxImages is reclaimed.
        runCatching { source.acquireLatestImage()?.close() }
    }

    private fun stopCapture() {
        val hadCapture = projection != null || display != null || reader != null
        if (!stopping.compareAndSet(false, true) && !hadCapture) return
        running = false
        ++captureGeneration
        ++readerGeneration
        runCatching { display?.setSurface(null) }
        runCatching { display?.release() }
        display = null
        runCatching { reader?.close() }
        reader = null
        val p = projection
        val cb = callback
        callback = null
        projection = null
        if (cb != null) runCatching { p?.unregisterCallback(cb) }
        runCatching { p?.stop() }
        worker?.quitSafely()
        worker = null
        handler = null
        FrameLoop.stop()
        if (hadCapture) TimerAccessibilityService.instance?.startScreenshotFallback()
    }

    companion object {
        const val CAPTURE_WIDTH = 1280
        const val DEFAULT_CAPTURE_HEIGHT = 720
        private const val MAX_IMAGES = 3
        const val EXTRA_RESULT_CODE = "resultCode"
        const val EXTRA_RESULT_DATA = "resultData"
        const val ACTION_STOP = "com.narutotimer.action.STOP_PROJECTION"
        const val NOTIFICATION_CHANNEL = "capture"
        const val NOTIFICATION_ID = 1
        @Volatile var running = false
            private set

        fun start(context: Context, resultCode: Int, data: Intent) {
            val i = Intent(context, ProjectionCaptureService::class.java)
                .putExtra(EXTRA_RESULT_CODE, resultCode)
                .putExtra(EXTRA_RESULT_DATA, data)
            if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(i) else context.startService(i)
        }

        fun stop(context: Context) {
            context.startService(Intent(context, ProjectionCaptureService::class.java).setAction(ACTION_STOP))
        }
    }
}

private object NativeMethod { const val PROJECTION = 0 }
