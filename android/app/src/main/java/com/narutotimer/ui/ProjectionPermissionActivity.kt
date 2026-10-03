package com.narutotimer.ui

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.media.projection.MediaProjectionManager
import android.os.Bundle
import com.narutotimer.capture.ProjectionCaptureService

/** Transparent one-shot trampoline for the system projection consent dialog. */
class ProjectionPermissionActivity : Activity() {
    private var requestStarted = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (savedInstanceState != null) {
            finish()
            return
        }
        val mgr = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
        requestStarted = true
        startActivityForResult(mgr.createScreenCaptureIntent(), REQUEST)
    }

    @Deprecated("Activity result API without androidx")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQUEST || !requestStarted) return
        requestStarted = false
        if (resultCode == RESULT_OK && data != null) {
            ProjectionCaptureService.start(this, resultCode, data)
        }
        finish()
    }

    companion object {
        private const val REQUEST = 71

        fun launch(context: Context) {
            context.startActivity(
                Intent(context, ProjectionPermissionActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_HISTORY)
            )
        }
    }
}
