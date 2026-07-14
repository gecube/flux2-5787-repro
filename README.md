# Reproduction for fluxcd/flux2#5787

Full reproduction and root cause analysis for
[fluxcd/flux2#5787](https://github.com/fluxcd/flux2/issues/5787):
a Flux `Kustomization` with `spec.images` setting only `newName` strips the
image tag that the base `kustomization.yaml` applies via `images: newTag`,
while plain kustomize preserves the tag in the equivalent overlay.

Confirmed with:

| component | version | result |
|---|---|---|
| kustomize CLI | v5.8.1 | tag preserved (baseline) |
| flux CLI (`flux build ... --dry-run`) | v2.8.8 | **tag lost** |
| kustomize-controller (live cluster) | v1.8.5 | **tag lost** |
| `fluxcd/pkg/kustomize` (unit test) | v1.38.0 | **tag lost** |

## Layout

```
base/                  deployment (untagged image) + kustomization.yaml with images/newTag
overlay/               plain-kustomize overlay changing only the image name (for comparison)
flux/                  Flux Kustomization with spec.images changing only the image name
cluster/               manifests for reproducing on a live cluster (OCIRepository + Kustomization)
generator-test/        Go test against fluxcd/pkg/kustomize — fails while the bug is present
repro.sh               CLI reproduction (kustomize vs flux build)
```

## CLI reproduction

Requires `kustomize` and `flux` in PATH, no cluster needed:

```console
$ ./repro.sh
==> 1. plain kustomize: base alone (tag set via images/newTag)
      - image: ghcr.io/example/myapp:v1.2.3
==> 2. plain kustomize: overlay changing only the image name (tag preserved)
      - image: registry.example.com/myapp:v1.2.3
==> 3. flux build: spec.images changing only the image name (tag LOST)
      - image: registry.example.com/myapp
```

## Unit test reproduction

`generator-test/` exercises the exact code path shared by kustomize-controller
and `flux build kustomization` (`kustomize.NewGenerator(...).WriteFile` +
`kustomize.SecureBuild`) against the released `fluxcd/pkg/kustomize` module.
The test asserts the kustomize-consistent behavior, so it fails while the bug
is present:

```console
$ cd generator-test && go test ./...
    generator_test.go:90: generated kustomization.yaml:
        apiVersion: kustomize.config.k8s.io/v1beta1
        images:
        - name: ghcr.io/example/myapp
          newName: registry.example.com/myapp
        kind: Kustomization
        resources:
        - deployment.yaml
    generator_test.go:111: tag from base images/newTag was lost:
          got  "image: registry.example.com/myapp"
          want "image: registry.example.com/myapp:v1.2.3"
FAIL
```

Note the generated `kustomization.yaml` above: the base entry's
`newTag: v1.2.3` is gone.

## In-cluster reproduction

The same happens with a real kustomize-controller (verified on v1.8.5,
Flux distribution v2.8.8). Push this repository as an OCI artifact to a
registry reachable from the cluster, set `spec.url` in
`cluster/repro-cluster.yaml`, and apply it:

```console
$ flux push artifact oci://<registry>/repro:v1 --path=. --source=flux2-5787-repro --revision=v1
$ kubectl apply -f cluster/repro-cluster.yaml
$ kubectl get deploy myapp -n default -o jsonpath='{.spec.template.spec.containers[0].image}'
registry.example.com/myapp
```

Control cases on the same cluster behave as expected:

- no `spec.images` at all → `ghcr.io/example/myapp:v1.2.3`
- `newName` + `newTag: v9.9.9` → `registry.example.com/myapp:v9.9.9`

## Root cause

Flux does not build an overlay on top of `spec.path` (the way the
plain-kustomize comparison does). Instead, the generator loads the existing
`kustomization.yaml` at `spec.path` and merges `spec.images` into it before
running the build. When an entry with the same `name` already exists, it is
**replaced wholesale** instead of merged field by field:

https://github.com/fluxcd/pkg/blob/c8dd701dd80aa97427fccded472ca1b3bac6d04a/kustomize/kustomize_generator.go#L301-L313

```go
for _, image := range images {
	newImage := kustypes.Image{
		Name:    image.Name,
		NewName: image.NewName,
		NewTag:  image.NewTag,
		Digest:  image.Digest,
	}
	if exists, index := checkKustomizeImageExists(kus.Images, image.Name); exists {
		kus.Images[index] = newImage // base entry's newTag/digest are dropped here
	} else {
		kus.Images = append(kus.Images, newImage)
	}
}
```

The base entry `{name: ghcr.io/example/myapp, newTag: v1.2.3}` is overwritten
by `{name: ghcr.io/example/myapp, newName: registry.example.com/myapp}`. The
resulting image transformer only renames; since the Deployment references the
untagged image, the output has no tag. The same replacement also silently
discards a base `digest` (or `newName`) whenever `spec.images` overrides only
a subset of the fields.

This is also why the Component workaround from the issue works: the generator
only rewrites the root `kustomization.yaml` at `spec.path`. The component's
own file is untouched, its `newTag` is applied during resource accumulation,
and the injected name-only image transformer then keeps the already-present
tag — normal kustomize transformer semantics.

## Suggested fix

Merge field-wise instead of replacing, which restores consistency with
kustomize overlay semantics:

```go
if exists, index := checkKustomizeImageExists(kus.Images, image.Name); exists {
	if image.NewName != "" {
		kus.Images[index].NewName = image.NewName
	}
	if image.NewTag != "" {
		kus.Images[index].NewTag = image.NewTag
	}
	if image.Digest != "" {
		kus.Images[index].Digest = image.Digest
	}
} else {
	kus.Images = append(kus.Images, newImage)
}
```

With this change the unit test in `generator-test/` passes and all three
reproductions produce `registry.example.com/myapp:v1.2.3`.
