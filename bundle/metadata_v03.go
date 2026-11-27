package bundle

import "context"

// BundleMetadata describes the standard OKF 0.3 root index fields. License
// retains the declared value. LicensePath identifies a captured license file.
type BundleMetadata struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	License     string `json:"license,omitempty"`
	LicensePath string `json:"license_path,omitempty"`
}

// BundleMetadataContext reads and resolves metadata from the captured root
// index. A bundle with no standard metadata has a nil projection.
func (b *Bundle) BundleMetadataContext(ctx context.Context, version VersionResolution) (*BundleMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if version.Effective != OKFVersion03 {
		return nil, nil
	}
	document, present, err := b.rootIndexDocumentForSelectorContext(ctx, version.Effective)
	if !present || err != nil {
		return nil, err
	}
	frontmatter := document.Frontmatter
	if !frontmatter.coreV03 {
		frontmatter, err = ParseFrontmatterForVersionContext(ctx, string(document.frontmatterYAML), version)
		if err != nil {
			return nil, err
		}
	}
	out := BundleMetadata{}
	for _, field := range []struct {
		key   string
		value *string
	}{{"title", &out.Title}, {"description", &out.Description}, {"license", &out.License}} {
		state, err := frontmatter.semanticStringStateContext(ctx, field.key)
		if err != nil {
			return nil, err
		}
		if state.Valid {
			*field.value = state.Value
		}
	}
	if out.License != "" {
		resolved, _, err := b.resolveURIReferenceContext(ctx, "index.md", out.License, false, false)
		if err != nil {
			return nil, err
		}
		if resolved.Exists {
			out.LicensePath = resolved.Path
		}
	}
	if out.Title == "" && out.Description == "" && out.License == "" {
		return nil, nil
	}
	return &out, ctx.Err()
}
