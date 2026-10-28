package io.github.filstawski.deployer

import android.app.Activity
import android.app.Application
import android.os.Bundle

class DeployerApplication : Application() {
    lateinit var container: AppContainer
        private set

    /** Activities on screen; while any is, the app shows changes itself instead of notifying. */
    private var started = 0

    override fun onCreate() {
        super.onCreate()
        container = AppContainer(this, inForeground = { started > 0 })
        container.notifier.createChannels()
        if (container.connections.connections.value.isNotEmpty()) container.serversChanged()
        registerActivityLifecycleCallbacks(object : ActivityLifecycleCallbacks {
            override fun onActivityStarted(activity: Activity) {
                started++
            }

            override fun onActivityStopped(activity: Activity) {
                started--
            }

            override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) = Unit
            override fun onActivityResumed(activity: Activity) = Unit
            override fun onActivityPaused(activity: Activity) = Unit
            override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) = Unit
            override fun onActivityDestroyed(activity: Activity) = Unit
        })
    }
}
