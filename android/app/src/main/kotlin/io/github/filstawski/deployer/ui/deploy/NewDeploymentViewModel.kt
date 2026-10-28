package io.github.filstawski.deployer.ui.deploy

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import io.github.filstawski.deployer.api.ApiException
import io.github.filstawski.deployer.api.Branch
import io.github.filstawski.deployer.api.CreateDeploymentRequest
import io.github.filstawski.deployer.api.DeployerApi
import io.github.filstawski.deployer.api.Deployment
import io.github.filstawski.deployer.api.Project
import io.github.filstawski.deployer.api.ServerStatus
import io.github.filstawski.deployer.api.State
import io.github.filstawski.deployer.model.LifetimeOption
import io.github.filstawski.deployer.model.lifetimeOptions
import io.github.filstawski.deployer.model.nameProblem
import io.github.filstawski.deployer.model.runEach
import io.github.filstawski.deployer.model.suggestName
import java.time.Instant
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** What deploying one chosen branch will do: create [name], or update an existing preview. */
data class Plan(val branch: String, val name: String, val update: Deployment?)

data class NewDeploymentState(
    val status: ServerStatus? = null,
    val projects: List<Project> = emptyList(),
    /** Live deployments: their names are taken, and their branches can be updated instead. */
    val live: List<Deployment> = emptyList(),
    val loaded: Boolean = false,
    val project: Project? = null,
    val branches: List<Branch> = emptyList(),
    val loadingBranches: Boolean = false,
    /** Listing branches failed; a branch name can still be typed. */
    val branchError: ApiException? = null,
    val query: String = "",
    /** The chosen branches, in the order chosen. */
    val selected: List<String> = emptyList(),
    /** Choosing several branches: a tap adds or removes one instead of choosing only it. */
    val picking: Boolean = false,
    /** The name of a single new preview, which can be edited. */
    val name: String = "",
    /** Set once the name was typed, so choosing another branch keeps it. */
    val nameEdited: Boolean = false,
    val lifetime: LifetimeOption? = null,
    /** Branches to deploy as new previews even though they already have one. */
    val createNewFor: Set<String> = emptySet(),
    /** When the choices were last made, for telling expired previews apart. */
    val now: Instant = Instant.EPOCH,
    val submitting: Boolean = false,
    /** Why deploying a single branch failed, or why the whole form failed to load. */
    val error: ApiException? = null,
    /** The branches that failed when several were deployed, with why. */
    val failures: List<Pair<String, ApiException>> = emptyList(),
    /** How many of those deployed together did start, when some failed. */
    val started: Int = 0,
    /** The deployment that was started or updated: open it. */
    val opened: String? = null,
    /** Several branches were started: go back to the list, with this message. */
    val finished: String? = null,
) {
    val lifetimes: List<LifetimeOption> get() = status?.let { lifetimeOptions(it.deployments) } ?: emptyList()

    /** The only chosen branch, as long as just one is chosen. */
    val branch: String? get() = selected.singleOrNull()

    /** Branches matching the search, plus what was typed when it names none of them. */
    val matches: List<String>
        get() {
            val q = query.trim()
            val names = branches.map { it.name }.filter { q.isEmpty() || it.contains(q, ignoreCase = true) }
            return if (q.isNotEmpty() && q !in names) names + q else names
        }

    /**
     * A branch's preview that the CLI would offer to update instead. Expired
     * previews can't be updated, so they don't count.
     */
    fun matchFor(branch: String): Deployment? = live.firstOrNull { d ->
        d.projectId == project?.id && d.ref == branch && d.state != State.Expired && d.expiresAt?.isAfter(now) != false
    }

    /** The single chosen branch's preview, if it has one. */
    val match: Deployment? get() = branch?.let(::matchFor)

    /** What each chosen branch will do. Names of new previews avoid live ones and each other. */
    val plans: List<Plan>
        get() {
            val project = project ?: return emptyList()
            val taken = live.map { it.name }.toMutableSet()
            return selected.map { branch ->
                val match = matchFor(branch)
                if (match != null && branch !in createNewFor) {
                    Plan(branch, match.name, match)
                } else {
                    val name = if (selected.size == 1) name else suggestName(project.name, branch, taken)
                    taken += name
                    Plan(branch, name, null)
                }
            }
        }

    /** The single chosen branch's preview to update, unless creating a new one was chosen. */
    val existing: Deployment? get() = plans.singleOrNull()?.update

    val nameError: String? get() = if (selected.size == 1 && existing == null && name.isNotEmpty()) nameProblem(name) else null

    /** How many new previews won't fit under the server's limit, if any. */
    val overLimit: Int
        get() {
            val limits = status?.deployments ?: return 0
            val new = plans.count { it.update == null }
            return if (limits.max > 0) (limits.live + new - limits.max).coerceAtLeast(0) else 0
        }

    val canSubmit: Boolean
        get() = !submitting && project != null && selected.isNotEmpty() &&
            (selected.size > 1 || existing != null || name.isNotEmpty() && nameError == null)
}

/**
 * Starts deployments the way `deployer deploy` does: pick a project and
 * branches, take or change the suggested name, and choose a lifetime. A
 * branch that already has a preview updates it instead, unless asked not to.
 */
class NewDeploymentViewModel(
    private val api: DeployerApi,
    private val clock: () -> Instant = Instant::now,
    /** Called with the operations once the server accepted them, to follow them after the app is left. */
    private val onSubmitted: (operationIds: List<String>) -> Unit = {},
) : ViewModel() {
    private val _state = MutableStateFlow(NewDeploymentState())
    val state: StateFlow<NewDeploymentState> = _state.asStateFlow()
    private var branchesJob: Job? = null

    init {
        load()
    }

    fun load() {
        viewModelScope.launch {
            try {
                coroutineScope {
                    val status = async { api.server() }
                    val projects = async { api.projects() }
                    val live = async { api.deployments() }
                    _state.update {
                        val s = status.await()
                        it.copy(
                            status = s,
                            projects = projects.await().sortedBy { p -> p.name },
                            live = live.await(),
                            loaded = true,
                            error = null,
                            lifetime = lifetimeOptions(s.deployments).firstOrNull { o -> o.isDefault },
                        )
                    }
                }
                _state.value.projects.firstOrNull()?.let(::selectProject)
            } catch (e: ApiException) {
                _state.update { it.copy(loaded = true, error = e) }
            }
        }
    }

    fun selectProject(project: Project) {
        _state.update {
            it.copy(
                project = project, branches = emptyList(), selected = emptyList(), picking = false, createNewFor = emptySet(),
                query = "", branchError = null, failures = emptyList(), loadingBranches = true,
            )
        }
        branchesJob?.cancel()
        branchesJob = viewModelScope.launch {
            try {
                val branches = api.branches(project.id)
                _state.update { s ->
                    val loaded = s.copy(branches = branches, loadingBranches = false)
                    // Start from the default branch, unless something was chosen while the list loaded.
                    val default = branches.firstOrNull { it.default }
                    if (default != null && s.selected.isEmpty()) choose(loaded, listOf(default.name)) else loaded
                }
            } catch (e: ApiException) {
                _state.update { it.copy(loadingBranches = false, branchError = e) }
            }
        }
    }

    fun setQuery(query: String) = _state.update { it.copy(query = query) }

    /** A tap: chooses only [branch], or while picking several, adds or removes it. */
    fun selectBranch(branch: String) = _state.update { s ->
        when {
            s.picking -> choose(s, if (branch in s.selected) s.selected - branch else s.selected + branch)
            else -> choose(s, listOf(branch)).copy(createNewFor = emptySet())
        }
    }

    /** A long press: starts choosing several branches, beginning with [branch]. */
    fun pick(branch: String) = _state.update { s ->
        choose(s.copy(picking = true), if (branch in s.selected) s.selected else s.selected + branch)
    }

    fun setCreateNew(createNew: Boolean) = _state.value.branch?.let { setCreateNew(it, createNew) }

    fun setCreateNew(branch: String, createNew: Boolean) = _state.update {
        it.copy(createNewFor = if (createNew) it.createNewFor + branch else it.createNewFor - branch, error = null)
    }

    fun setName(name: String) = _state.update { it.copy(name = name.trim().lowercase(), nameEdited = true, error = null) }

    fun setLifetime(option: LifetimeOption) = _state.update { it.copy(lifetime = option) }

    private fun choose(s: NewDeploymentState, selected: List<String>): NewDeploymentState {
        val project = s.project ?: return s
        val only = selected.singleOrNull()
        val name = if (only != null && !s.nameEdited) suggestName(project.name, only, s.live.map { it.name }.toSet()) else s.name
        return s.copy(selected = selected, name = name, now = clock(), error = null, failures = emptyList())
    }

    fun submit() {
        val s = _state.value
        val project = s.project ?: return
        if (!s.canSubmit) return
        val plans = s.plans
        val ttl = s.lifetime?.duration?.inWholeSeconds
        _state.update { it.copy(submitting = true, error = null, failures = emptyList()) }
        viewModelScope.launch {
            // Each result is the operation and the deployment's name, as the server gave it. Leaving
            // the screen mustn't cut this short: what the server accepted still has to be followed.
            val outcomes = withContext(NonCancellable) {
                runEach(plans, parallel = 3) { plan ->
                    if (plan.update != null) {
                        api.update(plan.update.id) to plan.update.name
                    } else {
                        val created = api.deploy(CreateDeploymentRequest(project.id, plan.branch, plan.name, ttl))
                        created.operation to created.deployment.name
                    }
                }.also { outcomes ->
                    val started = outcomes.mapNotNull { it.result?.first?.id }
                    if (started.isNotEmpty()) onSubmitted(started)
                }
            }
            val failed = outcomes.filter { it.error != null }
            when {
                failed.isEmpty() && plans.size == 1 -> _state.update { it.copy(submitting = false, opened = outcomes.single().result!!.second) }
                failed.isEmpty() -> _state.update { it.copy(submitting = false, finished = startedMessage(plans)) }
                plans.size == 1 -> _state.update { it.copy(submitting = false, error = failed.single().error) }
                // The project changed meanwhile; its choices are the person's own now.
                _state.value.project?.id != project.id -> _state.update { it.copy(submitting = false) }
                else -> {
                    // Keep the failed branches chosen, to change something and try them again.
                    val live = try {
                        api.deployments()
                    } catch (_: ApiException) {
                        null
                    }
                    _state.update {
                        val retry = it.copy(live = live ?: it.live, picking = true)
                        choose(retry, failed.map { o -> o.item.branch }).copy(
                            submitting = false,
                            failures = failed.map { o -> o.item.branch to o.error!! },
                            started = outcomes.size - failed.size,
                        )
                    }
                }
            }
        }
    }

    private fun startedMessage(plans: List<Plan>): String {
        val new = plans.count { it.update == null }
        val updates = plans.size - new
        val branches = if (new == 1) "1 branch" else "$new branches"
        val previews = if (updates == 1) "1 preview" else "$updates previews"
        return when {
            updates == 0 -> "Deploying $branches"
            new == 0 -> "Updating $previews"
            else -> "Deploying $branches and updating $previews"
        }
    }
}
