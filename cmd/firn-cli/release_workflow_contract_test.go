package main

// Contract tests for .github/workflows/release.yml, the workflow that turns a
// tag into the frostyard-firn packages snosi's installer ISO installs
// (ADR-0017). The workflow only runs on a tag push, so these tests are its
// pull-request gate.

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

type releaseWorkflow struct {
	On map[string]struct {
		Tags     []string `yaml:"tags"`
		Branches []string `yaml:"branches"`
	} `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		If    string         `yaml:"if"`
		Steps []workflowStep `yaml:"steps"`
	} `yaml:"jobs"`
}

func loadReleaseWorkflow(t *testing.T) releaseWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	return workflow
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
// id-token: write and attestations: write and runs a SHA-pinned
// actions/attest-build-provenance over checksums.txt and the .deb files, with
// no guard and no continue-on-error. apt-publisher verifies each .deb with
// `gh attestation verify --source-ref refs/tags/<tag>`, so a missing
// attestation refuses the publication.
func TestReleaseWorkflowAttestsBuildProvenance(t *testing.T) {
	workflow := loadReleaseWorkflow(t)

	for _, scope := range []string{"contents", "id-token", "attestations"} {
		if got := workflow.Permissions[scope]; got != "write" {
			t.Errorf("permissions.%s = %q, want write", scope, got)
		}
	}

	job, ok := workflow.Jobs["goreleaser"]
	if !ok {
		t.Fatal("release workflow is missing goreleaser job")
	}
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
