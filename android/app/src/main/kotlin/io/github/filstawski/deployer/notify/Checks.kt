package io.github.filstawski.deployer.notify

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import io.github.filstawski.deployer.DeployerApplication
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.delay

/**
 * When the app checks servers in the background. Android runs periodic work
 * at most every 15 minutes, so after an action from the phone the app also
 * checks every 10 seconds until that action's operation is done.
 */
object Checks {
    private const val PERIODIC = "deployment-checks"
    private const val FOLLOW = "follow:"
    private val network = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    fun schedule(context: Context) {
        val request = PeriodicWorkRequestBuilder<CheckWorker>(15, TimeUnit.MINUTES).setConstraints(network).build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(PERIODIC, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    /** Checks often until [operationId] on [serverId] finishes, to tell how it ended. */
    fun follow(context: Context, serverId: String, operationId: String, policy: ExistingWorkPolicy = ExistingWorkPolicy.KEEP) {
        val request = OneTimeWorkRequestBuilder<CheckWorker>()
            .setInputData(workDataOf(CheckWorker.SERVER to serverId, CheckWorker.OPERATION to operationId))
            .setConstraints(network)
            .addTag(FOLLOW)
            .build()
        WorkManager.getInstance(context).enqueueUniqueWork(FOLLOW + operationId, policy, request)
    }

    fun stop(context: Context) {
        WorkManager.getInstance(context).cancelUniqueWork(PERIODIC)
        WorkManager.getInstance(context).cancelAllWorkByTag(FOLLOW)
    }
}

class CheckWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val monitor = (applicationContext as DeployerApplication).container.monitor
        val server = inputData.getString(SERVER)
        val operation = inputData.getString(OPERATION)
        if (server == null || operation == null) {
            monitor.checkAll()
            return Result.success()
        }
        // Background work may run for 10 minutes; stop short of that and continue in a new run.
        val until = System.currentTimeMillis() + TimeUnit.MINUTES.toMillis(9)
        while (true) {
            // Looked up before the check, so the check after it finishes still tells how it ended.
            val finished = monitor.finished(server, operation)
            monitor.checkAll()
            if (finished) return Result.success()
            if (System.currentTimeMillis() >= until) break
            delay(10_000)
        }
        Checks.follow(applicationContext, server, operation, ExistingWorkPolicy.APPEND_OR_REPLACE)
        return Result.success()
    }

    companion object {
        const val SERVER = "server"
        const val OPERATION = "operation"
    }
}
