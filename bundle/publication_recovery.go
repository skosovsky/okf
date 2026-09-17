package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

func privatePublicationNamespaceRule() publicationNamespaceRule {
	return publicationNamespaceRule{
		name: "private-v2",
		classify: func(name string) (string, bool) {
			return "claim", hasASCIIFoldPrefix(name, publicationPrivateClaimPrefix)
		},
	}
}

// inspectGlobalPublicationRecoveryNamespace is the single recovery entrypoint
// for every bundle publisher. It inventories and prepares both protocols
// before either protocol is allowed to mutate the tree.
func inspectGlobalPublicationRecoveryNamespace(
	ctx context.Context,
	root *os.Root,
	indexHooks indexPublishHooks,
	documentHooks documentPublishHooks,
) error {
	return inspectGlobalPublicationRecoveryNamespaceWithDocumentTargets(
		ctx,
		root,
		indexHooks,
		documentHooks,
		nil,
	)
}

func inspectGlobalPublicationRecoveryNamespaceWithDocumentTargets(
	ctx context.Context,
	root *os.Root,
	indexHooks indexPublishHooks,
	documentHooks documentPublishHooks,
	recoveredTargets map[string]publicationFileSpec,
) error {
	rules := []publicationNamespaceRule{
		indexNamespaceRule(),
		documentNamespaceRule(),
		privatePublicationNamespaceRule(),
	}
	snapshot, err := discoverPublicationNamespaces(ctx, root, rules)
	if err != nil || len(snapshot) == 0 {
		return err
	}
	normalized, err := normalizePrivatePublicationClaims(ctx, root, snapshot)
	if err != nil {
		return err
	}
	var indexSnapshot, documentSnapshot []publicationNamespaceDiscovery
	for _, discovery := range normalized {
		switch discovery.namespace {
		case "index-v1":
			indexSnapshot = append(indexSnapshot, discovery)
		case "document-v2":
			documentSnapshot = append(documentSnapshot, discovery)
		case "private-v2":
			return publicationConflict(
				fmt.Sprintf("unresolved private publication claim %q", discovery.relative),
				nil,
			)
		default:
			return publicationConflict("unknown publication recovery protocol", nil)
		}
	}

	var indexPrepared *preparedIndexRecovery
	if len(indexSnapshot) != 0 {
		discoveries, err := indexDiscoveriesFromPublication(root, indexSnapshot)
		if err != nil {
			return err
		}
		indexPrepared, err = prepareIndexRecoveryWithSharedInventory(
			root,
			discoveries,
			indexHooks,
			true,
		)
		if err != nil {
			return err
		}
		defer indexPrepared.close()
	}

	var documentPrepared *preparedDocumentRecovery
	if len(documentSnapshot) != 0 {
		documentPrepared, err = prepareDocumentRecovery(
			ctx,
			root,
			documentSnapshot,
			documentHooks,
		)
		if err != nil {
			return err
		}
		defer documentPrepared.close()
		documentPrepared.sharedInventory = true
	}
	if err := rejectCrossProtocolRecoveryCollisions(
		root,
		indexPrepared,
		documentPrepared,
	); err != nil {
		return err
	}
	if indexPrepared != nil && indexPrepared.blocker != nil {
		return indexPrepared.blocker
	}
	if documentPrepared != nil &&
		documentPrepared.hooks.afterRecoveryInventory != nil {
		if err := documentPrepared.hooks.afterRecoveryInventory(); err != nil {
			return err
		}
		documentPrepared.hooks.afterRecoveryInventory = nil
	}
	expectedSnapshot := snapshot
	if indexPrepared != nil && indexPrepared.batch != nil {
		expectedSnapshot = indexPrepared.batch.reconcileSharedRecoverySnapshot(snapshot)
	}
	if err := revalidatePublicationNamespaceSnapshot(ctx, root, rules, expectedSnapshot); err != nil {
		return err
	}
	if indexPrepared != nil {
		if err := indexPrepared.apply(); err != nil {
			return err
		}
	}
	if documentPrepared != nil {
		expectedDocumentNamespace := make([]publicationNamespaceDiscovery, 0, len(normalized))
		for index, discovery := range normalized {
			if discovery.namespace == "document-v2" {
				expectedDocumentNamespace = append(expectedDocumentNamespace, snapshot[index])
			}
		}
		if err := revalidatePublicationNamespaceSnapshot(
			ctx,
			root,
			rules,
			expectedDocumentNamespace,
		); err != nil {
			return err
		}
		if err := documentPrepared.apply(ctx); err != nil {
			return err
		}
		for _, group := range documentPrepared.groups {
			if recoveredTargets != nil &&
				group.completed &&
				group.leafSpec.info != nil &&
				group.leafSpec.complete {
				recoveredTargets[group.manifest.Target] = group.leafSpec
			}
		}
	}
	remaining, err := discoverPublicationNamespaces(ctx, root, rules)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return publicationConflict("publication recovery did not converge", nil)
	}
	return nil
}

type privatePublicationClaim struct {
	index      int
	discovery  publicationNamespaceDiscovery
	spec       publicationFileSpec
	nameDigest string
	resolved   bool
}

const maxLivePrivatePublicationClaims = 1

func normalizePrivatePublicationClaims(
	ctx context.Context,
	root *os.Root,
	snapshot []publicationNamespaceDiscovery,
) ([]publicationNamespaceDiscovery, error) {
	normalized := append([]publicationNamespaceDiscovery(nil), snapshot...)
	privateCount := 0
	for _, discovery := range snapshot {
		if discovery.namespace == "private-v2" {
			privateCount++
		}
	}
	if privateCount > maxLivePrivatePublicationClaims {
		return nil, publicationConflict("private publication claim inventory exceeds protocol limit", nil)
	}
	var claims []*privatePublicationClaim
	for index, discovery := range snapshot {
		if discovery.namespace != "private-v2" {
			continue
		}
		nameDigest, ok := parsePrivatePublicationClaimName(discovery.name)
		if !ok {
			return nil, publicationConflict("non-canonical private publication claim", nil)
		}
		parent, err := openDirectory(root, discovery.directory)
		if err != nil {
			return nil, err
		}
		spec, captureErr := capturePublicationFile(ctx, parent, discovery.name)
		closeErr := parent.Close()
		if err := errors.Join(captureErr, closeErr); err != nil {
			return nil, err
		}
		claims = append(claims, &privatePublicationClaim{
			index: index, discovery: discovery, spec: spec, nameDigest: nameDigest,
		})
	}
	if len(claims) == 0 {
		return normalized, nil
	}

	for _, claim := range claims {
		_, canonicalName, ok := privateIndexBatchManifestCandidate(claim.spec)
		if !ok || !privateClaimBindsName(claim.nameDigest, canonicalName) {
			continue
		}
		normalizePrivateClaim(
			&normalized[claim.index],
			"index-v1",
			string(indexArtifactManifest),
			canonicalName,
		)
		claim.resolved = true
	}

	for _, claim := range claims {
		if claim.resolved {
			continue
		}
		manifest, id, canonicalName, ok := privateDocumentManifestCandidate(
			claim.discovery.directory,
			claim.spec,
		)
		if !ok || !privateClaimBindsName(claim.nameDigest, canonicalName) {
			continue
		}
		_ = manifest
		normalizePrivateClaim(
			&normalized[claim.index],
			"document-v2",
			"manifest",
			canonicalName,
		)
		claim.resolved = true
		_ = id
	}

	manifests, err := collectPrivateRecoveryDocumentManifests(ctx, root, normalized)
	if err != nil {
		return nil, err
	}
	for _, claim := range claims {
		if claim.resolved {
			continue
		}
		for _, candidate := range manifests {
			if candidate.directory != claim.discovery.directory {
				continue
			}
			for _, kind := range []string{"witness", "backup", "stage"} {
				canonicalName := documentBoundArtifactName(
					kind,
					candidate.manifest.Target,
					claim.spec.mode,
					claim.spec.data,
				)
				if !privateClaimBindsName(claim.nameDigest, canonicalName) ||
					!privateDocumentArtifactMatches(kind, candidate.manifest, claim.spec) {
					continue
				}
				normalizePrivateClaim(
					&normalized[claim.index],
					"document-v2",
					kind,
					canonicalName,
				)
				claim.resolved = true
			}
			for _, kind := range []string{"claim", "discard"} {
				canonicalName := documentArtifactName(kind, candidate.id)
				if !privateClaimBindsName(claim.nameDigest, canonicalName) ||
					!privateDocumentArtifactMatches(kind, candidate.manifest, claim.spec) {
					continue
				}
				normalizePrivateClaim(
					&normalized[claim.index],
					"document-v2",
					kind,
					canonicalName,
				)
				claim.resolved = true
			}
			for slot := 0; slot < maxDocumentRollbackAnchors; slot++ {
				kind := documentRollbackAnchorProtocolKind(slot)
				canonicalName := documentRollbackAnchorName(
					candidate.manifest.Target,
					claim.spec.mode,
					claim.spec.data,
					slot,
				)
				if !privateClaimBindsName(claim.nameDigest, canonicalName) ||
					!privateDocumentArtifactMatches(kind, candidate.manifest, claim.spec) {
					continue
				}
				normalizePrivateClaim(
					&normalized[claim.index],
					"document-v2",
					kind,
					canonicalName,
				)
				claim.resolved = true
			}
		}
	}
	for _, claim := range claims {
		if claim.resolved {
			continue
		}
		kind, canonicalName, candidateErr := privateDetachedDocumentArtifactCandidate(
			ctx,
			root,
			claim,
		)
		if candidateErr != nil {
			return nil, candidateErr
		}
		if canonicalName == "" {
			continue
		}
		normalizePrivateClaim(
			&normalized[claim.index],
			"document-v2",
			kind,
			canonicalName,
		)
		claim.resolved = true
	}
	for _, claim := range claims {
		if claim.resolved {
			continue
		}
		destination := pathJoin(claim.discovery.directory, indexFilename)
		for _, kind := range []indexArtifactKind{
			indexArtifactStage,
			indexArtifactBackup,
			indexArtifactRestore,
		} {
			template, nameErr := newIndexArtifactNameTemplate(
				kind,
				destination,
				claim.spec.mode,
				claim.spec.data,
			)
			if nameErr != nil {
				return nil, nameErr
			}
			for slot := 0; slot < 10_000; slot++ {
				canonicalName := template.Name(slot)
				if !privateClaimBindsName(claim.nameDigest, canonicalName) {
					continue
				}
				normalizePrivateClaim(
					&normalized[claim.index],
					"index-v1",
					string(kind),
					canonicalName,
				)
				claim.resolved = true
				break
			}
			if claim.resolved {
				break
			}
		}
		if !claim.resolved {
			return nil, publicationConflict(
				fmt.Sprintf("unresolved private publication claim %q", claim.discovery.relative),
				nil,
			)
		}
	}
	return normalized, nil
}

type privateRecoveryDocumentManifest struct {
	directory string
	id        string
	manifest  documentTransactionManifest
}

func collectPrivateRecoveryDocumentManifests(
	ctx context.Context,
	root *os.Root,
	snapshot []publicationNamespaceDiscovery,
) ([]privateRecoveryDocumentManifest, error) {
	var manifests []privateRecoveryDocumentManifest
	for _, discovery := range snapshot {
		if discovery.namespace != "document-v2" || discovery.kind != "manifest" {
			continue
		}
		parent, err := openDirectory(root, discovery.directory)
		if err != nil {
			return nil, err
		}
		spec, captureErr := capturePublicationFile(ctx, parent, discovery.name)
		closeErr := parent.Close()
		if err := errors.Join(captureErr, closeErr); err != nil {
			return nil, err
		}
		manifest, id, canonicalName, ok := privateDocumentManifestCandidate(
			discovery.directory,
			spec,
		)
		if !ok || canonicalName != discovery.protocolName {
			return nil, publicationConflict("invalid document manifest in shared recovery inventory", nil)
		}
		manifests = append(manifests, privateRecoveryDocumentManifest{
			directory: discovery.directory,
			id:        id,
			manifest:  manifest,
		})
	}
	return manifests, nil
}

func privateDocumentManifestCandidate(
	directory string,
	spec publicationFileSpec,
) (documentTransactionManifest, string, string, bool) {
	var manifest documentTransactionManifest
	if spec.mode != 0o600 ||
		json.Unmarshal(spec.data, &manifest) != nil ||
		manifest.Format != documentTransactionFormat {
		return documentTransactionManifest{}, "", "", false
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, spec.data) {
		return documentTransactionManifest{}, "", "", false
	}
	group := &documentRecoveryGroup{directory: directory, manifest: manifest}
	if !validDocumentRecoveryManifest(group) {
		return documentTransactionManifest{}, "", "", false
	}
	id := documentManifestDigest(spec.data)
	return manifest, id, documentArtifactName("manifest", id), true
}

func privateDocumentArtifactMatches(
	kind string,
	manifest documentTransactionManifest,
	spec publicationFileSpec,
) bool {
	if spec.mode != fs.FileMode(manifest.Mode) {
		return false
	}
	size, digest := manifest.OriginalSize, manifest.OriginalSHA256
	if kind == "stage" || kind == "discard" {
		size, digest = manifest.PublishedSize, manifest.PublishedSHA256
	}
	return uint64(spec.size) == size && hex.EncodeToString(spec.digest[:]) == digest
}

func privateDetachedDocumentArtifactCandidate(
	ctx context.Context,
	root *os.Root,
	claim *privatePublicationClaim,
) (string, string, error) {
	parent, err := openDirectory(root, claim.discovery.directory)
	if err != nil {
		return "", "", err
	}
	defer parent.Close()
	file, err := parent.Open(".")
	if err != nil {
		return "", "", err
	}
	entries, readErr := file.ReadDir(-1)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", "", err
	}
	type candidate struct {
		kind string
		name string
	}
	var matches []candidate
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if isReservedTransactionPath(entry.Name()) {
			continue
		}
		target := pathJoin(claim.discovery.directory, entry.Name())
		for _, kind := range []string{"witness", "backup", "stage"} {
			name := documentBoundArtifactName(kind, target, claim.spec.mode, claim.spec.data)
			if privateClaimBindsName(claim.nameDigest, name) {
				matches = append(matches, candidate{kind: kind, name: name})
			}
		}
		for slot := 0; slot < maxDocumentRollbackAnchors; slot++ {
			kind := documentRollbackAnchorProtocolKind(slot)
			name := documentRollbackAnchorName(
				target,
				claim.spec.mode,
				claim.spec.data,
				slot,
			)
			if privateClaimBindsName(claim.nameDigest, name) {
				matches = append(matches, candidate{kind: kind, name: name})
			}
		}
	}
	if len(matches) == 0 {
		return "", "", nil
	}
	if len(matches) != 1 {
		return "", "", publicationConflict(
			"private detached document artifact target is ambiguous",
			nil,
		)
	}
	return matches[0].kind, matches[0].name, nil
}

func documentManifestDigestMust(manifest documentTransactionManifest) string {
	data, _ := json.Marshal(manifest)
	return documentManifestDigest(data)
}

func normalizePrivateClaim(
	discovery *publicationNamespaceDiscovery,
	namespace string,
	kind string,
	protocolName string,
) {
	discovery.namespace = namespace
	discovery.kind = kind
	discovery.protocolName = protocolName
}

func parsePrivatePublicationClaimName(name string) (string, bool) {
	if !strings.HasPrefix(name, publicationPrivateClaimPrefix) {
		return "", false
	}
	remainder := strings.TrimPrefix(name, publicationPrivateClaimPrefix)
	nameDigest, nonce, found := strings.Cut(remainder, "-")
	if !found || len(nameDigest) != sha256.Size*2 || len(nonce) != 16*2 {
		return "", false
	}
	decodedDigest, digestErr := hex.DecodeString(nameDigest)
	decodedNonce, nonceErr := hex.DecodeString(nonce)
	return nameDigest, digestErr == nil &&
		nonceErr == nil &&
		hex.EncodeToString(decodedDigest) == nameDigest &&
		hex.EncodeToString(decodedNonce) == nonce
}

func privateClaimBindsName(nameDigest string, canonicalName string) bool {
	digest := sha256.Sum256([]byte(canonicalName))
	return hex.EncodeToString(digest[:]) == nameDigest
}

func rejectCrossProtocolRecoveryCollisions(
	root *os.Root,
	indexPrepared *preparedIndexRecovery,
	documentPrepared *preparedDocumentRecovery,
) error {
	if indexPrepared == nil || documentPrepared == nil {
		return nil
	}
	indexTargets := make(map[string]string)
	for _, item := range indexPrepared.inventory {
		target := item.destination.output.relative
		key, err := publicationPathPhysicalKey(root, target)
		if err != nil {
			return err
		}
		indexTargets[key] = target
	}
	for _, group := range documentPrepared.groups {
		target := group.manifest.Target
		key, err := publicationPathPhysicalKey(root, target)
		if err != nil {
			return err
		}
		if indexTarget, exists := indexTargets[key]; exists {
			return publicationConflict(
				fmt.Sprintf(
					"recovery protocols claim one physical destination: index %q and document %q",
					indexTarget,
					target,
				),
				nil,
			)
		}
	}
	return nil
}
