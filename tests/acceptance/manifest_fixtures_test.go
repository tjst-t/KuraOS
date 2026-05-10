// Regression for the "fixture manifest fields don't exist on the
// schema" class of bugs (2026-05-10): tests/fixtures/app-registry
// shipped manifests with `routing.base_path` and `health.http`,
// neither of which the parser knows about. The Manifest unit tests
// pass because they use a different, valid sample. The fixtures only
// fail at install time inside the daemon — caught by the user, not
// the test suite.
//
// This test loads every manifest under tests/fixtures/app-registry/
// and runs Parse + Validate to catch schema drift before it leaks to
// the install path. Cheap (one pass per fixture, no network, no
// docker), so safe to run in every CI build.
package acceptance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuraos-org/kura/engine/app"
)

func TestAllFixtureManifestsParseAndValidate(t *testing.T) {
	root := "../../tests/fixtures/app-registry/apps"
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "manifest.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no fixture manifests found under %s", root)
	}
	for _, p := range matches {
		t.Run(p, func(t *testing.T) {
			body, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			m, err := app.ParseManifest(body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := app.Validate(m); err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}
