package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func TestStripUnsupportedManifestsRewritesNestedIndex(t *testing.T) {
	image := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	attestation := mutate.MediaType(mutate.ConfigMediaType(empty.Image, types.OCIEmptyJSON), types.OCIManifestSchema1)
	nested := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{
			Add: image,
			Descriptor: v1.Descriptor{
				Platform: &v1.Platform{OS: "linux", Architecture: "amd64"},
			},
		},
		mutate.IndexAddendum{
			Add: attestation,
			Descriptor: v1.Descriptor{
				Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"},
				Annotations: map[string]string{
					"vnd.docker.reference.type": "attestation-manifest",
				},
			},
		},
	)
	root := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: nested})
	got, changed, err := stripUnsupportedManifests(root)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("嵌套索引应被改写")
	}
	manifest, err := got.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Manifests) != 1 {
		t.Fatalf("根清单数 = %d", len(manifest.Manifests))
	}
	child, err := got.ImageIndex(manifest.Manifests[0].Digest)
	if err != nil {
		t.Fatal(err)
	}
	nestedManifest, err := child.IndexManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(nestedManifest.Manifests) != 1 || nestedManifest.Manifests[0].Platform == nil || nestedManifest.Manifests[0].Platform.Architecture != "amd64" {
		t.Fatalf("嵌套清单 = %+v", nestedManifest.Manifests)
	}
}

func TestCopyDropsEmptyConfigManifest(t *testing.T) {
	src := startRegistry(t, false)
	dst := startRegistry(t, true)
	image := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	attestation := mutate.MediaType(mutate.ConfigMediaType(empty.Image, types.OCIEmptyJSON), types.OCIManifestSchema1)
	if err := remote.Write(mustRef(t, src+"/app:image"), image); err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(mustRef(t, src+"/app:attestation"), attestation); err != nil {
		t.Fatal(err)
	}

	index := v1.IndexManifest{
		SchemaVersion: 2,
		MediaType:     types.OCIImageIndex,
		Manifests: []v1.Descriptor{
			indexChild(t, image, v1.Platform{OS: "linux", Architecture: "amd64"}),
			indexChild(t, attestation, v1.Platform{OS: "linux", Architecture: "amd64"}),
		},
	}
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Put(mustRef(t, src+"/app:latest"), rawTaggable{mediaType: types.OCIImageIndex, raw: raw}); err != nil {
		t.Fatal(err)
	}

	if err := NewCrane().Copy(context.Background(), src+"/app:latest", dst+"/app:latest", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := remote.Get(mustRef(t, dst+"/app:latest"))
	if err != nil {
		t.Fatal(err)
	}
	manifests := indexManifests(t, got)
	if len(manifests) != 1 || manifests[0].Platform == nil || manifests[0].Platform.Architecture != "amd64" {
		t.Fatalf("目标清单 = %+v", manifests)
	}
	child, err := remote.Get(mustRef(t, dst+"/app@"+manifests[0].Digest.String()))
	if err != nil {
		t.Fatal(err)
	}
	mf, err := v1.ParseManifest(bytes.NewReader(child.Manifest))
	if err != nil {
		t.Fatal(err)
	}
	if mf.Config.MediaType == types.OCIEmptyJSON {
		t.Fatalf("空配置清单被推送: %s", child.Manifest)
	}
}

func indexChild(t *testing.T, img v1.Image, platform v1.Platform) v1.Descriptor {
	t.Helper()
	desc, err := partial.Descriptor(img)
	if err != nil {
		t.Fatal(err)
	}
	return v1.Descriptor{
		MediaType: desc.MediaType,
		Size:      desc.Size,
		Digest:    desc.Digest,
		Platform:  &platform,
	}
}

func startRegistry(t *testing.T, rejectEmpty bool) string {
	t.Helper()
	handler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	if rejectEmpty {
		handler = rejectEmptyConfig(handler)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.Listener.Addr().String()
}

func rejectEmptyConfig(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !isManifestPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var manifest struct {
			Config struct {
				MediaType string `json:"mediaType"`
			} `json:"config"`
		}
		_ = json.Unmarshal(body, &manifest)
		if manifest.Config.MediaType == string(types.OCIEmptyJSON) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":[{"code":"DENIED","message":"unknown manifest class for application/vnd.oci.empty.v1+json"}]}`))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func isManifestPath(path string) bool {
	return len(path) > 0 && bytes.Contains([]byte(path), []byte("/manifests/"))
}

func mustRef(t *testing.T, ref string) name.Reference {
	t.Helper()
	parsed, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type rawTaggable struct {
	mediaType types.MediaType
	raw       []byte
}

func (r rawTaggable) RawManifest() ([]byte, error)        { return r.raw, nil }
func (r rawTaggable) MediaType() (types.MediaType, error) { return r.mediaType, nil }
