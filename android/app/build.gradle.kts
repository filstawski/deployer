plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// Release builds pass -PdeployerVersion=v1.2.3. The version code is derived
// from it, so each release installs over the previous one.
val deployerVersion = (findProperty("deployerVersion") as String?)?.removePrefix("v") ?: "0.0.0-dev"
val deployerVersionCode = Regex("""^(\d+)\.(\d+)\.(\d+)""").find(deployerVersion)?.destructured
    ?.let { (major, minor, patch) -> major.toInt() * 1_000_000 + minor.toInt() * 1_000 + patch.toInt() }
    ?.takeIf { it > 0 }
    ?: 1

// Published releases are signed with the key these variables name; see
// docs/development.md. Other builds use this machine's debug key.
val releaseKeystore = providers.environmentVariable("DEPLOYER_ANDROID_KEYSTORE").orNull

android {
    namespace = "io.github.filstawski.deployer"
    compileSdk = 37

    defaultConfig {
        applicationId = "io.github.filstawski.deployer"
        minSdk = 26
        // Android 16. Raise it after testing on the newer release.
        targetSdk = 36
        versionName = deployerVersion
        versionCode = deployerVersionCode
    }

    signingConfigs {
        if (releaseKeystore != null) {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = providers.environmentVariable("DEPLOYER_ANDROID_KEYSTORE_PASSWORD").get()
                keyAlias = providers.environmentVariable("DEPLOYER_ANDROID_KEY_ALIAS").get()
                keyPassword = providers.environmentVariable("DEPLOYER_ANDROID_KEY_PASSWORD").get()
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.getByName(if (releaseKeystore != null) "release" else "debug")
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    lint {
        // The target is raised deliberately, after testing on a new release.
        disable += "OldTargetApi"
        // Wants fullBackupContent for Android 11 and earlier, which
        // allowBackup="false" already covers.
        disable += "DataExtractionRules"
    }

    sourceSets {
        // The API's JSON, written by internal/domain/wire_test.go.
        getByName("test").resources.directories.add("../../internal/domain/testdata")
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(platform(libs.androidx.compose.bom))
    implementation(libs.androidx.compose.ui)
    implementation(libs.androidx.compose.ui.tooling.preview)
    implementation(libs.androidx.compose.material3)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.okhttp)
    implementation(libs.androidx.camera.camera2)
    implementation(libs.androidx.camera.lifecycle)
    implementation(libs.androidx.camera.view)
    implementation(libs.zxing.core)
    implementation(libs.androidx.work.runtime)
    implementation(libs.androidx.glance.appwidget)
    debugImplementation(libs.androidx.compose.ui.tooling)

    testImplementation(libs.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.okhttp.mockwebserver)
}
