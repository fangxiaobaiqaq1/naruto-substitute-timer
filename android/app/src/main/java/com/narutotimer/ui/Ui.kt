package com.narutotimer.ui

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import kotlin.math.roundToInt

/**
 * Programmatic widget kit shared by MainActivity and the in-game settings
 * panel, so both screens use one palette, one button style and one spacing.
 */
object Ui {
    const val BG = 0xFF071219.toInt()
    const val SURFACE = 0xFF0D1E29.toInt()
    const val SURFACE_2 = 0xFF15303F.toInt()
    const val OUTLINE = 0xFF24485A.toInt()
    const val TEXT = 0xFFE6F4F8.toInt()
    const val TEXT_2 = 0xFF95B2BE.toInt()
    const val ACCENT = 0xFF5CE1FF.toInt()
    const val ON_ACCENT = 0xFF03222C.toInt()
    const val OK = 0xFF6EE7A0.toInt()
    const val WARN = 0xFFFFC266.toInt()
    const val BAD = 0xFFFF8A80.toInt()

    enum class Kind { PRIMARY, TONAL, DANGER, QUIET }

    fun dp(c: Context, v: Float): Int = (v * c.resources.displayMetrics.density).roundToInt()

    fun text(c: Context, s: CharSequence, sp: Float = 14f, color: Int = TEXT, bold: Boolean = false): TextView =
        TextView(c).apply {
            text = s
            textSize = sp
            setTextColor(color)
            if (bold) typeface = Typeface.create(Typeface.DEFAULT, 600, false)
            setLineSpacing(0f, 1.12f)
        }

    fun title(c: Context, s: String): TextView = text(c, s, 16f, TEXT, bold = true)

    fun note(c: Context, s: String): TextView = text(c, s, 12.5f, TEXT_2)

    fun rounded(c: Context, color: Int, radiusDp: Float, strokeColor: Int = 0): GradientDrawable =
        GradientDrawable().apply {
            setColor(color)
            cornerRadius = dp(c, radiusDp).toFloat()
            if (strokeColor != 0) setStroke(dp(c, 1f), strokeColor)
        }

    /** A vertical surface with consistent padding. */
    fun card(c: Context): LinearLayout = LinearLayout(c).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(c, 16f), dp(c, 14f), dp(c, 16f), dp(c, 16f))
        background = rounded(c, SURFACE, 16f, OUTLINE)
    }

    fun button(c: Context, label: String, kind: Kind = Kind.TONAL, onClick: () -> Unit): Button =
        UiButton(c).apply {
            text = label
            isAllCaps = false
            textSize = 14f
            typeface = Typeface.create(Typeface.DEFAULT, 500, false)
            stateListAnimator = null
            elevation = 0f
            minWidth = 0
            minimumWidth = 0
            minHeight = dp(c, 44f)
            minimumHeight = dp(c, 44f)
            gravity = Gravity.CENTER
            setPadding(dp(c, 14f), 0, dp(c, 14f), 0)
            style(this, kind)
            setOnClickListener { onClick() }
        }

    /** (Re)applies a button kind's colours. */
    fun style(b: Button, kind: Kind) {
        val (bg, fg) = when (kind) {
            Kind.PRIMARY -> ACCENT to ON_ACCENT
            Kind.TONAL -> SURFACE_2 to TEXT
            Kind.DANGER -> SURFACE_2 to BAD
            Kind.QUIET -> 0 to ACCENT
        }
        b.setTextColor(fg)
        b.background = RippleDrawable(
            ColorStateList.valueOf(if (kind == Kind.PRIMARY) 0x33000000 else 0x33FFFFFF),
            rounded(b.context, bg, 12f),
            rounded(b.context, 0xFFFFFFFF.toInt(), 12f),
        )
    }

    /** Horizontal row; children share the width equally. */
    fun row(c: Context, vararg views: View, gapDp: Float = 8f): LinearLayout = LinearLayout(c).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        views.forEachIndexed { i, v ->
            addView(v, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                if (i > 0) marginStart = dp(c, gapDp)
            })
        }
    }

    fun add(parent: ViewGroup, child: View, topDp: Float = 8f) {
        parent.addView(child, LinearLayout.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.WRAP_CONTENT,
        ).apply { topMargin = dp(parent.context, topDp) })
    }

    /** Small filled circle used as a status indicator. */
    fun dot(c: Context): View = View(c).apply {
        background = GradientDrawable().apply { shape = GradientDrawable.OVAL; setColor(TEXT_2) }
    }

    fun setDot(dot: View, color: Int) {
        (dot.background as? GradientDrawable)?.setColor(color)
    }

    /** Buttons dim when disabled instead of looking clickable. */
    private class UiButton(c: Context) : Button(c) {
        override fun setEnabled(enabled: Boolean) {
            super.setEnabled(enabled)
            alpha = if (enabled) 1f else 0.4f
        }
    }
}
