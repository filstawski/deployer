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
 * checks every 10 seconds until the operations it started are done.
 */
object Checks {
    private const val PERIODIC = "deployment-checks"
    private const val FOLLOW = "follow:"
    private val network = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    fun schedule(context: Context) {
        val request = PeriodicWorkRequestBuilder<CheckWorker>(15, TimeUnit.MINUTES).setConstraints(network).build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(PERIODIC, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    /**
     * Checks often until [operationIds] on [serverId] finish, to tell how
     * they ended. One worker follows a whole batch.
     */
    fun follow(context: Context, serverId: String, operationIds: List<String>, policy: ExistingWorkPolicy = ExistingWorkPolicy.KEEP) {
        if (operationIds.isEmpty()) return
        val request = OneTimeWorkRequestBuilder<CheckWorker>()
            .setInputData(workDataOf(CheckWorker.SERVER to serverId, CheckWorker.OPERATIONS to operationIds.toTypedArray()))
            .setConstraints(network)
            .addTag(FOLLOW)
            .build()
        // Named after the whole batch: a batch sharing its first operation with another,
        // as a destroy reuses a pending one, mustn't be dropped as a duplicate.
        val name = FOLLOW + operationIds.sorted().joinToString(",").hashCode().toUInt().toString(16)
        WorkManager.getInstance(context).enqueueUniqueWork(name, policy, request)
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
        // Version 0.6.0 queued a single operation under its own key.
        var operations = inputData.getNullableStringArray(OPERATIONS)?.filterNotNull() ?: listOfNotNull(inputData.getString("operation"))
        if (server == null || operations.isEmpty()) {
            monitor.checkAll()
            return Result.success()
        }
        // Background work may run for 10 minutes; stop short of that and continue in a new run.
        val until = System.currentTimeMillis() + TimeUnit.MINUTES.toMillis(9)
        while (true) {
            // Looked up before the check, so the check after the last one finishes still tells how it ended.
            operations = operations.filterNot { monitor.finished(server, it) }
            monitor.checkAll()
            if (operations.isEmpty()) return Result.success()
            if (System.currentTimeMillis() >= until) break
            delay(10_000)
        }
        Checks.follow(applicationContext, server, operations, ExistingWorkPolicy.APPEND_OR_REPLACE)
        return Result.success()
    }

    companion object {
        const val SERVER = "server"
        const val OPERATIONS = "operations"
    }
}
