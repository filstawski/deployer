package controller

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/container"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/fakes"
)

func (h *harness) update(name string, req domain.UpdateDeploymentRequest) domain.Operation {
	h.t.Helper()
	op, err := h.ctrl.Update(context.Background(), "tok_1", name, "", req)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.wait(op.ID)
}

func (h *harness) running(name string) domain.Deployment {
	h.t.Helper()
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "design/a", Name: name, TTLSeconds: 3600})
	if op := h.wait(resp.Operation.ID); op.Status != domain.OpSucceeded {
		h.t.Fatalf("deploy: %+v", op.Error)
	}
	return h.get(name)
}

func updateResult(t *testing.T, op domain.Operation) domain.UpdateResult {
	t.Helper()
	if op.Status != domain.OpSucceeded {
		t.Fatalf("update: %s %+v", op.Status, op.Error)
	}
	var r domain.UpdateResult
	if err := json.Unmarshal(op.Result, &r); err != nil {
		t.Fatalf("update result %s: %v", op.Result, err)
	}
	return r
}

func TestUpdateRunningDeployment(t *testing.T) {
	h := newHarness(t)
	before := h.running("design-a")
	h.advance(10 * time.Minute)
	commit := h.source.Push("design/a")

	r := updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{}))
	if r.PreviousCommit != before.Commit || r.Commit != commit || !r.Changed {
		t.Fatalf("result: %+v", r)
	}
	d := h.get("design-a")
	if d.ID != before.ID || d.State != domain.StateRunning || d.Commit != commit || d.Image == before.Image || d.Error != nil {
		t.Fatalf("updated deployment: %+v", d)
	}
	if !d.ExpiresAt.Equal(*before.ExpiresAt) || !d.ReadyAt.Equal(*before.ReadyAt) || d.URL != before.URL {
		t.Fatalf("update changed the lifetime or URL: %+v", d)
	}
	if env := h.runtime.Env(d.ID); env["DEPLOYER_COMMIT"] != commit || h.runtime.Image(d.ID) != d.Image {
		t.Fatalf("container not replaced: %v %s", env, h.runtime.Image(d.ID))
	}
	if images := h.runtime.Images(d.ID); !slices.Equal(images, []string{d.Image}) {
		t.Fatalf("previous image not removed: %v", images)
	}

	// Without new commits, nothing is rebuilt.
	builds := h.runtime.Builds()
	r = updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{}))
	if r.Changed || r.Commit != commit || h.runtime.Builds() != builds || h.get("design-a").Image != d.Image {
		t.Fatalf("unchanged update rebuilt: %+v", r)
	}

	// A new ref moves the deployment to another branch.
	r = updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{Ref: "design/b"}))
	if d = h.get("design-a"); d.Ref != "design/b" || d.Commit != fakes.Commit("design/b") || !r.Changed || h.runtime.Env(d.ID)["DEPLOYER_REF"] != "design/b" {
		t.Fatalf("moved to design/b: %+v %+v", r, d)
	}
}

func TestUpdateFailureKeepsPreviousVersion(t *testing.T) {
	h := newHarness(t)
	before := h.running("design-a")
	broken := h.source.Push("design/a")
	h.runtime.Set(func(r *fakes.Runtime) {
		r.StartErr = func(spec container.RunSpec) error {
			if spec.Env["DEPLOYER_COMMIT"] == broken {
				return domain.Errorf(domain.CodeStartFailed, "could not start the container: boom")
			}
			return nil
		}
	})

	op := h.update("design-a", domain.UpdateDeploymentRequest{})
	if op.Status != domain.OpFailed || op.Error.Code != domain.CodeStartFailed || !strings.Contains(op.Error.Message, "design-a still runs "+before.Commit[:12]) ||
		!strings.Contains(op.Error.Hint, "deployer logs design-a --build") {
		t.Fatalf("failed update: %+v", op.Error)
	}
	d := h.get("design-a")
	if d.State != domain.StateRunning || d.Commit != before.Commit || d.Image != before.Image || d.Error != nil || h.runtime.Image(d.ID) != before.Image || !h.runtime.IsRunning(d.ID) {
		t.Fatalf("previous version not restored: %+v", d)
	}
	if images := h.runtime.Images(d.ID); !slices.Equal(images, []string{before.Image}) {
		t.Fatalf("failed image kept: %v", images)
	}
	logs, _ := h.ctrl.Logs(context.Background(), d, LogsRequest{Source: LogsBuild, Offset: 0})
	if !strings.Contains(logs.Logs, "Output of "+broken[:12]) || !strings.Contains(logs.Logs, "listening on") {
		t.Fatalf("the failed version's output is not in the build log:\n%s", logs.Logs)
	}

	// A failed build leaves the running version alone too.
	h.source.Push("design/a")
	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = domain.Errorf(domain.CodeBuildFailed, "the image build failed") })
	op = h.update("design-a", domain.UpdateDeploymentRequest{})
	if op.Error.Code != domain.CodeBuildFailed || !strings.Contains(op.Error.Message, "still runs") || !strings.Contains(op.Error.Hint, "--build") {
		t.Fatalf("failed build: %+v", op.Error)
	}
	if d := h.get("design-a"); d.State != domain.StateRunning || d.Commit != before.Commit || d.Error != nil {
		t.Fatalf("after failed build: %+v", d)
	}
	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = nil })
	if op := h.update("design-a", domain.UpdateDeploymentRequest{Ref: "missing"}); op.Error.Code != domain.CodeSourceFailed {
		t.Fatalf("missing ref: %+v", op.Error)
	}
	if d := h.get("design-a"); d.State != domain.StateRunning || d.Ref != "design/a" {
		t.Fatalf("after a missing ref: %+v", d)
	}
}

func TestUpdateStoppedAndFailedDeployments(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	d := h.running("design-a")
	stop, _ := h.ctrl.Stop(ctx, "tok_1", d.ID, "")
	h.wait(stop.ID)
	commit := h.source.Push("design/a")
	updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{}))
	if d = h.get("design-a"); d.State != domain.StateRunning || d.Commit != commit || !h.runtime.IsRunning(d.ID) {
		t.Fatalf("updated stopped deployment: %+v", d)
	}

	// A preview whose first build failed gets its lifetime once it works.
	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = domain.Errorf(domain.CodeBuildFailed, "the image build failed") })
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "fix/me", Name: "broken", TTLSeconds: 3600})
	h.wait(resp.Operation.ID)
	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = nil })
	h.advance(time.Hour)
	updateResult(t, h.update("broken", domain.UpdateDeploymentRequest{}))
	if d = h.get("broken"); d.State != domain.StateRunning || d.Error != nil || !d.ExpiresAt.Equal(h.now.Add(time.Hour)) {
		t.Fatalf("updated failed deployment: %+v", d)
	}

	// A failed update of a stopped deployment leaves it failed, like a deploy.
	stop, _ = h.ctrl.Stop(ctx, "tok_1", "broken", "")
	h.wait(stop.ID)
	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = domain.Errorf(domain.CodeBuildFailed, "the image build failed") })
	if op := h.update("broken", domain.UpdateDeploymentRequest{}); op.Error.Code != domain.CodeBuildFailed || strings.Contains(op.Error.Message, "still runs") {
		t.Fatalf("failed update of a stopped deployment: %+v", op.Error)
	}
	if d = h.get("broken"); d.State != domain.StateFailed || d.Error.Code != domain.CodeBuildFailed {
		t.Fatalf("after a failed update: %+v", d)
	}
}

func TestUpdateRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	d := h.running("design-a")

	if _, err := h.ctrl.Update(ctx, "tok_1", d.ID, "", domain.UpdateDeploymentRequest{Ref: "-bad"}); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("invalid ref: %v", err)
	}

	// The same key and request return the same operation.
	h.source.Push("design/a")
	h.runtime.Set(func(r *fakes.Runtime) { r.BlockBuild = d.ID })
	first, err := h.ctrl.Update(ctx, "tok_1", d.ID, "key-1", domain.UpdateDeploymentRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := h.ctrl.Update(ctx, "tok_1", d.ID, "key-1", domain.UpdateDeploymentRequest{}); err != nil || again.ID != first.ID {
		t.Fatalf("retry: %v %s", err, again.ID)
	}
	if _, err := h.ctrl.Update(ctx, "tok_1", d.ID, "key-1", domain.UpdateDeploymentRequest{Ref: "design/b"}); domain.CodeOf(err) != domain.CodeIdempotencyConflict {
		t.Fatalf("reused key: %v", err)
	}
	if _, err := h.ctrl.Update(ctx, "tok_1", d.ID, "", domain.UpdateDeploymentRequest{}); domain.CodeOf(err) != domain.CodeBusy {
		t.Fatalf("second update: %v", err)
	}
	// While it builds, the previous version keeps serving.
	time.Sleep(50 * time.Millisecond)
	if d := h.get(d.ID); d.State != domain.StateRunning || !h.runtime.IsRunning(d.ID) {
		t.Fatalf("not serving during the build: %+v", d)
	}
	if r, _ := h.router.Route(d.Hostname); r.State != domain.StateRunning || r.Upstream == "" {
		t.Fatalf("route during the build: %+v", r)
	}

	// Destroy cancels the update.
	destroy, err := h.ctrl.Destroy(ctx, "tok_1", d.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if op := h.wait(first.ID); op.Status != domain.OpCancelled {
		t.Fatalf("update not cancelled: %s", op.Status)
	}
	h.wait(destroy.ID)
	if h.runtime.HasImage(d.ID) {
		t.Fatal("images remain after destroy")
	}

	h.runtime.Set(func(r *fakes.Runtime) { r.BlockBuild = "" })
	d = h.running("design-b")
	h.advance(2 * time.Hour)
	if _, err := h.ctrl.Update(ctx, "tok_1", d.ID, "", domain.UpdateDeploymentRequest{}); domain.CodeOf(err) != domain.CodeInvalidState {
		t.Fatalf("update of an expired deployment: %v", err)
	}
}

// plannedUpdate builds the next commit of a running deployment and returns
// an update operation that was interrupted while switching to it.
func (h *harness) plannedUpdate(d domain.Deployment, failure *domain.Error) (domain.Operation, updatePlan) {
	h.t.Helper()
	ctx := context.Background()
	commit := h.source.Push(d.Ref)
	next, err := h.ctrl.build(ctx, d, buildOptions{ref: d.Ref})
	if err != nil {
		h.t.Fatal(err)
	}
	plan := updatePlan{Previous: versionOf(d), Next: next, Serving: true, Failure: failure,
		Result: domain.UpdateResult{PreviousCommit: d.Commit, Commit: commit, Changed: true}}
	checkpoint, _ := json.Marshal(plan)
	if _, err := h.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		next.apply(d)
		d.State = domain.StateStarting
		return nil
	}); err != nil {
		h.t.Fatal(err)
	}
	return domain.Operation{DeploymentID: d.ID, Kind: domain.OpUpdateDeployment, Checkpoint: checkpoint}, plan
}

func TestUpdateResumesAfterInterruption(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Interrupted while starting a working version: finish starting it.
	d := h.running("design-a")
	op, plan := h.plannedUpdate(d, nil)
	result, err := h.ctrl.handleUpdate(ctx, op)
	if err != nil || result.(domain.UpdateResult) != plan.Result {
		t.Fatalf("resumed update: %v %+v", err, result)
	}
	if d = h.get(d.ID); d.State != domain.StateRunning || d.Commit != plan.Next.Commit || h.runtime.Image(d.ID) != plan.Next.Image {
		t.Fatalf("after resuming: %+v", d)
	}
	if images := h.runtime.Images(d.ID); !slices.Equal(images, []string{plan.Next.Image}) {
		t.Fatalf("images after resuming: %v", images)
	}

	// Interrupted while starting a broken version: return to the previous one.
	op, plan = h.plannedUpdate(d, nil)
	h.runtime.Set(func(r *fakes.Runtime) {
		r.StartErr = func(spec container.RunSpec) error {
			if spec.Image == plan.Next.Image {
				return domain.Errorf(domain.CodeStartFailed, "could not start the container")
			}
			return nil
		}
	})
	if _, err := h.ctrl.handleUpdate(ctx, op); !strings.Contains(err.Error(), "design-a still runs "+short(plan.Previous.Commit)) {
		t.Fatalf("resumed broken update: %v", err)
	}
	if d = h.get(d.ID); d.State != domain.StateRunning || d.Image != plan.Previous.Image || !h.runtime.IsRunning(d.ID) {
		t.Fatalf("previous version not restored: %+v", d)
	}

	// Interrupted while returning to the previous version: finish that, and
	// still report the failure.
	op, plan = h.plannedUpdate(d, domain.Errorf(domain.CodeHealthcheckFailed, "GET /health did not succeed"))
	if _, err := h.ctrl.handleUpdate(ctx, op); domain.CodeOf(err) != domain.CodeHealthcheckFailed || !strings.Contains(err.Error(), "still runs") {
		t.Fatalf("resumed return: %v", err)
	}
	if d = h.get(d.ID); d.State != domain.StateRunning || d.Image != plan.Previous.Image {
		t.Fatalf("after resuming the return: %+v", d)
	}
	if images := h.runtime.Images(d.ID); !slices.Equal(images, []string{plan.Previous.Image}) {
		t.Fatalf("images after resuming the return: %v", images)
	}
}

// After a host reboot, a pending update finds the container stopped while
// the deployment is still recorded as running.
func TestUpdateAfterReboot(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	d := h.running("design-a")
	h.runtime.Stop(ctx, d.ID)
	if r := updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{})); r.Changed || !h.runtime.IsRunning(d.ID) {
		t.Fatalf("unchanged update after a reboot: %+v, running %v", r, h.runtime.IsRunning(d.ID))
	}
	h.runtime.Stop(ctx, d.ID)
	commit := h.source.Push("design/a")
	updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{}))
	if d = h.get(d.ID); d.Commit != commit || !h.runtime.IsRunning(d.ID) {
		t.Fatalf("update after a reboot: %+v", d)
	}
}

func TestUpdateHousekeeping(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Updating a stopped deployment removes its previous image.
	d := h.running("design-a")
	stop, _ := h.ctrl.Stop(ctx, "tok_1", d.ID, "")
	h.wait(stop.ID)
	h.source.Push("design/a")
	updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{}))
	if d = h.get(d.ID); !slices.Equal(h.runtime.Images(d.ID), []string{d.Image}) {
		t.Fatalf("images: %v, using %s", h.runtime.Images(d.ID), d.Image)
	}

	// Queueing an update clears the previous build's log.
	logs, _ := h.ctrl.Logs(ctx, d, LogsRequest{Source: LogsBuild, Offset: 0})
	if !strings.HasPrefix(logs.Logs, "Building design/a") {
		t.Fatalf("build log: %q", logs.Logs)
	}
	updateResult(t, h.update("design-a", domain.UpdateDeploymentRequest{}))
	logs, _ = h.ctrl.Logs(ctx, d, LogsRequest{Source: LogsBuild, Offset: 0})
	if logs.Logs != "design/a still points at "+d.Commit+"; nothing was rebuilt\n" {
		t.Fatalf("build log of an unchanged update: %q", logs.Logs)
	}

	// The lifetime is checked when switching, after any extension during the
	// build.
	gate := make(chan struct{})
	h.runtime.Set(func(r *fakes.Runtime) { r.Gate = gate })
	h.source.Push("design/a")
	builds := h.runtime.Builds()
	op, err := h.ctrl.Update(ctx, "tok_1", d.ID, "", domain.UpdateDeploymentRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); h.runtime.Builds() == builds; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the update never started building")
		}
	}
	h.advance(2 * time.Hour)
	if _, err := h.ctrl.Extend(ctx, "tok_1", d.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	gate <- struct{}{}
	updateResult(t, h.wait(op.ID))
}
