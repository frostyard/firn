package sysconfig

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/firn/internal/recipe"
)

func TestCreateUserFullnameParity(t *testing.T) {
	for _, tt := range []struct {
		name     string
		fullname string
	}{
		{name: "empty"},
		{name: "normal", fullname: "Ada Lovelace"},
		{name: "unicode", fullname: "Zoë 雪"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target, deployDir := ostreeTarget(t)
			writeFile(t, filepath.Join(deployDir, "etc", "group"), "root:x:0:\n")
			var calls []call
			deployment := &DeploymentWriter{TargetDir: target, Runner: fakeRunner(&calls)}
			if _, err := deployment.CreateUser(context.Background(), recipe.User{Name: "dev", Fullname: tt.fullname}); err != nil {
				t.Fatal(err)
			}
			useradd := findCall(t, calls, "chroot")
			var deploymentGECOS string
			for i, arg := range useradd.args {
				if arg == "--comment" && i+1 < len(useradd.args) {
					deploymentGECOS = useradd.args[i+1]
					break
				}
			}

			stubChown(t)
			overlay := newOverlayWriter(t)
			if _, err := overlay.CreateUser(recipe.User{Name: "dev", Fullname: tt.fullname}); err != nil {
				t.Fatal(err)
			}
			passwd := readOverlayFile(t, filepath.Join(upperOf(overlay), "passwd"), 0o644)
			var overlayGECOS string
			for line := range strings.SplitSeq(passwd, "\n") {
				fields := strings.Split(line, ":")
				if len(fields) == 7 && fields[0] == "dev" {
					overlayGECOS = fields[4]
					break
				}
			}

			if deploymentGECOS != tt.fullname || overlayGECOS != tt.fullname || deploymentGECOS != overlayGECOS {
				t.Fatalf("GECOS mismatch: deployment=%q overlay=%q want=%q", deploymentGECOS, overlayGECOS, tt.fullname)
			}
		})
	}
}

func TestDeploymentWriterRejectsInvalidFullnameBeforeMutation(t *testing.T) {
	for _, fullname := range []string{"Bad:Name", "Bad\nName", "Bad\rName"} {
		var calls []call
		w := &DeploymentWriter{TargetDir: t.TempDir(), Runner: fakeRunner(&calls)}
		if _, err := w.CreateUser(context.Background(), recipe.User{Name: "dev", Fullname: fullname}); err == nil || !strings.Contains(err.Error(), "full name") {
			t.Errorf("full name %q error = %v", fullname, err)
		}
		if len(calls) != 0 {
			t.Errorf("full name %q made runner calls before rejection: %+v", fullname, calls)
		}
	}
}
