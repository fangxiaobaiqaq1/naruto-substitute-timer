import com.android.build.api.variant.ApplicationAndroidComponentsExtension
import org.gradle.api.file.DirectoryProperty
import org.gradle.api.file.FileSystemOperations
import javax.inject.Inject

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val releaseVersionName = providers.environmentVariable("TIMER_VERSION").orNull
    ?.trim()
    ?.removePrefix("v")
    ?.takeIf { it.matches(Regex("\\d+\\.\\d+\\.\\d+")) }
    ?: "0.2.10"
val releaseKeystorePath = providers.environmentVariable("ANDROID_KEYSTORE_PATH").orNull
val releaseKeystorePassword = providers.environmentVariable("ANDROID_KEYSTORE_PASSWORD").orNull
val releaseKeyAlias = providers.environmentVariable("ANDROID_KEY_ALIAS").orNull
val releaseKeyPassword = providers.environmentVariable("ANDROID_KEY_PASSWORD").orNull
val hasReleaseSigning = listOf(
    releaseKeystorePath,
    releaseKeystorePassword,
    releaseKeyAlias,
    releaseKeyPassword,
).all { !it.isNullOrBlank() }
// Release signing has exactly two modes and no debug-key fallback (a debug-signed "release"
// can neither update nor be updated by the official APKs):
//  - local: all four ANDROID_* variables above are set and Gradle signs the APK;
//  - CI: -Ptimer.unsignedRelease=true builds app-release-unsigned.apk, which the release
//    workflow signs with apksigner in a separate job, so the keystore never meets Gradle
//    plugins or dependencies.
// Anything else fails before the release variant is built (checkReleaseSigning below).
val unsignedReleaseRequested = providers.gradleProperty("timer.unsignedRelease").orNull
    ?.trim()
    ?.equals("true", ignoreCase = true) == true

android {
    namespace = "com.narutotimer"
    compileSdk = 35
    ndkVersion = "27.2.12479018"

    defaultConfig {
        applicationId = "com.narutotimer"
        minSdk = 31
        targetSdk = 34
        versionCode = 30
        versionName = releaseVersionName

        ndk {
            abiFilters += listOf("x86_64", "arm64-v8a")
        }
        externalNativeBuild {
            cmake {
                // 识别核心必须与 Go 版逐位一致：不开 fast-math，不融合乘加。
                cppFlags += listOf("-std=c++17", "-fexceptions", "-frtti")
                arguments += listOf("-DANDROID_STL=c++_static")
            }
        }
    }

    buildFeatures {
        buildConfig = true
    }

    signingConfigs {
        if (hasReleaseSigning) {
            create("release") {
                storeFile = file(releaseKeystorePath!!)
                storePassword = releaseKeystorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (hasReleaseSigning && !unsignedReleaseRequested) {
                signingConfig = signingConfigs.getByName("release")
            }
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
        }
        debug {
            // 识别循环在 debug 包里也要跑满速，native 一律 -O3（见 CMakeLists.txt）。
            isJniDebuggable = true
        }
    }

    externalNativeBuild {
        cmake {
            path = file("src/main/cpp/CMakeLists.txt")
            version = "3.22.1"
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    androidResources {
        // PNG 已经压缩；json 很小。避免 aapt 对识别模板做任何再处理。
        noCompress += listOf("png")
    }

    packaging {
        jniLibs { useLegacyPackaging = false }
    }
}

kotlin {
    jvmToolchain(17)
}

// 把仓库根目录的识别资源同步进 APK assets（生成目录，不污染 src/main/assets）。
//
// 映射（APK assets 根目录下的相对路径即 native AssetStore 的 key）：
//   ../assets/templates/{*.png,manifest.json} -> templates/
//   ../assets/avatars/{*.png,index.json}      -> avatars/
//   ../assets/game/*.json                     -> game/
//   ../internal/ninja/templates/*.png         -> ninja_templates/   (排除 *.source.png 原图)
// 注意：这里不能用 /** */ 块注释——Kotlin 块注释可嵌套，路径里的 "/*" 会吞掉后面整个脚本。
abstract class SyncRecognitionAssets : DefaultTask() {
    @get:InputDirectory
    @get:PathSensitive(PathSensitivity.RELATIVE)
    abstract val repoAssets: DirectoryProperty

    @get:InputDirectory
    @get:PathSensitive(PathSensitivity.RELATIVE)
    abstract val ninjaTemplates: DirectoryProperty

    @get:OutputDirectory
    abstract val outputDir: DirectoryProperty

    @get:Inject
    abstract val fs: FileSystemOperations

    @TaskAction
    fun sync() {
        val assets = repoAssets.get().asFile
        val ninja = ninjaTemplates.get().asFile
        fs.sync {
            into(outputDir)
            from(assets.resolve("templates")) {
                include("*.png", "manifest.json")
                into("templates")
            }
            from(assets.resolve("avatars")) {
                include("*.png", "index.json")
                into("avatars")
            }
            from(assets.resolve("game")) {
                include("*.json")
                into("game")
            }
            from(ninja) {
                include("*.png")
                exclude("*.source.png")
                into("ninja_templates")
            }
        }
    }
}

val checkReleaseSigning = tasks.register("checkReleaseSigning") {
    // Plain values only, so the task stays configuration-cache friendly.
    val signed = hasReleaseSigning
    val unsignedAllowed = unsignedReleaseRequested
    doLast {
        if (!signed && !unsignedAllowed) {
            throw GradleException(
                "Release signing is not configured. Set ANDROID_KEYSTORE_PATH, ANDROID_KEYSTORE_PASSWORD, " +
                    "ANDROID_KEY_ALIAS and ANDROID_KEY_PASSWORD, or pass -Ptimer.unsignedRelease=true to build " +
                    "an unsigned APK and sign it with apksigner. Release builds are never signed with the debug key.",
            )
        }
    }
}
tasks.matching { it.name == "preReleaseBuild" }.configureEach { dependsOn(checkReleaseSigning) }

val syncRecognitionAssets = tasks.register<SyncRecognitionAssets>("syncRecognitionAssets") {
    repoAssets.set(rootProject.layout.projectDirectory.dir("../assets"))
    ninjaTemplates.set(rootProject.layout.projectDirectory.dir("../internal/ninja/templates"))
}

extensions.getByType(ApplicationAndroidComponentsExtension::class.java).onVariants { variant ->
    variant.sources.assets?.addGeneratedSourceDirectory(syncRecognitionAssets, SyncRecognitionAssets::outputDir)
}
