package com.narutotimer.capture

import android.app.Activity
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
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
import android.util.Log
import android.view.WindowManager
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import com.narutotimer.R
import com.narutotimer.service.TimerAccessibilityService
import java.util.concurrent.atomic.AtomicBoolean

class ProjectionCaptureService : Service() {
    /**
     * Guards every field below. Start/stop run on the main thread (onStartCommand/onDestroy), the
     * MediaProjection callbacks and image callbacks on the capture HandlerThread (onStop → stop,
     * resize). Teardown therefore never interleaves with a resize or with an image being taken
     * from a reader (acquire → hardwareBuffer): the reader is only closed under this lock.
     * Lock order: [lock] → FrameLoop.imageLock / FrameLoop's own lock. Nothing here waits for
     * the capture thread, so holding it on main is short.
     */
    private val lock = Any()
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
            .onFailure {
                FrameLoop.offerFailure(it.message ?: "projection failed", NativeMethod.PROJECTION)
                stopCapture()
                finishService()
            }
        return START_NOT_STICKY
    }

    /** Without a projection there is nothing to show: drop the notification too. */
    private fun finishService() {
        mainHandler.post {
            runCatching { stopForeground(STOP_FOREGROUND_REMOVE) }
            stopSelf()
        }
    }

    override fun onDestroy() {
        stopCapture()
        super.onDestroy()
    }

    private fun startCapture(code: Int, data: Intent) {
        stopCapture()
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(NOTIFICATION_CHANNEL, getString(R.string.notification_channel_name), NotificationManager.IMPORTANCE_LOW)
                .apply { description = getString(R.string.notification_channel_description) }
        )
        val open = PendingIntent.getActivity(
            this, 0,
            Intent(this, com.narutotimer.ui.MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val stop = PendingIntent.getService(
            this, 1,
            Intent(this, ProjectionCaptureService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = Notification.Builder(this, NOTIFICATION_CHANNEL)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(getString(R.string.notification_text))
            .setSmallIcon(R.drawable.ic_launcher)
            .setContentIntent(open)
            .addAction(Notification.Action.Builder(null, getString(R.string.notification_stop), stop).build())
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }

        val mgr = getSystemService(MediaProjectionManager::class.java)
        val p = mgr.getMediaProjection(code, data) ?: error("projection token rejected")
        // Always landscape, long side 1280, even when started in portrait (CaptureSize, AND-01).
        val bounds = getSystemService(WindowManager::class.java).maximumWindowMetrics.bounds
        val (w, h) = CaptureSize.landscape(bounds.width(), bounds.height())
        synchronized(lock) {
            stopping.set(false)
            projection = p
            width = w
            height = h
            densityDpi = resources.displayMetrics.densityDpi
            val run = ++captureGeneration
            readerGeneration = 0L
            val thread = HandlerThread("nt-capture-$run").also { it.start() }
            worker = thread
            val captureHandler = Handler(thread.looper)
            handler = captureHandler
            val cb = object : MediaProjection.Callback() {
                override fun onCapturedContentResize(newWidth: Int, newHeight: Int) {
                    // Android 14+ reports the captured content's size here (right after the start
                    // and on every change). Keep this projection token and resize its existing
                    // VirtualDisplay.
                    resizeCapture(run, newWidth, newHeight)
                }

                override fun onStop() {
                    // "Stop sharing" from the system UI ends this token for good.
                    synchronized(lock) {
                        if (run != captureGeneration || stopping.get()) return
                        stopCapture()
                    }
                    finishService()
                }
            }
            callback = cb
            p.registerCallback(cb, captureHandler)
            val first = createReader(w, h, run, captureHandler)
            display = p.createVirtualDisplay(
                "NarutoTimer",
                w,
                h,
                densityDpi,
                DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
                first.surface,
                null,
                captureHandler,
            )
            running = true
        }
        Log.i(TAG, "capture ${w}x$h for screen ${bounds.width()}x${bounds.height()}")
        FrameLoop.start(this)
        TimerAccessibilityService.instance?.stopScreenshotFallback()
    }

    /** Capture thread (onCapturedContentResize). A failure keeps the current size instead of killing the process. */
    private fun resizeCapture(run: Long, newWidth: Int, newHeight: Int): Unit = synchronized(lock) {
        if (run != captureGeneration || stopping.get()) return
        val vd = display ?: return
        val h = handler ?: return
        val w = newWidth.coerceAtLeast(1)
        val hh = newHeight.coerceAtLeast(1)
        if (w == width && hh == height) return
        var nextReader: ImageReader? = null
        var resized = false
        try {
            val next = ImageReader.newInstance(w, hh, PixelFormat.RGBA_8888, MAX_IMAGES)
            nextReader = next
            val readerRun = readerGeneration + 1
            next.setOnImageAvailableListener({ source -> onImageAvailable(source, run, readerRun) }, h)
            // Resize/rebind the existing display; never request another projection token.
            vd.resize(w, hh, densityDpi)
            resized = true
            vd.setSurface(next.surface)
            // Images still queued in the old reader are drained (generation check) from now on.
            readerGeneration = readerRun
            val oldReader = reader
            reader = next
            nextReader = null
            width = w
            height = hh
            Log.i(TAG, "capture resized to ${w}x$hh")
            // Closing a reader closes its acquired images; never while a snapshot reads their planes.
            synchronized(FrameLoop.imageLock) { runCatching { oldReader?.close() } }
        } catch (e: RuntimeException) {
            Log.w(TAG, "capture resize to ${w}x$hh failed; keeping ${width}x$height", e)
            if (resized) runCatching { vd.resize(width, height, densityDpi) }
        } finally {
            nextReader?.let { runCatching { it.close() } }
        }
    }

    /** Caller holds [lock]. */
    private fun createReader(w: Int, h: Int, run: Long, captureHandler: Handler): ImageReader {
        val next = ImageReader.newInstance(w, h, PixelFormat.RGBA_8888, MAX_IMAGES)
        val readerRun = ++readerGeneration
        next.setOnImageAvailableListener({ source -> onImageAvailable(source, run, readerRun) }, captureHandler)
        reader = next
        return next
    }

    private fun onImageAvailable(source: ImageReader, run: Long, readerRun: Long): Unit = synchronized(lock) {
        // Under the lock no stop/resize can close [source] between acquire and hardwareBuffer.
        if (run != captureGeneration || readerRun != readerGeneration || stopping.get()) {
            drain(source)
            return
        }
        // Newest frame wins as with acquireLatestImage, but one image at a time: FrameLoop.offer
        // closes the frame it replaces before the next acquire. acquireLatestImage would probe for
        // a newer image while the analysis holds one and another is pending (= maxImages), which
        // logs "Unable to acquire a buffer item" for nearly every frame of a busy fight.
        while (true) {
            val image = try {
                source.acquireNextImage()
            } catch (_: IllegalStateException) {
                // The producer may signal while a resize/close is in progress. Do not
                // retain an image or let the ImageReader callback kill the service.
                drain(source)
                return
            } ?: return
            if (!offerImage(image)) return
        }
    }

    /** Hands [image] to FrameLoop; false if it could not be read (it is closed either way). */
    private fun offerImage(image: android.media.Image): Boolean {
        var hb: android.hardware.HardwareBuffer? = null
        var handedOff = false
        try {
            hb = image.hardwareBuffer
            if (hb == null) return true
            val timestamp = image.timestamp.takeIf { it > 0 } ?: System.nanoTime()
            // CapturedFrame owns both objects until the analysis thread has finished.
            val owned = hb
            FrameLoop.offer(CapturedFrame(owned, timestamp, NativeMethod.PROJECTION, image) {
                runCatching { owned.close() }
                runCatching { image.close() }
            })
            handedOff = true
            return true
        } catch (e: RuntimeException) {
            // An image invalidated under us (closed reader, dead buffer): drop it, keep the service.
            Log.w(TAG, "capture frame dropped", e)
            return false
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

    /** Main thread (start/destroy/failure) or capture thread (onStop); idempotent. */
    private fun stopCapture() {
        val hadCapture: Boolean
        synchronized(lock) {
            hadCapture = projection != null || display != null || reader != null
            if (!stopping.compareAndSet(false, true) && !hadCapture) return
            running = false
            ++captureGeneration
            ++readerGeneration
            val vd = display
            display = null
            runCatching { vd?.setSurface(null) }
            runCatching { vd?.release() }
            val r = reader
            reader = null
            synchronized(FrameLoop.imageLock) { runCatching { r?.close() } }
            val p = projection
            val cb = callback
            callback = null
            projection = null
            if (cb != null) runCatching { p?.unregisterCallback(cb) }
            runCatching { p?.stop() }
            worker?.quitSafely()
            worker = null
            handler = null
        }
        FrameLoop.stop()
        if (hadCapture) TimerAccessibilityService.instance?.startScreenshotFallback()
    }

    companion object {
        private const val TAG = "ProjectionCapture"
        const val CAPTURE_WIDTH = CaptureSize.LONG_SIDE
        const val DEFAULT_CAPTURE_HEIGHT = 720
        private const val MAX_IMAGES = 3
        const val EXTRA_RESULT_CODE = "resultCode"
        const val EXTRA_RESULT_DATA = "resultData"
        const val ACTION_STOP = "com.narutotimer.action.STOP_PROJECTION"
        const val NOTIFICATION_CHANNEL = "capture"
        const val NOTIFICATION_ID = 1
        private val mainHandler = Handler(android.os.Looper.getMainLooper())
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
