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
import java.time.Instant
import io.github.filstawski.deployer.model.LifetimeOption
import io.github.filstawski.deployer.model.lifetimeOptions
import io.github.filstawski.deployer.model.nameProblem
import io.github.filstawski.deployer.model.suggestName
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

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
    val branch: String? = null,
    val name: String = "",
    /** Set once the name was typed, so choosing another branch keeps it. */
    val nameEdited: Boolean = false,
    val lifetime: LifetimeOption? = null,
    /** Create a new preview even though the branch already has one. */
    val createNew: Boolean = false,
    /** When the choices were last made, for telling expired previews apart. */
    val now: Instant = Instant.EPOCH,
    val submitting: Boolean = false,
    val error: ApiException? = null,
    /** The deployment that was started or updated: open it. */
    val opened: String? = null,
) {
    val lifetimes: List<LifetimeOption> get() = status?.let { lifetimeOptions(it.deployments) } ?: emptyList()

    /** Branches matching the search, plus what was typed when it names none of them. */
    val matches: List<String>
        get() {
            val q = query.trim()
            val names = branches.map { it.name }.filter { q.isEmpty() || it.contains(q, ignoreCase = true) }
            return if (q.isNotEmpty() && q !in names) names + q else names
        }

    /**
     * The chosen branch's preview that the CLI would offer to update instead.
     * Expired previews can't be updated, so they don't count.
     */
    val match: Deployment?
        get() = live.firstOrNull { d ->
            d.projectId == project?.id && d.ref == branch && d.state != State.Expired && d.expiresAt?.isAfter(now) != false
        }

    /** The preview to update, unless creating a new one was chosen. */
    val existing: Deployment? get() = if (createNew) null else match

    val nameError: String? get() = if (existing == null && name.isNotEmpty()) nameProblem(name) else null

    val canSubmit: Boolean
        get() = !submitting && project != null && branch != null && (existing != null || name.isNotEmpty() && nameError == null)
}

/**
 * Starts a deployment the way `deployer deploy` does: pick a project and
 * branch, take or change the suggested name, and choose a lifetime. A branch
 * that is already live is updated instead.
 */
class NewDeploymentViewModel(
    private val api: DeployerApi,
    private val clock: () -> Instant = Instant::now,
    /** Called with the operation once the server accepted the deployment, to follow it after the app is left. */
    private val onSubmitted: (operationId: String) -> Unit = {},
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
        _state.update { it.copy(project = project, branches = emptyList(), branch = null, query = "", branchError = null, loadingBranches = true) }
        branchesJob?.cancel()
        branchesJob = viewModelScope.launch {
            try {
                val branches = api.branches(project.id)
                _state.update { it.copy(branches = branches, loadingBranches = false) }
                branches.firstOrNull { it.default }?.let { selectBranch(it.name) }
            } catch (e: ApiException) {
                _state.update { it.copy(loadingBranches = false, branchError = e) }
            }
        }
    }

    fun setQuery(query: String) = _state.update { it.copy(query = query) }

    fun selectBranch(branch: String) = _state.update { s ->
        val project = s.project ?: return@update s
        val taken = s.live.map { it.name }.toSet()
        s.copy(
            branch = branch,
            name = if (s.nameEdited) s.name else suggestName(project.name, branch, taken),
            createNew = false,
            now = clock(),
            error = null,
        )
    }

    fun setCreateNew(createNew: Boolean) = _state.update { it.copy(createNew = createNew, error = null) }

    fun setName(name: String) = _state.update { it.copy(name = name.trim().lowercase(), nameEdited = true, error = null) }

    fun setLifetime(option: LifetimeOption) = _state.update { it.copy(lifetime = option) }

    fun submit() {
        val s = _state.value
        val project = s.project ?: return
        val branch = s.branch ?: return
        if (!s.canSubmit) return
        _state.update { it.copy(submitting = true, error = null) }
        viewModelScope.launch {
            try {
                val existing = s.existing
                val opened = if (existing != null) {
                    onSubmitted(api.update(existing.id).id)
                    existing.name
                } else {
                    val request = CreateDeploymentRequest(project.id, branch, s.name, s.lifetime?.duration?.inWholeSeconds)
                    val created = api.deploy(request)
                    onSubmitted(created.operation.id)
                    created.deployment.name
                }
                _state.update { it.copy(submitting = false, opened = opened) }
            } catch (e: ApiException) {
                _state.update { it.copy(submitting = false, error = e) }
            }
        }
    }
}
