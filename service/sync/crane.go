package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

const (
	attestationTypeAnnotation = "vnd.docker.reference.type"
	attestationManifest       = "attestation-manifest"
	attestationArtifactType   = "application/vnd.docker.attestation.manifest.v1+json"
)

var errNoRunnableManifest = errors.New("索引中没有可复制的镜像清单")
var errEmptyConfigManifest = errors.New("目标仓库不支持 application/vnd.oci.empty.v1+json 配置的镜像清单")

type Crane struct{}

func NewCrane() *Crane {
	return &Crane{}
}

func authenticator(auth *Auth) authn.Authenticator {
	if auth == nil || auth.Username == "" {
		return authn.Anonymous
	}
	return &authn.Basic{Username: auth.Username, Password: auth.Password}
}

func (c *Crane) Digest(ctx context.Context, ref string, auth *Auth) (string, error) {
	parsed, err := name.ParseReference(ref)
	if err != nil {
		return "", err
	}
	puller, err := remote.NewPuller(remote.WithAuth(authenticator(auth)), remote.WithTransport(http.DefaultTransport))
	if err != nil {
		return "", err
	}
	desc, err := puller.Head(ctx, parsed)
	if err != nil {
		return "", err
	}
	return desc.Digest.String(), nil
}

func withoutAttestations(desc *remote.Descriptor) (remote.Taggable, error) {
	if desc == nil {
		return nil, nil
	}
	if !desc.MediaType.IsIndex() {
		if _, ok := parseIndexBody(desc.Manifest); ok {
			desc.MediaType = types.OCIImageIndex
		} else {
			manifest, err := v1.ParseManifest(bytes.NewReader(desc.Manifest))
			if err == nil && manifestUnsupported(manifest) {
				return nil, errEmptyConfigManifest
			}
			return desc, nil
		}
	}
	idx, err := desc.ImageIndex()
	if err != nil {
		return nil, err
	}
	filtered, changed, err := stripUnsupportedManifests(idx)
	if err != nil {
		return nil, err
	}
	if !changed {
		return desc, nil
	}
	return filtered, nil
}

func parseIndexBody(raw []byte) (*v1.IndexManifest, bool) {
	manifest, err := v1.ParseIndexManifest(bytes.NewReader(raw))
	if err != nil || len(manifest.Manifests) == 0 {
		return nil, false
	}
	return manifest, true
}

func stripUnsupportedManifests(idx v1.ImageIndex) (v1.ImageIndex, bool, error) {
	manifest, err := idx.IndexManifest()
	if err != nil {
		return nil, false, fmt.Errorf("解析镜像索引失败: %w", err)
	}
	drop := map[v1.Hash]struct{}{}
	nested := map[v1.Hash]v1.ImageIndex{}
	for _, desc := range manifest.Manifests {
		if desc.MediaType.IsIndex() {
			child, err := idx.ImageIndex(desc.Digest)
			if err != nil {
				return nil, false, fmt.Errorf("读取子索引 %s 失败: %w", desc.Digest, err)
			}
			stripped, changed, err := stripUnsupportedManifests(child)
			if errors.Is(err, errNoRunnableManifest) {
				drop[desc.Digest] = struct{}{}
				continue
			}
			if err != nil {
				return nil, false, err
			}
			if changed {
				nested[desc.Digest] = stripped
			}
			continue
		}
		unsupported, err := childUnsupported(idx, desc)
		if err != nil {
			return nil, false, err
		}
		if unsupported {
			drop[desc.Digest] = struct{}{}
		}
	}
	if len(drop) == 0 && len(nested) == 0 {
		return idx, false, nil
	}
	return rebuildIndex(idx, manifest, drop, nested)
}

func rebuildIndex(idx v1.ImageIndex, manifest *v1.IndexManifest, drop map[v1.Hash]struct{}, nested map[v1.Hash]v1.ImageIndex) (v1.ImageIndex, bool, error) {
	if len(nested) == 0 {
		filtered := mutate.RemoveManifests(idx, func(desc v1.Descriptor) bool {
			_, ok := drop[desc.Digest]
			return ok
		})
		next, err := filtered.IndexManifest()
		if err != nil {
			return nil, false, fmt.Errorf("解析镜像索引失败: %w", err)
		}
		if len(next.Manifests) == 0 {
			return nil, true, errNoRunnableManifest
		}
		return filtered, true, nil
	}
	mediaType, err := idx.MediaType()
	if err != nil {
		return nil, false, fmt.Errorf("解析镜像索引失败: %w", err)
	}
	next := manifest.DeepCopy()
	next.MediaType = mediaType
	next.Manifests = make([]v1.Descriptor, 0, len(manifest.Manifests))
	replaced := map[v1.Hash]v1.ImageIndex{}
	for _, desc := range manifest.Manifests {
		if _, ok := drop[desc.Digest]; ok {
			continue
		}
		child, ok := nested[desc.Digest]
		if !ok {
			next.Manifests = append(next.Manifests, *desc.DeepCopy())
			continue
		}
		updated, err := describableDescriptor(child, desc)
		if err != nil {
			return nil, false, err
		}
		next.Manifests = append(next.Manifests, updated)
		replaced[updated.Digest] = child
	}
	if len(next.Manifests) == 0 {
		return nil, true, errNoRunnableManifest
	}
	return &filteredIndex{base: idx, mediaType: mediaType, manifest: next, nested: replaced}, true, nil
}

func childUnsupported(idx v1.ImageIndex, desc v1.Descriptor) (bool, error) {
	if descriptorUnsupported(desc) {
		return true, nil
	}
	manifest, err := childManifest(idx, desc)
	if err != nil {
		return false, err
	}
	return manifestUnsupported(manifest), nil
}

func childManifest(idx v1.ImageIndex, desc v1.Descriptor) (*v1.Manifest, error) {
	if len(desc.Data) > 0 {
		manifest, err := v1.ParseManifest(bytes.NewReader(desc.Data))
		if err != nil {
			return nil, fmt.Errorf("解析子镜像 %s 失败: %w", desc.Digest, err)
		}
		return manifest, nil
	}
	if !desc.MediaType.IsImage() {
		return nil, nil
	}
	img, err := idx.Image(desc.Digest)
	if err != nil {
		return nil, fmt.Errorf("读取子镜像 %s 失败: %w", desc.Digest, err)
	}
	manifest, err := img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("解析子镜像 %s 失败: %w", desc.Digest, err)
	}
	return manifest, nil
}

func descriptorUnsupported(desc v1.Descriptor) bool {
	if desc.Annotations[attestationTypeAnnotation] == attestationManifest {
		return true
	}
	if desc.Platform != nil && desc.Platform.OS == "unknown" && desc.Platform.Architecture == "unknown" {
		return true
	}
	if desc.MediaType == types.OCIEmptyJSON || desc.ArtifactType == string(types.OCIEmptyJSON) || desc.ArtifactType == attestationArtifactType {
		return true
	}
	return false
}

func manifestUnsupported(manifest *v1.Manifest) bool {
	if manifest == nil {
		return false
	}
	if manifest.Config.MediaType == types.OCIEmptyJSON {
		return true
	}
	return manifest.ArtifactType == string(types.OCIEmptyJSON) || manifest.ArtifactType == attestationArtifactType
}

type filteredIndex struct {
	base      v1.ImageIndex
	mediaType types.MediaType
	manifest  *v1.IndexManifest
	nested    map[v1.Hash]v1.ImageIndex
}

func (f *filteredIndex) MediaType() (types.MediaType, error) { return f.mediaType, nil }
func (f *filteredIndex) IndexManifest() (*v1.IndexManifest, error) {
	return f.manifest.DeepCopy(), nil
}
func (f *filteredIndex) RawManifest() ([]byte, error) { return json.Marshal(f.manifest) }
func (f *filteredIndex) Digest() (v1.Hash, error)     { return partial.Digest(f) }
func (f *filteredIndex) Size() (int64, error)         { return partial.Size(f) }
func (f *filteredIndex) Image(h v1.Hash) (v1.Image, error) {
	return f.base.Image(h)
}
func (f *filteredIndex) ImageIndex(h v1.Hash) (v1.ImageIndex, error) {
	if idx, ok := f.nested[h]; ok {
		return idx, nil
	}
	return f.base.ImageIndex(h)
}

func (c *Crane) Copy(ctx context.Context, src, dst string, srcAuth, dstAuth *Auth, progress func(phase string) error) error {
	srcRef, err := name.ParseReference(src)
	if err != nil {
		return err
	}
	dstRef, err := name.ParseReference(dst)
	if err != nil {
		return err
	}
	puller, err := remote.NewPuller(remote.WithAuth(authenticator(srcAuth)), remote.WithTransport(http.DefaultTransport))
	if err != nil {
		return err
	}
	desc, err := puller.Get(ctx, srcRef)
	if err != nil {
		return err
	}
	taggable, err := withoutAttestations(desc)
	if err != nil {
		return err
	}
	taggable, err = prepareForRegistry(dstRef, taggable)
	if err != nil {
		return err
	}
	if progress != nil {
		if err := progress(PhaseCopy); err != nil {
			return err
		}
	}
	pusher, err := remote.NewPusher(remote.WithAuth(authenticator(dstAuth)), remote.WithTransport(http.DefaultTransport))
	if err != nil {
		return err
	}
	return pusher.Push(ctx, dstRef, taggable)
}
