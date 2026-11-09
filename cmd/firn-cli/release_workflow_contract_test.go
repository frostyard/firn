package main

// Contract tests for .github/workflows/release.yml, the workflow that turns a
// tag into the frostyard-firn packages snosi's installer ISO installs
// (ADR-0017), and for .github/workflows/snapshot.yml, the nightly that
// republishes the dev tag. Neither workflow runs on a pull request, so these
// tests are their pull-request gate.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflowStep struct {
	Name            string            `yaml:"name"`
	If              string            `yaml:"if"`
	Uses            string            `yaml:"uses"`
	With            map[string]string `yaml:"with"`
	ContinueOnError yaml.Node         `yaml:"continue-on-error"`
}

type workflowJob struct {
	If          string            `yaml:"if"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
}

type releaseWorkflow struct {
	On map[string]struct {
		Tags     []string `yaml:"tags"`
		Branches []string `yaml:"branches"`
	} `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

// snapshotWorkflow is .github/workflows/snapshot.yml: these tests read only
// its permissions and its steps.
type snapshotWorkflow struct {
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

// readWorkflow returns the bytes of a workflow file under .github/workflows.
func readWorkflow(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatalf("read workflow %s: %v", name, err)
	}
	return data
}

func loadReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(readWorkflow(t, "release.yml"), &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	return workflow
}

func loadSnapshotWorkflow(t *testing.T) snapshotWorkflow {
	t.Helper()
	var workflow snapshotWorkflow
	if err := yaml.Unmarshal(readWorkflow(t, "snapshot.yml"), &workflow); err != nil {
		t.Fatalf("parse snapshot workflow: %v", err)
	}
	return workflow
}

// requireEmptyTopLevelPermissions fails unless the workflow's top-level
// `permissions` key is present and grants nothing. yaml unmarshals
// `permissions: {}` to an empty non-nil map and an absent key to nil, so a nil
// map means the key is missing.
func requireEmptyTopLevelPermissions(t *testing.T, name string, permissions map[string]string) {
	t.Helper()
	if permissions == nil {
		t.Fatalf("%s has no top-level permissions key; set `permissions: {}` (core ADR-0021)", name)
	}
	if len(permissions) != 0 {
		t.Fatalf("%s grants %v at the top level; grant nothing there and put each scope on the job that needs it", name, permissions)
	}
}

// requireJobPermissions fails unless the job grants exactly want, so a new
// scope cannot appear without widening this contract deliberately.
func requireJobPermissions(t *testing.T, workflowName, jobName string, job workflowJob, want map[string]string) {
	t.Helper()
	for scope, level := range want {
		if got := job.Permissions[scope]; got != level {
			t.Errorf("%s job %s permissions.%s = %q, want %q", workflowName, jobName, scope, got, level)
		}
	}
	for scope := range job.Permissions {
		if _, ok := want[scope]; !ok {
			t.Errorf("%s job %s grants permissions.%s = %q; it needs only %v", workflowName, jobName, scope, job.Permissions[scope], want)
		}
	}
}

// requireCheckoutWithoutPersistedCredentials fails unless every
// actions/checkout step of every job sets `persist-credentials: false`, so
// the checkout leaves no git credential for later steps to reuse.
func requireCheckoutWithoutPersistedCredentials(t *testing.T, workflowName string, jobs map[string]workflowJob) {
	t.Helper()
	checkouts := 0
	for jobName, job := range jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/checkout@") {
				continue
			}
			checkouts++
			if got := step.With["persist-credentials"]; got != "false" {
				t.Errorf("%s job %s step %q sets persist-credentials = %q, want false (core ADR-0021)", workflowName, jobName, step.Name, got)
			}
		}
	}
	if checkouts == 0 {
		t.Errorf("%s has no actions/checkout step, so this contract checks nothing", workflowName)
	}
}

// stepIndex returns the index of the only step in steps whose uses has the
// given prefix, failing the test unless there is exactly one.
func stepIndex(t *testing.T, steps []workflowStep, usesPrefix string) int {
	t.Helper()
	found := -1
	for i, step := range steps {
		if !strings.HasPrefix(step.Uses, usesPrefix) {
			continue
		}
		if found >= 0 {
			t.Fatalf("goreleaser job has more than one %s step", usesPrefix)
		}
		found = i
	}
	if found < 0 {
		t.Fatalf("goreleaser job has no %s step", usesPrefix)
	}
	return found
}

// TestReleaseWorkflowRequestsAptPublicationForTags pins the publication
// contract of frostyard/core ADR-0055 and ADR-0056 (firn ADR-0017): a tag
// release asks frostyard/apt-publisher to publish its .deb files with one
// unguarded `publish-deb` repository_dispatch that may not continue on error
// (if it fails, nothing was published), sent only after GoReleaser has
// created the release and its assets have been attested. The publisher, not
// this workflow, dispatches `build` to frostyard/snosi once the packages are
// installable, so a direct snosi dispatch or a repogen publish step here
// would rebuild the ISO before its firn package exists.
func TestReleaseWorkflowRequestsAptPublicationForTags(t *testing.T) {
	workflow := loadReleaseWorkflow(t)

	push, ok := workflow.On["push"]
	if len(workflow.On) != 1 || !ok {
		t.Fatalf("release workflow triggers on %d events, want only push", len(workflow.On))
	}
	if len(push.Tags) == 0 {
		t.Fatal("release workflow must run on tag pushes")
	}
	if len(push.Branches) != 0 {
		t.Fatalf("release workflow branch filters = %v, want a tag-only push trigger", push.Branches)
	}

	for name, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "frostyard/repogen/") {
				t.Fatalf("job %s uses %s; .deb files are published by frostyard/apt-publisher", name, step.Uses)
			}
			if strings.HasPrefix(step.Uses, "peter-evans/repository-dispatch@") &&
				step.With["repository"] == "frostyard/snosi" {
				t.Fatalf("job %s dispatches to frostyard/snosi; frostyard/apt-publisher does after publishing", name)
			}
		}
	}

	job, ok := workflow.Jobs["goreleaser"]
	if !ok {
		t.Fatal("release workflow is missing goreleaser job")
	}
	if job.If != "" {
		t.Fatalf("goreleaser job has guard %q; tag releases must reach the publication request", job.If)
	}

	request := -1
	for i, step := range job.Steps {
		if !strings.HasPrefix(step.Uses, "peter-evans/repository-dispatch@") ||
			step.With["repository"] != "frostyard/apt-publisher" {
			continue
		}
		if request >= 0 {
			t.Fatal("release workflow sends more than one request to frostyard/apt-publisher")
		}
		request = i
		if step.With["event-type"] != "publish-deb" {
			t.Fatalf("apt-publisher dispatch event-type = %q, want publish-deb", step.With["event-type"])
		}
		if step.With["token"] != "${{ secrets.APT_PUBLISH_TOKEN }}" {
			t.Fatalf("apt-publisher dispatch token = %q, want ${{ secrets.APT_PUBLISH_TOKEN }}", step.With["token"])
		}
		if step.If != "" {
			t.Fatalf("publication request has guard %q; tag releases must reach it", step.If)
		}
		if !step.ContinueOnError.IsZero() {
			t.Fatal("publication request sets continue-on-error; a failed request must fail the release")
		}
		payload := step.With["client-payload"]
		for _, want := range []string{`"repo": "${{ github.repository }}"`, `"tag": "${{ github.ref_name }}"`} {
			if !strings.Contains(payload, want) {
				t.Fatalf("publication request client-payload %q lacks %s", payload, want)
			}
		}
	}
	if request < 0 {
		t.Fatal("release workflow must send a publish-deb request to frostyard/apt-publisher")
	}

	release := stepIndex(t, job.Steps, "goreleaser/goreleaser-action@")
	attest := stepIndex(t, job.Steps, "actions/attest-build-provenance@")
	if release >= attest || attest >= request {
		t.Fatalf("goreleaser job step order: release %d, attest %d, request %d; want release, then attest, then request", release, attest, request)
	}
}

// TestReleaseWorkflowAttestsBuildProvenance pins the provenance contract that
// lets apt-publisher register firn `attested=yes`: the tag workflow grants
// nothing at the top level and its single goreleaser job grants exactly
// contents: write (GoReleaser creates the release and uploads its assets),
// id-token: write and attestations: write (actions/attest-build-provenance),
// and runs that SHA-pinned action over checksums.txt and the .deb files, with
// no guard and no continue-on-error. apt-publisher verifies each .deb with
// `gh attestation verify --source-ref refs/tags/<tag>`, so a missing
// attestation refuses the publication. It also pins core ADR-0021: the
// checkout leaves no git credential for later steps to reuse.
func TestReleaseWorkflowAttestsBuildProvenance(t *testing.T) {
	workflow := loadReleaseWorkflow(t)

	requireEmptyTopLevelPermissions(t, "release workflow", workflow.Permissions)

	job, ok := workflow.Jobs["goreleaser"]
	if !ok {
		t.Fatal("release workflow is missing goreleaser job")
	}
	requireJobPermissions(t, "release workflow", "goreleaser", job, map[string]string{
		"contents":     "write",
		"id-token":     "write",
		"attestations": "write",
	})
	requireCheckoutWithoutPersistedCredentials(t, "release workflow", workflow.Jobs)

	step := job.Steps[stepIndex(t, job.Steps, "actions/attest-build-provenance@")]
	if step.If != "" {
		t.Errorf("attest step has guard %q; every tag release must be attested", step.If)
	}
	if !step.ContinueOnError.IsZero() {
		t.Error("attest step sets continue-on-error; an unattested release must not request publication")
	}
	subjects := strings.Fields(step.With["subject-path"])
	for _, want := range []string{"dist/checksums.txt", "dist/*.deb"} {
		if !slices.Contains(subjects, want) {
			t.Errorf("attest step subject-path %q lacks %s", step.With["subject-path"], want)
		}
	}
}

// TestSnapshotWorkflowLeastPrivilege pins core ADR-0021 for the nightly
// publication workflow (.github/workflows/snapshot.yml): nothing is granted at
// the top level, the single snapshot job grants exactly contents: write
// because GoReleaser replaces the dev nightly tag and release through the
// GitHub API from GITHUB_TOKEN, and every checkout step sets
// persist-credentials: false because no step runs a git push.
func TestSnapshotWorkflowLeastPrivilege(t *testing.T) {
	workflow := loadSnapshotWorkflow(t)

	requireEmptyTopLevelPermissions(t, "snapshot workflow", workflow.Permissions)

	job, ok := workflow.Jobs["snapshot"]
	if !ok {
		t.Fatal("snapshot workflow is missing snapshot job")
	}
	requireJobPermissions(t, "snapshot workflow", "snapshot", job, map[string]string{
		"contents": "write",
	})
	requireCheckoutWithoutPersistedCredentials(t, "snapshot workflow", workflow.Jobs)
}

// TestReleaseWorkflowPinsActionsToCommits pins core ADR-0021 for the release
// path, which holds id-token: write and the APT publication token: every
// action is referenced by a full commit SHA, never a movable tag or branch.
func TestReleaseWorkflowPinsActionsToCommits(t *testing.T) {
	workflow := loadReleaseWorkflow(t)
	pinned := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	for name, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Uses != "" && !pinned.MatchString(step.Uses) {
				t.Errorf("job %s step %q uses %q; pin it to a 40-character commit SHA", name, step.Name, step.Uses)
			}
		}
	}
}
