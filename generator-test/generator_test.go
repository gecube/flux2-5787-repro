// Reproduction for https://github.com/fluxcd/flux2/issues/5787
//
// The test asserts the behavior one would expect from kustomize overlay
// semantics: a Flux Kustomization spec.images entry that sets only newName
// should preserve the tag applied by the base kustomization's images/newTag.
//
// It FAILS against fluxcd/pkg/kustomize as of v1.38.0, demonstrating the bug:
// the generator replaces the whole images entry of the base kustomization.yaml
// instead of merging fields, so newTag is dropped and the build output
// references an untagged image.
package generatortest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxcd/pkg/kustomize"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const deployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: myapp
spec:
  selector:
    matchLabels:
      app: myapp
  template:
    metadata:
      labels:
        app: myapp
    spec:
      containers:
        - name: myapp
          image: ghcr.io/example/myapp
`

const baseKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml
images:
  - name: ghcr.io/example/myapp
    newTag: v1.2.3
`

const fluxKustomization = `apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: myapp
  namespace: default
spec:
  path: ./base
  images:
    - name: ghcr.io/example/myapp
      newName: registry.example.com/myapp
`

func TestSpecImagesNewNameOnlyPreservesBaseTag(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "deployment.yaml"), []byte(deployment), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "kustomization.yaml"), []byte(baseKustomization), 0o640); err != nil {
		t.Fatal(err)
	}

	var ks unstructured.Unstructured
	if err := yaml.Unmarshal([]byte(fluxKustomization), &ks.Object); err != nil {
		t.Fatal(err)
	}

	// Same code path as kustomize-controller and `flux build kustomization`:
	// merge spec.images into the kustomization.yaml at spec.path, then build.
	if _, err := kustomize.NewGenerator(root, ks).WriteFile(base); err != nil {
		t.Fatal(err)
	}

	generated, err := os.ReadFile(filepath.Join(base, "kustomization.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated kustomization.yaml:\n%s", generated)

	resMap, err := kustomize.SecureBuild(root, base, false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := resMap.AsYaml()
	if err != nil {
		t.Fatal(err)
	}

	var image string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "image:") {
			image = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
		}
	}

	const want = "image: registry.example.com/myapp:v1.2.3"
	if image != want {
		t.Errorf("tag from base images/newTag was lost:\n  got  %q\n  want %q", image, want)
	}
}
